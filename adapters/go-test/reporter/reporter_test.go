package reporter_test

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
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v5"

	"github.com/saphan-ai/tests-studio/adapters/go-test/reporter"
)

const (
	coreURL   = "http://core.example.test"
	testRunID = "tr-fixture"
	legRunID  = "leg-run-1"
	legToken  = "leg-run-credential-xyz"
	testToken = "ttr_fixture-credential-123"
)

// schemaSet compiles the specification's schemas with the validator the conformance suite uses.
type schemaSet struct{ event, result *jsonschema.Schema }

func loadSchemas(t *testing.T) schemaSet {
	t.Helper()
	compile := func(file string) *jsonschema.Schema {
		raw, err := os.ReadFile(filepath.Join("..", "..", "..", "spec", "schemas", file))
		if err != nil {
			t.Fatal(err)
		}
		c := jsonschema.NewCompiler()
		c.Draft = jsonschema.Draft2020
		if err := c.AddResource(file, bytes.NewReader(raw)); err != nil {
			t.Fatal(err)
		}
		s, err := c.Compile(file)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	return schemaSet{event: compile("tests-event-v1.schema.json"), result: compile("tests-run-result-v1.schema.json")}
}

func (s schemaSet) validate(schema *jsonschema.Schema, raw []byte) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var doc any
	if err := dec.Decode(&doc); err != nil {
		return err
	}
	return schema.Validate(doc)
}

type call struct {
	method, path string
	header       http.Header
	body         []byte
}

// fakeCore answers the calls of specification section 6 with the minimum a conversation needs and
// records every call. It is a double of the core, held to the same schemas by the tests.
type fakeCore struct {
	mu       sync.Mutex
	versions []string
	leg      bool
	calls    []call
	closed   bool
	events   []json.RawMessage
	steps    map[string]string // step_id -> traceparent
	results  []map[string]any
	evidence []evidence
}

type evidence struct{ digest, step, kind, body string }

func (f *fakeCore) RoundTrip(req *http.Request) (*http.Response, error) {
	rec := httptest.NewRecorder()
	f.ServeHTTP(rec, req)
	return rec.Result(), nil
}

func (f *fakeCore) refuse(w http.ResponseWriter, status int, code string) {
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"message": "The fake core refused this. NEXT — fix it.", "reason_code": code})
}

func (f *fakeCore) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var body []byte
	if r.Body != nil { // a client request without a body carries a nil Body until a real server hands it one
		body, _ = io.ReadAll(r.Body)
	}
	f.calls = append(f.calls, call{r.Method, r.URL.Path, r.Header.Clone(), body})
	reply := func(status int, v any) {
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(v)
	}
	opened := map[string]any{"test_run_id": testRunID, "run_id": "conf-run-1", "credential": testToken, "started_at": "2026-10-30T00:00:00.000Z", "mocks_core_url": "http://mocks.example.test", "endpoints": map[string]any{}}
	switch {
	case r.URL.Path == "/v1/spec":
		reply(200, map[string]any{"versions": f.versions, "core": map[string]any{"name": "fake-core", "version": "0"}})
	case r.URL.Path == "/v1/test-runs" && r.Method == "POST":
		if f.leg {
			f.refuse(w, 403, "run-open-not-allowed")
			return
		}
		opened["run_credential"] = "run_standalone-credential"
		reply(201, opened)
	case r.URL.Path == "/v1/runs/"+legRunID+"/test-runs":
		if r.Header.Get("Authorization") != "Bearer "+legToken {
			f.refuse(w, 403, "run-credential-invalid")
			return
		}
		reply(201, opened)
	case strings.HasPrefix(r.URL.Path, "/v1/test-runs/"+testRunID+"/"):
		if f.closed || r.Header.Get("Authorization") != "Bearer "+testToken {
			f.refuse(w, 403, "run-credential-invalid")
			return
		}
		f.testRunCall(w, r, body, reply)
	default:
		f.refuse(w, 404, "request-invalid")
	}
}

