package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/saphan-ai/tests-studio/adapters/go-test/reporter"
)

// The runner that run() builds is what starts every go test, so its environment is where the
// adapter's credentials must be absent. The negative control is the unscrubbed wiring
// (`Environ: environ`), which carries both variables and turns this test red.
func TestRunnerCarriesNoCredentialOfTheAdapter(t *testing.T) {
	environ := []string{"PATH=/bin", "MOCKS_RUN_ID=r1", "MOCKS_RUN_TOKEN=leg-secret", "TESTS_RUN_TOKEN=child-secret",
		"TESTS_STEP_TRACEPARENT=stale", "TESTS_CORE_URL=http://core.example.test"}
	r := newRunner(environ, []string{"./..."}, []string{"-race"})
	if len(r.Environ) == 0 {
		t.Fatal("the runner has no environment at all")
	}
	for _, kv := range r.Environ {
		if strings.HasPrefix(kv, "MOCKS_RUN_TOKEN=") || strings.HasPrefix(kv, "TESTS_RUN_TOKEN=") || strings.Contains(kv, "secret") {
			t.Errorf("the runner's environment carries %q", kv)
		}
	}
	if len(r.Packages) != 1 || len(r.GoArgs) != 1 || r.GoArgs[0] != "-race" {
		t.Errorf("the runner is %+v, want the packages and the go flags handed to it", r)
	}
}

// A refused flag ends the run with exit 2 and a message before the core is called: the core URL
// here accepts no connection, so a call to it would end with exit 3 instead.
func TestARefusedGoFlagEndsTheRunBeforeTheCoreIsCalled(t *testing.T) {
	for _, flag := range []string{"-run", "-skip", "-count", "-list", "-json", "-test.run", "-test.count=3", "--test.json"} {
		var stdout, stderr bytes.Buffer
		code := run(context.Background(), []string{"./...", "--", flag, "x"}, []string{"TESTS_CORE_URL=http://127.0.0.1:1"}, &stdout, &stderr)
		name := strings.TrimPrefix(strings.TrimLeft(strings.SplitN(flag, "=", 2)[0], "-"), "test.")
		if code != reporter.ExitRefused {
			t.Errorf("%s: exit %d, want %d\n%s", flag, code, reporter.ExitRefused, stderr.String())
		}
		if !strings.Contains(stderr.String(), "-"+name) || !strings.Contains(stderr.String(), "NEXT") {
			t.Errorf("%s: the refusal %q does not name the flag and the next act", flag, stderr.String())
		}
	}
}
