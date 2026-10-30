// Package checks holds the conformance checks. Each check is a pure function of the recording
// the stubs made, the adapter's output and the run result file: nothing here opens a socket.
package checks

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/saphan-ai/tests-studio/conformance/internal/proto"
	"github.com/saphan-ai/tests-studio/conformance/internal/schemas"
	"github.com/saphan-ai/tests-studio/conformance/internal/stubs"
)

// Check ids. They are the public names of the checks; the specification lists them.
const (
	IDSpecVersion     = "spec-version-known"
	IDRunOpened       = "run-opened"
	IDNoSecondRun     = "no-second-run"
	IDStepPerItem     = "step-per-item"
	IDStepResult      = "step-result-valid"
	IDTraceparent     = "traceparent-to-sut"
	IDEvidenceDigest  = "evidence-digest"
	IDEventsValid     = "events-valid"
	IDRunResultSchema = "run-result-schema"
	IDRunResultCounts = "run-result-counts"
	IDCredentialSent  = "credential-presented"
	IDCredentialLeak  = "credential-not-leaked"
	IDMockControl     = "mock-control-bypass"
	IDComposition     = "sample-composition"
	IDRefusalRetried  = "refusal-not-retried"
)

// Input is everything the checks read.
type Input struct {
	Calls     []stubs.Call
	Mode      stubs.Mode
	Items     int    // the number of test items in the adapter's sample project
	LegRunID  string // leg mode: the run id put in the adapter's environment
	LegToken  string // leg mode: the run credential put in the adapter's environment
	Served    []string
	Stdout    string
	Stderr    string
	ResultRaw []byte // the run result file the adapter wrote; nil when it wrote none
	Validator *schemas.Validator
}

// Result is the outcome of one check.
type Result struct {
	ID       string
	Pass     bool
	Measured string
}

// Line renders a result as the one line the runner prints.
func (r Result) Line() string {
	if r.Pass {
		return "PASS " + r.ID + " — " + r.Measured
	}
	return "FAIL " + r.ID + " — " + r.Measured
}

func ok(id, format string, a ...any) Result {
	return Result{ID: id, Pass: true, Measured: fmt.Sprintf(format, a...)}
}

func fail(id, format string, a ...any) Result {
	return Result{ID: id, Pass: false, Measured: fmt.Sprintf(format, a...)}
}

// All runs every check, in the order the specification lists them.
func All(in Input) []Result {
	f := derive(in)
	return []Result{
		specVersion(in, f), runOpened(in, f), noSecondRun(in, f), stepPerItem(in, f), stepResult(in, f),
		traceparent(in, f), evidenceDigest(in, f), eventsValid(in, f), runResultSchema(in, f),
		runResultCounts(in, f), credentialSent(in, f), credentialLeak(in, f), mockControl(in, f),
		sampleComposition(in, f), refusalNotRetried(in, f),
	}
}

// facts are what the recording says about the run, derived once.
type facts struct {
	opened      *proto.TestRunOpened // the accepted open or attach answer
	closeResult *proto.RunResult     // the accepted close answer
	issued      map[string]proto.StepStarted
	secrets     []string
}

func isCore(c stubs.Call) bool { return c.Service == stubs.ServiceCore }

func isOpen(c stubs.Call) bool { return isCore(c) && c.Method == "POST" && c.Path == "/v1/test-runs" }

func isAttach(c stubs.Call) bool {
	return isCore(c) && c.Method == "POST" && strings.HasPrefix(c.Path, "/v1/runs/") && strings.HasSuffix(c.Path, "/test-runs")
}

func under(c stubs.Call, suffix string) bool {
	return isCore(c) && strings.HasPrefix(c.Path, "/v1/test-runs/") && strings.HasSuffix(c.Path, suffix)
}

func isStart(c stubs.Call) bool { return c.Method == "POST" && under(c, "/steps") }

func isFinish(c stubs.Call) bool { return c.Method == "POST" && under(c, "/step-results") }

func isEvent(c stubs.Call) bool { return c.Method == "POST" && under(c, "/events") }

