// Package suite holds the one test that lets the conformance suite judge this adapter: it builds
// the suite's runner and the adapter, and runs the runner over the sample project in both modes.
package suite

import (
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// requireSocketsEnv is the suite's own convention (spec/TEST_RUNNER_V1.md section 12): the tests
// that need loopback skip themselves where it is denied, and fail where this variable is set.
const requireSocketsEnv = "TESTS_CONFORMANCE_REQUIRE_SOCKETS"

// allPassed matches the suite's summary line when every check it ran passed, whatever their number:
// the count grows with the specification (13 checks in v1, 15 in v1.1).
var allPassed = regexp.MustCompile(`SUMMARY ([1-9][0-9]*) checks, ([1-9][0-9]*) passed, 0 failed`)

// sampleItems is the number of test items in testdata/sample.
const sampleItems = "4"

func needLoopback(t *testing.T) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		if os.Getenv(requireSocketsEnv) != "" {
			t.Fatalf("loopback is denied (%v) and %s is set", err, requireSocketsEnv)
		}
		t.Skipf("UNRUN: loopback is denied on this host (%v); the conformance suite starts its stubs there. Run with %s=1 outside the jail", err, requireSocketsEnv)
	}
	_ = ln.Close() // a probe listener; nothing was served on it
}

func build(t *testing.T, dir, out, pkg string) {
	t.Helper()
	cmd := exec.Command("go", "-C", dir, "build", "-o", out, pkg)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build %s: %v\n%s", pkg, err, b)
	}
}

func TestConformanceSuiteAcceptsTheAdapter(t *testing.T) {
	needLoopback(t)
	moduleDir, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(moduleDir, "..", "..")
	bin := t.TempDir()
	runner, adapter := filepath.Join(bin, "tests-conformance"), filepath.Join(bin, "tests-go-test")
	build(t, filepath.Join(root, "conformance"), runner, "./cmd/tests-conformance")
	build(t, moduleDir, adapter, "./cmd/tests-go-test")
	for _, mode := range []string{"standalone", "leg"} {
		t.Run(mode, func(t *testing.T) {
			cmd := exec.Command(runner, "--items", sampleItems, "--mode", mode, "--spec-dir", filepath.Join(root, "spec"), "--", adapter, "./testdata/sample")
			cmd.Dir = moduleDir
			out, err := cmd.CombinedOutput()
			t.Logf("%s", out)
			if err != nil {
				t.Fatalf("tests-conformance --mode %s: %v", mode, err)
			}
			m := allPassed.FindStringSubmatch(string(out))
			if strings.Contains(string(out), "FAIL ") || m == nil || m[1] != m[2] {
				t.Errorf("not every check passed in %s mode", mode)
			}
		})
	}
}
