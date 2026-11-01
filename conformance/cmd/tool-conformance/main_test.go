package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

const (
	recordSchemaPath = "../../../spec/schemas/tool-invocation-v1.schema.json"
	fixturesRootPath = "../../testdata/tool-fixtures"
)

// TestMain lets this test binary stand in for a real tool: when it is started as
// "<test binary> __fake-tool__ <mode> <arg>", it behaves as the fake tool and never runs the tests.
func TestMain(m *testing.M) {
	if len(os.Args) > 3 && os.Args[1] == "__fake-tool__" {
		os.Exit(fakeTool(os.Args[2], os.Args[3]))
	}
	os.Exit(m.Run())
}

const fakeJUnit = `<?xml version="1.0" encoding="UTF-8"?>
<testsuites>
  <testsuite name="gopassfail" tests="2" failures="1">
    <testcase classname="gopassfail" name="TestAddPasses"/>
    <testcase classname="gopassfail" name="TestSubFails"><failure message="Sub(5, 3) = 8, want 2"/></testcase>
  </testsuite>
</testsuites>
`

const fakeSARIF = `{"version":"2.1.0","runs":[{"tool":{"driver":{"name":"fake"}},"results":[]}]}`

// fakeTool writes a report and exits 1, the way a tool that found something does.
func fakeTool(mode, arg string) int {
	switch mode {
	case "junit":
		os.WriteFile(arg, []byte(fakeJUnit), 0o644)
	case "sarif":
		os.WriteFile(arg, []byte(fakeSARIF), 0o644)
	case "sarif-trailing":
		os.WriteFile(arg, []byte(`{"version":"2.1.0","runs":[{}]} x`), 0o644)
	case "json-trailing":
		os.WriteFile(arg, []byte(`{"a":1} x`), 0o644)
	case "cyclonedx-trailing":
		os.WriteFile(arg, []byte(`{"bomFormat":"CycloneDX","specVersion":"1.5"} x`), 0o644)
	case "junit-at-root":
		// arg is {{repo_root}}: it must be a directory holding the fixture, and the tool runs inside it.
		if _, err := os.Stat(filepath.Join(arg, "calc.go")); err != nil {
			fmt.Fprintln(os.Stderr, "repo_root does not hold the fixture:", err)
			return 3
		}
		if wd, _ := os.Getwd(); !sameDir(wd, arg) {
			fmt.Fprintln(os.Stderr, "cwd is not repo_root:", wd, arg)
			return 3
		}
		os.WriteFile("report.xml", []byte(fakeJUnit), 0o644)
	case "sleep":
		var wait chan struct{}
		<-wait
	}
	return 1
}

func sameDir(a, b string) bool {
	ai, err1 := os.Stat(a)
	bi, err2 := os.Stat(b)
	return err1 == nil && err2 == nil && os.SameFile(ai, bi)
}

func testExe(t *testing.T) (path, sum string) {
	t.Helper()
	p, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	s, err := fileSHA256(p)
	if err != nil {
		t.Fatal(err)
	}
	return p, s
}

// validRecord is the one valid record every control mutates. Its tool is this test binary in fake-tool mode,
// which writes JUnit for go-pass-fail's expected outcome (one test passes, one fails; exit code 1).
func validRecord(t *testing.T) map[string]any {
	exe, sum := testExe(t)
	return map[string]any{
		"schema":        "tool-invocation/v1",
		"id":            "fake-junit",
		"ecosystem":     "go",
		"version":       "1.0.0",
		"pin":           map[string]any{"asset": "fake-tool", "sha256": sum},
		"licence_class": "osi",
		"command":       []any{exe, "__fake-tool__", "junit", "report.xml"},
		"inputs":        []any{"."},
		"egress":        "none",
		"exit_map":      map[string]any{"0": "clean", "1": "findings", "default": "tool-error"},
		"output":        map[string]any{"format": "junit-xml", "path": "report.xml"},
		"fixture":       map[string]any{"repo": "go-pass-fail", "expect": "findings"},
	}
}

