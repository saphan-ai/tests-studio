// Package stubs holds the two stub cores an adapter is run against, and the stand-in for the
// world around them (the system under test and the mocks' own URLs). Every request any of them
// receives is recorded; the checks read only the recording.
package stubs

import (
	"bytes"
	"io"
	"net/http"
	"sync"
)

// Service names the stub that received a call.
type Service string

// The three stubs. Core is the Tests Studio core API, Mocks the Mocks Studio core API
// (Appendix C of the ecosystem contract), World the system under test and every mock's own URL.
const (
	ServiceCore  Service = "core"
	ServiceMocks Service = "mocks"
	ServiceWorld Service = "world"
)

// maxRecordedBody bounds how much of one request or response body a recording keeps.
const maxRecordedBody = 8 << 20

// Call is one recorded request and the response the stub gave.
type Call struct {
	Seq      int         `json:"seq"`
	Service  Service     `json:"service"`
	Method   string      `json:"method"`
	Path     string      `json:"path"`
	Header   http.Header `json:"header"`
	Body     []byte      `json:"body"`
	Status   int         `json:"status"`
	Response []byte      `json:"response"`
}

// Recorder records the calls its wrapped handlers serve. A wrapped handler serves one request at
// a time, so the recording order is the order the adapter's calls were served in.
type Recorder struct {
	mu    sync.Mutex
	calls []Call
}

// Wrap returns h wrapped so that every request it serves is recorded under svc.
func (r *Recorder) Wrap(svc Service, h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		r.mu.Lock()
		defer r.mu.Unlock()
		body, _ := io.ReadAll(io.LimitReader(req.Body, maxRecordedBody))
		req.Body = io.NopCloser(bytes.NewReader(body))
		cw := &captureWriter{ResponseWriter: w, status: http.StatusOK}
		h.ServeHTTP(cw, req)
		r.calls = append(r.calls, Call{
			Seq: len(r.calls) + 1, Service: svc, Method: req.Method, Path: req.URL.RequestURI(),
			Header: req.Header.Clone(), Body: body, Status: cw.status, Response: cw.body.Bytes(),
		})
	})
}

// Calls returns a copy of the recording.
func (r *Recorder) Calls() []Call {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Call(nil), r.calls...)
}

type captureWriter struct {
	http.ResponseWriter
	status int
	body   bytes.Buffer
}

func (c *captureWriter) WriteHeader(code int) {
	c.status = code
	c.ResponseWriter.WriteHeader(code)
}

func (c *captureWriter) Write(b []byte) (int, error) {
	if c.body.Len() < maxRecordedBody {
		c.body.Write(b)
	}
	return c.ResponseWriter.Write(b)
}
