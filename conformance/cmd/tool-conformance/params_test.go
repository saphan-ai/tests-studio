package main

import (
	"strings"
	"testing"
)

// withParam adds the typed input `lang` and the command element that names it. The baseline of the controls
// below is therefore the valid record plus one declared param; the first subtest shows that baseline passes, and
// each control then changes one field of it: the param's type or its value.
func withParam(key, typ, value string) func(r map[string]any) {
	return func(r map[string]any) {
		r[key] = map[string]any{"lang": map[string]any{"type": typ, "value": value}}
		r["command"] = append(r["command"].([]any), "{{lang}}")
	}
}

func TestParamsMatchTheEngineShape(t *testing.T) {
	t.Run("params with a relpath passes check (a)", func(t *testing.T) {
		code, out := runRecord(t, mutate(t, withParam("params", "relpath", "src")), withAsset(t)...)
		if l := statusLine(out, "a"); !strings.HasPrefix(l, "(a) PASS:") {
			t.Fatalf("check (a) is %q, want PASS (exit %d)\n%s", l, code, out)
		}
	})
	// The engine's relpath check does not refuse '*'; the runner agrees, so a glob is a valid relpath value.
	t.Run("a relpath value with a star reads (a) PASS for that param", func(t *testing.T) {
		code, out := runRecord(t, mutate(t, withParam("params", "relpath", "src/*.go")), withAsset(t)...)
		if l := statusLine(out, "a"); !strings.HasPrefix(l, "(a) PASS:") {
			t.Fatalf("check (a) is %q, want PASS (exit %d)\n%s", l, code, out)
		}
	})
	t.Run("the largest 64-bit int reads (a) PASS", func(t *testing.T) {
		code, out := runRecord(t, mutate(t, withParam("params", "int", "9223372036854775807")), withAsset(t)...)
		if l := statusLine(out, "a"); !strings.HasPrefix(l, "(a) PASS:") {
			t.Fatalf("check (a) is %q, want PASS (exit %d)\n%s", l, code, out)
		}
	})
	t.Run("extra_inputs is refused, naming it and listing the allowed keys", func(t *testing.T) {
		code, out := runRecord(t, mutate(t, withParam("extra_inputs", "relpath", "src")), withAsset(t)...)
		if code != 1 || !strings.HasPrefix(statusLine(out, "a"), "(a) FAIL:") {
			t.Fatalf("exit %d\n%s", code, out)
		}
		for _, w := range []string{"extra_inputs", "allowed keys:", "params", "config", "schema"} {
			if !strings.Contains(out, w) {
				t.Errorf("output does not name %q\n%s", w, out)
			}
		}
	})
}

// Each control mutates one field: schema, params.lang.type, params.lang.value or a param's name. The field is
// asserted on a problem line (a "schema:" line from the schema and a "rule:" line from the runner), never
// anywhere in the output.
func TestSchemaAndParamControls(t *testing.T) {
	rename := func(name string) func(r map[string]any) {
		return func(r map[string]any) {
			r["params"] = map[string]any{name: map[string]any{"type": "string", "value": "x"}}
		}
	}
	tests := []struct {
		name   string
		change func(r map[string]any)
		want   []problem
	}{
		{"schema absent", func(r map[string]any) { delete(r, "schema") },
			[]problem{{"schema", "'schema'"}, {"rule", "schema: the required key is missing"}}},
		{"schema is tool-invocation/v2", func(r map[string]any) { r["schema"] = "tool-invocation/v2" },
			[]problem{{"schema", "tool-invocation/v1"}, {"rule", `schema: tool-invocation/v2 is not "tool-invocation/v1"`}}},
		{"param of type path", withParam("params", "path", "src"),
			[]problem{{"schema", "params.lang.type"}, {"rule", "params.lang.type"}, {"rule", "string, int, relpath"}}},
		{"relpath value climbs out", withParam("params", "relpath", "../x"),
			[]problem{{"schema", "params.lang.value"}, {"rule", "params.lang.value"}}},
		{"relpath value is absolute", withParam("params", "relpath", "/etc/passwd"),
			[]problem{{"schema", "params.lang.value"}, {"rule", "params.lang.value"}}},
		{"relpath value starts with a tilde", withParam("params", "relpath", "~/x"),
			[]problem{{"schema", "params.lang.value"}, {"rule", "params.lang.value"}}},
		{"relpath value is empty", withParam("params", "relpath", ""),
			[]problem{{"schema", "params.lang.value"}, {"rule", "params.lang.value"}}},
		// The rule line carries the character, which only the metacharacter guard prints; the int rule's
		// "is not a signed decimal integer" line names the value in double quotes and never the quoted rune.
		{"int value carries a pipe", withParam("params", "int", "4|5"),
			[]problem{{"schema", "params.lang.value"}, {"rule", "params.lang.value"}, {"rule", `'|'`}}},
		{"relpath value carries a pipe", withParam("params", "relpath", "a|b"),
			[]problem{{"schema", "params.lang.value"}, {"rule", "params.lang.value"}, {"rule", `'|'`}}},
		{"int value is beyond the 64-bit range", withParam("params", "int", "99999999999999999999"),
			[]problem{{"rule", "params.lang.value"}, {"rule", "64-bit range"}}},
		{"param name has a capital", rename("Lang"),
			[]problem{{"schema", "params.Lang"}, {"rule", "params.Lang"}}},
		{"param name is one character", rename("x"),
			[]problem{{"schema", "params.x"}, {"rule", "params.x"}}},
		{"param name is a built-in input", rename("repo_root"),
			[]problem{{"schema", "params.repo_root"}, {"rule", "params.repo_root"}}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			code, out := runRecord(t, mutate(t, tc.change), withAsset(t)...)
			if code != 1 || !strings.HasPrefix(statusLine(out, "a"), "(a) FAIL:") {
				t.Fatalf("exit %d, want check (a) FAIL\n%s", code, out)
			}
			if statusLine(out, "b") != "" {
				t.Errorf("check (b) ran after the FAIL in (a)\n%s", out)
			}
			for _, p := range tc.want {
				if !hasProblem(out, p) {
					t.Errorf("no %s problem line contains %q\n%s", p.source, p.sub, out)
				}
			}
		})
	}
}