// mutate returns a deep copy of a valid record with exactly one change applied.
func mutate(t *testing.T, change func(rec map[string]any)) map[string]any {
	t.Helper()
	b, err := json.Marshal(validRecord(t))
	if err != nil {
		t.Fatal(err)
	}
	var rec map[string]any
	if err := json.Unmarshal(b, &rec); err != nil {
		t.Fatal(err)
	}
	change(rec)
	return rec
}

func sub(rec map[string]any, key string) map[string]any { return rec[key].(map[string]any) }

func runRecord(t *testing.T, rec map[string]any, extra ...string) (int, string) {
	t.Helper()
	b, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	return runRecordFile(t, "record.json", b, extra...)
}

func runRecordFile(t *testing.T, name string, content []byte, extra ...string) (int, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}
	args := append([]string{"--record", path, "--fixtures-root", fixturesRootPath, "--record-schema", recordSchemaPath}, extra...)
	var out bytes.Buffer
	code := run(args, &out)
	return code, out.String()
}

func withAsset(t *testing.T) []string {
	exe, _ := testExe(t)
	return []string{"--asset", exe}
}

// statusLine returns the line for one check letter, or "" when that check printed none.
func statusLine(out, letter string) string {
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, "("+letter+") ") {
			return l
		}
	}
	return ""
}

// problem names one refusal line of check (a): a line the runner printed under the (a) FAIL, sourced from the
// schema ("schema:") or from the runner's own rules ("rule:"), that contains sub. Asserting on a problem line
// rather than on the whole output keeps a test from being satisfied by a remark or a line prefix.
type problem struct{ source, sub string }

func hasProblem(out string, p problem) bool {
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, "    "+p.source+": ") && strings.Contains(l, p.sub) {
			return true
		}
	}
	return false
}

func lastLine(out string) string {
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	return lines[len(lines)-1]
}

func TestValidRecordPasses(t *testing.T) {
	code, out := runRecord(t, validRecord(t), withAsset(t)...)
	if code != 0 {
		t.Fatalf("exit code %d, want 0\n%s", code, out)
	}
	for _, letter := range []string{"a", "b", "c", "d", "e"} {
		if l := statusLine(out, letter); !strings.HasPrefix(l, "("+letter+") PASS:") {
			t.Errorf("check (%s): %q, want PASS\n%s", letter, l, out)
		}
	}
	if !strings.Contains(out, "summary: 5 PASS, 0 FAIL, 0 NOT APPLICABLE, 0 UNMEASURED") {
		t.Errorf("summary line missing or wrong\n%s", out)
	}
}

func TestYAMLRecordPasses(t *testing.T) {
	exe, sum := testExe(t)
	doc := fmt.Sprintf(`schema: tool-invocation/v1
id: fake-junit
ecosystem: go
version: "1.0.0"
pin: {asset: fake-tool, sha256: %s}
licence_class: osi
command: [%q, __fake-tool__, junit, report.xml]
inputs: ["."]
egress: none
exit_map:
  0: clean
  1: findings
  default: tool-error
output: {format: junit-xml, path: report.xml}
fixture: {repo: go-pass-fail, expect: findings}
`, sum, exe)
	code, out := runRecordFile(t, "record.yaml", []byte(doc), "--asset", exe)
	if code != 0 {
		t.Fatalf("exit code %d, want 0\n%s", code, out)
	}
}

func TestPlaceholdersAreBoundToAFreshCopy(t *testing.T) {
	rec := mutate(t, func(r map[string]any) {
		r["command"] = []any{r["command"].([]any)[0], "__fake-tool__", "junit-at-root", "{{repo_root}}"}
	})
	code, out := runRecord(t, rec, withAsset(t)...)
	if code != 0 {
		t.Fatalf("exit code %d, want 0\n%s", code, out)
	}
	if _, err := os.Stat(filepath.Join(fixturesRootPath, "go-pass-fail", "report.xml")); err == nil {
		t.Fatal("the tool wrote into the fixture itself; it must run in a copy")
	}
}

