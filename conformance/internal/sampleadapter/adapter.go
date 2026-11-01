// Package sampleadapter is the smallest possible test-runner adapter, kept as a fixture of the
// conformance suite: it passes every check, and each mutant breaks exactly one rule. It reads a
// sample project of test items (a JSON file) rather than a real framework's tests, because the
// point is to show the calls an adapter makes, not to be an adapter for any framework.
//
// It speaks two profiles (spec/TEST_RUNNER_V1.md, section 1.1). The default, live, runs each item
// between startStep and finishStep. With --profile import it runs every item first, writes a JUnit
// XML file of their outcomes and durations, and only then reports from that file: for each item,
// in order, startStep, finishStep with the outcome, the real duration and the failure text read
// from the file, and the file itself as evidence. An import run hands no test a traceparent, and
// the adapter never claims that one reached the system under test.
//
// It lives in internal/ rather than testdata/ so that `go build ./...` and `go vet ./...` cover
// it. The sample project itself is in testdata/sample-project/.
package sampleadapter

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/saphan-ai/tests-studio/conformance/internal/proto"
)

// The mutants. Each one breaks exactly one rule of the specification.
const (
	MutantSpecV2            = "spec-v2"
	MutantResultNoStepID    = "result-without-step-id"
	MutantEvidenceDigest    = "evidence-digest-mismatch"
	MutantResultDigest      = "run-result-digest-malformed"
	MutantEventNoRunID      = "event-missing-run-id"
	MutantCredentialSummary = "credential-in-summary"
	MutantMockControlDirect = "mock-control-direct"
	MutantSecondRunInLeg    = "second-run-in-leg"

	// The mutants of v1.1; each one breaks the rule of the ruling named beside it.
	MutantCountsMismatch   = "counts-mismatch"          // ruling 1: close with counts the core's tally contradicts
	MutantDuplicateStepID  = "duplicate-step-id"        // ruling 3: a second start of a step_id
	MutantRetryNewStep     = "retry-as-new-step"        // ruling 4: a retry becomes a second step
	MutantNeverRanNoStep   = "never-ran-without-step"   // ruling 5: an item whose body never ran gets no step
	MutantEvidenceUnknown  = "evidence-unstarted-step"  // ruling 6: evidence for a step that was never started
	MutantRunClosedLate    = "run-closed-after-close"   // ruling 7: run.closed emitted after closeTestRun
	MutantConcurrentAttach = "concurrent-attach-in-leg" // ruling 12: an attach while a test run is attached
	MutantSampleNoSkip     = "sample-without-skip"      // ruling 14: a sample project with no skipped test
	MutantRetryRefused     = "retry-refused-call"       // ruling 15: a refused call sent again unchanged
	MutantEventIDReused    = "event-id-reused"          // ruling 16: one event_id on two different events

	// The profiles of v1.2 (section 1.1 of the specification).
	ProfileLive   = "live"
	ProfileImport = "import"

	adapterName      = "sample-adapter"
	adapterVersion   = "1.0.0"
	httpTimeout      = 30 * time.Second
	timeLayout       = "2006-01-02T15:04:05.000Z"
	maxResponseBytes = 8 << 20
)

// Project is the sample project file.
type Project struct {
	Project string `json:"project"`
	Tests   []Item `json:"tests"`
}

// Item is one test of the sample project.
type Item struct {
	Name     string `json:"name"`
	Outcome  string `json:"outcome"` // pass, fail, error or skip; error stands for an item whose body never ran
	Failure  string `json:"failure,omitempty"`
	CallsSUT bool   `json:"calls_sut,omitempty"`
	// DurationMS is how long the item's body takes under --profile import, where the item runs
	// before the report and its duration travels through the JUnit file; live measures its own.
	DurationMS int64     `json:"duration_ms,omitempty"`
	Evidence   string    `json:"evidence,omitempty"`
	Scenario   *Scenario `json:"scenario,omitempty"`
}

// Scenario is a mock scenario the item sets before it runs.
type Scenario struct {
	Mock     string         `json:"mock"`
	Settings map[string]int `json:"settings"`
}

type adapter struct {
	env    map[string]string
	client *http.Client
	stdout io.Writer
	stderr io.Writer
	mutant string
	spec   string
	// profile is ProfileLive or ProfileImport; evidenceKind is the Tests-Evidence-Kind of an item's evidence.
	profile      string
	evidenceKind string

	coreURL  string
	opened   proto.TestRunOpened
	runCred  string
	events   int
	started  time.Time
	failures int // calls the core refused; reported, never fatal
}

