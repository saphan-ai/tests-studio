package main

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/saphan-ai/tests-studio/conformance/internal/harness"
)

// TestRunnerExitsWithUsageCodeWhenTheSuiteCannotRun covers every exit-4 path that is reached
// before any listener is started, so it opens no socket.
func TestRunnerExitsWithUsageCodeWhenTheSuiteCannotRun(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		args []string
	}{
		{"an unknown flag", []string{"--no-such-flag"}},
		{"no adapter command", []string{"--items", "3"}},
		{"an unknown mode", []string{"--items", "3", "--mode", "sideways", "--", "adapter"}},
		{"no --items", []string{"--", "adapter"}},
		{"--items below one", []string{"--items", "0", "--", "adapter"}},
		{"a spec directory that does not exist", []string{"--items", "3", "--spec-dir", filepath.Join(t.TempDir(), "absent"), "--", "adapter"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := run(c.args); got != harness.ExitUsage {
				t.Fatalf("run(%q) exited %d; want %d", c.args, got, harness.ExitUsage)
			}
		})
	}
}

func TestSplitNamesDropsEmptyEntries(t *testing.T) {
	t.Parallel()
	got := splitNames(" VIRTUAL_ENV, ,NODE_PATH,")
	if want := []string{"VIRTUAL_ENV", "NODE_PATH"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
	if got := splitNames(""); len(got) != 0 {
		t.Fatalf("an empty list gave %q", got)
	}
}