func (f *fakeCore) testRunCall(w http.ResponseWriter, r *http.Request, body []byte, reply func(int, any)) {
	var doc map[string]any
	_ = json.Unmarshal(body, &doc)
	rest := strings.TrimPrefix(r.URL.Path, "/v1/test-runs/"+testRunID+"/")
	if digest, ok := strings.CutPrefix(rest, "evidence/"); ok {
		sum := sha256.Sum256(body)
		if r.Method != http.MethodPut || digest != hex.EncodeToString(sum[:]) {
			f.refuse(w, 422, "evidence-digest-mismatch")
			return
		}
		f.evidence = append(f.evidence, evidence{digest: digest, step: r.Header.Get("Tests-Step-Id"), kind: r.Header.Get("Tests-Evidence-Kind"), body: string(body)})
		reply(201, map[string]any{})
		return
	}
	switch rest {
	case "steps":
		if f.steps == nil {
			f.steps = map[string]string{}
		}
		id, _ := doc["step_id"].(string)
		trace := fmt.Sprintf("%032x", len(f.steps)+1)
		tp := "00-" + trace + "-00f067aa0ba902b7-01"
		f.steps[id] = tp
		reply(201, map[string]any{"step_id": id, "correlation_id": trace, "traceparent": tp, "baggage": "mocks.run=conf-run-1,mocks.step=" + id})
	case "step-results":
		f.results = append(f.results, doc)
		reply(200, map[string]any{})
	case "events":
		f.events = append(f.events, body)
		reply(202, map[string]any{})
	case "close":
		f.closed = true
		counts, _ := doc["counts"].(map[string]any)
		verdict, _ := doc["verdict"].(string)
		digest := "sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
		reply(200, map[string]any{
			"schema": "tests.run-result/v1", "run_id": "conf-run-1", "test_run_id": testRunID, "project": "example.test/project",
			"verdict": verdict, "counts": counts, "started_at": doc["started_at"], "ended_at": doc["ended_at"], "events_digest": digest,
			"adapter": doc["adapter"], "evidence_line": "- evidence: tests-studio-run conf-run-1/" + testRunID + " verdict=" + verdict + " events=" + digest,
		})
	default:
		f.refuse(w, 404, "request-invalid")
	}
}

func (f *fakeCore) paths() []string {
	var out []string
	for _, c := range f.calls {
		out = append(out, c.method+" "+c.path)
	}
	return out
}

// fakeRunner stands for `go test`: its items and their outcomes are fixed.
type fakeRunner struct {
	items    []reporter.Item
	outcomes map[string]reporter.Outcome
	listErr  error
	handed   map[string][]string
}

func (f *fakeRunner) List(context.Context) ([]reporter.Item, error) { return f.items, f.listErr }

func (f *fakeRunner) Run(_ context.Context, it reporter.Item, env []string) reporter.Outcome {
	if f.handed == nil {
		f.handed = map[string][]string{}
	}
	f.handed[it.Name] = env
	return f.outcomes[it.Name]
}

func sampleRunner() *fakeRunner {
	return &fakeRunner{
		items: []reporter.Item{{Package: "example.test/p", Name: "TestPasses"}, {Package: "example.test/p", Name: "TestFails"}, {Package: "example.test/p", Name: "TestSkipped"}},
		outcomes: map[string]reporter.Outcome{
			"TestPasses":  {Result: "pass", Duration: 12 * time.Millisecond, Output: "=== RUN TestPasses\n--- PASS: TestPasses\n"},
			"TestFails":   {Result: "fail", Duration: 3 * time.Millisecond, Failure: "x_test.go:9: boom", Output: "x_test.go:9: boom\n"},
			"TestSkipped": {Result: "skip", Output: "skipped\n"},
		},
	}
}

type outcome struct {
	code           int
	stdout, stderr string
	resultFile     string
}

func run(t *testing.T, core *fakeCore, runner reporter.Runner, environ ...string) outcome {
	t.Helper()
	resultFile := filepath.Join(t.TempDir(), "result.json")
	var stdout, stderr bytes.Buffer
	code := reporter.Run(context.Background(), reporter.Config{
		Environ: append([]string{"TESTS_CORE_URL=" + coreURL, "TESTS_RESULT_FILE=" + resultFile}, environ...),
		Runner:  runner, Transport: core, Project: "example.test/project", Stdout: &stdout, Stderr: &stderr,
	})
	return outcome{code, stdout.String(), stderr.String(), resultFile}
}

