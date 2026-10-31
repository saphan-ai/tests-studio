package main

import (
	"strings"
	"testing"
)

// withParam adds the typed input `lang` and a command element that names it, leaving every other field alone.
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

// Each control mutates exactly one field of the valid record: schema, params.lang.type or params.lang.value.
func TestSchemaAndParamControls(t *testing.T) {
	tests := []struct {
		name   string
		change func(r map[string]any)
		want   []string
	}{
		{"schema absent", func(r map[string]any) { delete(r, "schema") }, []string{"schema"}},
		{"schema is tool-invocation/v2", func(r map[string]any) { r["schema"] = "tool-invocation/v2" }, []string{"schema", "tool-invocation/v1"}},
		{"param of type path", withParam("params", "path", "src"), []string{"params.lang", "string, int, relpath"}},
		{"relpath value climbs out", withParam("params", "relpath", "../x"), []string{"params.lang.value", "../x"}},
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
			for _, w := range tc.want {
				if !strings.Contains(out, w) {
					t.Errorf("output does not name %q\n%s", w, out)
				}
			}
		})
	}
}