// Main runs the adapter and returns its exit status. rt is the HTTP transport (nil for the
// default); a test passes one that reaches the stubs without a socket.
func Main(args, environ []string, rt http.RoundTripper, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet(adapterName, flag.ContinueOnError)
	fs.SetOutput(stderr)
	projectPath := fs.String("project", "", "path to the sample project file")
	profile := fs.String("profile", ProfileLive, "live: the items run between startStep and finishStep; import: the items run first and the report is read from a JUnit XML file")
	mutant := fs.String("mutant", "", "break exactly one rule of the specification (conformance fixture only)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *profile != ProfileLive && *profile != ProfileImport {
		fmt.Fprintf(stderr, "sample-adapter: --profile is %q; it must be %s or %s\n", *profile, ProfileLive, ProfileImport)
		return 2
	}
	raw, err := os.ReadFile(*projectPath)
	var proj Project
	if err == nil {
		err = json.Unmarshal(raw, &proj)
	}
	if err != nil {
		fmt.Fprintln(stderr, "sample-adapter: cannot read the sample project:", err)
		return 2
	}
	env := map[string]string{}
	for _, kv := range environ {
		if k, v, ok := strings.Cut(kv, "="); ok {
			env[k] = v
		}
	}
	a := &adapter{env: env, client: &http.Client{Transport: rt, Timeout: httpTimeout}, stdout: stdout, stderr: stderr, mutant: *mutant, spec: proto.SpecVersion, profile: *profile, evidenceKind: "log", started: time.Now().UTC()}
	if err := a.run(proj); err != nil {
		fmt.Fprintln(stderr, "sample-adapter:", err)
		return 1
	}
	return 0
}

func (a *adapter) run(proj Project) error {
	a.coreURL = a.env[proto.EnvCoreURL]
	if a.coreURL == "" {
		return fmt.Errorf("%s is not set", proto.EnvCoreURL)
	}
	if err := a.negotiate(); err != nil {
		return err
	}
	if a.profile == ProfileImport {
		// The items run before any step exists; what is reported is what the result file says.
		imported, err := a.runAndImport(proj)
		if err != nil {
			return err
		}
		proj.Tests = imported
	}
	if err := a.open(proj); err != nil {
		return err
	}
	var counts proto.Counts
	for i, it := range proj.Tests {
		if a.mutant == MutantSampleNoSkip && it.Outcome == "skip" {
			it.Outcome = "pass" // breaks one rule: a sample project with no skipped test
		}
		if a.mutant == MutantNeverRanNoStep && it.Outcome == "error" {
			continue // breaks one rule: an item whose body never ran is left without a step
		}
		stepID := fmt.Sprintf("s%d-%s", i+1, it.Name)
		a.runItem(i, stepID, it, &counts)
		if a.mutant == MutantRetryNewStep && it.Outcome == "fail" {
			a.runItem(i, stepID+"-retry", it, &counts) // breaks one rule: a retry is a second step
		}
	}
	return a.finish(counts)
}

// negotiate asks the core which spec versions it serves and picks the one this adapter speaks.
func (a *adapter) negotiate() error {
	var doc proto.SpecDocument
	if _, err := a.call("GET", a.coreURL+"/v1/spec", "", nil, nil, &doc); err != nil {
		return err
	}
	if !has(doc.Versions, proto.SpecVersion) {
		return fmt.Errorf("refused: the core serves spec versions %s and this adapter speaks %s", strings.Join(doc.Versions, ", "), proto.SpecVersion)
	}
	if a.mutant == MutantSpecV2 {
		a.spec = "v2"
	}
	return nil
}

