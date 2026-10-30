package harness_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/saphan-ai/tests-studio/conformance/internal/checks"
	"github.com/saphan-ai/tests-studio/conformance/internal/harness"
	"github.com/saphan-ai/tests-studio/conformance/internal/sampleadapter"
	"github.com/saphan-ai/tests-studio/conformance/internal/schemas"
	"github.com/saphan-ai/tests-studio/conformance/internal/stubs"
)

const sampleItems = 4

// inProcess runs the sample adapter in this process over the in-memory transport, so a whole
// conformance run needs no socket. Args[0] is the command name and is ignored.
type inProcess struct{ rt http.RoundTripper }

func (l inProcess) Launch(_ context.Context, ln harness.Launch) (harness.Output, error) {
	var out, errw bytes.Buffer
	code := sampleadapter.Main(ln.Args[1:], ln.Env, l.rt, &out, &errw)
	return harness.Output{Stdout: out.String(), Stderr: errw.String(), ExitCode: code}, nil
}

func projectFile() string {
	return filepath.Join("..", "..", "testdata", "sample-project", "tests.json")
}

func runInProcess(t *testing.T, mode stubs.Mode, mutant string) harness.Report {
	t.Helper()
	dir, err := schemas.FindSpecDir("", ".")
	if err != nil {
		t.Fatal(err)
	}
	hosts := &harness.MemoryHosts{}
	cmd := []string{"sample-adapter", "--project", projectFile()}
	if mutant != "" {
		cmd = append(cmd, "--mutant", mutant)
	}
	rep, err := harness.Run(context.Background(), harness.Config{
		Command: cmd, Mode: mode, Items: sampleItems, SpecDir: dir,
		Hosts: hosts, Launcher: inProcess{rt: hosts.RoundTripper()}, WorkDir: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return rep
}

func failedIDs(rep harness.Report) []string {
	var ids []string
	for _, r := range rep.Results {
		if !r.Pass {
			ids = append(ids, r.ID)
		}
	}
	return ids
}

func TestSampleAdapterPassesEveryCheck(t *testing.T) {
	t.Parallel()
	for _, mode := range []stubs.Mode{stubs.ModeStandalone, stubs.ModeLeg} {
		t.Run(string(mode), func(t *testing.T) {
			rep := runInProcess(t, mode, "")
			if rep.Exit != harness.ExitPass || len(failedIDs(rep)) != 0 {
				t.Fatalf("exit %d, failed checks %v\n%s", rep.Exit, failedIDs(rep), strings.Join(rep.Lines, "\n"))
			}
			if got := len(rep.Results); got != 15 {
				t.Fatalf("%d checks ran; the specification lists 15", got)
			}
			for _, l := range rep.Lines {
				if !strings.HasPrefix(l, "PASS ") && !strings.HasPrefix(l, "SUMMARY ") && !strings.HasPrefix(l, "remark: ") {
					t.Errorf("unexpected line %q", l)
				}
			}
		})
	}
}

// mutantCases is the mutant → expected exit code → the one check that goes red.
var mutantCases = []struct {
	mutant string
	mode   stubs.Mode
	exit   int
	failed string
}{
	{sampleadapter.MutantSpecV2, stubs.ModeStandalone, harness.ExitRefused, checks.IDSpecVersion},
	{sampleadapter.MutantResultNoStepID, stubs.ModeStandalone, harness.ExitFail, checks.IDStepPerItem},
	{sampleadapter.MutantEvidenceDigest, stubs.ModeStandalone, harness.ExitFail, checks.IDEvidenceDigest},
	{sampleadapter.MutantResultDigest, stubs.ModeStandalone, harness.ExitFail, checks.IDRunResultSchema},
	{sampleadapter.MutantEventNoRunID, stubs.ModeStandalone, harness.ExitFail, checks.IDEventsValid},
	{sampleadapter.MutantCredentialSummary, stubs.ModeStandalone, harness.ExitFail, checks.IDCredentialLeak},
	{sampleadapter.MutantMockControlDirect, stubs.ModeStandalone, harness.ExitFail, checks.IDMockControl},
	{sampleadapter.MutantSecondRunInLeg, stubs.ModeLeg, harness.ExitFail, checks.IDNoSecondRun},
	// v1.1: one mutant per ruling the suite can observe.
	{sampleadapter.MutantCountsMismatch, stubs.ModeStandalone, harness.ExitFail, checks.IDRunResultCounts}, // ruling 1
	{sampleadapter.MutantDuplicateStepID, stubs.ModeStandalone, harness.ExitFail, checks.IDStepPerItem},    // ruling 3
	{sampleadapter.MutantRetryNewStep, stubs.ModeStandalone, harness.ExitFail, checks.IDStepPerItem},       // ruling 4
	{sampleadapter.MutantNeverRanNoStep, stubs.ModeStandalone, harness.ExitFail, checks.IDStepPerItem},     // ruling 5
	{sampleadapter.MutantEvidenceUnknown, stubs.ModeStandalone, harness.ExitFail, checks.IDEvidenceDigest}, // ruling 6
	{sampleadapter.MutantRunClosedLate, stubs.ModeStandalone, harness.ExitFail, checks.IDEventsValid},      // ruling 7
	{sampleadapter.MutantConcurrentAttach, stubs.ModeLeg, harness.ExitFail, checks.IDNoSecondRun},          // ruling 12
	{sampleadapter.MutantSampleNoSkip, stubs.ModeStandalone, harness.ExitFail, checks.IDComposition},       // ruling 14
	{sampleadapter.MutantRetryRefused, stubs.ModeLeg, harness.ExitFail, checks.IDRefusalRetried},           // ruling 15
	{sampleadapter.MutantEventIDReused, stubs.ModeStandalone, harness.ExitFail, checks.IDEventsValid},      // ruling 16
}

// TestMutantFailsExactlyItsCheck is the negative control of the checks: each mutant breaks one
// rule, and exactly that rule's check (and no other) goes red.
func TestMutantFailsExactlyItsCheck(t *testing.T) {
	t.Parallel()
	for _, c := range mutantCases {
		t.Run(c.mutant, func(t *testing.T) {
			rep := runInProcess(t, c.mode, c.mutant)
			got := failedIDs(rep)
			if rep.Exit != c.exit || len(got) != 1 || got[0] != c.failed {
				t.Fatalf("want exit %d and only %s red; got exit %d, red %v\n%s", c.exit, c.failed, rep.Exit, got, strings.Join(rep.Lines, "\n"))
			}
		})
	}
}

// differingMocksURL is a launcher that hands the adapter a MOCKS_CORE_URL different from the one
// the core's attach answer names.
type differingMocksURL struct{ inProcess }

func (l differingMocksURL) Launch(ctx context.Context, ln harness.Launch) (harness.Output, error) {
	for i, kv := range ln.Env {
		if strings.HasPrefix(kv, "MOCKS_CORE_URL=") {
			ln.Env[i] = "MOCKS_CORE_URL=http://another-mocks-core.example.test"
		}
	}
	return l.inProcess.Launch(ctx, ln)
}

// TestAdapterRefusesByNameWhenMocksCoreURLsDiffer pins ruling 9 of v1.1: the sample adapter stops
// with mocks-core-mismatch instead of choosing between two Mocks Studio cores.
func TestAdapterRefusesByNameWhenMocksCoreURLsDiffer(t *testing.T) {
	t.Parallel()
	dir, err := schemas.FindSpecDir("", ".")
	if err != nil {
		t.Fatal(err)
	}
	hosts := &harness.MemoryHosts{}
	rep, err := harness.Run(context.Background(), harness.Config{
		Command: []string{"sample-adapter", "--project", projectFile()}, Mode: stubs.ModeLeg, Items: sampleItems, SpecDir: dir,
		Hosts: hosts, Launcher: differingMocksURL{inProcess{rt: hosts.RoundTripper()}}, WorkDir: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Input.Stderr == "" || !strings.Contains(rep.Input.Stderr, "mocks-core-mismatch") {
		t.Fatalf("the adapter's standard error does not name mocks-core-mismatch: %q", rep.Input.Stderr)
	}
	for _, c := range rep.Calls {
		if strings.Contains(c.Path, "/steps") {
			t.Fatalf("the adapter went on to start a step (call %d) after the mismatch", c.Seq)
		}
	}
}

func TestAdapterThatNeverReachesTheCoreExits3(t *testing.T) {
	t.Parallel()
	dir, err := schemas.FindSpecDir("", ".")
	if err != nil {
		t.Fatal(err)
	}
	rep, err := harness.Run(context.Background(), harness.Config{
		Command: []string{"quiet"}, Mode: stubs.ModeStandalone, Items: sampleItems, SpecDir: dir,
		Hosts: &harness.MemoryHosts{}, Launcher: quietLauncher{},
	})
	if err != nil || rep.Exit != harness.ExitNoAdapter {
		t.Fatalf("exit %d, err %v; want exit %d\n%s", rep.Exit, err, harness.ExitNoAdapter, strings.Join(rep.Lines, "\n"))
	}
}

type quietLauncher struct{}

func (quietLauncher) Launch(context.Context, harness.Launch) (harness.Output, error) {
	return harness.Output{}, nil
}

func TestAdapterCommandThatDoesNotExistExits3(t *testing.T) {
	t.Parallel()
	dir, err := schemas.FindSpecDir("", ".")
	if err != nil {
		t.Fatal(err)
	}
	rep, err := harness.Run(context.Background(), harness.Config{
		Command: []string{filepath.Join(t.TempDir(), "no-such-adapter")}, Mode: stubs.ModeStandalone, Items: sampleItems, SpecDir: dir,
		Hosts: &harness.MemoryHosts{}, Launcher: harness.ExecLauncher{},
	})
	if err != nil || rep.Exit != harness.ExitNoAdapter {
		t.Fatalf("exit %d, err %v; want exit %d\n%s", rep.Exit, err, harness.ExitNoAdapter, strings.Join(rep.Lines, "\n"))
	}
}

// TestChecksReadTheRecording varies the recorded calls and outputs of a passing run, one thing at
// a time, and asserts exactly the check that guards it goes red. It is the check logic, run
// against recorded calls, with no adapter and no socket.
func TestChecksReadTheRecording(t *testing.T) {
	t.Parallel()
	standalone := runInProcess(t, stubs.ModeStandalone, "").Input
	leg := runInProcess(t, stubs.ModeLeg, "").Input

	firstCall := func(in *checks.Input, pred func(stubs.Call) bool) *stubs.Call {
		for i := range in.Calls {
			if pred(in.Calls[i]) {
				return &in.Calls[i]
			}
		}
		t.Fatal("the passing run holds no call of the kind this case varies")
		return nil
	}
	path := func(sub string) func(stubs.Call) bool {
		return func(c stubs.Call) bool { return strings.Contains(c.Path, sub) }
	}
	// editBody rewrites the JSON object a recorded call carried, so a case can drop or change one field.
	editBody := func(c *stubs.Call, edit func(m map[string]any)) {
		var m map[string]any
		if err := json.Unmarshal(c.Body, &m); err != nil {
			t.Fatalf("call %d does not carry a JSON object: %v", c.Seq, err)
		}
		edit(m)
		b, err := json.Marshal(m)
		if err != nil {
			t.Fatal(err)
		}
		c.Body = b
	}
	bodyHas := func(field, value string) func(stubs.Call) bool {
		return func(c stubs.Call) bool {
			var m map[string]any
			return json.Unmarshal(c.Body, &m) == nil && m[field] == value
		}
	}
	finishWith := func(result string) func(stubs.Call) bool {
		return func(c stubs.Call) bool {
			return strings.HasSuffix(c.Path, "/step-results") && bodyHas("result", result)(c)
		}
	}
	eventOfKind := func(kind string) func(stubs.Call) bool {
		return func(c stubs.Call) bool { return strings.HasSuffix(c.Path, "/events") && bodyHas("kind", kind)(c) }
	}
	type variation struct {
		name   string
		failed string
		vary   func(in *checks.Input)
	}
	// Each case below guards one rule the review of the first round found unguarded.
	guardCases := []variation{
		{"a step result without duration_ms", checks.IDStepResult, func(in *checks.Input) {
			editBody(firstCall(in, path("/step-results")), func(m map[string]any) { delete(m, "duration_ms") })
		}},
		{"a step result with a negative duration", checks.IDStepResult, func(in *checks.Input) {
			editBody(firstCall(in, path("/step-results")), func(m map[string]any) { m["duration_ms"] = -1 })
		}},
		{"a failing step result without failure text", checks.IDStepResult, func(in *checks.Input) {
			editBody(firstCall(in, finishWith("fail")), func(m map[string]any) { delete(m, "failure") })
		}},
		{"run.closed refused because it came after closeTestRun", checks.IDEventsValid, func(in *checks.Input) {
			firstCall(in, eventOfKind("run.closed")).Status = http.StatusForbidden
		}},
		{"evidence that names a step the core never started", checks.IDEvidenceDigest, func(in *checks.Input) {
			c := firstCall(in, path("/evidence/"))
			c.Header = c.Header.Clone()
			c.Header.Set("Tests-Step-Id", "no-such-step")
		}},
		{"evidence with no Tests-Step-Id", checks.IDEvidenceDigest, func(in *checks.Input) {
			c := firstCall(in, path("/evidence/"))
			c.Header = c.Header.Clone()
			c.Header.Del("Tests-Step-Id")
		}},
		{"a step event with a valid correlation_id the core did not hand out", checks.IDEventsValid, func(in *checks.Input) {
			editBody(firstCall(in, eventOfKind("step.started")), func(m map[string]any) { m["correlation_id"] = strings.Repeat("ab", 16) })
		}},
		{"a second openTestRun in standalone mode", checks.IDNoSecondRun, func(in *checks.Input) {
			in.Calls = append(in.Calls, stubs.Call{Seq: 998, Service: stubs.ServiceCore, Method: "POST", Path: "/v1/test-runs", Header: http.Header{}, Status: http.StatusConflict})
		}},
	}
	legCases := []variation{
		{"an attach to another run than the leg's", checks.IDRunOpened, func(in *checks.Input) {
			c := firstCall(in, func(c stubs.Call) bool {
				return strings.HasPrefix(c.Path, "/v1/runs/") && strings.HasSuffix(c.Path, "/test-runs")
			})
			c.Path = "/v1/runs/another-run/test-runs"
		}},
	}
	cases := []variation{
		{"a step call without the bearer credential", checks.IDCredentialSent, func(in *checks.Input) {
			c := firstCall(in, path("/steps"))
			c.Header = c.Header.Clone()
			c.Header.Del("Authorization")
		}},
		{"the SUT receives a traceparent the core never handed out", checks.IDTraceparent, func(in *checks.Input) {
			c := firstCall(in, path("/sut/"))
			c.Header = c.Header.Clone()
			c.Header.Set("traceparent", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01")
		}},
		{"evidence bytes differ from the declared digest", checks.IDEvidenceDigest, func(in *checks.Input) {
			c := firstCall(in, path("/evidence/"))
			c.Body = append([]byte("x"), c.Body...)
		}},
		{"counts that do not equal the steps", checks.IDRunResultCounts, func(in *checks.Input) {
			in.ResultRaw = bytes.Replace(in.ResultRaw, []byte(`"total": 4`), []byte(`"total": 5`), 1)
		}},
		{"a close the core refused as run-result-mismatch", checks.IDRunResultCounts, func(in *checks.Input) {
			c := firstCall(in, path("/close"))
			accepted := *c // the adapter's later, corrected close is the accepted one
			accepted.Seq, accepted.Body = 997, append(append([]byte(nil), c.Body...), ' ')
			c.Status = http.StatusUnprocessableEntity
			c.Response = []byte(`{"message":"The close carried counts the core's tally contradicts.","reason_code":"run-result-mismatch"}`)
			in.Calls = append(in.Calls, accepted)
		}},
		{"a skipped test turned into a pass", checks.IDComposition, func(in *checks.Input) {
			editBody(firstCall(in, finishWith("skip")), func(m map[string]any) { m["result"] = "pass" })
			// the run result follows, so only the composition is varied
			in.ResultRaw = bytes.Replace(bytes.Replace(in.ResultRaw, []byte(`"passed": 1`), []byte(`"passed": 2`), 1), []byte(`"skipped": 1`), []byte(`"skipped": 0`), 1)
		}},
		{"a refused call sent again unchanged", checks.IDRefusalRetried, func(in *checks.Input) {
			c := *firstCall(in, func(c stubs.Call) bool { return c.Service == stubs.ServiceMocks })
			c.Status, c.Seq = http.StatusUnprocessableEntity, 990
			c.Response = []byte(`{"message":"refused","reason_code":"request-invalid"}`)
			again := c
			again.Seq = 991
			in.Calls = append(in.Calls, c, again)
		}},
		{"the same event_id on two different events", checks.IDEventsValid, func(in *checks.Input) {
			first := firstCall(in, eventOfKind("run.opened"))
			var id string
			var m map[string]any
			if err := json.Unmarshal(first.Body, &m); err != nil {
				t.Fatal(err)
			}
			id, _ = m["event_id"].(string)
			editBody(firstCall(in, eventOfKind("step.started")), func(m map[string]any) { m["event_id"] = id })
		}},
		{"a step result with no step_id, even when a right one follows", checks.IDStepPerItem, func(in *checks.Input) {
			c := *firstCall(in, path("/step-results"))
			c.Seq, c.Status = 989, http.StatusUnprocessableEntity
			c.Response = []byte(`{"message":"refused","reason_code":"step-id-missing"}`)
			c.Body = []byte(`{"result":"pass","duration_ms":1}`)
			in.Calls = append(in.Calls, c)
		}},
		{"the evidence line is not on standard output", checks.IDRunResultSchema, func(in *checks.Input) { in.Stdout = "" }},
		{"a credential on standard error", checks.IDCredentialLeak, func(in *checks.Input) {
			in.Stderr = "token " + in.LegToken + string(firstCall(in, path("/test-runs")).Response)
		}},
		{"more test items than steps", checks.IDStepPerItem, func(in *checks.Input) { in.Items = sampleItems + 1 }},
		{"the spec version was never asked", checks.IDSpecVersion, func(in *checks.Input) {
			c := firstCall(in, path("/v1/spec"))
			c.Method = "HEAD"
		}},
		{"a call to a mock's own URL", checks.IDMockControl, func(in *checks.Input) {
			in.Calls = append(in.Calls, stubs.Call{Seq: 999, Service: stubs.ServiceWorld, Method: "PUT", Path: "/mock/sample-http/admin/config", Header: http.Header{}})
		}},
		{"no event was emitted", checks.IDEventsValid, func(in *checks.Input) {
			var kept []stubs.Call
			for _, c := range in.Calls {
				if !strings.HasSuffix(c.Path, "/events") {
					kept = append(kept, c)
				}
			}
			in.Calls = kept
		}},
	}
	runCases := func(base checks.Input, list []variation) {
		for _, c := range list {
			t.Run(c.name, func(t *testing.T) {
				in := base
				in.Calls = append([]stubs.Call(nil), base.Calls...)
				in.ResultRaw = append([]byte(nil), base.ResultRaw...)
				// The varied call is copied before it is changed, so the base recording stays intact.
				for i := range in.Calls {
					in.Calls[i].Body = append([]byte(nil), in.Calls[i].Body...)
				}
				c.vary(&in)
				var red []string
				for _, r := range checks.All(in) {
					if !r.Pass {
						red = append(red, r.ID)
					}
				}
				if len(red) != 1 || red[0] != c.failed {
					t.Fatalf("want only %s red, got %v", c.failed, red)
				}
			})
		}
	}
	runCases(standalone, append(cases, guardCases...))
	runCases(leg, legCases)
}
