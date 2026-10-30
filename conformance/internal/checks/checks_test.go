package checks

import (
	"net/http"
	"strings"
	"testing"

	"github.com/saphan-ai/tests-studio/conformance/internal/proto"
	"github.com/saphan-ai/tests-studio/conformance/internal/schemas"
	"github.com/saphan-ai/tests-studio/conformance/internal/stubs"
)

const legRun = "leg-conformance-0001"

func attach(seq, status int) stubs.Call {
	return stubs.Call{Seq: seq, Service: stubs.ServiceCore, Method: "POST", Path: "/v1/runs/" + legRun + "/test-runs", Header: http.Header{}, Status: status}
}

func closeCall(seq int, testRun string) stubs.Call {
	return stubs.Call{Seq: seq, Service: stubs.ServiceCore, Method: "POST", Path: "/v1/test-runs/" + testRun + "/close", Header: http.Header{}, Status: http.StatusOK}
}

func specDir(t *testing.T) string {
	t.Helper()
	dir, err := schemas.FindSpecDir("", ".")
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func validator(t *testing.T) *schemas.Validator {
	t.Helper()
	val, err := schemas.Load(specDir(t))
	if err != nil {
		t.Fatal(err)
	}
	return val
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

// TestEventsValidSameDocumentDifferentBytes reproduces review condition 2: the same event
// document re-sent under one event_id with its keys in another order is the same event and must
// pass events-valid. At a2dd7d7 eventsValid compares raw bytes, so it FAILS (a conforming adapter
// is failed). After the fix it compares as JSON values (json.Number; an undecodable body falls
// back to bytes) and passes.
func TestEventsValidSameDocumentDifferentBytes(t *testing.T) {
	t.Parallel()
	val := validator(t)
	same := `{"schema":"tests.event/v1","event_id":"evt-open-1","at":"2026-10-29T04:10:00.000Z","run_id":"run-1","test_run_id":"tr-1","kind":"run.opened","emitter":{"name":"sample-adapter","version":"1.0.0","spec":"v1"},"summary":"the run opened"}`
	resent := `{"summary":"the run opened","kind":"run.opened","test_run_id":"tr-1","run_id":"run-1","at":"2026-10-29T04:10:00.000Z","event_id":"evt-open-1","schema":"tests.event/v1","emitter":{"spec":"v1","version":"1.0.0","name":"sample-adapter"}}`
	in := Input{
		Validator: val,
		Calls: []stubs.Call{
			{Seq: 1, Service: stubs.ServiceCore, Method: "POST", Path: "/v1/test-runs/tr-1/steps", Header: http.Header{}, Status: http.StatusCreated,
				Body: []byte(`{"step_id":"s1","name":"one"}`), Response: []byte(`{"step_id":"s1","correlation_id":"0123456789abcdef0123456789abcdef","traceparent":"00-0123456789abcdef0123456789abcdef-0011223344556677-01","baggage":"mocks.run=run-1,mocks.step=s1"}`)},
			{Seq: 2, Service: stubs.ServiceCore, Method: "POST", Path: "/v1/test-runs/tr-1/events", Header: http.Header{}, Status: http.StatusAccepted, Body: []byte(same)},
			{Seq: 3, Service: stubs.ServiceCore, Method: "POST", Path: "/v1/test-runs/tr-1/events", Header: http.Header{}, Status: http.StatusAccepted,
				Body: []byte(`{"schema":"tests.event/v1","event_id":"evt-start-1","at":"2026-10-29T04:10:00.000Z","run_id":"run-1","test_run_id":"tr-1","kind":"step.started","step_id":"s1","correlation_id":"0123456789abcdef0123456789abcdef","emitter":{"name":"sample-adapter","version":"1.0.0","spec":"v1"},"summary":"step one started"}`)},
			{Seq: 4, Service: stubs.ServiceCore, Method: "POST", Path: "/v1/test-runs/tr-1/events", Header: http.Header{}, Status: http.StatusAccepted, Body: []byte(resent)}, // same document, other bytes
			{Seq: 5, Service: stubs.ServiceCore, Method: "POST", Path: "/v1/test-runs/tr-1/events", Header: http.Header{}, Status: http.StatusAccepted,
				Body: []byte(`{"schema":"tests.event/v1","event_id":"evt-finish-1","at":"2026-10-29T04:10:00.000Z","run_id":"run-1","test_run_id":"tr-1","kind":"step.finished","step_id":"s1","correlation_id":"0123456789abcdef0123456789abcdef","emitter":{"name":"sample-adapter","version":"1.0.0","spec":"v1"},"summary":"step one finished","outcome":"pass"}`)},
			{Seq: 6, Service: stubs.ServiceCore, Method: "POST", Path: "/v1/test-runs/tr-1/events", Header: http.Header{}, Status: http.StatusAccepted,
				Body: []byte(`{"schema":"tests.event/v1","event_id":"evt-close-1","at":"2026-10-29T04:10:00.000Z","run_id":"run-1","test_run_id":"tr-1","kind":"run.closed","emitter":{"name":"sample-adapter","version":"1.0.0","spec":"v1"},"summary":"the run closed","outcome":"pass"}`)},
		},
	}
	got := eventsValid(in, derive(in))
	if !got.Pass {
		t.Fatalf("want events-valid PASS for a re-sent same document; got %s", got.Line())
	}
}

// TestEventsValidIdenticalBytesReSend pins that re-sending the identical bytes under one
// event_id passes events-valid both before and after the condition-2 fix (the surviving
// inversion: raw-compare never complained about identical bytes).
func TestEventsValidIdenticalBytesReSend(t *testing.T) {
	t.Parallel()
	val := validator(t)
	body := []byte(`{"schema":"tests.event/v1","event_id":"evt-open-1","at":"2026-10-29T04:10:00.000Z","run_id":"run-1","test_run_id":"tr-1","kind":"run.opened","emitter":{"name":"sample-adapter","version":"1.0.0","spec":"v1"},"summary":"the run opened"}`)
	startResp := []byte(`{"step_id":"s1","correlation_id":"0123456789abcdef0123456789abcdef","traceparent":"00-0123456789abcdef0123456789abcdef-0011223344556677-01","baggage":"mocks.run=run-1,mocks.step=s1"}`)
	in := Input{
		Validator: val,
		Calls: []stubs.Call{
			{Seq: 1, Service: stubs.ServiceCore, Method: "POST", Path: "/v1/test-runs/tr-1/steps", Header: http.Header{}, Status: http.StatusCreated,
				Body: []byte(`{"step_id":"s1","name":"one"}`), Response: startResp},
			{Seq: 2, Service: stubs.ServiceCore, Method: "POST", Path: "/v1/test-runs/tr-1/events", Header: http.Header{}, Status: http.StatusAccepted, Body: body},
			{Seq: 3, Service: stubs.ServiceCore, Method: "POST", Path: "/v1/test-runs/tr-1/events", Header: http.Header{}, Status: http.StatusAccepted, Body: body}, // identical bytes, same id: allowed
			{Seq: 4, Service: stubs.ServiceCore, Method: "POST", Path: "/v1/test-runs/tr-1/events", Header: http.Header{}, Status: http.StatusAccepted,
				Body: []byte(`{"schema":"tests.event/v1","event_id":"evt-start-1","at":"2026-10-29T04:10:00.000Z","run_id":"run-1","test_run_id":"tr-1","kind":"step.started","step_id":"s1","correlation_id":"0123456789abcdef0123456789abcdef","emitter":{"name":"sample-adapter","version":"1.0.0","spec":"v1"},"summary":"step one started"}`)},
			{Seq: 5, Service: stubs.ServiceCore, Method: "POST", Path: "/v1/test-runs/tr-1/events", Header: http.Header{}, Status: http.StatusAccepted,
				Body: []byte(`{"schema":"tests.event/v1","event_id":"evt-finish-1","at":"2026-10-29T04:10:00.000Z","run_id":"run-1","test_run_id":"tr-1","kind":"step.finished","step_id":"s1","correlation_id":"0123456789abcdef0123456789abcdef","emitter":{"name":"sample-adapter","version":"1.0.0","spec":"v1"},"summary":"step one finished","outcome":"pass"}`)},
			{Seq: 6, Service: stubs.ServiceCore, Method: "POST", Path: "/v1/test-runs/tr-1/events", Header: http.Header{}, Status: http.StatusAccepted,
				Body: []byte(`{"schema":"tests.event/v1","event_id":"evt-close-1","at":"2026-10-29T04:10:00.000Z","run_id":"run-1","test_run_id":"tr-1","kind":"run.closed","emitter":{"name":"sample-adapter","version":"1.0.0","spec":"v1"},"summary":"the run closed","outcome":"pass"}`)},
		},
	}
	got := eventsValid(in, derive(in))
	if !got.Pass {
		t.Fatalf("want events-valid PASS for an identical-bytes re-send; got %s", got.Line())
	}
}

// TestEvidenceDigestRejectsEvidenceAfterAcceptedClose reproduces review condition 3 for section 6.2:
// evidence must still precede closeTestRun (closing revokes the credential, section 5). A valid
// evidence PUT recorded after the accepted closeTestRun of its test run must FAIL evidence-digest,
// naming the order. At a2dd7d7 evidenceDigest checks neither status nor order, so the check PASSES
// and this test is red until the fix lands.
func TestEvidenceDigestRejectsEvidenceAfterAcceptedClose(t *testing.T) {
	t.Parallel()
	body := []byte("the log bytes")
	declared := strings.TrimPrefix(proto.SHA256Digest(body), "sha256:")
	evidenceHdr := http.Header{"Tests-Evidence-Kind": {"log"}, "Tests-Step-Id": {"s1"}}
	closeResult := []byte(`{"schema":"tests.run-result/v1","run_id":"run-1","test_run_id":"tr-1","project":"p","verdict":"pass","counts":{"total":1,"passed":1,"failed":0,"errored":0,"skipped":0},"started_at":"2026-10-29T04:10:00.000Z","ended_at":"2026-10-29T04:10:01.000Z","adapter":{"name":"sample-adapter","version":"1.0.0","spec":"v1"},"events_digest":"sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855","evidence_line":"- evidence: tests-studio-run run-1/tr-1 verdict=pass events=sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"}`)
	in := Input{
		Calls: []stubs.Call{
			{Seq: 1, Service: stubs.ServiceCore, Method: "POST", Path: "/v1/test-runs/tr-1/steps", Header: http.Header{}, Status: http.StatusCreated,
				Body: []byte(`{"step_id":"s1","name":"one"}`), Response: []byte(`{"step_id":"s1","correlation_id":"0123456789abcdef0123456789abcdef","traceparent":"00-0123456789abcdef0123456789abcdef-0011223344556677-01","baggage":"mocks.run=run-1,mocks.step=s1"}`)},
			{Seq: 2, Service: stubs.ServiceCore, Method: "PUT", Path: "/v1/test-runs/tr-1/evidence/" + declared, Header: evidenceHdr, Status: http.StatusCreated, Body: body},
			{Seq: 3, Service: stubs.ServiceCore, Method: "POST", Path: "/v1/test-runs/tr-1/close", Header: http.Header{}, Status: http.StatusOK, Response: closeResult},       // accepted close at seq 3
			{Seq: 4, Service: stubs.ServiceCore, Method: "PUT", Path: "/v1/test-runs/tr-1/evidence/" + declared, Header: evidenceHdr, Status: http.StatusCreated, Body: body}, // after the accepted close, still valid
		},
	}
	got := evidenceDigest(in, derive(in))
	if got.Pass {
		t.Fatalf("want evidence-digest FAIL for evidence recorded after the accepted closeTestRun; got %s", got.Line())
	}
	if !strings.Contains(got.Measured, "after the accepted closeTestRun") {
		t.Fatalf("want the failure to name the order; got %s", got.Line())
	}
}