func isClose(c stubs.Call) bool { return c.Method == "POST" && under(c, "/close") }

func isEvidence(c stubs.Call) bool {
	return isCore(c) && c.Method == "PUT" && strings.Contains(c.Path, "/evidence/")
}

func derive(in Input) facts {
	f := facts{issued: map[string]proto.StepStarted{}}
	for _, c := range in.Calls {
		switch {
		case (isOpen(c) || isAttach(c)) && c.Status == 201 && f.opened == nil:
			var o proto.TestRunOpened
			if json.Unmarshal(c.Response, &o) == nil {
				f.opened = &o
				f.secrets = append(f.secrets, o.Credential, o.RunCredential)
			}
		case isStart(c) && c.Status == 201:
			var s proto.StepStarted
			if json.Unmarshal(c.Response, &s) == nil {
				f.issued[s.StepID] = s
			}
		case isClose(c) && c.Status == 200 && f.closeResult == nil:
			var r proto.RunResult
			if json.Unmarshal(c.Response, &r) == nil {
				f.closeResult = &r
			}
		}
	}
	if in.LegToken != "" {
		f.secrets = append(f.secrets, in.LegToken)
	}
	var kept []string
	for _, s := range f.secrets {
		if s != "" {
			kept = append(kept, s)
		}
	}
	f.secrets = kept
	return f
}

// DeclaredVersionRefused reports the spec version the stub core refused by name, if it did.
func DeclaredVersionRefused(calls []stubs.Call) (declared string, refused bool) {
	for _, c := range calls {
		var r proto.Refusal
		if isCore(c) && c.Status == 422 && json.Unmarshal(c.Response, &r) == nil && r.ReasonCode == proto.ReasonSpecUnsupported {
			return c.Header.Get(proto.HeaderSpecVersion), true
		}
	}
	return "", false
}

func specVersion(in Input, _ facts) Result {
	asked := -1
	for i, c := range in.Calls {
		if isCore(c) && c.Method == "GET" && c.Path == "/v1/spec" {
			asked = i
			break
		}
	}
	if asked < 0 {
		return fail(IDSpecVersion, "the adapter never called GET /v1/spec, so it did not ask which spec versions the core serves")
	}
	for i, c := range in.Calls {
		if !isOpen(c) && !isAttach(c) {
			continue
		}
		got := c.Header.Get(proto.HeaderSpecVersion)
		if i < asked {
			return fail(IDSpecVersion, "the adapter opened or attached (call %d) before it asked GET /v1/spec (call %d)", c.Seq, in.Calls[asked].Seq)
		}
		if !contains(in.Served, got) {
			return fail(IDSpecVersion, "the adapter declared spec version %q in %s; the core serves %s", got, proto.HeaderSpecVersion, strings.Join(in.Served, ", "))
		}
		return ok(IDSpecVersion, "asked GET /v1/spec (call %d), then declared %q, which the core serves (%s)", in.Calls[asked].Seq, got, strings.Join(in.Served, ", "))
	}
	return fail(IDSpecVersion, "the adapter asked GET /v1/spec but no open or attach call carried a %s header", proto.HeaderSpecVersion)
}

