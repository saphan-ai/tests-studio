package harness_test

import (
	"context"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/saphan-ai/tests-studio/conformance/internal/checks"
	"github.com/saphan-ai/tests-studio/conformance/internal/harness"
	"github.com/saphan-ai/tests-studio/conformance/internal/schemas"
	"github.com/saphan-ai/tests-studio/conformance/internal/stubs"
)

// requireSocketsEnv turns the skip of a socket test into a failure. The master sets it when it
// runs the suite outside the jail, so a denied loopback can never read as a pass there.
const requireSocketsEnv = "TESTS_CONFORMANCE_REQUIRE_SOCKETS"

// needLoopback skips a test that starts a listener when the host denies loopback, and says so;
// with TESTS_CONFORMANCE_REQUIRE_SOCKETS=1 it fails instead.
func needLoopback(t *testing.T) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		if os.Getenv(requireSocketsEnv) != "" {
			t.Fatalf("loopback is denied (%v) and %s is set", err, requireSocketsEnv)
		}
		t.Skipf("UNRUN: loopback is denied on this host (%v); run with %s=1 outside the jail", err, requireSocketsEnv)
	}
	_ = ln.Close() // a probe listener; nothing was served on it
}

// buildSampleAdapter builds the fixture adapter as a real executable.
func buildSampleAdapter(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "sample-adapter")
	cmd := exec.Command("go", "build", "-o", bin, "github.com/saphan-ai/tests-studio/conformance/cmd/sample-adapter")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build of the sample adapter: %v\n%s", err, out)
	}
	return bin
}

func runOverSockets(t *testing.T, bin string, mode stubs.Mode, mutant string) harness.Report {
	t.Helper()
	return runOverSocketsAs(t, bin, mode, "", "", mutant)
}

// runOverSocketsAs runs the adapter over loopback reporting as adapterProfile ("" is live) and
// judged as judgedAs ("" is live).
func runOverSocketsAs(t *testing.T, bin string, mode stubs.Mode, adapterProfile, judgedAs, mutant string) harness.Report {
	t.Helper()
	dir, err := schemas.FindSpecDir("", ".")
	if err != nil {
		t.Fatal(err)
	}
	cmd := []string{bin, "--project", projectFile()}
	if adapterProfile != "" {
		cmd = append(cmd, "--profile", adapterProfile)
	}
	if mutant != "" {
		cmd = append(cmd, "--mutant", mutant)
	}
	rep, err := harness.Run(context.Background(), harness.Config{
		Command: cmd, Mode: mode, Profile: judgedAs, Items: sampleItems, SpecDir: dir,
		Hosts: &harness.LoopbackHosts{}, Launcher: harness.ExecLauncher{}, WorkDir: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return rep
}

func TestSocketSampleAdapterPassesEveryCheck(t *testing.T) {
	needLoopback(t)
	bin := buildSampleAdapter(t)
	for _, mode := range []stubs.Mode{stubs.ModeStandalone, stubs.ModeLeg} {
		t.Run(string(mode), func(t *testing.T) {
			rep := runOverSockets(t, bin, mode, "")
			if rep.Exit != harness.ExitPass || len(failedIDs(rep)) != 0 {
				t.Fatalf("exit %d, red %v\n%s", rep.Exit, failedIDs(rep), strings.Join(rep.Lines, "\n"))
			}
		})
	}
}

// TestSocketImportProfileIsAcceptedOverLoopback is the import profile's measurement over real
// sockets and a real executable: accepted in both modes with the two not-applicable checks named,
// and the same adapter judged as live still fails them.
func TestSocketImportProfileIsAcceptedOverLoopback(t *testing.T) {
	needLoopback(t)
	bin := buildSampleAdapter(t)
	for _, mode := range []stubs.Mode{stubs.ModeStandalone, stubs.ModeLeg} {
		t.Run(string(mode), func(t *testing.T) {
			rep := runOverSocketsAs(t, bin, mode, checks.ProfileImport, checks.ProfileImport, "")
			if rep.Exit != harness.ExitPass || len(failedIDs(rep)) != 0 || !contains(rep.Lines, importSummary) {
				t.Fatalf("exit %d, red %v\n%s", rep.Exit, failedIDs(rep), strings.Join(rep.Lines, "\n"))
			}
			live := runOverSocketsAs(t, bin, mode, checks.ProfileImport, "", "")
			if live.Exit != harness.ExitFail || len(failedIDs(live)) != 2 {
				t.Fatalf("judged as live: exit %d, red %v; want the two checks red", live.Exit, failedIDs(live))
			}
		})
	}
}

func TestSocketMutantFailsExactlyItsCheck(t *testing.T) {
	needLoopback(t)
	bin := buildSampleAdapter(t)
	for _, c := range mutantCases {
		t.Run(c.mutant, func(t *testing.T) {
			rep := runOverSockets(t, bin, c.mode, c.mutant)
			got := failedIDs(rep)
			if rep.Exit != c.exit || len(got) != 1 || got[0] != c.failed {
				t.Fatalf("want exit %d and only %s red; got exit %d, red %v\n%s", c.exit, c.failed, rep.Exit, got, strings.Join(rep.Lines, "\n"))
			}
		})
	}
}

// TestSocketRunnerExitCodes is the before-and-after control: a command that does not exist exits 3,
// the v2 mutant exits 2 naming the supported versions, the sample adapter exits 0.
func TestSocketRunnerExitCodes(t *testing.T) {
	needLoopback(t)
	bin := buildSampleAdapter(t)
	missing := runOverSockets(t, filepath.Join(t.TempDir(), "no-such-adapter"), stubs.ModeStandalone, "")
	v2 := runOverSockets(t, bin, stubs.ModeStandalone, "spec-v2")
	good := runOverSockets(t, bin, stubs.ModeStandalone, "")
	for _, c := range []struct {
		name string
		rep  harness.Report
		want int
	}{{"missing command", missing, harness.ExitNoAdapter}, {"v2 mutant", v2, harness.ExitRefused}, {"sample adapter", good, harness.ExitPass}} {
		t.Logf("%s: exit %d: %s", c.name, c.rep.Exit, strings.Join(c.rep.Lines, " | "))
		if c.rep.Exit != c.want {
			t.Errorf("%s: exit %d, want %d", c.name, c.rep.Exit, c.want)
		}
	}
	if !strings.Contains(strings.Join(v2.Lines, "\n"), "serves v1") {
		t.Errorf("the v2 refusal does not name the supported versions: %v", v2.Lines)
	}
}