func TestOutputPathPlaceholder(t *testing.T) {
	rec := mutate(t, func(r map[string]any) {
		r["command"] = []any{r["command"].([]any)[0], "__fake-tool__", "junit", "{{output_path}}"}
	})
	if code, out := runRecord(t, rec, withAsset(t)...); code != 0 {
		t.Fatalf("exit code %d, want 0\n%s", code, out)
	}
}

// Each control below mutates exactly one field of the valid record and asserts the check letter and the field named.

func TestNegativeControls(t *testing.T) {
	tests := []struct {
		name   string
		change func(r map[string]any)
		letter string
		later  string // a check letter that must NOT have printed, because the run stops at the first FAIL
		want   []string
	}{
		{
			name:   "command element with a semicolon",
			change: func(r map[string]any) { r["command"].([]any)[3] = "report.xml;true" },
			letter: "a", later: "b",
			want: []string{"command[3]", `';'`, "report.xml;true"},
		},
		{
			name:   "output.format outside the closed list",
			change: func(r map[string]any) { sub(r, "output")["format"] = "spdx-2.3" },
			letter: "a", later: "b",
			want: []string{"output.format", "spdx-2.3", "sarif-2.1.0", "junit-xml", "cyclonedx-1.5", "json"},
		},
		{
			name: "pin.sha256 with one hex digit changed",
			change: func(r map[string]any) {
				s := sub(r, "pin")["sha256"].(string)
				flip := "0"
				if s[0] == '0' {
					flip = "1"
				}
				sub(r, "pin")["sha256"] = flip + s[1:]
			},
			letter: "b", later: "c",
			want: []string{"pin.sha256"},
		},
		{
			name:   "exit_map maps the fixture's code to clean while expect is findings",
			change: func(r map[string]any) { sub(r, "exit_map")["1"] = "clean" },
			letter: "d", later: "e",
			want: []string{"exit_map.1", `"clean"`, "fixture.expect", `"findings"`},
		},
		{
			name:   "output.path names a file the tool does not write",
			change: func(r map[string]any) { sub(r, "output")["path"] = "never-written.xml" },
			letter: "e",
			want:   []string{"output.path", "never-written.xml"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			code, out := runRecord(t, mutate(t, tc.change), withAsset(t)...)
			if code != 1 {
				t.Fatalf("exit code %d, want 1\n%s", code, out)
			}
			l := statusLine(out, tc.letter)
			if !strings.HasPrefix(l, "("+tc.letter+") FAIL:") {
				t.Fatalf("check (%s) is %q, want FAIL\n%s", tc.letter, l, out)
			}
			for _, w := range tc.want {
				if !strings.Contains(out, w) {
					t.Errorf("output does not name %q\n%s", w, out)
				}
			}
			if tc.later != "" && statusLine(out, tc.later) != "" {
				t.Errorf("check (%s) ran after the FAIL in (%s)\n%s", tc.later, tc.letter, out)
			}
			for _, letter := range []string{"a", "b", "c", "d", "e"} {
				if letter < tc.letter && !strings.HasPrefix(statusLine(out, letter), "("+letter+") PASS:") {
					t.Errorf("check (%s), before the mutated one, is not PASS: %q", letter, statusLine(out, letter))
				}
			}
		})
	}
}

func TestUnknownKeyIsRefusedWithTheAllowedList(t *testing.T) {
	rec := mutate(t, func(r map[string]any) { r["shell"] = "sh -c" })
	code, out := runRecord(t, rec, withAsset(t)...)
	if code != 1 || !strings.HasPrefix(statusLine(out, "a"), "(a) FAIL:") {
		t.Fatalf("exit %d\n%s", code, out)
	}
	for _, w := range []string{`unknown key "shell"`, "allowed keys:", "licence_class", "exit_map"} {
		if !strings.Contains(out, w) {
			t.Errorf("output does not contain %q\n%s", w, out)
		}
	}
}

