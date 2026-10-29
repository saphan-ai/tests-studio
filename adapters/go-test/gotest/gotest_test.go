package gotest_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/saphan-ai/tests-studio/adapters/go-test/gotest"
	"github.com/saphan-ai/tests-studio/adapters/go-test/reporter"
)

func TestParseList(t *testing.T) {
	cases := []struct {
		name   string
		output string
		want   []reporter.Item
	}{
		{"two packages", "TestA\nTestB\nok  \texample.test/a\t0.002s\nTestC\nok  \texample.test/b\t0.003s\n",
			[]reporter.Item{{Package: "example.test/a", Name: "TestA"}, {Package: "example.test/a", Name: "TestB"}, {Package: "example.test/b", Name: "TestC"}}},
		{"examples and fuzz are items, benchmarks are not", "TestA\nExampleA\nFuzzA\nBenchmarkA\nok  \texample.test/a\t0.1s\n",
			[]reporter.Item{{Package: "example.test/a", Name: "TestA"}, {Package: "example.test/a", Name: "ExampleA"}, {Package: "example.test/a", Name: "FuzzA"}}},
		{"no test files", "?   \texample.test/a\t[no test files]\n", nil},
		{"empty", "", nil},
		{"a package without tests among others", "ok  \texample.test/a\t0.1s\nTestB\nok  \texample.test/b\t0.1s\n",
			[]reporter.Item{{Package: "example.test/b", Name: "TestB"}}},
		{"build failure becomes one item of the package", "# example.test/a\na_test.go:3:1: syntax error\nFAIL\texample.test/a [build failed]\nTestB\nok  \texample.test/b\t0.1s\n",
			[]reporter.Item{
				{Package: "example.test/a", ListFailure: "# example.test/a\na_test.go:3:1: syntax error\nFAIL\texample.test/a [build failed]"},
				{Package: "example.test/b", Name: "TestB"}}},
		{"the closing FAIL line without a package is not an item", "TestA\nok  \texample.test/a\t0.1s\nFAIL\n",
			[]reporter.Item{{Package: "example.test/a", Name: "TestA"}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := gotest.ParseList(c.output)
			if len(got) != len(c.want) {
				t.Fatalf("got %d items %+v, want %d %+v", len(got), got, len(c.want), c.want)
			}
			for i := range got {
				if got[i] != c.want[i] {
					t.Errorf("item %d: got %+v, want %+v", i, got[i], c.want[i])
				}
			}
		})
	}
}

func events(t *testing.T, evs ...map[string]any) []byte {
	t.Helper()
	var out []byte
	for _, e := range evs {
		b, err := json.Marshal(e)
		if err != nil {
			t.Fatal(err)
		}
		out = append(append(out, b...), '\n')
	}
	return out
}

func TestClassify(t *testing.T) {
	out := func(test, s string) map[string]any {
		return map[string]any{"Action": "output", "Package": "p", "Test": test, "Output": s}
	}
	done := func(test, action string, elapsed float64) map[string]any {
		return map[string]any{"Action": action, "Package": "p", "Test": test, "Elapsed": elapsed}
	}
	cases := []struct {
		name         string
		input        []byte
		wantResult   string
		wantFailure  string // a substring; empty means the failure must be empty
		wantDuration time.Duration
	}{
		{"pass", events(t, out("TestX", "=== RUN TestX\n"), done("TestX", "pass", 0.25)), reporter.ResultPass, "", 250 * time.Millisecond},
		{"skip", events(t, out("TestX", "skipped\n"), done("TestX", "skip", 0)), reporter.ResultSkip, "", 0},
		{"fail carries the test's own output", events(t, out("TestX", "    x_test.go:9: boom\n"), done("TestX", "fail", 0.01)), reporter.ResultFail, "boom", 10 * time.Millisecond},
		{"fail carries a subtest's output", events(t, out("TestX/inner", "inner text\n"), done("TestX/inner", "fail", 0), done("TestX", "fail", 0)), reporter.ResultFail, "inner text", 0},
		{"another test's output is not this test's", events(t, out("TestY", "other text\n"), done("TestX", "fail", 0)), reporter.ResultFail, "", 0},
		{"a panic is an error", events(t, out("TestX", "panic: boom\n"), done("TestX", "fail", 0)), reporter.ResultError, "panic: boom", 0},
		{"no result is an error with the package's output", events(t, out("", "FAIL p [build failed]\n"), done("", "fail", 0)), reporter.ResultError, "build failed", 5 * time.Second},
		{"a line that is not an event is kept as output", []byte("not json: build error\n"), reporter.ResultError, "build error", 5 * time.Second},
		{"empty output is an error", nil, reporter.ResultError, "", 5 * time.Second},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := gotest.Classify("TestX", c.input, 5*time.Second)
			if got.Result != c.wantResult || got.Duration != c.wantDuration {
				t.Fatalf("got %+v, want result %s duration %s", got, c.wantResult, c.wantDuration)
			}
			if c.wantFailure == "" && c.wantResult == reporter.ResultFail && got.Failure != "" {
				t.Errorf("failure %q, want none", got.Failure)
			}
			if c.wantFailure != "" && !strings.Contains(got.Failure, c.wantFailure) {
				t.Errorf("failure %q does not contain %q", got.Failure, c.wantFailure)
			}
		})
	}
}

