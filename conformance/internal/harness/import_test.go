package harness_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/saphan-ai/tests-studio/conformance/internal/checks"
	"github.com/saphan-ai/tests-studio/conformance/internal/harness"
	"github.com/saphan-ai/tests-studio/conformance/internal/proto"
	"github.com/saphan-ai/tests-studio/conformance/internal/schemas"
	"github.com/saphan-ai/tests-studio/conformance/internal/stubs"
)

const importSummary = "SUMMARY 15 checks, 13 passed, 0 failed, 2 not applicable (import profile)"

// TestImportProfileAcceptsTheSampleAdapter pins rulings 3 and 5: the sample adapter reporting
// after the run is accepted in both modes, exactly two checks answer NOT APPLICABLE (never PASS,
// never dropped) and the summary line names them.
func TestImportProfileAcceptsTheSampleAdapter(t *testing.T) {
	t.Parallel()
	for _, mode := range []stubs.Mode{stubs.ModeStandalone, stubs.ModeLeg} {
		t.Run(string(mode), func(t *testing.T) {
			rep := runProfile(t, mode, checks.ProfileImport, "")
			if rep.Exit != harness.ExitPass || len(failedIDs(rep)) != 0 {
				t.Fatalf("exit %d, failed %v\n%s", rep.Exit, failedIDs(rep), strings.Join(rep.Lines, "\n"))
			}
			if got := len(rep.Results); got != 15 {
				t.Fatalf("%d checks ran; the import profile runs every check", got)
			}
			want := []string{checks.IDTraceparent, checks.IDComposition}
			if got := notApplicableIDs(rep); strings.Join(got, ",") != strings.Join(want, ",") {
				t.Fatalf("not applicable %v; want exactly %v\n%s", got, want, strings.Join(rep.Lines, "\n"))
			}
			for _, r := range rep.Results {
				if r.NotApplicable && (r.Pass || !strings.Contains(r.Measured, "the import profile reports after the run, so no test could carry a traceparent")) {
					t.Errorf("%s: a not-applicable result must not be a pass and must carry the reason; got %+v", r.ID, r)
				}
			}
			if !contains(rep.Lines, importSummary) {
				t.Fatalf("the summary line %q is missing\n%s", importSummary, strings.Join(rep.Lines, "\n"))
			}
		})
	}
}

// TestLiveRunNeverPrintsNotApplicable pins ruling 3's last sentence and ruling 4: a live run's
// output keeps the v1.1 shape, byte for byte.
func TestLiveRunNeverPrintsNotApplicable(t *testing.T) {
	t.Parallel()
	for _, mode := range []stubs.Mode{stubs.ModeStandalone, stubs.ModeLeg} {
		rep := runInProcess(t, mode, "")
		for _, l := range rep.Lines {
			if strings.Contains(l, "NOT APPLICABLE") || strings.Contains(l, "not applicable") || strings.Contains(l, "import") {
				t.Errorf("%s mode: a live run printed %q", mode, l)
			}
		}
		if want := "SUMMARY 15 checks, 15 passed, 0 failed (" + string(mode) + " mode)"; !contains(rep.Lines, want) {
			t.Errorf("%s mode: the live summary %q is missing\n%s", mode, want, strings.Join(rep.Lines, "\n"))
		}
	}
}

// TestProfilesJudgeEachAdapterByItsOwnBar is ruling 6's negative control: a live adapter (whose
// tests do reach the system under test with the right traceparent) judged as import passes, and an
// import adapter judged as live still FAILS the two checks it cannot keep.
func TestProfilesJudgeEachAdapterByItsOwnBar(t *testing.T) {
	t.Parallel()
	for _, mode := range []stubs.Mode{stubs.ModeStandalone, stubs.ModeLeg} {
		t.Run(string(mode)+"/a live adapter judged as import passes", func(t *testing.T) {
			rep := runAs(t, mode, checks.ProfileLive, checks.ProfileImport, "")
			if rep.Exit != harness.ExitPass || len(failedIDs(rep)) != 0 || len(notApplicableIDs(rep)) != 2 {
				t.Fatalf("exit %d, failed %v, not applicable %v\n%s", rep.Exit, failedIDs(rep), notApplicableIDs(rep), strings.Join(rep.Lines, "\n"))
			}
			sut := 0
			for _, c := range rep.Calls {
				if c.Service == stubs.ServiceWorld && strings.HasPrefix(c.Path, "/sut/") {
					sut++
				}
			}
			if sut == 0 {
				t.Fatal("the live adapter sent no request to /sut/, so this does not show that a correlated request is still accepted")
			}
		})
		t.Run(string(mode)+"/an import adapter judged as live still fails", func(t *testing.T) {
			rep := runAs(t, mode, checks.ProfileImport, checks.ProfileLive, "")
			want := checks.IDTraceparent + "," + checks.IDComposition
			if rep.Exit != harness.ExitFail || strings.Join(failedIDs(rep), ",") != want || len(notApplicableIDs(rep)) != 0 {
				t.Fatalf("exit %d, failed %v; want exit %d and %s red\n%s", rep.Exit, failedIDs(rep), harness.ExitFail, want, strings.Join(rep.Lines, "\n"))
			}
		})
	}
}