func TestMissingRequiredFieldIsNamed(t *testing.T) {
	rec := mutate(t, func(r map[string]any) { delete(r, "egress") })
	code, out := runRecord(t, rec, withAsset(t)...)
	if code != 1 || !strings.HasPrefix(statusLine(out, "a"), "(a) FAIL:") {
		t.Fatalf("exit %d\n%s", code, out)
	}
	// The egress remark names "egress" on every run, so the field is asserted on the problem lines.
	for _, p := range []problem{{"schema", "'egress'"}, {"rule", "egress: the required key is missing"}} {
		if !hasProblem(out, p) {
			t.Errorf("no %s problem line contains %q\n%s", p.source, p.sub, out)
		}
	}
}

func TestDuplicateYAMLKeyIsRefused(t *testing.T) {
	doc := "id: one-tool\nid: another-tool\n"
	code, out := runRecordFile(t, "record.yaml", []byte(doc))
	if code != 1 || !strings.Contains(out, `duplicate key "id"`) {
		t.Fatalf("exit %d\n%s", code, out)
	}
}

func TestUnknownPlaceholderIsRefused(t *testing.T) {
	rec := mutate(t, func(r map[string]any) { r["command"].([]any)[3] = "{{home_dir}}" })
	code, out := runRecord(t, rec, withAsset(t)...)
	if code != 1 || !strings.Contains(out, "command[3]") || !strings.Contains(out, "{{home_dir}}") {
		t.Fatalf("exit %d\n%s", code, out)
	}
}

func TestEveryMetacharacterIsRefused(t *testing.T) {
	for _, ch := range []string{";", "|", "&", "$", "`", "<", ">", "\n"} {
		rec := mutate(t, func(r map[string]any) { r["command"].([]any)[3] = "a" + ch + "b" })
		code, out := runRecord(t, rec, withAsset(t)...)
		if code != 1 || !strings.Contains(out, "command[3]") || !strings.Contains(out, "metacharacter") {
			t.Errorf("%q: exit %d\n%s", ch, code, out)
		}
	}
}

func TestPathClimbingOutOfTheFixtureIsRefused(t *testing.T) {
	rec := mutate(t, func(r map[string]any) { sub(r, "fixture")["repo"] = "../secret-synthetic" })
	code, out := runRecord(t, rec, withAsset(t)...)
	if code != 1 || !strings.Contains(out, "fixture.repo") || !strings.Contains(out, "'..'") {
		t.Fatalf("exit %d\n%s", code, out)
	}
}

// The honest gaps: what this runner does not measure is said every time.

func TestEgressRemarkIsTheLastLineOfEveryRun(t *testing.T) {
	cases := map[string]map[string]any{
		"passing run":    validRecord(t),
		"failing run":    mutate(t, func(r map[string]any) { sub(r, "exit_map")["1"] = "clean" }),
		"refused record": mutate(t, func(r map[string]any) { delete(r, "id") }),
	}
	for name, rec := range cases {
		t.Run(name, func(t *testing.T) {
			_, out := runRecord(t, rec, withAsset(t)...)
			last := lastLine(out)
			if !strings.HasPrefix(last, "remark: egress was NOT observed") {
				t.Fatalf("last line is %q\n%s", last, out)
			}
		})
	}
}

func TestImagePinIsNotApplicableNeverPass(t *testing.T) {
	rec := mutate(t, func(r map[string]any) {
		r["pin"] = map[string]any{"image": "registry.invalid/tool@sha256:" + strings.Repeat("a", 64)}
	})
	code, out := runRecord(t, rec)
	if code != 0 {
		t.Fatalf("exit code %d, want 0\n%s", code, out)
	}
	l := statusLine(out, "b")
	if !strings.HasPrefix(l, "(b) NOT APPLICABLE: this runner does not pull images") {
		t.Fatalf("check (b) is %q\n%s", l, out)
	}
	if strings.Contains(out, "(b) PASS") {
		t.Fatalf("an image pin was reported as PASS\n%s", out)
	}
	if !strings.Contains(out, "1 NOT APPLICABLE") {
		t.Errorf("summary does not count it separately\n%s", out)
	}
}

