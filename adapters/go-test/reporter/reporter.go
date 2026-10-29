// Package reporter is the protocol half of the Go test adapter: it speaks the Tests Studio test
// runner specification v1 (spec/TEST_RUNNER_V1.md) and knows nothing about how a Go test is
// started. A Runner supplies the test items and runs them; package gotest is the Runner that uses
// the `go` command.
package reporter

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/saphan-ai/tests-studio/adapters/go-test/tracectx"
)

// Adapter identity, written into every event and into the run result.
const (
	Name        = "tests-go-test"
	Version     = "0.1.0"
	SpecVersion = "v1"
)

// Exit statuses of Run.
const (
	ExitPass    = 0 // every test passed or was skipped, and every call to the core was accepted
	ExitFail    = 1 // the verdict is fail or error, or a call to the core was refused
	ExitRefused = 2 // the adapter did not start: bad environment, no test items, or a spec version the core does not serve
	ExitCore    = 3 // the core could not be reached, or it refused to open or close the test run
)

const (
	httpTimeout      = 30 * time.Second
	maxResponseBytes = 8 << 20
	maxSummaryRunes  = 512
	maxFailureBytes  = 8 << 10
	maxEvidenceBytes = 1 << 20
	maxStepIDRunes   = 128
	timeLayout       = "2006-01-02T15:04:05.000Z"
	redacted         = "[redacted]"
	minSecretLen     = 4
)

// Environment variables of specification section 4.
const (
	envCoreURL    = "TESTS_CORE_URL"
	envResultFile = "TESTS_RESULT_FILE"
	envLegRunID   = "MOCKS_RUN_ID"
	envLegToken   = "MOCKS_RUN_TOKEN"
	envRunToken   = "TESTS_RUN_TOKEN"
)

// Test results, as the specification names them.
const (
	ResultPass  = "pass"
	ResultFail  = "fail"
	ResultError = "error"
	ResultSkip  = "skip"
)

// Item is one test item. Package and Name together identify it; ListFailure is set instead of a
// test name when listing the package failed, and then the item stands for the package itself.
type Item struct {
	Package     string
	Name        string
	ListFailure string
}

// Outcome is what running one item produced.
type Outcome struct {
	Result   string // one of the Result constants
	Duration time.Duration
	Failure  string // text for fail and error; empty otherwise
	Output   string // everything the test printed, attached to its step as log evidence
}

// Runner lists test items and runs them one at a time.
type Runner interface {
	// List returns every test item of the project, in a stable order.
	List(ctx context.Context) ([]Item, error)
	// Run starts one item with env (KEY=value entries) added to its environment and waits for it.
	Run(ctx context.Context, it Item, env []string) Outcome
}

// Config is one run of the adapter.
type Config struct {
	Environ   []string // KEY=value entries of the adapter's own environment
	Runner    Runner
	Transport http.RoundTripper // nil for the default transport
	Project   string
	Mocks     []string // mocks to ask for when opening a standalone test run
	Stdout    io.Writer
	Stderr    io.Writer
}

// Scrub returns environ without the credentials of section 4, so a test never inherits them.
func Scrub(environ []string) []string {
	out := make([]string, 0, len(environ))
	for _, kv := range environ {
		k, _, _ := strings.Cut(kv, "=")
		switch k {
		case envLegToken, envRunToken, tracectx.EnvTraceparent, tracectx.EnvBaggage:
			continue
		}
		out = append(out, kv)
	}
	return out
}

type reporter struct {
	cfg     Config
	env     map[string]string
	client  *http.Client
	coreURL string
	secrets []string

	opened   openedRun
	events   int
	refusals int // calls the core refused; reported, and they make the exit status non-zero
}