func TestStandaloneRunReportsEveryTest(t *testing.T) {
	schemas := loadSchemas(t)
	core := &fakeCore{versions: []string{"v1"}}
	runner := sampleRunner()
	out := run(t, core, runner)
	if out.code != reporter.ExitFail {
		t.Fatalf("exit %d, want %d (one test fails)\n%s", out.code, reporter.ExitFail, out.stderr)
	}
	want := []string{"GET /v1/spec", "POST /v1/test-runs"}
	if got := core.paths(); len(got) < 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("calls begin %v, want %v", got, want)
	}
	if h := core.calls[1].header.Get("Tests-Spec-Version"); h != "v1" {
		t.Errorf("the open call declared spec version %q", h)
	}
	if len(core.steps) != 3 || len(core.results) != 3 {
		t.Fatalf("%d steps and %d results, want 3 and 3", len(core.steps), len(core.results))
	}
	for _, res := range core.results {
		id, _ := res["step_id"].(string)
		if _, ok := core.steps[id]; !ok {
			t.Errorf("a result names step %q, which was never started", id)
		}
		if _, ok := res["duration_ms"]; !ok {
			t.Errorf("result %v has no duration_ms", res)
		}
	}
	kinds := map[string]bool{}
	for i, raw := range core.events {
		if err := schemas.validate(schemas.event, raw); err != nil {
			t.Errorf("event %d does not validate: %v\n%s", i, err, raw)
		}
		var ev struct{ Kind string }
		_ = json.Unmarshal(raw, &ev)
		kinds[ev.Kind] = true
	}
	for _, k := range []string{"run.opened", "step.started", "step.finished", "run.closed"} {
		if !kinds[k] {
			t.Errorf("no %s event was emitted", k)
		}
	}
	raw, err := os.ReadFile(out.resultFile)
	if err != nil {
		t.Fatal(err)
	}
	if err := schemas.validate(schemas.result, raw); err != nil {
		t.Errorf("the published run result does not validate: %v", err)
	}
	var rr struct {
		Counts reporter.Counts `json:"counts"`
	}
	_ = json.Unmarshal(raw, &rr)
	if rr.Counts != (reporter.Counts{Total: 3, Passed: 1, Failed: 1, Skipped: 1}) {
		t.Errorf("counts %+v", rr.Counts)
	}
	if line := strings.TrimSpace(out.stdout); !strings.HasPrefix(line, "- evidence: tests-studio-run conf-run-1/"+testRunID+" verdict=fail events=sha256:") || strings.Contains(strings.TrimSpace(out.stdout), "\n") {
		t.Errorf("standard output is %q, want exactly the evidence line", out.stdout)
	}
}

func TestEachTestIsHandedItsOwnStepsTraceContext(t *testing.T) {
	core := &fakeCore{versions: []string{"v1"}}
	runner := sampleRunner()
	run(t, core, runner)
	seen := map[string]bool{}
	for name, env := range runner.handed {
		if len(env) != 2 || !strings.HasPrefix(env[0], "TESTS_STEP_TRACEPARENT=00-") || !strings.HasPrefix(env[1], "TESTS_STEP_BAGGAGE=mocks.run=conf-run-1,mocks.step=example.test/p."+name) {
			t.Errorf("%s was handed %v", name, env)
		}
		seen[env[0]] = true
		if want := core.steps["example.test/p."+name]; env[0] != "TESTS_STEP_TRACEPARENT="+want {
			t.Errorf("%s was handed %q, the core gave step %q %q", name, env[0], name, want)
		}
	}
	if len(seen) != 3 {
		t.Errorf("%d distinct traceparents for 3 tests", len(seen))
	}
}