// TestRunnerOverRealGoTests drives the real go command over the fixture packages: no socket is
// involved, so it runs wherever a Go toolchain does.
func TestRunnerOverRealGoTests(t *testing.T) {
	env := reporter.Scrub(append(os.Environ(), "MOCKS_RUN_TOKEN=leg-secret-token", "TESTS_RUN_TOKEN=child-secret-token"))
	probeOut := filepath.Join(t.TempDir(), "probe.json")
	env = append(env, "ENVPROBE_OUT="+probeOut)
	r := gotest.Runner{Packages: []string{"../testdata/envprobe", "../testdata/sample"}, Environ: env}
	items, err := r.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]reporter.Item{}
	for _, it := range items {
		byName[filepath.Base(it.Package)+"."+it.Name] = it
	}
	if len(items) != 9 {
		t.Fatalf("listed %d items %+v, want 5 of envprobe and 4 of sample", len(items), items)
	}
	traceparent := "00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01"
	handed := []string{"TESTS_STEP_TRACEPARENT=" + traceparent, "TESTS_STEP_BAGGAGE=mocks.run=r1,mocks.step=s1"}
	want := map[string]string{
		"envprobe.TestReportsItsEnvironment": reporter.ResultPass, "envprobe.TestAlsoPasses": reporter.ResultPass,
		"envprobe.TestSkipsToo": reporter.ResultSkip, "envprobe.TestFailsInSubtest": reporter.ResultFail, "envprobe.TestPanics": reporter.ResultError,
		"sample.TestPasses": reporter.ResultPass, "sample.TestFails": reporter.ResultFail, "sample.TestSkipped": reporter.ResultSkip,
		"sample.TestCallsSystemUnderTest": reporter.ResultSkip, // TESTS_CONFORMANCE_SUT_URL is not set here
	}
	for key, result := range want {
		it, ok := byName[key]
		if !ok {
			t.Errorf("%s was not listed", key)
			continue
		}
		got := r.Run(context.Background(), it, handed)
		if got.Result != result {
			t.Errorf("%s: result %s, want %s (failure %q)", key, got.Result, result, got.Failure)
		}
		if (result == reporter.ResultFail || result == reporter.ResultError) && got.Failure == "" {
			t.Errorf("%s: %s without failure text", key, got.Result)
		}
	}
	raw, err := os.ReadFile(probeOut)
	if err != nil {
		t.Fatal("the probe test did not run: ", err)
	}
	var probe struct {
		Traceparent, Baggage string
		OK                   bool
		TokenVariables       []string `json:"token_variables"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		t.Fatal(err)
	}
	if !probe.OK || probe.Traceparent != traceparent || probe.Baggage != "mocks.run=r1,mocks.step=s1" {
		t.Errorf("the test was handed %+v, want the step's traceparent and baggage unchanged", probe)
	}
	if len(probe.TokenVariables) != 0 {
		t.Errorf("the test inherited credential variables %v", probe.TokenVariables)
	}
}

func TestRunnerReportsABuildFailureAsAnErrorItem(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.test/broken\n\ngo 1.26\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "broken_test.go"), []byte("package broken\n\nfunc TestX(t *testing.T) {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := gotest.Runner{Packages: []string{"./..."}, Environ: os.Environ(), Dir: dir}
	items, err := r.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].ListFailure == "" {
		t.Fatalf("items %+v, want one item standing for the package that did not build", items)
	}
	got := r.Run(context.Background(), items[0], nil)
	if got.Result != reporter.ResultError || !strings.Contains(got.Failure, "undefined") {
		t.Errorf("got %+v, want an error naming the compile failure", got)
	}
}
