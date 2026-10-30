package sampleadapter

import (
	"bytes"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/saphan-ai/tests-studio/conformance/internal/proto"
)

// recordingRoundTripper answers the stub core's two calls the adapter needs and records every
// request it receives. No socket is used.
type recordingRoundTripper struct {
	calls []*http.Request
}

func (rt *recordingRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	rt.calls = append(rt.calls, req)
	respond := func(code int, body string) *http.Response {
		return &http.Response{
			StatusCode: code, Status: http.StatusText(code), Proto: "HTTP/1.1", ProtoMajor: 1, ProtoMinor: 1,
			Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{"Content-Type": {"application/json"}},
		}
	}
	switch {
	case req.Method == http.MethodGet && req.URL.Path == "/v1/spec":
		return respond(http.StatusOK, `{"versions":["v1"],"core":{"name":"stub-core","version":"1.0.0","spec":"v1"}}`), nil
	case req.Method == http.MethodPost && req.URL.Path == "/v1/test-runs":
		// MOCKS_CORE_URL (http://other.example.test) differs from this answer's mocks_core_url:
		// the adapter must refuse mocks-core-mismatch and attempt no closeTestRun.
		return respond(http.StatusCreated, `{"test_run_id":"tr-1","run_id":"run-1","started_at":"2026-10-29T04:10:00.000Z","credential":"cred-1","run_credential":"run-cred-1","mocks_core_url":"http://mocks.example.test/v1","endpoints":{}}`), nil
	default:
		return respond(http.StatusNotFound, `{"message":"no such stub call","reason_code":"no-such-call"}`), nil
	}
}

func (rt *recordingRoundTripper) attemptedClose() bool {
	for _, req := range rt.calls {
		if req.Method == http.MethodPost && strings.HasSuffix(req.URL.Path, "/close") {
			return true
		}
	}
	return false
}

// TestAdapterRefusesMismatchedMocksCoreAttemptsNoClose pins section 4 / 10.1: an adapter that
// refuses mocks-core-mismatch stops before any test run exists, so it attempts no closeTestRun
// — there is nothing to close.
func TestAdapterRefusesMismatchedMocksCoreAttemptsNoClose(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	project := dir + "/project.json"
	if err := os.WriteFile(project, []byte(`{"project":"p","tests":[{"name":"one","outcome":"pass"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	rt := &recordingRoundTripper{}
	var stderr bytes.Buffer
	code := Main([]string{"-project", project}, []string{
		"TESTS_CORE_URL=http://core.example.test",
		"MOCKS_CORE_URL=http://other.example.test",
		"TESTS_RESULT_FILE=" + dir + "/result.json",
	}, rt, &bytes.Buffer{}, &stderr)
	if code == 0 {
		t.Fatalf("adapter exited 0; want non-zero (refused by name for mocks-core-mismatch)")
	}
	if !strings.Contains(stderr.String(), proto.ReasonMocksCoreMismatch) {
		t.Fatalf("stderr does not name mocks-core-mismatch: %q", stderr.String())
	}
	if rt.attemptedClose() {
		t.Fatalf("closeTestRun was attempted although no test run was opened for this adapter")
	}
	if len(rt.calls) != 2 {
		t.Fatalf("want exactly 2 calls (spec negotiation, open attempt); got %d", len(rt.calls))
	}
}