// TestImportAdapterReportsTheFileAsItIs pins ruling 2: the report is made from the result file —
// the real duration and the failure text per item, in order, startStep before finishStep, and the
// file itself as evidence — and no traceparent is handed to the system under test.
func TestImportAdapterReportsTheFileAsItIs(t *testing.T) {
	t.Parallel()
	project := filepath.Join(t.TempDir(), "project.json")
	doc := `{"project":"timed","tests":[
	  {"name":"TestPasses","outcome":"pass","duration_ms":1250,"calls_sut":true},
	  {"name":"TestFails","outcome":"fail","duration_ms":37,"failure":"expected 3, got 4","calls_sut":true},
	  {"name":"TestSkipped","outcome":"skip"},
	  {"name":"TestFixtureFails","outcome":"error","duration_ms":4,"failure":"fixture setup failed: the test body never ran"}]}`
	if err := os.WriteFile(project, []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	dir, err := schemas.FindSpecDir("", ".")
	if err != nil {
		t.Fatal(err)
	}
	hosts := &harness.MemoryHosts{}
	rep, err := harness.Run(context.Background(), harness.Config{
		Command: []string{"sample-adapter", "--project", project, "--profile", "import"}, Mode: stubs.ModeStandalone, Profile: checks.ProfileImport,
		Items: 4, SpecDir: dir, Hosts: hosts, Launcher: inProcess{rt: hosts.RoundTripper()}, WorkDir: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Exit != harness.ExitPass {
		t.Fatalf("exit %d\n%s", rep.Exit, strings.Join(rep.Lines, "\n"))
	}
	type step struct {
		result   string
		duration int64
		failure  string
	}
	want := []step{{"pass", 1250, ""}, {"fail", 37, "expected 3, got 4"}, {"skip", 0, ""}, {"error", 4, "fixture setup failed: the test body never ran"}}
	var order []string // start and finish calls, in the order the core saw them
	var got []step
	evidence := 0
	for _, c := range rep.Calls {
		switch {
		case c.Service == stubs.ServiceWorld:
			t.Errorf("the import adapter sent a request to the world (%s %s)", c.Method, c.Path)
		case c.Method == "POST" && strings.HasSuffix(c.Path, "/steps"):
			order = append(order, "start")
		case c.Method == "POST" && strings.HasSuffix(c.Path, "/step-results"):
			order = append(order, "finish")
			var r proto.StepResult
			if err := json.Unmarshal(c.Body, &r); err != nil || r.DurationMS == nil {
				t.Fatalf("call %d: unreadable result %q (%v)", c.Seq, c.Body, err)
			}
			got = append(got, step{r.Result, *r.DurationMS, r.Failure})
		case c.Method == "PUT" && strings.Contains(c.Path, "/evidence/"):
			evidence++
			if k := c.Header.Get(proto.HeaderEvidenceKind); k != "artifact" || !strings.Contains(string(c.Body), "<testcase") {
				t.Errorf("evidence kind %q, body %q; want the JUnit file as an artifact", k, c.Body)
			}
		}
	}
	if strings.Join(order, " ") != strings.TrimSpace(strings.Repeat("start finish ", 4)) {
		t.Errorf("the core saw %v; want startStep then finishStep for each of the 4 items in turn", order)
	}
	if len(got) != len(want) {
		t.Fatalf("%d results; want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("item %d reported %+v; the file says %+v", i, got[i], want[i])
		}
	}
	if evidence != 1 {
		t.Errorf("%d evidence uploads; want the result file once", evidence)
	}
}

func contains(lines []string, want string) bool {
	for _, l := range lines {
		if l == want {
			return true
		}
	}
	return false
}