func TestAssetPinWithoutAssetFileIsUnmeasured(t *testing.T) {
	code, out := runRecord(t, validRecord(t))
	if code != 0 || !strings.HasPrefix(statusLine(out, "b"), "(b) UNMEASURED:") {
		t.Fatalf("exit %d\n%s", code, out)
	}
}

func sarifRecord(t *testing.T) map[string]any {
	return mutate(t, func(r map[string]any) {
		r["command"] = []any{r["command"].([]any)[0], "__fake-tool__", "sarif", "report.sarif"}
		sub(r, "output")["format"] = "sarif-2.1.0"
		sub(r, "output")["path"] = "report.sarif"
	})
}

func TestSARIFWithoutOutputSchemaIsUnmeasuredNeverPass(t *testing.T) {
	code, out := runRecord(t, sarifRecord(t), withAsset(t)...)
	if code != 0 {
		t.Fatalf("exit code %d, want 0 (UNMEASURED does not fail the run)\n%s", code, out)
	}
	l := statusLine(out, "e")
	if !strings.HasPrefix(l, "(e) UNMEASURED: no schema given") {
		t.Fatalf("check (e) is %q\n%s", l, out)
	}
	if strings.Contains(out, "(e) PASS") {
		t.Fatalf("SARIF without a schema was reported as PASS\n%s", out)
	}
	if !strings.Contains(out, "1 UNMEASURED") {
		t.Errorf("summary does not count it separately\n%s", out)
	}
}

func writeSchema(t *testing.T, body string) (path, sum string) {
	t.Helper()
	path = filepath.Join(t.TempDir(), "output.schema.json")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	sum, err := fileSHA256(path)
	if err != nil {
		t.Fatal(err)
	}
	return path, sum
}

func TestSARIFAgainstAnOutputSchema(t *testing.T) {
	t.Run("validates and prints the schema's sha256", func(t *testing.T) {
		path, sum := writeSchema(t, `{"type":"object","required":["version","runs"]}`)
		code, out := runRecord(t, sarifRecord(t), append(withAsset(t), "--output-schema", path)...)
		if code != 0 || !strings.HasPrefix(statusLine(out, "e"), "(e) PASS:") || !strings.Contains(out, sum) {
			t.Fatalf("exit %d, want PASS naming %s\n%s", code, sum, out)
		}
	})
	t.Run("an output the schema refuses fails", func(t *testing.T) {
		path, _ := writeSchema(t, `{"type":"object","required":["no-such-field"]}`)
		code, out := runRecord(t, sarifRecord(t), append(withAsset(t), "--output-schema", path)...)
		if code != 1 || !strings.HasPrefix(statusLine(out, "e"), "(e) FAIL:") {
			t.Fatalf("exit %d\n%s", code, out)
		}
	})
	t.Run("a schema that does not compile is unmeasured, not a pass", func(t *testing.T) {
		path, _ := writeSchema(t, `{"type": 12}`)
		code, out := runRecord(t, sarifRecord(t), append(withAsset(t), "--output-schema", path)...)
		if code != 0 || !strings.HasPrefix(statusLine(out, "e"), "(e) UNMEASURED:") {
			t.Fatalf("exit %d\n%s", code, out)
		}
	})
}

func TestToolThatNeverFinishesFailsCheckC(t *testing.T) {
	rec := mutate(t, func(r map[string]any) { r["command"].([]any)[2] = "sleep" })
	code, out := runRecord(t, rec, append(withAsset(t), "--timeout", "300ms")...)
	if code != 1 || !strings.HasPrefix(statusLine(out, "c"), "(c) FAIL:") || !strings.Contains(out, "did not finish") {
		t.Fatalf("exit %d\n%s", code, out)
	}
}