// Run executes the whole conversation of specification section 1 and returns the exit status.
func Run(ctx context.Context, cfg Config) int {
	r := &reporter{cfg: cfg, env: map[string]string{}, client: &http.Client{Transport: cfg.Transport, Timeout: httpTimeout}}
	for _, kv := range cfg.Environ {
		if k, v, ok := strings.Cut(kv, "="); ok {
			r.env[k] = v
		}
	}
	for _, name := range []string{envLegToken, envRunToken} {
		r.addSecret(r.env[name])
	}
	r.coreURL = strings.TrimRight(r.env[envCoreURL], "/")
	if r.coreURL == "" {
		r.logf("%s is not set, so there is no Tests Studio core to report to. NEXT — set it to the core's base URL.", envCoreURL)
		return ExitRefused
	}
	if code := r.negotiate(ctx); code != ExitPass {
		return code
	}
	items, err := cfg.Runner.List(ctx)
	if err != nil {
		r.logf("the test items could not be listed: %v. NEXT — run `go test -list .` on the same packages and fix what it prints.", err)
		return ExitRefused
	}
	if len(items) == 0 {
		r.logf("no test item was found in the given packages, so no test run was opened. NEXT — name a package that holds tests.")
		return ExitRefused
	}
	started := time.Now().UTC()
	if code := r.open(ctx); code != ExitPass {
		return code
	}
	r.emit(ctx, "run.opened", "", "", "test run opened for project "+cfg.Project, "", nil)
	var counts Counts
	seen := map[string]bool{}
	for _, it := range items {
		r.runItem(ctx, it, stepID(it, seen), &counts)
	}
	return r.finish(ctx, counts, started)
}

// negotiate asks which spec versions the core serves, before any other call, and refuses by name
// when it serves none this adapter speaks.
func (r *reporter) negotiate(ctx context.Context) int {
	var doc specDocument
	status, err := r.call(ctx, http.MethodGet, r.coreURL+"/v1/spec", "", nil, nil, &doc)
	if err != nil || status != http.StatusOK {
		r.logf("the core's GET /v1/spec could not be read (status %d, error %v), so no version was agreed. NEXT — check %s.", status, err, envCoreURL)
		return ExitCore
	}
	for _, v := range doc.Versions {
		if v == SpecVersion {
			return ExitPass
		}
	}
	r.logf("refused: the core serves spec versions [%s] and this adapter speaks %s. NEXT — use an adapter that speaks one of them, or a core that serves %s.", strings.Join(doc.Versions, ", "), SpecVersion, SpecVersion)
	return ExitRefused
}

// open attaches to the leg's run when the environment names one, and opens a standalone test run
// otherwise. It never opens a run inside a leg.
func (r *reporter) open(ctx context.Context) int {
	hdr := map[string]string{headerSpecVersion: SpecVersion}
	var status int
	var err error
	if legRun := r.env[envLegRunID]; legRun != "" {
		status, err = r.call(ctx, http.MethodPost, r.coreURL+"/v1/runs/"+url.PathEscape(legRun)+"/test-runs", r.env[envLegToken], hdr,
			openRequest{Project: r.cfg.Project}, &r.opened)
	} else {
		status, err = r.call(ctx, http.MethodPost, r.coreURL+"/v1/test-runs", "", hdr,
			openRequest{Project: r.cfg.Project, Mocks: r.cfg.Mocks}, &r.opened)
	}
	if err != nil || status != http.StatusCreated || r.opened.TestRunID == "" || r.opened.Credential == "" {
		r.logf("the core did not open or attach the test run (status %d, error %v). NEXT — read the refusal above, or check %s and %s.", status, err, envCoreURL, envLegRunID)
		return ExitCore
	}
	r.addSecret(r.opened.Credential)
	r.addSecret(r.opened.RunCredential)
	return ExitPass
}

func (r *reporter) testRunURL() string {
	return r.coreURL + "/v1/test-runs/" + url.PathEscape(r.opened.TestRunID)
}

// runItem is one test item: start a step, hand its trace context to the test, finish the step.
func (r *reporter) runItem(ctx context.Context, it Item, id string, counts *Counts) {
	display := it.display()
	var ss stepStarted
	status, err := r.call(ctx, http.MethodPost, r.testRunURL()+"/steps", r.opened.Credential, nil, startStepRequest{StepID: id, Name: display}, &ss)
	if err != nil || status != http.StatusCreated || ss.Traceparent == "" {
		r.refusals++
		r.logf("the step for %s was not started (status %d, error %v), so the test was not run and is not in the result.", display, status, err)
		return
	}
	r.emit(ctx, "step.started", id, ss.CorrelationID, "step "+display+" started", "", nil)
	out := r.cfg.Runner.Run(ctx, it, []string{tracectx.EnvTraceparent + "=" + ss.Traceparent, tracectx.EnvBaggage + "=" + ss.Baggage})
	r.attachLog(ctx, id, ss.CorrelationID, display, out.Output)
	failure := ""
	if out.Result == ResultFail || out.Result == ResultError {
		failure = r.redact(truncate(out.Failure, maxFailureBytes))
		if strings.TrimSpace(failure) == "" {
			failure = "the test did not pass and left no output"
		}
	}
	ms := out.Duration.Milliseconds()
	if ms < 0 {
		ms = 0
	}
	status, err = r.call(ctx, http.MethodPost, r.testRunURL()+"/step-results", r.opened.Credential, nil,
		stepResult{StepID: id, Result: out.Result, DurationMS: ms, Failure: failure}, nil)
	if err != nil || status/100 != 2 {
		r.refusals++
		r.logf("the result of step %s was not accepted (status %d, error %v).", display, status, err)
		return
	}
	counts.add(out.Result)
	r.emit(ctx, "step.finished", id, ss.CorrelationID, "step "+display+" finished: "+out.Result, out.Result, nil)
}

