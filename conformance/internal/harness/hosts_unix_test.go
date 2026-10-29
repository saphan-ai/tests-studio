//go:build unix

package harness_test

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/saphan-ai/tests-studio/conformance/internal/harness"
)

const (
	grandchildLifetimeSeconds = 20               // how long the grandchild would live if nothing killed it
	launchBound               = 10 * time.Second // the longest a bounded Launch may take
	pidGoneWithin             = 3 * time.Second
)

// TestExecLauncherTimeoutStopsAGrandchildThatHoldsStdout starts a shell whose child (a sleeping
// grandchild) inherits the output pipes, and stops them at the timeout. It opens no socket.
// Without a process-group kill and a wait delay, Launch returns only when the grandchild exits.
func TestExecLauncherTimeoutStopsAGrandchildThatHoldsStdout(t *testing.T) {
	t.Parallel()
	pidFile := filepath.Join(t.TempDir(), "grandchild.pid")
	script := "sleep " + strconv.Itoa(grandchildLifetimeSeconds) + " & echo $! > " + pidFile + "; wait"
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	start := time.Now()
	out, err := harness.ExecLauncher{WaitDelay: time.Second}.Launch(ctx, harness.Launch{Args: []string{"sh", "-c", script}})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatal(err)
	}
	if elapsed > launchBound {
		t.Fatalf("Launch took %s; the timeout was 500ms and the grandchild lives %ds, so the timeout did not bound the command", elapsed, grandchildLifetimeSeconds)
	}
	if !out.TimedOut {
		t.Errorf("TimedOut is false after %s; the context had expired", elapsed)
	}
	raw, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatalf("the shell never recorded its grandchild: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatalf("grandchild pid %q: %v", raw, err)
	}
	deadline := time.Now().Add(pidGoneWithin)
	for processAlive(pid) {
		if time.Now().After(deadline) {
			t.Fatalf("grandchild %d is still alive %s after Launch returned; the process group was not killed", pid, pidGoneWithin)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestExecLauncherScrubsTheEnvironmentExceptWhatIsPassed pins specification section 12: a host
// variable outside the default list does not reach the adapter, VIRTUAL_ENV does, and --pass-env
// adds one more. It opens no socket.
func TestExecLauncherScrubsTheEnvironmentExceptWhatIsPassed(t *testing.T) {
	t.Setenv("CONFORMANCE_HOST_SECRET", "leaked")
	t.Setenv("CONFORMANCE_EXTRA", "extra")
	t.Setenv("VIRTUAL_ENV", "/venv")
	script := "echo secret=$CONFORMANCE_HOST_SECRET extra=$CONFORMANCE_EXTRA venv=$VIRTUAL_ENV own=$OWN"
	run := func(l harness.ExecLauncher) string {
		out, err := l.Launch(context.Background(), harness.Launch{Args: []string{"sh", "-c", script}, Env: []string{"OWN=given"}})
		if err != nil {
			t.Fatal(err)
		}
		return strings.TrimSpace(out.Stdout)
	}
	if got, want := run(harness.ExecLauncher{}), "secret= extra= venv=/venv own=given"; got != want {
		t.Errorf("default environment: got %q, want %q", got, want)
	}
	if got, want := run(harness.ExecLauncher{PassEnv: []string{"CONFORMANCE_EXTRA"}}), "secret= extra=extra venv=/venv own=given"; got != want {
		t.Errorf("with PassEnv: got %q, want %q", got, want)
	}
}

// processAlive says whether pid names a live process; a zombie awaiting its reaper counts as gone.
func processAlive(pid int) bool {
	if syscall.Kill(pid, 0) != nil {
		return false
	}
	state, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return true // no /proc (macOS): a signal-able process is alive
	}
	return !strings.Contains(string(state), ") Z")
}
