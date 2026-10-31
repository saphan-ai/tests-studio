package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// runIn runs the runner on rec against the fixtures under root, with the extra flags and no implicit schema flag.
func runIn(t *testing.T, rec map[string]any, root string, extra ...string) (int, string) {
	t.Helper()
	b, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "record.json")
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	code := run(append([]string{"--record", path, "--fixtures-root", root}, extra...), &out)
	return code, out.String()
}

func TestEmbeddedSchemaIsTheSpecSchema(t *testing.T) {
	spec, err := os.ReadFile(recordSchemaPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(spec, embeddedRecordSchema) {
		t.Fatal("the schema embedded in the runner differs from spec/schemas/tool-invocation-v1.schema.json; copy the spec file over it")
	}
}

// A record schema named with --record-schema is identified by its digest, and it cannot weaken the runner's own
// rules: the schema key and the required keys hold even when the file is `{}`.
func TestRecordSchemaOverrideIsIdentifiedAndCannotWeakenTheRules(t *testing.T) {
	path, sum := writeSchema(t, `{}`)
	t.Run("a valid record names the digest of the file that judged it", func(t *testing.T) {
		code, out := runRecord(t, validRecord(t), append(withAsset(t), "--record-schema", path)...)
		l := statusLine(out, "a")
		if code != 0 || !strings.HasPrefix(l, "(a) PASS:") || !strings.Contains(l, sum) {
			t.Fatalf("exit %d, (a) line %q must be PASS naming sha256 %s\n%s", code, l, sum, out)
		}
	})
	t.Run("a record without egress fails, naming egress", func(t *testing.T) {
		rec := mutate(t, func(r map[string]any) { delete(r, "egress") })
		code, out := runRecord(t, rec, append(withAsset(t), "--record-schema", path)...)
		if code != 1 || !strings.HasPrefix(statusLine(out, "a"), "(a) FAIL:") || !hasProblem(out, problem{"rule", "egress: the required key is missing"}) {
			t.Fatalf("exit %d\n%s", code, out)
		}
		if !strings.Contains(statusLine(out, "a"), sum) {
			t.Errorf("the (a) line does not name the schema file's sha256 %s\n%s", sum, out)
		}
	})
	t.Run("schema v9 fails, naming schema", func(t *testing.T) {
		rec := mutate(t, func(r map[string]any) { r["schema"] = "tool-invocation/v9" })
		code, out := runRecord(t, rec, append(withAsset(t), "--record-schema", path)...)
		if code != 1 || !hasProblem(out, problem{"rule", `schema: tool-invocation/v9 is not "tool-invocation/v1"`}) {
			t.Fatalf("exit %d\n%s", code, out)
		}
	})
	t.Run("an exit_map without default fails, naming it", func(t *testing.T) {
		rec := mutate(t, func(r map[string]any) { delete(sub(r, "exit_map"), "default") })
		code, out := runRecord(t, rec, append(withAsset(t), "--record-schema", path)...)
		if code != 1 || !hasProblem(out, problem{"rule", "exit_map.default: the required key is missing"}) {
			t.Fatalf("exit %d\n%s", code, out)
		}
	})
}

// With no --record-schema the runner uses the schema it carries, from any working directory.
func TestDefaultSchemaIsEmbeddedAndNeedsNoWorkingDirectory(t *testing.T) {
	root, err := filepath.Abs(fixturesRootPath)
	if err != nil {
		t.Fatal(err)
	}
	exe, _ := testExe(t)
	t.Chdir(t.TempDir())
	code, out := runIn(t, validRecord(t), root, "--asset", exe)
	sum := sha256.Sum256(embeddedRecordSchema)
	l := statusLine(out, "a")
	if code != 0 || !strings.HasPrefix(l, "(a) PASS:") || !strings.Contains(l, "embedded schema (sha256 "+hex.EncodeToString(sum[:])+")") {
		t.Fatalf("exit %d, (a) line %q\n%s", code, l, out)
	}
}

// Check (e) reads one JSON value and then the end of the file.
func TestTrailingDataAfterTheFirstJSONValueIsRefused(t *testing.T) {
	path, _ := writeSchema(t, `{}`)
	for _, tc := range []struct{ format, mode string }{
		{"sarif-2.1.0", "sarif-trailing"},
		{"json", "json-trailing"},
		{"cyclonedx-1.5", "cyclonedx-trailing"},
	} {
		t.Run(tc.format, func(t *testing.T) {
			rec := mutate(t, func(r map[string]any) {
				r["command"] = []any{r["command"].([]any)[0], "__fake-tool__", tc.mode, "out.json"}
				sub(r, "output")["format"] = tc.format
				sub(r, "output")["path"] = "out.json"
			})
			code, out := runRecord(t, rec, append(withAsset(t), "--output-schema", path)...)
			l := statusLine(out, "e")
			if code != 1 || !strings.HasPrefix(l, "(e) FAIL:") || !strings.Contains(l, "output.path") {
				t.Fatalf("exit %d, (e) line %q, want FAIL naming output.path\n%s", code, l, out)
			}
		})
	}
}

// The seven refusals the spec advertises and an earlier round left untested. Each case mutates one field of the
// valid record (or, for the symlink, the fixture it names) and asserts the refusal where it is printed.
func TestAdvertisedRefusals(t *testing.T) {
	linkRoot := t.TempDir()
	linked := filepath.Join(linkRoot, "linked")
	if err := os.MkdirAll(linked, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(linked, "a.txt"), []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("a.txt", filepath.Join(linked, "b.txt")); err != nil {
		t.Skipf("cannot make a symlink here: %v", err)
	}
	fixtures, _ := filepath.Abs(fixturesRootPath)
	tests := []struct {
		name   string
		root   string
		change func(r map[string]any)
		letter string // the check that must FAIL
		want   []string
		rule   string // for check (a): the substring of the "rule:" problem line
	}{
		{name: "an output file already present in the fixture", root: fixtures,
			change: func(r map[string]any) { sub(r, "output")["path"] = "calc.go" },
			letter: "c", want: []string{"output.path", "already exists"}},
		{name: "a star outside inputs", root: fixtures,
			change: func(r map[string]any) { sub(r, "output")["path"] = "re*.xml" },
			letter: "a", rule: "output.path"},
		{name: "a placeholder as the program", root: fixtures,
			change: func(r map[string]any) { r["command"].([]any)[0] = "{{repo_root}}" },
			letter: "a", rule: "command[0]: the program must be a literal"},
		{name: "a metacharacter in a string param", root: fixtures,
			change: withParam("params", "string", "a;b"),
			letter: "a", rule: "params.lang.value"},
		{name: "config_path with no config", root: fixtures,
			change: func(r map[string]any) { r["command"] = append(r["command"].([]any), "{{config_path}}") },
			letter: "a", rule: "{{config_path}} is used but the record declares no \"config\""},
		{name: "a symlink in the fixture copy", root: linkRoot,
			change: func(r map[string]any) { sub(r, "fixture")["repo"] = "linked" },
			letter: "c", want: []string{"cannot copy fixture.repo", "symlink"}},
		{name: "an unbalanced placeholder", root: fixtures,
			change: func(r map[string]any) { r["command"].([]any)[3] = "{{repo_root" },
			letter: "a", rule: "unbalanced"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			code, out := runIn(t, mutate(t, tc.change), tc.root, withAsset(t)...)
			l := statusLine(out, tc.letter)
			if code != 1 || !strings.HasPrefix(l, "("+tc.letter+") FAIL:") {
				t.Fatalf("exit %d, check (%s) line %q, want FAIL\n%s", code, tc.letter, l, out)
			}
			if tc.rule != "" && !hasProblem(out, problem{"rule", tc.rule}) {
				t.Errorf("no rule: problem line contains %q\n%s", tc.rule, out)
			}
			for _, w := range tc.want {
				if !strings.Contains(l, w) {
					t.Errorf("the (%s) line does not contain %q: %q", tc.letter, w, l)
				}
			}
		})
	}
}

func TestExitTwoPathsEndWithTheEgressRemark(t *testing.T) {
	for name, args := range map[string][]string{
		"missing flags":            {"--record", "x.yaml"},
		"unloadable record schema": {"--record", "x.yaml", "--fixtures-root", ".", "--record-schema", filepath.Join(t.TempDir(), "absent.json")},
		"unknown flag":             {"--no-such-flag"},
	} {
		t.Run(name, func(t *testing.T) {
			var out bytes.Buffer
			if code := run(args, &out); code != 2 {
				t.Fatalf("exit %d, want 2\n%s", code, out.String())
			}
			if l := lastLine(out.String()); !strings.HasPrefix(l, "remark: egress was NOT observed") {
				t.Fatalf("last line is %q\n%s", l, out.String())
			}
		})
	}
}

func TestImagePinWithAssetSaysTheAssetWasIgnored(t *testing.T) {
	rec := mutate(t, func(r map[string]any) {
		r["pin"] = map[string]any{"image": "registry.invalid/tool@sha256:" + strings.Repeat("a", 64)}
	})
	code, out := runRecord(t, rec, withAsset(t)...)
	l := statusLine(out, "b")
	if code != 0 || !strings.HasPrefix(l, "(b) NOT APPLICABLE:") || !strings.Contains(l, "--asset") || !strings.Contains(l, "ignored") {
		t.Fatalf("exit %d, (b) line %q\n%s", code, l, out)
	}
}

func TestMultiDocumentYAMLIsRefused(t *testing.T) {
	doc := "schema: tool-invocation/v1\nid: one-tool\n---\nid: another-tool\n"
	code, out := runRecordFile(t, "record.yaml", []byte(doc))
	l := statusLine(out, "a")
	if code != 1 || !strings.HasPrefix(l, "(a) FAIL:") || !strings.Contains(l, "more than one YAML document") {
		t.Fatalf("exit %d, (a) line %q\n%s", code, l, out)
	}
}