func TestEachTestsOutputIsAttachedAsLogEvidenceOfItsStep(t *testing.T) {
	schemas := loadSchemas(t)
	core := &fakeCore{versions: []string{"v1"}}
	runner := sampleRunner()
	runner.outcomes["TestFails"] = reporter.Outcome{Result: "fail", Failure: "boom", Output: "boom with " + testToken + "\n"}
	run(t, core, runner)
	if len(core.evidence) != 3 {
		t.Fatalf("%d evidence uploads, want 3", len(core.evidence))
	}
	for _, ev := range core.evidence {
		if _, ok := core.steps[ev.step]; !ok || ev.kind != "log" {
			t.Errorf("evidence %+v does not name a started step with kind log", ev)
		}
		if strings.Contains(ev.body, testToken) {
			t.Errorf("evidence of %s holds a credential: %q", ev.step, ev.body)
		}
	}
	stored := 0
	for _, raw := range core.events {
		if !strings.Contains(string(raw), `"evidence.stored"`) {
			continue
		}
		stored++
		if err := schemas.validate(schemas.event, raw); err != nil {
			t.Errorf("evidence.stored does not validate: %v\n%s", err, raw)
		}
		if !strings.Contains(string(raw), `"redacted":true`) {
			t.Errorf("evidence.stored carries its payload unredacted: %s", raw)
		}
	}
	if stored != 3 {
		t.Errorf("%d evidence.stored events, want 3", stored)
	}
}

func TestRunClosedIsEmittedBeforeTheCloseCall(t *testing.T) {
	core := &fakeCore{versions: []string{"v1"}}
	run(t, core, sampleRunner())
	paths := core.paths()
	closeAt, closedEventAt := -1, -1
	for i, c := range core.calls {
		if strings.HasSuffix(c.path, "/close") {
			closeAt = i
		}
		if strings.HasSuffix(c.path, "/events") && strings.Contains(string(c.body), `"run.closed"`) {
			closedEventAt = i
		}
	}
	if closeAt < 0 || closedEventAt < 0 || closedEventAt > closeAt {
		t.Errorf("run.closed at call %d, close at call %d: %v", closedEventAt, closeAt, paths)
	}
}

func TestLegAttachesWithTheLegsCredentialAndNeverOpens(t *testing.T) {
	core := &fakeCore{versions: []string{"v1"}, leg: true}
	out := run(t, core, sampleRunner(), "MOCKS_RUN_ID="+legRunID, "MOCKS_RUN_TOKEN="+legToken)
	if out.code != reporter.ExitFail {
		t.Fatalf("exit %d\n%s", out.code, out.stderr)
	}
	attached := 0
	for _, c := range core.calls {
		if c.method == "POST" && c.path == "/v1/test-runs" {
			t.Fatalf("a run was opened inside a leg: %v", core.paths())
		}
		if c.path == "/v1/runs/"+legRunID+"/test-runs" {
			attached++
			if c.header.Get("Authorization") != "Bearer "+legToken || c.header.Get("Tests-Spec-Version") != "v1" {
				t.Errorf("the attach carried %v", c.header)
			}
		}
	}
	if attached != 1 {
		t.Errorf("%d attach calls, want 1: %v", attached, core.paths())
	}
}

func TestACoreServingOnlyV2IsRefusedByNameBeforeAnythingElse(t *testing.T) {
	core := &fakeCore{versions: []string{"v2"}}
	runner := sampleRunner()
	out := run(t, core, runner)
	if out.code != reporter.ExitRefused {
		t.Errorf("exit %d, want %d", out.code, reporter.ExitRefused)
	}
	if got := core.paths(); len(got) != 1 || got[0] != "GET /v1/spec" {
		t.Errorf("calls %v, want only GET /v1/spec", got)
	}
	if !strings.Contains(out.stderr, "serves spec versions [v2]") || !strings.Contains(out.stderr, "speaks v1") {
		t.Errorf("the refusal does not name both sides: %q", out.stderr)
	}
	if len(runner.handed) != 0 {
		t.Error("a test was run although the version was refused")
	}
}

func TestNothingToReport(t *testing.T) {
	cases := []struct {
		name    string
		environ []string
		runner  *fakeRunner
		want    int
		calls   int
	}{
		{"no core url", []string{"TESTS_CORE_URL="}, sampleRunner(), reporter.ExitRefused, 0},
		{"no test items", nil, &fakeRunner{}, reporter.ExitRefused, 1},
		{"listing failed", nil, &fakeRunner{listErr: errors.New("boom")}, reporter.ExitRefused, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			core := &fakeCore{versions: []string{"v1"}}
			out := run(t, core, c.runner, c.environ...)
			if out.code != c.want || len(core.calls) != c.calls {
				t.Errorf("exit %d after %d calls %v, want %d after %d\n%s", out.code, len(core.calls), core.paths(), c.want, c.calls, out.stderr)
			}
		})
	}
}

