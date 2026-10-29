package tracectx_test

import (
	"net/http"
	"testing"

	"github.com/saphan-ai/tests-studio/adapters/go-test/tracectx"
)

const tp = "00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01"

type capture struct{ got *http.Request }

func (c *capture) RoundTrip(r *http.Request) (*http.Response, error) {
	c.got = r
	return &http.Response{StatusCode: 204, Body: http.NoBody, Request: r}, nil
}

func TestApplySetsBothHeadersUnchanged(t *testing.T) {
	t.Setenv(tracectx.EnvTraceparent, tp)
	t.Setenv(tracectx.EnvBaggage, "mocks.run=r1,mocks.step=s1")
	req, _ := http.NewRequest(http.MethodGet, "http://sut.example.test/ping", nil)
	if !tracectx.Apply(req) {
		t.Fatal("Apply reported no trace context")
	}
	if req.Header.Get("traceparent") != tp || req.Header.Get("baggage") != "mocks.run=r1,mocks.step=s1" {
		t.Errorf("headers %v", req.Header)
	}
}

func TestApplyOutsideAStepLeavesTheRequestAlone(t *testing.T) {
	t.Setenv(tracectx.EnvTraceparent, "")
	t.Setenv(tracectx.EnvBaggage, "stale")
	req, _ := http.NewRequest(http.MethodGet, "http://sut.example.test/ping", nil)
	if tracectx.Apply(req) || len(req.Header) != 0 {
		t.Errorf("Apply touched a request outside a step: %v", req.Header)
	}
	if _, _, ok := tracectx.Get(); ok {
		t.Error("Get reported a trace context outside a step")
	}
}

func TestTransportSendsACopyWithTheHeaders(t *testing.T) {
	t.Setenv(tracectx.EnvTraceparent, tp)
	t.Setenv(tracectx.EnvBaggage, "")
	c := &capture{}
	req, _ := http.NewRequest(http.MethodGet, "http://sut.example.test/ping", nil)
	if _, err := (&http.Client{Transport: tracectx.Transport(c)}).Do(req); err != nil {
		t.Fatal(err)
	}
	if c.got.Header.Get("traceparent") != tp {
		t.Errorf("the request sent carried %v", c.got.Header)
	}
	if c.got.Header.Get("baggage") != "" {
		t.Error("an empty baggage was sent")
	}
	if len(req.Header) != 0 {
		t.Errorf("the caller's request was modified: %v", req.Header)
	}
}
