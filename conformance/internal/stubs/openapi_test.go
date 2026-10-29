package stubs_test

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/saphan-ai/tests-studio/conformance/internal/schemas"
	"github.com/saphan-ai/tests-studio/conformance/internal/stubs"
)

var httpMethods = []string{"get", "put", "post", "delete", "patch", "head", "options"}

func loadYAML(t *testing.T, rel string) (map[string]any, string) {
	t.Helper()
	dir, err := schemas.FindSpecDir("", ".")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, rel))
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("%s is not YAML: %v", rel, err)
	}
	return doc, dir
}

// operations lists "METHOD /path" for every operation of an OpenAPI document; every one must
// carry an operationId, and operationIds must be unique.
func operations(t *testing.T, doc map[string]any) []string {
	t.Helper()
	paths, _ := doc["paths"].(map[string]any)
	if len(paths) == 0 {
		t.Fatal("the document has no paths")
	}
	var ops []string
	ids := map[string]bool{}
	for p, item := range paths {
		for _, m := range httpMethods {
			op, ok := item.(map[string]any)[m].(map[string]any)
			if !ok {
				continue
			}
			id, _ := op["operationId"].(string)
			if id == "" || ids[id] {
				t.Errorf("%s %s: operationId %q is missing or repeated", m, p, id)
			}
			ids[id] = true
			ops = append(ops, strings.ToUpper(m)+" "+p)
		}
	}
	sort.Strings(ops)
	return ops
}

func TestStubCoreServesExactlyTheOperationsOfTheOpenAPIDocument(t *testing.T) {
	doc, _ := loadYAML(t, "runner-v1.openapi.yaml")
	want, got := operations(t, doc), stubs.CoreRoutes()
	if strings.Join(want, "\n") != strings.Join(got, "\n") {
		t.Fatalf("the stub core and spec/runner-v1.openapi.yaml disagree\nOpenAPI:\n  %s\nstub:\n  %s", strings.Join(want, "\n  "), strings.Join(got, "\n  "))
	}
}

func TestStubMocksCoreServesOnlyOperationsOfAppendixC(t *testing.T) {
	doc, _ := loadYAML(t, "ecosystem-v1/mocks-runs-v1.openapi.yaml")
	in := map[string]bool{}
	for _, op := range operations(t, doc) {
		in[op] = true
	}
	for _, r := range stubs.MocksRoutes() {
		if !in[r] {
			t.Errorf("the stub Mocks Studio core serves %q, which Appendix C does not define", r)
		}
	}
}

// TestReferencesResolve walks both OpenAPI documents: every "#/components/..." reference reaches
// an existing node and every relative file reference names a file of the specification.
func TestReferencesResolve(t *testing.T) {
	for _, rel := range []string{"runner-v1.openapi.yaml", "ecosystem-v1/mocks-runs-v1.openapi.yaml"} {
		doc, dir := loadYAML(t, rel)
		var walk func(v any)
		walk = func(v any) {
			switch x := v.(type) {
			case map[string]any:
				if ref, ok := x["$ref"].(string); ok {
					checkRef(t, rel, dir, doc, ref)
				}
				for _, c := range x {
					walk(c)
				}
			case []any:
				for _, c := range x {
					walk(c)
				}
			}
		}
		walk(doc)
	}
}

func checkRef(t *testing.T, rel, specDir string, doc map[string]any, ref string) {
	t.Helper()
	if !strings.HasPrefix(ref, "#/") {
		if _, err := os.Stat(filepath.Join(specDir, filepath.Dir(rel), ref)); err != nil {
			t.Errorf("%s: $ref %q names no file", rel, ref)
		}
		return
	}
	var node any = doc
	for _, part := range strings.Split(strings.TrimPrefix(ref, "#/"), "/") {
		m, ok := node.(map[string]any)
		if !ok || m[part] == nil {
			t.Errorf("%s: $ref %q does not resolve (stops at %q)", rel, ref, part)
			return
		}
		node = m[part]
	}
}
