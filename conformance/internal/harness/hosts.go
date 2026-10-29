// Package harness runs an adapter's command against the stub cores and reports the checks. The
// two seams that touch the operating system, where the stubs listen and how the command starts,
// are interfaces, so the whole run can also be made in one process without a socket.
package harness

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// Hosts serves the stubs and says where each one is.
type Hosts interface {
	// Serve starts h under the given name and returns the base URL clients use for it.
	Serve(name string, h http.Handler) (string, error)
	// Close stops every started server.
	Close()
}

const (
	serverShutdownGrace = 2 * time.Second
	readHeaderTimeout   = 10 * time.Second
)

// LoopbackHosts serves each stub on its own loopback listener on an ephemeral port.
type LoopbackHosts struct {
	servers []*http.Server
}

// Serve implements Hosts.
func (l *LoopbackHosts) Serve(_ string, h http.Handler) (string, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", fmt.Errorf("cannot listen on loopback: %w", err)
	}
	srv := &http.Server{Handler: h, ReadHeaderTimeout: readHeaderTimeout}
	l.servers = append(l.servers, srv)
	go func() { _ = srv.Serve(ln) }() // Serve returns http.ErrServerClosed at Close; nothing else to report
	return "http://" + ln.Addr().String(), nil
}

// Close implements Hosts.
func (l *LoopbackHosts) Close() {
	ctx, cancel := context.WithTimeout(context.Background(), serverShutdownGrace)
	defer cancel()
	for _, s := range l.servers {
		_ = s.Shutdown(ctx) // a server that will not drain is closed with the process
	}
}

// MemoryHosts serves the stubs without a socket: each stub gets a URL under example.test and
// RoundTripper routes requests to the handlers directly. An adapter run in the same process
// uses RoundTripper as its HTTP transport.
type MemoryHosts struct {
	mu       sync.Mutex
	handlers map[string]http.Handler
}

// Serve implements Hosts.
func (m *MemoryHosts) Serve(name string, h http.Handler) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.handlers == nil {
		m.handlers = map[string]http.Handler{}
	}
	host := name + ".example.test"
	m.handlers[host] = h
	return "http://" + host, nil
}

// Close implements Hosts.
func (m *MemoryHosts) Close() {}

// RoundTripper returns the transport that reaches the served handlers.
func (m *MemoryHosts) RoundTripper() http.RoundTripper { return memoryTransport{m} }

type memoryTransport struct{ m *MemoryHosts }

func (t memoryTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	t.m.mu.Lock()
	h := t.m.handlers[req.URL.Host]
	t.m.mu.Unlock()
	if h == nil {
		return nil, fmt.Errorf("no stub is served at host %q", req.URL.Host)
	}
	rec := &memoryResponse{header: http.Header{}, status: http.StatusOK}
	h.ServeHTTP(rec, req)
	return &http.Response{
		Status: fmt.Sprintf("%d %s", rec.status, http.StatusText(rec.status)), StatusCode: rec.status,
		Header: rec.header, Body: io.NopCloser(strings.NewReader(rec.body.String())), Request: req,
		ContentLength: int64(rec.body.Len()),
	}, nil
}

type memoryResponse struct {
	header http.Header
	status int
	body   strings.Builder
}

func (r *memoryResponse) Header() http.Header         { return r.header }
func (r *memoryResponse) WriteHeader(code int)        { r.status = code }
func (r *memoryResponse) Write(b []byte) (int, error) { return r.body.Write(b) }

// Launch is one start of the adapter's command.
type Launch struct {
	Args []string // the command and its arguments
	Env  []string // KEY=value entries the adapter's environment carries
}

// Output is what a finished command left behind.
type Output struct {
	Stdout, Stderr string
	ExitCode       int
	TimedOut       bool
}

// Launcher starts the adapter's command. An error means the command could not be started.
type Launcher interface {
	Launch(ctx context.Context, l Launch) (Output, error)
}

// passthroughEnv names the host variables an adapter's environment keeps, so that toolchains and
// temporary directories still work while no other secret of the host reaches the adapter.
var passthroughEnv = []string{
	"PATH", "HOME", "TMPDIR", "LANG",
	"GOCACHE", "GOMODCACHE", "GOFLAGS",
	"VIRTUAL_ENV", "NODE_PATH", "PLAYWRIGHT_BROWSERS_PATH",
	"SystemRoot", "SystemDrive", "ComSpec", "PATHEXT", "USERPROFILE", "TEMP", "TMP", "APPDATA", "LOCALAPPDATA",
}

const maxOutputBytes = 1 << 20

// DefaultWaitDelay is how long the runner waits, after the adapter's command exits or is killed,
// for a descendant that still holds the output pipes before it closes them.
const DefaultWaitDelay = 5 * time.Second

// ExecLauncher runs the command as a child process.
type ExecLauncher struct {
	// WaitDelay bounds the wait for output pipes a descendant keeps open; DefaultWaitDelay when zero.
	WaitDelay time.Duration
	// PassEnv names host environment variables kept in the adapter's environment besides the
	// defaults (passthroughEnv).
	PassEnv []string
}

// Launch implements Launcher.
func (e ExecLauncher) Launch(ctx context.Context, l Launch) (Output, error) {
	if len(l.Args) == 0 {
		return Output{}, errors.New("no adapter command was given")
	}
	cmd := exec.CommandContext(ctx, l.Args[0], l.Args[1:]...)
	ownProcessGroup(cmd)
	cmd.WaitDelay = e.WaitDelay
	if cmd.WaitDelay <= 0 {
		cmd.WaitDelay = DefaultWaitDelay
	}
	env := append([]string(nil), l.Env...)
	for _, k := range append(append([]string(nil), passthroughEnv...), e.PassEnv...) {
		if v, ok := os.LookupEnv(k); ok {
			env = append(env, k+"="+v)
		}
	}
	cmd.Env = env
	var stdout, stderr limitedBuffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	out := Output{Stdout: stdout.String(), Stderr: stderr.String()}
	var exitErr *exec.ExitError
	switch {
	case err == nil, errors.Is(err, exec.ErrWaitDelay): // the command exited; a descendant holding the pipes was cut off
	case ctx.Err() != nil:
		out.TimedOut, out.ExitCode = true, -1
	case errors.As(err, &exitErr):
		out.ExitCode = exitErr.ExitCode()
	default:
		return Output{}, err
	}
	return out, nil
}

type limitedBuffer struct{ b strings.Builder }

func (l *limitedBuffer) Write(p []byte) (int, error) {
	if room := maxOutputBytes - l.b.Len(); room > 0 {
		if len(p) > room {
			l.b.Write(p[:room])
		} else {
			l.b.Write(p)
		}
	}
	return len(p), nil
}

func (l *limitedBuffer) String() string { return l.b.String() }