func TestCredentialsNeverReachAnyOutput(t *testing.T) {
	core := &fakeCore{versions: []string{"v1"}, leg: true}
	runner := sampleRunner()
	// A test that prints a credential it was never given still cannot get one into the failure text
	// if it guessed one the adapter holds: the text is redacted before it is sent.
	runner.outcomes["TestFails"] = reporter.Outcome{Result: "fail", Failure: "token was " + testToken + " and " + legToken + " and " + "child-token-value"}
	out := run(t, core, runner, "MOCKS_RUN_ID="+legRunID, "MOCKS_RUN_TOKEN="+legToken, "TESTS_RUN_TOKEN=child-token-value")
	secrets := []string{testToken, legToken, "child-token-value", "run_standalone-credential"}
	seen := map[string]string{"stdout": out.stdout, "stderr": out.stderr}
	for i, c := range core.calls {
		if !strings.HasSuffix(c.path, "/test-runs") && !strings.HasPrefix(c.path, "/v1/runs/") { // calls that carry the credential in the header only
			seen[fmt.Sprintf("body of call %d %s", i, c.path)] = string(c.body)
		}
	}
	if raw, err := os.ReadFile(out.resultFile); err == nil {
		seen["result file"] = string(raw)
	}
	for where, text := range seen {
		for _, s := range secrets {
			if strings.Contains(text, s) {
				t.Errorf("%s contains a credential: %q", where, text)
			}
		}
	}
	var failure string
	for _, res := range core.results {
		if f, _ := res["failure"].(string); f != "" {
			failure = f
		}
	}
	if !strings.Contains(failure, "[redacted]") {
		t.Errorf("failure text %q was not redacted", failure)
	}
	for _, env := range runner.handed {
		if strings.Contains(strings.Join(env, " "), "TOKEN") {
			t.Errorf("a test was handed %v", env)
		}
	}
}

func TestScrubDropsCredentialsAndStaleStepContext(t *testing.T) {
	got := reporter.Scrub([]string{"PATH=/bin", "MOCKS_RUN_TOKEN=a", "TESTS_RUN_TOKEN=b", "TESTS_STEP_TRACEPARENT=c", "TESTS_STEP_BAGGAGE=d", "MOCKS_RUN_ID=r"})
	if strings.Join(got, ",") != "PATH=/bin,MOCKS_RUN_ID=r" {
		t.Errorf("got %v", got)
	}
}

func TestStepIDsAreUniqueAndBounded(t *testing.T) {
	long := strings.Repeat("a", 300)
	core := &fakeCore{versions: []string{"v1"}}
	runner := &fakeRunner{
		items:    []reporter.Item{{Package: long, Name: "TestX"}, {Package: long, Name: "TestX"}, {Package: "p", Name: "TestX"}},
		outcomes: map[string]reporter.Outcome{"TestX": {Result: "pass"}},
	}
	run(t, core, runner)
	if len(core.steps) != 3 {
		t.Fatalf("%d distinct step ids for 3 items", len(core.steps))
	}
	for id := range core.steps {
		if n := len([]rune(id)); n < 1 || n > 128 {
			t.Errorf("step id of %d characters", n)
		}
	}
}

func TestAnErrorResultMakesTheVerdictError(t *testing.T) {
	core := &fakeCore{versions: []string{"v1"}}
	runner := sampleRunner()
	runner.outcomes["TestSkipped"] = reporter.Outcome{Result: "error", Failure: "panic: boom"}
	out := run(t, core, runner)
	if out.code != reporter.ExitFail || !strings.Contains(out.stdout, "verdict=error") {
		t.Errorf("exit %d, stdout %q", out.code, out.stdout)
	}
}

func TestAPassingRunExitsZero(t *testing.T) {
	core := &fakeCore{versions: []string{"v1"}}
	runner := &fakeRunner{items: []reporter.Item{{Package: "p", Name: "TestA"}, {Package: "p", Name: "TestB"}},
		outcomes: map[string]reporter.Outcome{"TestA": {Result: "pass"}, "TestB": {Result: "skip"}}}
	out := run(t, core, runner)
	if out.code != reporter.ExitPass || !strings.Contains(out.stdout, "verdict=pass") {
		t.Errorf("exit %d, stdout %q, stderr %q", out.code, out.stdout, out.stderr)
	}
}
