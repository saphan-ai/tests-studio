package schemas_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/saphan-ai/tests-studio/conformance/internal/schemas"
)

const allZeroCorrelationID = "00000000000000000000000000000000"

// sharedHeaderFields are the properties whose schemas tests.event/v1 and mocks.event/v1 share
// byte for byte (spec/TEST_RUNNER_V1.md, section "The shared header").
var sharedHeaderFields = []string{"run_id", "test_run_id", "step_id", "correlation_id", "at", "payload"}

func specDir(t *testing.T) string {
	t.Helper()
	dir, err := schemas.FindSpecDir("", ".")
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func readJSON(t *testing.T, path string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	return doc
}

// canonical returns the canonical JSON of v: Go's encoder writes object keys sorted, with no
// insignificant whitespace.
func canonical(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func properties(t *testing.T, doc map[string]any) map[string]any {
	t.Helper()
	p, ok := doc["properties"].(map[string]any)
	if !ok {
		t.Fatal("schema has no properties object")
	}
	return p
}

func TestSharedHeaderSchemasAreByteIdentical(t *testing.T) {
	dir := specDir(t)
	mocks := readJSON(t, filepath.Join(dir, schemas.MocksEventFile))
	tests := readJSON(t, filepath.Join(dir, schemas.TestsEventFile))
	mp, tp := properties(t, mocks), properties(t, tests)

	for _, f := range sharedHeaderFields {
		t.Run(f, func(t *testing.T) {
			m, mok := mp[f]
			s, sok := tp[f]
			if !mok || !sok {
				t.Fatalf("field %q missing: in mocks.event/v1 %v, in tests.event/v1 %v", f, mok, sok)
			}
			if a, b := canonical(t, m), canonical(t, s); !bytes.Equal(a, b) {
				t.Fatalf("shared-header field %q differs\nmocks.event/v1: %s\ntests.event/v1: %s", f, a, b)
			}
		})
	}

	t.Run("$defs/run_id", func(t *testing.T) {
		md, _ := mocks["$defs"].(map[string]any)
		td, _ := tests["$defs"].(map[string]any)
		if md["run_id"] == nil || td["run_id"] == nil {
			t.Fatal("$defs/run_id missing in one of the schemas")
		}
		if a, b := canonical(t, md["run_id"]), canonical(t, td["run_id"]); !bytes.Equal(a, b) {
			t.Fatalf("$defs/run_id differs\nmocks.event/v1: %s\ntests.event/v1: %s", a, b)
		}
	})
}

// TestSharedHeaderSchemasCompileOnRE2 compiles both schemas with the same RE2 validator and
// shows the instrument can see the defect the amendment cleared: the pre-amendment lookahead
// pattern does not compile.
func TestSharedHeaderSchemasCompileOnRE2(t *testing.T) {
	dir := specDir(t)
	for _, f := range []string{schemas.MocksEventFile, schemas.TestsEventFile} {
		if _, err := schemas.Compile(filepath.Join(dir, f)); err != nil {
			t.Errorf("%s does not compile: %v", f, err)
		}
	}

	const preAmendment = `{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"string","pattern":"^(?!0{32})[0-9a-f]{32}$"}`
	if _, err := schemas.CompileBytes("pre-amendment.json", []byte(preAmendment)); err == nil {
		t.Error("negative control: the lookahead pattern compiled, so this instrument cannot see the RE2 defect")
	}
}

func TestAllZeroCorrelationIDIsRefusedByBothSchemas(t *testing.T) {
	dir := specDir(t)
	mocks, err := schemas.Compile(filepath.Join(dir, schemas.MocksEventFile))
	if err != nil {
		t.Fatal(err)
	}
	tests, err := schemas.Compile(filepath.Join(dir, schemas.TestsEventFile))
	if err != nil {
		t.Fatal(err)
	}

	// Each case mutates exactly the correlation_id of an otherwise valid event.
	mocksEvent := func(corr string) any {
		return map[string]any{
			"schema": "mocks.event/v1", "event_id": "evt-00000001", "at": "2026-10-29T04:10:00.123Z",
			"run_id": "run-aaa", "correlation_id": corr, "correlation_source": "generated",
			"mock": map[string]any{"name": "openapi", "version": "0.1.0", "contract": "mocks.adapter/v1"},
			"kind": "request.received", "direction": "inbound", "protocol": "http", "summary": "GET /",
		}
	}
	testsEvent := func(corr string) any {
		return map[string]any{
			"schema": "tests.event/v1", "event_id": "evt-00000001", "at": "2026-10-29T04:10:00.123Z",
			"run_id": "run-aaa", "test_run_id": "tr-aaa", "step_id": "s1", "correlation_id": corr,
			"emitter": map[string]any{"name": "sample", "version": "0.1.0", "spec": "v1"},
			"kind":    "step.started", "summary": "step started",
		}
	}
	const valid = "4bf92f3577b34da6a3ce929d0e0e4736"
	for name, c := range map[string]struct {
		schema interface{ Validate(any) error }
		doc    func(string) any
	}{"mocks.event/v1": {mocks, mocksEvent}, "tests.event/v1": {tests, testsEvent}} {
		t.Run(name, func(t *testing.T) {
			if err := c.schema.Validate(c.doc(valid)); err != nil {
				t.Fatalf("the control event with a valid correlation_id was refused: %v", err)
			}
			if err := c.schema.Validate(c.doc(allZeroCorrelationID)); err == nil {
				t.Fatal("an all-zero correlation_id was accepted")
			}
		})
	}
}