func has(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// open attaches to the leg's run when the environment names one, and opens a test run otherwise.
func (a *adapter) open(proj Project) error {
	hdr := map[string]string{proto.HeaderSpecVersion: a.spec}
	legRun, legToken := a.env[proto.EnvLegRunID], a.env[proto.EnvLegToken]
	var status int
	var err error
	if legRun != "" {
		a.runCred = legToken
		attachURL := a.coreURL + "/v1/runs/" + legRun + "/test-runs"
		if a.mutant == MutantRetryRefused {
			// Breaks one rule: the core refuses this attach (the id is malformed), and the adapter
			// sends the very same request again instead of accepting the decision.
			bad := proto.OpenRequest{Project: proj.Project, TestRunID: "X"}
			_, _ = a.call("POST", attachURL, legToken, hdr, bad, nil)
			_, _ = a.call("POST", attachURL, legToken, hdr, bad, nil)
		}
		status, err = a.call("POST", attachURL, legToken, hdr, proto.OpenRequest{Project: proj.Project}, &a.opened)
		if a.mutant == MutantConcurrentAttach {
			// Breaks one rule: a second attach while the first test run is still attached.
			_, _ = a.call("POST", attachURL, legToken, hdr, proto.OpenRequest{Project: proj.Project}, nil)
		}
		if a.mutant == MutantSecondRunInLeg {
			// Breaks one rule: a leg's run is the only run; the adapter opens a second one anyway.
			_, _ = a.call("POST", a.coreURL+"/v1/test-runs", "", hdr, proto.OpenRequest{Project: proj.Project, Mocks: []string{"sample-http"}}, nil)
		}
	} else {
		status, err = a.call("POST", a.coreURL+"/v1/test-runs", "", hdr, proto.OpenRequest{Project: proj.Project, Mocks: []string{"sample-http"}}, &a.opened)
		a.runCred = a.opened.RunCredential
	}
	if err != nil {
		return err
	}
	if status != http.StatusCreated {
		return errors.New("the core refused to open or attach the test run (see the refusal above)")
	}
	// Ruling 9 of v1.1: MOCKS_CORE_URL and the answer's mocks_core_url name one service; when
	// they differ the adapter refuses by name rather than choosing one.
	if env := a.env[proto.EnvMocksURL]; env != "" && env != a.opened.MocksCoreURL {
		return fmt.Errorf("refused [%s]: %s is %q and the core's answer names %q; they must name one Mocks Studio core",
			proto.ReasonMocksCoreMismatch, proto.EnvMocksURL, env, a.opened.MocksCoreURL)
	}
	summary := "test run opened for project " + proj.Project
	if a.mutant == MutantCredentialSummary {
		summary += " with credential " + a.opened.Credential // breaks one rule: a credential in a summary
	}
	a.emit("run.opened", "", "", summary, "", nil)
	return nil
}

// runItem turns one test item into one step: one start, one result carrying the item's final
// outcome. The counts it fills are the core's own rule (v1.1, ruling 1): a step is counted when
// the core accepted its start, a result when the core accepted it.
func (a *adapter) runItem(i int, stepID string, it Item, counts *proto.Counts) {
	var ss proto.StepStarted
	status, err := a.call("POST", a.testRunURL()+"/steps", a.opened.Credential, nil, proto.StartStepRequest{StepID: stepID, Name: it.Name}, &ss)
	if a.mutant == MutantDuplicateStepID && i == 2 {
		// Breaks one rule: the step_id is started a second time (the core refuses it, 409).
		_, _ = a.call("POST", a.testRunURL()+"/steps", a.opened.Credential, nil, proto.StartStepRequest{StepID: stepID, Name: it.Name}, nil)
	}
	if err != nil || status != http.StatusCreated || ss.Traceparent == "" {
		a.failures++
		return
	}
	counts.Total++
	a.emit("step.started", stepID, ss.CorrelationID, "step "+it.Name+" started", "", nil)
	result := it.Outcome
	var duration int64
	if a.profile == ProfileImport {
		// The item ran before this step existed: its duration is the one the result file holds, and
		// no test could carry the step's traceparent, so none is handed on.
		duration = it.DurationMS
		if a.mutant == MutantMockControlDirect {
			a.setScenario(it) // only the mutant touches a mock here: an import run sets no scenario
		}
	} else {
		t0 := time.Now()
		a.setScenario(it)
		a.callSUT(it, ss)
		duration = time.Since(t0).Milliseconds()
	}
	res := proto.StepResult{StepID: &stepID, Result: result, DurationMS: &duration, Failure: it.Failure}
	if a.mutant == MutantResultNoStepID && i == 0 {
		// Breaks one rule: a result without its step_id. The core refuses it; the adapter then
		// sends the right one, so the tally stays closable and only this rule is broken.
		_, _ = a.call("POST", a.testRunURL()+"/step-results", a.opened.Credential, nil, proto.StepResult{Result: result, DurationMS: &duration, Failure: it.Failure}, nil)
	}
	status, err = a.call("POST", a.testRunURL()+"/step-results", a.opened.Credential, nil, res, nil)
	switch {
	case err != nil || status != http.StatusNoContent:
		a.failures++
	case result == "pass":
		counts.Passed++
	case result == "fail":
		counts.Failed++
	case result == "error":
		counts.Errored++
	case result == "skip":
		counts.Skipped++
	}
	a.emit("step.finished", stepID, ss.CorrelationID, "step "+it.Name+" finished: "+result, result, nil)
	// Ruling 6 of v1.1: evidence needs a started step and may be attached once it is finished
	// (a video or a trace is complete only after the test), so it is attached here.
	a.attachEvidence(it, stepID, ss.CorrelationID, i == 0)
	if it.Scenario != nil && a.mutant != MutantMockControlDirect && a.profile == ProfileLive {
		a.callMocks("POST", "/reset", nil)
	}
}

func (a *adapter) testRunURL() string { return a.coreURL + "/v1/test-runs/" + a.opened.TestRunID }

// setScenario sets the item's mock scenario through the Mocks Studio core, never through the mock.
func (a *adapter) setScenario(it Item) {
	if it.Scenario == nil {
		return
	}
	if a.mutant == MutantMockControlDirect {
		// Breaks one rule: the mock's own control URL is called instead of the Mocks Studio core.
		_, _ = a.call("PUT", a.opened.Endpoints[it.Scenario.Mock].URL+"/admin/config", "", nil, it.Scenario.Settings, nil)
		return
	}
	a.callMocks("PUT", "/mocks/"+it.Scenario.Mock+"/settings", it.Scenario.Settings)
}

func (a *adapter) callMocks(method, suffix string, body any) {
	url := a.opened.MocksCoreURL + "/v1/runs/" + a.opened.RunID + suffix
	if _, err := a.call(method, url, a.runCred, nil, body, nil); err != nil {
		a.failures++
	}
}

// callSUT stands for the test calling the system under test: it carries the step's trace headers.
func (a *adapter) callSUT(it Item, ss proto.StepStarted) {
	if !it.CallsSUT {
		return
	}
	hdr := map[string]string{proto.HeaderTraceparent: ss.Traceparent, proto.HeaderBaggage: ss.Baggage}
	if _, err := a.call("GET", a.env[proto.EnvSutURL]+"/ping", "", hdr, nil, nil); err != nil {
		a.failures++
	}
}

func (a *adapter) attachEvidence(it Item, stepID, corr string, first bool) {
	if it.Evidence == "" {
		return
	}
	data := []byte(it.Evidence)
	sum := sha256.Sum256(data)
	declared := hex.EncodeToString(sum[:])
	if a.mutant == MutantEvidenceDigest && first {
		other := sha256.Sum256([]byte("different bytes")) // breaks one rule: the digest is not the bytes'
		declared = hex.EncodeToString(other[:])
	}
	hdrStep := stepID
	if a.mutant == MutantEvidenceUnknown && first {
		hdrStep = "no-such-step" // breaks one rule: evidence for a step that was never started
	}
	hdr := map[string]string{proto.HeaderEvidenceKind: a.evidenceKind, proto.HeaderStepID: hdrStep, "Content-Type": "text/plain"}
	if _, err := a.rawCall("PUT", a.testRunURL()+"/evidence/"+declared, a.opened.Credential, hdr, data, nil); err != nil {
		a.failures++
		return
	}
	a.emit("evidence.stored", stepID, corr, a.evidenceKind+" evidence stored for step "+stepID, "", map[string]any{"redacted": true, "digest": "sha256:" + hex.EncodeToString(sum[:]), "size": len(data)})
}

// emit posts one tests.event/v1 document. A refused event is counted and never fatal.
func (a *adapter) emit(kind, stepID, corr, summary, outcome string, payload map[string]any) {
	a.events++
	ev := map[string]any{
		"schema": "tests.event/v1", "event_id": fmt.Sprintf("%s-evt-%03d", a.opened.TestRunID, a.events),
		"at": time.Now().UTC().Format(timeLayout), "run_id": a.opened.RunID, "test_run_id": a.opened.TestRunID,
		"kind": kind, "emitter": proto.Identity{Name: adapterName, Version: adapterVersion, Spec: a.spec}, "summary": summary,
	}
	if stepID != "" {
		ev["step_id"], ev["correlation_id"] = stepID, corr
	}
	if outcome != "" {
		ev["outcome"] = outcome
	}
	if payload != nil {
		ev["payload"] = payload
	}
	if a.mutant == MutantEventIDReused && a.events == 3 {
		ev["event_id"] = fmt.Sprintf("%s-evt-%03d", a.opened.TestRunID, 1) // breaks one rule: the id of the first event on a different event
	}
	if a.mutant == MutantEventNoRunID && kind == "step.started" && a.events == 2 {
		delete(ev, "run_id") // breaks one rule: an event without run_id
	}
	if _, err := a.call("POST", a.testRunURL()+"/events", a.opened.Credential, nil, ev, nil); err != nil {
		a.failures++
	}
}

// finish closes the test run, publishes the run result and prints the evidence line.
func (a *adapter) finish(counts proto.Counts) error {
	verdict := proto.VerdictOf(counts)
	if a.mutant != MutantRunClosedLate {
		a.emit("run.closed", "", "", "test run closed with verdict "+verdict, verdict, nil)
	}
	var rr proto.RunResult
	req := proto.CloseRequest{
		Verdict: verdict, Counts: counts, StartedAt: a.started.Format(timeLayout), EndedAt: time.Now().UTC().Format(timeLayout),
		Adapter: proto.Identity{Name: adapterName, Version: adapterVersion, Spec: a.spec},
	}
	if a.mutant == MutantCountsMismatch {
		// Breaks one rule: counts that are not the core's tally. The core refuses; the adapter
		// then closes with the right counts, so the run result itself stays correct.
		wrong := req
		wrong.Counts.Total++
		_, _ = a.call("POST", a.testRunURL()+"/close", a.opened.Credential, nil, wrong, nil)
	}
	status, err := a.call("POST", a.testRunURL()+"/close", a.opened.Credential, nil, req, &rr)
	if err != nil {
		return err
	}
	if a.mutant == MutantRunClosedLate {
		// Breaks one rule: run.closed after closeTestRun. Closing revoked the credential, so the core refuses it.
		a.emit("run.closed", "", "", "test run closed with verdict "+verdict, verdict, nil)
	}
	if status != http.StatusOK {
		return errors.New("the core refused to close the test run (see the refusal above)")
	}
	if a.mutant == MutantResultDigest {
		rr.EventsDigest = "sha256:not-a-digest" // breaks one rule: a malformed events_digest
		rr.EvidenceLine = proto.EvidenceLine(rr.RunID, rr.TestRunID, rr.Verdict, rr.EventsDigest)
	}
	if path := a.env[proto.EnvResultFile]; path != "" {
		doc, err := json.MarshalIndent(rr, "", "  ")
		if err == nil {
			err = os.WriteFile(path, append(doc, '\n'), 0o600)
		}
		if err != nil {
			return fmt.Errorf("cannot write the run result: %w", err)
		}
	}
	fmt.Fprintln(a.stdout, rr.EvidenceLine)
	if a.failures > 0 {
		fmt.Fprintf(a.stderr, "sample-adapter: %d calls were refused by the core or a mock\n", a.failures)
	}
	return nil
}

// call sends a JSON request and decodes a JSON answer into out; a refusal's message is printed.
func (a *adapter) call(method, url, token string, hdr map[string]string, body, out any) (int, error) {
	var raw []byte
	if body != nil {
		var err error
		if raw, err = json.Marshal(body); err != nil {
			return 0, err
		}
		if hdr == nil {
			hdr = map[string]string{}
		}
		hdr["Content-Type"] = "application/json"
	}
	return a.rawCall(method, url, token, hdr, raw, out)
}

func (a *adapter) rawCall(method, url, token string, hdr map[string]string, body []byte, out any) (int, error) {
	req, err := http.NewRequest(method, url, bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := a.client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return resp.StatusCode, err
	}
	if resp.StatusCode >= 400 {
		var ref proto.Refusal
		if json.Unmarshal(data, &ref) == nil && ref.ReasonCode != "" {
			fmt.Fprintf(a.stderr, "sample-adapter: refused [%s]: %s\n", ref.ReasonCode, ref.Message)
		}
		return resp.StatusCode, nil
	}
	if out != nil && len(data) > 0 {
		if err := json.Unmarshal(data, out); err != nil {
			return resp.StatusCode, fmt.Errorf("the core's answer to %s %s is not the document the specification describes: %w", method, redact(url), err)
		}
	}
	return resp.StatusCode, nil
}

func redact(url string) string {
	if i := strings.IndexByte(url, '?'); i >= 0 {
		return url[:i]
	}
	return url
}
