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

// coreClient serves the stub core in memory, with no socket.
func coreClient(t *testing.T, mode stubs.Mode) (stack *stubs.Stack, do func(method, path, token, body string) *httptest.ResponseRecorder) {
	t.Helper()
	dir, err := schemas.FindSpecDir("", ".")
	if err != nil {
		t.Fatal(err)
	}
	val, err := schemas.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	stack = stubs.New(mode, val)
	core, _, _ := stack.Handlers()
	stack.SetURLs("http://core.example.test", "http://mocks.example.test", "http://world.example.test")
	return stack, func(method, path, token, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set(proto.HeaderSpecVersion, proto.SpecVersion)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		rec := httptest.NewRecorder()
		core.ServeHTTP(rec, req)
		return rec
	}
}

func closeWith(total, passed, failed, errored, skipped int, verdict string) string {
	b, _ := json.Marshal(proto.CloseRequest{
		Verdict: verdict, Counts: proto.Counts{Total: total, Passed: passed, Failed: failed, Errored: errored, Skipped: skipped},
		StartedAt: "2026-10-29T04:10:00.000Z", EndedAt: "2026-10-29T04:10:01.000Z", Adapter: proto.Identity{Name: "a", Version: "1", Spec: "v1"},
	})
	return string(b)
}

func reason(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var r proto.Refusal
	if err := json.Unmarshal(rec.Body.Bytes(), &r); err != nil {
		t.Fatalf("answer %d is not a refusal document: %s", rec.Code, rec.Body.String())
	}
	return r.ReasonCode
}

// TestCoreRetalliesAtClose pins ruling 1 of v1.1: the core counts the results it accepted, and
// refuses a close whose verdict or counts differ, leaving the test run open.
func TestCoreRetalliesAtClose(t *testing.T) {
	t.Parallel()
	_, do := coreClient(t, stubs.ModeStandalone)
	var o proto.TestRunOpened
	if rec := do("POST", "/v1/test-runs", "", `{"project":"p"}`); json.Unmarshal(rec.Body.Bytes(), &o) != nil || rec.Code != http.StatusCreated {
		t.Fatalf("open answered %d: %s", rec.Code, rec.Body.String())
	}
	base := "/v1/test-runs/" + o.TestRunID
	for _, id := range []string{"s1", "s2"} {
		if rec := do("POST", base+"/steps", o.Credential, `{"step_id":"`+id+`","name":"n"}`); rec.Code != http.StatusCreated {
			t.Fatalf("start %s answered %d", id, rec.Code)
		}
	}
	if rec := do("POST", base+"/step-results", o.Credential, `{"step_id":"s1","result":"pass","duration_ms":1}`); rec.Code != http.StatusNoContent {
		t.Fatalf("result answered %d", rec.Code)
	}
	// A refused result (a failing step with no failure text) is not counted.
	if rec := do("POST", base+"/step-results", o.Credential, `{"step_id":"s2","result":"fail","duration_ms":1}`); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("a failure without text answered %d; want 422", rec.Code)
	}
	// s2 has no accepted result, so no counts can be right yet.
	if rec := do("POST", base+"/close", o.Credential, closeWith(2, 1, 1, 0, 0, "fail")); rec.Code != http.StatusUnprocessableEntity || reason(t, rec) != proto.ReasonResultMismatch {
		t.Fatalf("a close counting a refused result answered %d %s; want 422 %s", rec.Code, rec.Body.String(), proto.ReasonResultMismatch)
	}
	if rec := do("POST", base+"/step-results", o.Credential, `{"step_id":"s2","result":"skip","duration_ms":1}`); rec.Code != http.StatusNoContent {
		t.Fatalf("result answered %d", rec.Code)
	}
	for name, body := range map[string]string{
		"counts that differ": closeWith(2, 2, 0, 0, 0, "pass"),
		"a wrong verdict":    closeWith(2, 1, 0, 0, 1, "fail"),
		"a wrong total":      closeWith(3, 1, 0, 0, 1, "pass"),
	} {
		if rec := do("POST", base+"/close", o.Credential, body); rec.Code != http.StatusUnprocessableEntity || reason(t, rec) != proto.ReasonResultMismatch {
			t.Fatalf("%s answered %d %s; want 422 %s", name, rec.Code, rec.Body.String(), proto.ReasonResultMismatch)
		}
	}
	if rec := do("POST", base+"/close", o.Credential, closeWith(2, 1, 0, 0, 1, "pass")); rec.Code != http.StatusOK {
		t.Fatalf("the right tally answered %d: %s (the refused closes must leave the test run open)", rec.Code, rec.Body.String())
	}
}

// TestLegHoldsTestRunsInSequence pins ruling 12 of v1.1 and ecosystem contract section 1: after
// the close, a new attach with a new test_run_id is accepted; only an attach while one is
// attached is refused run-attached.
func TestLegHoldsTestRunsInSequence(t *testing.T) {
	t.Parallel()
	stack, do := coreClient(t, stubs.ModeLeg)
	runID, token := stack.LegEnv()
	attach := func(testRunID string) *httptest.ResponseRecorder {
		return do("POST", "/v1/runs/"+runID+"/test-runs", token, `{"project":"p","test_run_id":"`+testRunID+`"}`)
	}
	for _, id := range []string{"tr-first", "tr-second"} {
		rec := attach(id)
		var o proto.TestRunOpened
		if rec.Code != http.StatusCreated || json.Unmarshal(rec.Body.Bytes(), &o) != nil {
			t.Fatalf("attach %s answered %d: %s", id, rec.Code, rec.Body.String())
		}
		if again := attach(id + "-x"); again.Code != http.StatusConflict || reason(t, again) != proto.ReasonRunAttached {
			t.Fatalf("an attach while %s is attached answered %d %s; want 409 %s", id, again.Code, again.Body.String(), proto.ReasonRunAttached)
		}
		if closed := do("POST", "/v1/test-runs/"+id+"/close", o.Credential, closeWith(0, 0, 0, 0, 0, "pass")); closed.Code != http.StatusOK {
			t.Fatalf("close of %s answered %d: %s", id, closed.Code, closed.Body.String())
		}
	}
}