// attachLog stores what the test printed as log evidence of its step. The evidence is addressed by
// the SHA-256 of the exact bytes sent, which are the output with every credential removed.
func (r *reporter) attachLog(ctx context.Context, stepID, correlation, display, output string) {
	data := []byte(r.redact(truncate(output, maxEvidenceBytes)))
	if len(data) == 0 {
		return
	}
	sum := sha256.Sum256(data)
	digest := hex.EncodeToString(sum[:])
	hdr := map[string]string{headerEvidenceKind: "log", headerStepID: stepID, "Content-Type": "text/plain; charset=utf-8"}
	status, _, err := r.send(ctx, http.MethodPut, r.testRunURL()+"/evidence/"+digest, r.opened.Credential, hdr, data)
	if err != nil || status/100 != 2 {
		r.refusals++
		r.logf("the log evidence of %s was not accepted (status %d, error %v).", display, status, err)
		return
	}
	r.emit(ctx, "evidence.stored", stepID, correlation, "log evidence stored for step "+display, "",
		&eventPayload{Redacted: true, Digest: "sha256:" + digest, Size: len(data)})
}

// emit posts one tests.event/v1 document. A refused event is counted and never fatal.
func (r *reporter) emit(ctx context.Context, kind, stepID, correlation, summary, outcome string, payload *eventPayload) {
	r.events++
	ev := event{
		Schema: "tests.event/v1", EventID: fmt.Sprintf("%s-evt-%04d", r.opened.TestRunID, r.events),
		At: time.Now().UTC().Format(timeLayout), RunID: r.opened.RunID, TestRunID: r.opened.TestRunID,
		StepID: stepID, CorrelationID: correlation, Kind: kind,
		Emitter: identity{Name: Name, Version: Version, Spec: SpecVersion},
		Summary: r.redact(truncateRunes(summary, maxSummaryRunes)), Outcome: outcome, Payload: payload,
	}
	status, err := r.call(ctx, http.MethodPost, r.testRunURL()+"/events", r.opened.Credential, nil, ev, nil)
	if err != nil || status/100 != 2 {
		r.refusals++
		r.logf("the event %s was not accepted (status %d, error %v).", kind, status, err)
	}
}

// finish emits run.closed, closes the test run, publishes the run result and prints its evidence line.
func (r *reporter) finish(ctx context.Context, counts Counts, started time.Time) int {
	verdict := counts.verdict()
	// Closing revokes the credential, so run.closed must come first (specification section 9).
	r.emit(ctx, "run.closed", "", "", "test run closed with verdict "+verdict, verdict, nil)
	req := closeRequest{
		Verdict: verdict, Counts: counts, StartedAt: started.Format(timeLayout), EndedAt: time.Now().UTC().Format(timeLayout),
		Adapter: identity{Name: Name, Version: Version, Spec: SpecVersion},
	}
	var rr runResult
	status, raw, err := r.do(ctx, http.MethodPost, r.testRunURL()+"/close", r.opened.Credential, nil, req)
	if err == nil && status == http.StatusOK {
		err = json.Unmarshal(raw, &rr)
	}
	if err != nil || status != http.StatusOK || rr.EvidenceLine == "" {
		r.logf("the core did not close the test run with a run result (status %d, error %v). NEXT — read the refusal above.", status, err)
		return ExitCore
	}
	if path := r.env[envResultFile]; path != "" {
		if err := os.WriteFile(path, append(bytes.TrimRight(raw, "\n"), '\n'), 0o600); err != nil {
			r.logf("the run result could not be written to %s: %v.", envResultFile, err)
			return ExitCore
		}
	}
	fmt.Fprintln(r.cfg.Stdout, rr.EvidenceLine)
	if r.refusals > 0 {
		r.logf("%d calls were refused or failed; the run result may not show every test.", r.refusals)
	}
	if verdict != ResultPass || r.refusals > 0 {
		return ExitFail
	}
	return ExitPass
}

