// Command tests-conformance runs the conformance suite of the test-runner specification v1
// against an adapter's sample project. Usage:
//
//	tests-conformance --items N [--mode standalone|leg] [--profile live|import] [--spec-dir DIR] [--timeout 120s]
//	    [--wait-delay 5s] [--pass-env NAME[,NAME...]] -- COMMAND [ARGS...]
//
// It prints one line per check and exits 0, 1, 2, 3 or 4 (spec/TEST_RUNNER_V1.md, "Running the suite").
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/saphan-ai/tests-studio/conformance/internal/checks"
	"github.com/saphan-ai/tests-studio/conformance/internal/harness"
	"github.com/saphan-ai/tests-studio/conformance/internal/schemas"
	"github.com/saphan-ai/tests-studio/conformance/internal/stubs"
)

func main() { os.Exit(run(os.Args[1:])) }

func splitNames(list string) []string {
	var names []string
	for _, n := range strings.Split(list, ",") {
		if n = strings.TrimSpace(n); n != "" {
			names = append(names, n)
		}
	}
	return names
}

func run(args []string) int {
	fs := flag.NewFlagSet("tests-conformance", flag.ContinueOnError)
	items := fs.Int("items", 0, "number of test items in the adapter's sample project (required; the project needs a passing, a failing and a skipped test)")
	mode := fs.String("mode", string(stubs.ModeStandalone), "standalone: the adapter opens its own run; leg: it attaches to a leg's run named in its environment")
	profile := fs.String("profile", checks.ProfileLive, "live: tests carry the step's traceparent to the system under test; import: the adapter reports after the run, so traceparent-to-sut and the stand-in-system half of sample-composition answer NOT APPLICABLE")
	specDir := fs.String("spec-dir", "", "the spec/ directory; found by searching upwards from the working directory when empty")
	timeout := fs.Duration("timeout", harness.DefaultTimeout, "how long the adapter's command may run")
	waitDelay := fs.Duration("wait-delay", harness.DefaultWaitDelay, "how long to wait, after the adapter's command ends or is killed, for a descendant that still holds its output")
	passEnv := fs.String("pass-env", "", "comma-separated names of host environment variables to keep in the adapter's environment, besides the defaults the specification lists")
	if err := fs.Parse(args); err != nil {
		return harness.ExitUsage
	}
	if fs.NArg() == 0 || (*mode != string(stubs.ModeStandalone) && *mode != string(stubs.ModeLeg)) ||
		(*profile != checks.ProfileLive && *profile != checks.ProfileImport) {
		fmt.Fprintln(os.Stderr, "usage: tests-conformance --items N [--mode standalone|leg] [--profile live|import] [--spec-dir DIR] -- COMMAND [ARGS...]")
		return harness.ExitUsage
	}
	dir, err := schemas.FindSpecDir(*specDir, ".")
	if err != nil {
		fmt.Fprintln(os.Stderr, "tests-conformance:", err)
		return harness.ExitUsage
	}
	rep, err := harness.Run(context.Background(), harness.Config{
		Command: fs.Args(), Mode: stubs.Mode(*mode), Profile: *profile, Items: *items, SpecDir: dir, Timeout: *timeout,
		Hosts: &harness.LoopbackHosts{}, Launcher: harness.ExecLauncher{WaitDelay: *waitDelay, PassEnv: splitNames(*passEnv)},
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "tests-conformance:", err)
		return rep.Exit
	}
	for _, l := range rep.Lines {
		fmt.Println(l)
	}
	return rep.Exit
}