func TestUsageErrorsExitTwo(t *testing.T) {
	var out bytes.Buffer
	if code := run([]string{"--record", "x.yaml"}, &out); code != 2 {
		t.Fatalf("missing --fixtures-root: exit %d, want 2", code)
	}
}

func TestJUnitStructure(t *testing.T) {
	tests := []struct {
		name string
		xml  string
		want outcome
	}{
		{"testsuites root", `<testsuites><testsuite name="s"><testcase classname="c" name="n"/></testsuite></testsuites>`, outPass},
		{"testsuite root", `<testsuite name="s"><testcase classname="c" name="n"/></testsuite>`, outPass},
		{"same name in two suites", `<testsuites><testsuite name="a"><testcase classname="c" name="n"/></testsuite><testsuite name="b"><testcase classname="c" name="n"/></testsuite></testsuites>`, outPass},
		{"duplicate pair in one suite", `<testsuite name="s"><testcase classname="c" name="n"/><testcase classname="c" name="n"/></testsuite>`, outFail},
		{"same name, different classname", `<testsuite name="s"><testcase classname="c1" name="n"/><testcase classname="c2" name="n"/></testsuite>`, outPass},
		{"no testcase", `<testsuites><testsuite name="s"/></testsuites>`, outFail},
		{"wrong root", `<report><testcase name="n"/></report>`, outFail},
		{"testcase without a name", `<testsuite name="s"><testcase classname="c"/></testsuite>`, outFail},
		{"not XML", `{"a":1}`, outFail},
		{"truncated XML", `<testsuite name="s"><testcase name="n"/>`, outFail},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got, msg := checkJUnit([]byte(tc.xml)); got != tc.want {
				t.Fatalf("got %v (%s), want %v", got, msg, tc.want)
			}
		})
	}
}

// The synthetic secret: it is found by the fixture's own rule and by no provider's pattern.

var gitleaksRegex = regexp.MustCompile(`(?m)^regex\s*=\s*'''(.+)'''\s*$`)

func fixtureRule(t *testing.T) *regexp.Regexp {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(fixturesRootPath, "secret-synthetic", ".gitleaks.toml"))
	if err != nil {
		t.Fatal(err)
	}
	m := gitleaksRegex.FindSubmatch(raw)
	if m == nil {
		t.Fatal(".gitleaks.toml has no single-line regex = '''...''' rule")
	}
	return regexp.MustCompile(string(m[1]))
}

func TestSyntheticSecretMatchesTheShippedRuleOnce(t *testing.T) {
	re := fixtureRule(t)
	var hits []string
	root := filepath.Join(fixturesRootPath, "secret-synthetic")
	err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		for range re.FindAll(b, -1) {
			hits = append(hits, filepath.Base(p))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0] != "app.conf" {
		t.Fatalf("the rule matches %v, want exactly one match, in app.conf", hits)
	}
}

func TestSyntheticSecretMatchesNoProviderPrefix(t *testing.T) {
	re := fixtureRule(t)
	raw, err := os.ReadFile(filepath.Join(fixturesRootPath, "secret-synthetic", "app.conf"))
	if err != nil {
		t.Fatal(err)
	}
	value := string(re.Find(raw))
	if value == "" {
		t.Fatal("the rule does not match app.conf")
	}
	for _, prefix := range []string{"AKIA", "ASIA", "ghp_", "gho_", "ghs_", "ghu_", "ghr_", "github_pat_", "glpat-", "xox",
		"sk_live_", "rk_live_", "sk-ant-", "sk-proj-", "npm_", "AIza", "-----BEGIN"} {
		if strings.Contains(string(raw), prefix) {
			t.Errorf("app.conf contains the provider prefix %q", prefix)
		}
		if strings.HasPrefix(value, prefix) {
			t.Errorf("the synthetic value starts with the provider prefix %q", prefix)
		}
	}
}