// call sends a JSON request and decodes a JSON answer into out. A status of 400 or more is not an
// error: the refusal's message is printed and the status returned.
func (r *reporter) call(ctx context.Context, method, target, token string, hdr map[string]string, body, out any) (int, error) {
	status, raw, err := r.do(ctx, method, target, token, hdr, body)
	if err != nil {
		return status, err
	}
	if status < 400 && out != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			return status, fmt.Errorf("the answer to %s %s is not the document the specification describes: %w", method, redactURL(target), err)
		}
	}
	return status, nil
}

func (r *reporter) do(ctx context.Context, method, target, token string, hdr map[string]string, body any) (int, []byte, error) {
	var payload []byte
	if body != nil {
		var err error
		if payload, err = json.Marshal(body); err != nil {
			return 0, nil, err
		}
		hdr = withHeader(hdr, "Content-Type", "application/json")
	}
	return r.send(ctx, method, target, token, hdr, payload)
}

func withHeader(hdr map[string]string, k, v string) map[string]string {
	out := map[string]string{k: v}
	for hk, hv := range hdr {
		out[hk] = hv
	}
	return out
}

// send sends payload as the request body, whatever its bytes are, and returns the status and the answer.
func (r *reporter) send(ctx context.Context, method, target, token string, hdr map[string]string, payload []byte) (int, []byte, error) {
	reader := io.Reader(http.NoBody) // a request always has a body to read, so an in-process transport never meets a nil one
	if payload != nil {
		reader = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, target, reader)
	if err != nil {
		return 0, nil, errors.New(r.redact(err.Error()))
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := r.client.Do(req)
	if err != nil {
		return 0, nil, errors.New(r.redact(err.Error()))
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return resp.StatusCode, nil, errors.New(r.redact(err.Error()))
	}
	if resp.StatusCode >= 400 {
		var ref refusal
		if json.Unmarshal(raw, &ref) == nil && ref.ReasonCode != "" {
			r.logf("refused [%s]: %s", ref.ReasonCode, ref.Message)
		} else {
			r.logf("the core answered %s %s with status %d", method, redactURL(target), resp.StatusCode)
		}
	}
	return resp.StatusCode, raw, nil
}

func (r *reporter) addSecret(s string) {
	if len(s) >= minSecretLen {
		r.secrets = append(r.secrets, s)
	}
}

// redact removes every credential the adapter has held from s.
func (r *reporter) redact(s string) string {
	for _, secret := range r.secrets {
		s = strings.ReplaceAll(s, secret, redacted)
	}
	return s
}

// logf writes one line to the adapter's own error output, credentials removed.
func (r *reporter) logf(format string, args ...any) {
	fmt.Fprintln(r.cfg.Stderr, Name+": "+r.redact(fmt.Sprintf(format, args...)))
}

func (it Item) display() string {
	if it.Name == "" {
		return it.Package + " (package)"
	}
	return it.Package + "." + it.Name
}

// stepID is the item's step_id: its package and name, unique in the run. An id longer than the
// specification allows is cut and made unique again by a counter.
func stepID(it Item, seen map[string]bool) string {
	id := it.display()
	if rs := []rune(id); len(rs) > maxStepIDRunes {
		id = string(rs[:maxStepIDRunes-8])
	}
	base := id
	for n := 2; seen[id]; n++ {
		id = fmt.Sprintf("%s~%d", base, n)
	}
	seen[id] = true
	return id
}

func truncateRunes(s string, n int) string {
	if rs := []rune(s); len(rs) > n {
		return string(rs[:n-1]) + "…"
	}
	return s
}

// truncate cuts s to at most n bytes at a rune boundary and says so.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	const mark = "\n…[truncated]"
	cut := n - len(mark)
	for cut > 0 && !isRuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + mark
}

func isRuneStart(b byte) bool { return b&0xC0 != 0x80 }

func redactURL(u string) string {
	if i := strings.IndexByte(u, '?'); i >= 0 {
		return u[:i]
	}
	return u
}
