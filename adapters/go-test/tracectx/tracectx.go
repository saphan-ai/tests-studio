// Package tracectx is the helper a Go test calls to carry its step's trace context to the system
// under test. tests-go-test starts every test item as a step of its own and puts the step's
// traceparent and baggage in the test process's environment (specification section 6.1); this
// package reads them from there and puts them on an outgoing request, unchanged.
package tracectx

import (
	"net/http"
	"os"
)

// Environment variables tests-go-test sets for the test it starts.
const (
	EnvTraceparent = "TESTS_STEP_TRACEPARENT"
	EnvBaggage     = "TESTS_STEP_BAGGAGE"
)

// Get returns the step's traceparent and baggage. ok is false when the test was not started by
// tests-go-test (no traceparent in the environment); baggage may be empty even when ok is true.
func Get() (traceparent, baggage string, ok bool) {
	traceparent = os.Getenv(EnvTraceparent)
	return traceparent, os.Getenv(EnvBaggage), traceparent != ""
}

// Apply sets the traceparent and baggage headers of req to the step's values and reports whether
// there were any. It leaves req untouched when the test is not running under tests-go-test.
func Apply(req *http.Request) bool {
	tp, bg, ok := Get()
	if !ok {
		return false
	}
	req.Header.Set("traceparent", tp)
	if bg != "" {
		req.Header.Set("baggage", bg)
	}
	return true
}

// Transport wraps base (http.DefaultTransport when nil) so that every request sent through it
// carries the step's trace context. The request it was given is not modified; a copy is sent.
func Transport(base http.RoundTripper) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	return roundTripper{base}
}

type roundTripper struct{ base http.RoundTripper }

func (t roundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	Apply(clone)
	return t.base.RoundTrip(clone)
}