func hasStep(issued map[string]proto.StepStarted, id string) bool {
	_, ok := issued[id]
	return ok
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

func count(in Input, pred func(stubs.Call) bool) (n int) {
	for _, c := range in.Calls {
		if pred(c) {
			n++
		}
	}
	return n
}

func runOpened(in Input, f facts) Result {
	if in.Mode == stubs.ModeLeg {
		want := "/v1/runs/" + in.LegRunID + "/test-runs"
		n := count(in, func(c stubs.Call) bool {
			return isAttach(c) && c.Path == want && c.Status == 201 && c.Header.Get("Authorization") == "Bearer "+in.LegToken
		})
		if n != 1 {
			return fail(IDRunOpened, "leg mode: %d accepted attach calls to POST %s with the leg's credential; exactly 1 is required", n, want)
		}
		return ok(IDRunOpened, "leg mode: attached to the leg's run %s as test run %s", in.LegRunID, f.opened.TestRunID)
	}
	n := count(in, func(c stubs.Call) bool { return isOpen(c) && c.Status == 201 })
	if n != 1 || f.opened == nil {
		return fail(IDRunOpened, "standalone mode: %d accepted POST /v1/test-runs calls; exactly 1 is required", n)
	}
	return ok(IDRunOpened, "standalone mode: opened test run %s on run %s", f.opened.TestRunID, f.opened.RunID)
}

func noSecondRun(in Input, _ facts) Result {
	opens := count(in, isOpen)
	attaches := count(in, isAttach)
	mocksOpens := count(in, func(c stubs.Call) bool {
		return c.Service == stubs.ServiceMocks && c.Method == "POST" && c.Path == "/v1/runs"
	})
	concurrent := 0 // seq of the first attach made while a test run was still attached
	attached := false
	for _, c := range in.Calls {
		switch {
		case isAttach(c):
			if attached && concurrent == 0 {
				concurrent = c.Seq
			}
			attached = attached || c.Status == http.StatusCreated
		case isClose(c) && c.Status == http.StatusOK:
			attached = false // the test run closed: the leg's run may take a new attach (ecosystem contract section 1)
		}
	}
	switch {
	case in.Mode == stubs.ModeLeg && (opens > 0 || mocksOpens > 0):
		return fail(IDNoSecondRun, "leg mode: %d openTestRun and %d openRun calls were made inside the leg's run; the adapter must attach and open nothing", opens, mocksOpens)
	case in.Mode == stubs.ModeLeg && concurrent > 0:
		return fail(IDNoSecondRun, "leg mode: call %d attached while a test run was attached; one test run is attached at a time (a new attach after the close is allowed)", concurrent)
	case in.Mode == stubs.ModeStandalone && (opens > 1 || mocksOpens > 0 || attaches > 0):
		return fail(IDNoSecondRun, "standalone mode: %d openTestRun, %d attach and %d openRun-on-Mocks calls; exactly one open and nothing else is allowed", opens, attaches, mocksOpens)
	}
	return ok(IDNoSecondRun, "%s mode: %d open, %d attach and %d openRun-on-Mocks calls", in.Mode, opens, attaches, mocksOpens)
}

func stepPerItem(in Input, f facts) Result {
	seen := map[string]int{}
	for _, c := range in.Calls {
		if isFinish(c) {
			var r proto.StepResult
			if json.Unmarshal(c.Body, &r) != nil || r.StepID == nil || *r.StepID == "" {
				return fail(IDStepPerItem, "call %d sent a step result with no step_id; a result names the step it finishes", c.Seq)
			}
		}
		if !isStart(c) {
			continue
		}
		var req proto.StartStepRequest
		_ = json.Unmarshal(c.Body, &req) // an unreadable body counts as an empty step_id below
		if req.StepID == "" {
			return fail(IDStepPerItem, "call %d started a step with no step_id", c.Seq)
		}
		seen[req.StepID]++
		if seen[req.StepID] > 1 {
			return fail(IDStepPerItem, "step_id %q was started %d times; every test item needs its own unique step_id", req.StepID, seen[req.StepID])
		}
	}
	if len(seen) != in.Items || len(f.issued) != in.Items {
		return fail(IDStepPerItem, "%d steps started (%d accepted) for %d test items; each item must become exactly one step", len(seen), len(f.issued), in.Items)
	}
	results := map[string]int{}
	for _, c := range in.Calls {
		if isFinish(c) && c.Status == 204 {
			var r proto.StepResult
			if json.Unmarshal(c.Body, &r) == nil && r.StepID != nil {
				results[*r.StepID]++
			}
		}
	}
	for id := range f.issued {
		if results[id] != 1 {
			return fail(IDStepPerItem, "step %q has %d accepted results carrying its step_id; exactly 1 is required", id, results[id])
		}
	}
	return ok(IDStepPerItem, "%d test items became %d steps with unique step_ids, each with exactly one result", in.Items, len(f.issued))
}

func stepResult(in Input, _ facts) Result {
	n := 0
	for _, c := range in.Calls {
		if !isFinish(c) {
			continue
		}
		n++
		var r proto.StepResult
		if err := json.Unmarshal(c.Body, &r); err != nil {
			return fail(IDStepResult, "call %d: the result body is not a JSON object (%v)", c.Seq, err)
		}
		switch {
		case !contains(proto.Results, r.Result):
			return fail(IDStepResult, "call %d: result %q is not one of pass, fail, error, skip", c.Seq, r.Result)
		case r.DurationMS == nil:
			return fail(IDStepResult, "call %d: the result carried no duration_ms; every result needs one", c.Seq)
		case *r.DurationMS < 0:
			return fail(IDStepResult, "call %d: duration_ms is %d; it must be 0 or more", c.Seq, *r.DurationMS)
		case (r.Result == "fail" || r.Result == "error") && r.Failure == "":
			return fail(IDStepResult, "call %d: a %s result carried no failure text", c.Seq, r.Result)
		}
	}
	if n == 0 {
		return fail(IDStepResult, "no step result was submitted, so none could be measured")
	}
	return ok(IDStepResult, "%d step results, each one of pass, fail, error, skip with a duration and, where failing, failure text", n)
}

func traceparent(in Input, f facts) Result {
	byTrace := map[string]proto.StepStarted{}
	for _, s := range f.issued {
		byTrace[proto.TraceID(s.Traceparent)] = s
	}
	hits := 0
	for _, c := range in.Calls {
		if c.Service != stubs.ServiceWorld || !strings.HasPrefix(c.Path, "/sut/") {
			continue
		}
		hits++
		got := c.Header.Get(proto.HeaderTraceparent)
		s, found := byTrace[proto.TraceID(got)]
		switch {
		case !found || got != s.Traceparent:
			return fail(IDTraceparent, "the system under test received traceparent %q (call %d), which the core never handed out", got, c.Seq)
		case c.Header.Get(proto.HeaderBaggage) != s.Baggage:
			return fail(IDTraceparent, "the system under test received baggage %q (call %d); the core handed out %q for step %q", c.Header.Get(proto.HeaderBaggage), c.Seq, s.Baggage, s.StepID)
		}
	}
	if hits == 0 {
		return fail(IDTraceparent, "the system under test received no request, so the handed-out traceparent was never seen to arrive")
	}
	return ok(IDTraceparent, "%d requests reached the system under test, each with exactly the traceparent and baggage the core handed out", hits)
}

func evidenceDigest(in Input, f facts) Result {
	n := 0
	for _, c := range in.Calls {
		if !isEvidence(c) {
			continue
		}
		n++
		declared := c.Path[strings.LastIndex(c.Path, "/")+1:]
		if got := proto.SHA256Digest(c.Body); got != "sha256:"+declared {
			return fail(IDEvidenceDigest, "call %d declared sha256:%s and its %d bytes hash to %s", c.Seq, declared, len(c.Body), got)
		}
		if k := c.Header.Get(proto.HeaderEvidenceKind); !contains(proto.EvidenceKinds, k) {
			return fail(IDEvidenceDigest, "call %d declared evidence kind %q, outside screenshot, log, artifact", c.Seq, k)
		}
		if step := c.Header.Get(proto.HeaderStepID); !hasStep(f.issued, step) {
			return fail(IDEvidenceDigest, "call %d carried %s %q, which is not a step the core accepted; evidence belongs to a started step", c.Seq, proto.HeaderStepID, step)
		}
	}
	if n == 0 {
		return fail(IDEvidenceDigest, "no evidence was attached, so no digest could be measured")
	}
	return ok(IDEvidenceDigest, "%d evidence uploads, each declaring the SHA-256 of its own bytes", n)
}

// requiredEventKinds are the kinds every adapter emits at least once in a run that has a step.
var requiredEventKinds = []string{"run.opened", "step.started", "step.finished", "run.closed"}

// stepEventKinds are the kinds that belong to a step: each names the step and carries the
// correlation_id the core returned when it started that step.
var stepEventKinds = map[string]bool{"step.started": true, "step.finished": true, "evidence.stored": true}

func eventsValid(in Input, f facts) Result {
	n := 0 // every emitted event; the kinds map below holds only the accepted ones
	kinds := map[string]bool{}
	byID := map[string][]byte{} // event_id -> the body first sent under it (ruling 16: one id, one event)
	for _, c := range in.Calls {
		if !isEvent(c) {
			continue
		}
		n++
		var head struct {
			Kind          string `json:"kind"`
			RunID         string `json:"run_id"`
			TestRunID     string `json:"test_run_id"`
			StepID        string `json:"step_id"`
			CorrelationID string `json:"correlation_id"`
			EventID       string `json:"event_id"`
		}
		_ = json.Unmarshal(c.Body, &head) // a body that is not JSON fails validation just below
		if c.Status == http.StatusAccepted {
			kinds[head.Kind] = true // an event the core refused never reached the timeline
		}
		if err := in.Validator.ValidateEvent(c.Body); err != nil {
			return fail(IDEventsValid, "event in call %d (kind %q) is not a valid tests.event/v1: %s", c.Seq, head.Kind, firstLine(err.Error()))
		}
		if prev, seen := byID[head.EventID]; seen && !bytes.Equal(prev, c.Body) {
			return fail(IDEventsValid, "event in call %d (kind %q) reuses event_id %q for a different event; an event_id names one event within the test run (re-sending the same document is allowed)", c.Seq, head.Kind, head.EventID)
		}
		byID[head.EventID] = c.Body
		if f.opened != nil && (head.RunID != f.opened.RunID || head.TestRunID != f.opened.TestRunID) {
			return fail(IDEventsValid, "event in call %d names run %q / test run %q, not this run's %q / %q", c.Seq, head.RunID, head.TestRunID, f.opened.RunID, f.opened.TestRunID)
		}
		if stepEventKinds[head.Kind] {
			if s, issued := f.issued[head.StepID]; !issued || s.CorrelationID != head.CorrelationID {
				return fail(IDEventsValid, "event in call %d (kind %q) names step %q with correlation_id %q, which is not the one the core returned when it started that step", c.Seq, head.Kind, head.StepID, head.CorrelationID)
			}
		}
	}
	if n == 0 {
		return fail(IDEventsValid, "no event was emitted, so none could be validated")
	}
	for _, k := range requiredEventKinds {
		if !kinds[k] {
			return fail(IDEventsValid, "no event of kind %q was accepted by the core (status 202)", k)
		}
	}
	return ok(IDEventsValid, "%d events, each valid against tests.event/v1 and carrying this run's ids; kinds %s all seen", n, strings.Join(requiredEventKinds, ", "))
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func runResultSchema(in Input, f facts) Result {
	if in.ResultRaw == nil {
		return fail(IDRunResultSchema, "the adapter wrote no run result file (%s)", proto.EnvResultFile)
	}
	if err := in.Validator.ValidateRunResult(in.ResultRaw); err != nil {
		return fail(IDRunResultSchema, "the run result is not a valid tests.run-result/v1: %s", firstLine(err.Error()))
	}
	var r proto.RunResult
	if err := json.Unmarshal(in.ResultRaw, &r); err != nil {
		return fail(IDRunResultSchema, "the run result could not be read: %v", err)
	}
	switch {
	case f.opened == nil || f.closeResult == nil:
		return fail(IDRunResultSchema, "the core never accepted a close, so there is no run result to compare with")
	case r.RunID != f.opened.RunID || r.TestRunID != f.opened.TestRunID:
		return fail(IDRunResultSchema, "the run result names %s/%s, not this run's %s/%s", r.RunID, r.TestRunID, f.opened.RunID, f.opened.TestRunID)
	case r.EventsDigest != f.closeResult.EventsDigest:
		return fail(IDRunResultSchema, "events_digest %s differs from the one the core returned at close, %s", r.EventsDigest, f.closeResult.EventsDigest)
	case r.EvidenceLine != proto.EvidenceLine(r.RunID, r.TestRunID, r.Verdict, r.EventsDigest):
		return fail(IDRunResultSchema, "evidence_line does not read the run result's own run_id, test_run_id, verdict and events_digest")
	case !hasLine(in.Stdout, r.EvidenceLine):
		return fail(IDRunResultSchema, "the evidence line is not on the adapter's standard output as a line of its own")
	}
	return ok(IDRunResultSchema, "the run result validates against tests.run-result/v1, repeats the core's events_digest, and its evidence line is on standard output")
}

func hasLine(out, line string) bool {
	for _, l := range strings.Split(out, "\n") {
		if strings.TrimRight(l, "\r") == line {
			return true
		}
	}
	return false
}

func runResultCounts(in Input, f facts) Result {
	for _, c := range in.Calls {
		var ref proto.Refusal
		if isClose(c) && c.Status == http.StatusUnprocessableEntity && json.Unmarshal(c.Response, &ref) == nil && ref.ReasonCode == proto.ReasonResultMismatch {
			return fail(IDRunResultCounts, "call %d closed the test run with a verdict or counts the core's own tally of the accepted results contradicts, and the core refused it: %s", c.Seq, firstLine(ref.Message))
		}
	}
	var r proto.RunResult
	if in.ResultRaw == nil || json.Unmarshal(in.ResultRaw, &r) != nil {
		return fail(IDRunResultCounts, "there is no readable run result, so its counts could not be compared with the steps")
	}
	tally := proto.Counts{}
	for _, c := range in.Calls {
		if !isFinish(c) || c.Status != http.StatusNoContent { // a refused result is not counted (ruling 1)
			continue
		}
		var s proto.StepResult
		if json.Unmarshal(c.Body, &s) != nil {
			continue
		}
		switch s.Result {
		case "pass":
			tally.Passed++
		case "fail":
			tally.Failed++
		case "error":
			tally.Errored++
		case "skip":
			tally.Skipped++
		}
	}
	tally.Total = len(f.issued)
	got := r.Counts
	switch {
	case got.Passed+got.Failed+got.Errored+got.Skipped != got.Total:
		return fail(IDRunResultCounts, "counts %+v: passed+failed+errored+skipped is not total", got)
	case got != tally:
		return fail(IDRunResultCounts, "counts %+v differ from the steps the core saw %+v (total = steps started, the rest = results submitted)", got, tally)
	case r.Verdict != proto.VerdictOf(got):
		return fail(IDRunResultCounts, "verdict %q does not follow from the counts %+v (error outranks fail, fail outranks pass)", r.Verdict, got)
	}
	return ok(IDRunResultCounts, "counts %+v equal the steps the core saw, and verdict %q follows from them", got, r.Verdict)
}

func credentialSent(in Input, f facts) Result {
	if f.opened == nil {
		return fail(IDCredentialSent, "no run was opened, so no credential was issued and none could be measured")
	}
	runCred := f.opened.RunCredential
	if in.Mode == stubs.ModeLeg {
		runCred = in.LegToken
	}
	n := 0
	for _, c := range in.Calls {
		var want string
		switch {
		case c.Service == stubs.ServiceCore && (c.Path == "/v1/spec" || isOpen(c)):
			continue
		case isAttach(c):
			want = in.LegToken
		case c.Service == stubs.ServiceCore:
			want = f.opened.Credential
		case c.Service == stubs.ServiceMocks && c.Method == "POST" && c.Path == "/v1/runs":
			continue
		case c.Service == stubs.ServiceMocks:
			want = runCred
		default:
			continue
		}
		n++
		if got := c.Header.Get("Authorization"); got != "Bearer "+want {
			return fail(IDCredentialSent, "call %d (%s %s) did not present the run's bearer credential", c.Seq, c.Method, redactPath(c.Path))
		}
	}
	if n == 0 {
		return fail(IDCredentialSent, "no call that needs a credential was made")
	}
	return ok(IDCredentialSent, "all %d calls that need a credential presented the credential of their own run", n)
}

func redactPath(p string) string {
	if i := strings.IndexByte(p, '?'); i >= 0 {
		return p[:i]
	}
	return p
}

func credentialLeak(in Input, f facts) Result {
	if len(f.secrets) == 0 {
		return fail(IDCredentialLeak, "no credential was issued, so nothing could be searched for")
	}
	surfaces := map[string]string{"standard output": in.Stdout, "standard error": in.Stderr, "the run result file": string(in.ResultRaw)}
	for _, c := range in.Calls {
		if isEvent(c) {
			surfaces[fmt.Sprintf("the event in call %d", c.Seq)] = string(c.Body)
		}
	}
	for name, text := range surfaces {
		for _, s := range f.secrets {
			if strings.Contains(text, s) {
				return fail(IDCredentialLeak, "a credential issued for this run appears in %s", name)
			}
		}
	}
	return ok(IDCredentialLeak, "none of %d credentials appears in any event, summary, the run result or the adapter's output", len(f.secrets))
}

// sampleComposition measures, from the results the core accepted and the requests the stand-in
// system received, that the sample project holds what section 12 requires of it (ruling 14).
func sampleComposition(in Input, _ facts) Result {
	seen := map[string]int{}
	for _, c := range in.Calls {
		if !isFinish(c) || c.Status != http.StatusNoContent {
			continue
		}
		var r proto.StepResult
		if json.Unmarshal(c.Body, &r) == nil {
			seen[r.Result]++
		}
	}
	sut := count(in, func(c stubs.Call) bool { return c.Service == stubs.ServiceWorld && strings.HasPrefix(c.Path, "/sut/") })
	for _, want := range []string{"pass", "fail", "skip"} {
		if seen[want] == 0 {
			return fail(IDComposition, "no accepted step result is %q; the sample project must hold a passing, a failing and a skipped test (accepted results: %v)", want, seen)
		}
	}
	if sut == 0 {
		return fail(IDComposition, "the stand-in system under test received no request; at least one test of the sample project must call it")
	}
	return ok(IDComposition, "the accepted results hold %d pass, %d fail, %d skip, and %d requests reached the stand-in system", seen["pass"], seen["fail"], seen["skip"], sut)
}

// refusalNotRetried measures ruling 15: a refused call (a 4xx with a Refusal document) is a
// decision, so the same request is never sent again. A changed request is a new call.
func refusalNotRetried(in Input, _ facts) Result {
	type key struct{ service, method, path, body string }
	refused := map[key]int{} // the request -> the seq of the call that was refused
	n := 0
	for _, c := range in.Calls {
		if c.Service == stubs.ServiceWorld {
			continue
		}
		k := key{string(c.Service), c.Method, c.Path, string(c.Body)}
		if first, again := refused[k]; again {
			return fail(IDRefusalRetried, "call %d (%s %s) repeats call %d, which was refused; a refusal is a decision and is never retried unchanged", c.Seq, c.Method, redactPath(c.Path), first)
		}
		var ref proto.Refusal
		if c.Status >= 400 && c.Status < 500 && json.Unmarshal(c.Response, &ref) == nil && ref.ReasonCode != "" {
			refused[k] = c.Seq
			n++
		}
	}
	return ok(IDRefusalRetried, "%d calls were refused by name and none was sent again unchanged", n)
}

func mockControl(in Input, _ facts) Result {
	via := 0
	for _, c := range in.Calls {
		if c.Service == stubs.ServiceWorld && strings.HasPrefix(c.Path, "/mock/") {
			return fail(IDMockControl, "call %d (%s %s) reached a mock's own URL; a mock is controlled only through the Mocks Studio core", c.Seq, c.Method, redactPath(c.Path))
		}
		if c.Service == stubs.ServiceMocks {
			via++
		}
	}
	return ok(IDMockControl, "%d calls observed, none to a mock's own URL; %d went through the Mocks Studio core", len(in.Calls), via)
}
