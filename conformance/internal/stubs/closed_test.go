package stubs_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/saphan-ai/tests-studio/conformance/internal/proto"
	"github.com/saphan-ai/tests-studio/conformance/internal/schemas"
	"github.com/saphan-ai/tests-studio/conformance/internal/stubs"
)

// TestClosedTestRunCredentialIsRefusedAsInvalid pins specification sections 5 and 10 to one
// answer: closing a test run revokes its credential, and a call that presents it afterwards is
// refused 403 run-credential-invalid. The test serves the handler in memory, with no socket.
func TestClosedTestRunCredentialIsRefusedAsInvalid(t *testing.T) {
	t.Parallel()
	dir, err := schemas.FindSpecDir("", ".")
	if err != nil {
		t.Fatal(err)
	}
	val, err := schemas.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	stack := stubs.New(stubs.ModeStandalone, val)
	core, _, _ := stack.Handlers()
	stack.SetURLs("http://core.example.test", "http://mocks.example.test", "http://world.example.test")

	do := func(method, path, token, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set(proto.HeaderSpecVersion, proto.SpecVersion)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		rec := httptest.NewRecorder()
		core.ServeHTTP(rec, req)
		return rec
	}
	opened := do("POST", "/v1/test-runs", "", `{"project":"p","mocks":["sample-http"]}`)
	var o proto.TestRunOpened
	if opened.Code != http.StatusCreated || json.Unmarshal(opened.Body.Bytes(), &o) != nil {
		t.Fatalf("open answered %d: %s", opened.Code, opened.Body.String())
	}
	base := "/v1/test-runs/" + o.TestRunID
	if before := do("POST", base+"/steps", o.Credential, `{"step_id":"s1","name":"n"}`); before.Code != http.StatusCreated {
		t.Fatalf("a live credential was refused %d: %s", before.Code, before.Body.String())
	}
	closeBody := `{"verdict":"pass","counts":{"total":0,"passed":0,"failed":0,"errored":0,"skipped":0},"started_at":"2026-10-29T04:10:00.000Z","ended_at":"2026-10-29T04:10:01.000Z","adapter":{"name":"a","version":"1","spec":"v1"}}`
	if closed := do("POST", base+"/close", o.Credential, closeBody); closed.Code != http.StatusOK {
		t.Fatalf("close answered %d: %s", closed.Code, closed.Body.String())
	}
	after := do("POST", base+"/steps", o.Credential, `{"step_id":"s2","name":"n"}`)
	var refusal proto.Refusal
	if err := json.Unmarshal(after.Body.Bytes(), &refusal); err != nil {
		t.Fatal(err)
	}
	if after.Code != http.StatusForbidden || refusal.ReasonCode != proto.ReasonCredentialBad {
		t.Fatalf("a closed test run's credential gave %d %q; want 403 %q", after.Code, refusal.ReasonCode, proto.ReasonCredentialBad)
	}
}
