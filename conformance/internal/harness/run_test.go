package harness_test

import (
	"bytes"
	"context"
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

const sampleItems = 3

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
			if got := len(rep.Results); got != 13 {
				t.Fatalf("%d checks ran; the specification lists 13", got)
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
}

// TestMutantFailsExactlyItsCheck is the negative control of law #27: each mutant breaks one
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
	base := runInProcess(t, stubs.ModeStandalone, "").Input

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
	cases := []struct {
		name   string
		failed string
		vary   func(in *checks.Input)
	}{
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
			in.ResultRaw = bytes.Replace(in.ResultRaw, []byte(`"total": 3`), []byte(`"total": 4`), 1)
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
	for _, c := range cases {
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
