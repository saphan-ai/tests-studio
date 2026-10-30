package checks

import (
	"net/http"
	"strings"
	"testing"

	"github.com/saphan-ai/tests-studio/conformance/internal/stubs"
)

const legRun = "leg-conformance-0001"

func attach(seq, status int) stubs.Call {
	return stubs.Call{Seq: seq, Service: stubs.ServiceCore, Method: "POST", Path: "/v1/runs/" + legRun + "/test-runs", Header: http.Header{}, Status: status}
}

func closeCall(seq int, testRun string) stubs.Call {
	return stubs.Call{Seq: seq, Service: stubs.ServiceCore, Method: "POST", Path: "/v1/test-runs/" + testRun + "/close", Header: http.Header{}, Status: http.StatusOK}
}

// TestNoSecondRunCountsConcurrentAttachesOnly pins ruling 12 of v1.1: the ecosystem contract
// section 1 lets a leg hold several test runs in sequence, so a new attach after the previous
// test run closed is legal, while an attach made while one is attached is not.
func TestNoSecondRunCountsConcurrentAttachesOnly(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		calls []stubs.Call
		pass  bool
		want  string // a fragment the measurement must carry
	}{
		{"attach, close, attach, close in sequence", []stubs.Call{
			attach(1, 201), closeCall(2, "tr-first"), attach(3, 201), closeCall(4, "tr-second"),
		}, true, "2 attach"},
		{"a second attach while one is attached", []stubs.Call{
			attach(1, 201), attach(2, 409), closeCall(3, "tr-first"),
		}, false, "while a test run was attached"},
		{"an attach after a refused close is still concurrent", []stubs.Call{
			attach(1, 201), {Seq: 2, Service: stubs.ServiceCore, Method: "POST", Path: "/v1/test-runs/tr-first/close", Header: http.Header{}, Status: 422}, attach(3, 409),
		}, false, "while a test run was attached"},
		{"an attach refused before any attach is accepted", []stubs.Call{
			attach(1, 422), attach(2, 201), closeCall(3, "tr-first"),
		}, true, "2 attach"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := noSecondRun(Input{Mode: stubs.ModeLeg, Calls: c.calls}, facts{})
			if got.Pass != c.pass || !strings.Contains(got.Measured, c.want) {
				t.Fatalf("want pass=%v with %q; got %s", c.pass, c.want, got.Line())
			}
		})
	}
}
