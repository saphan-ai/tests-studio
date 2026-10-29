// Command tests-conformance runs the conformance suite of the test-runner specification v1
// against an adapter's sample project. Usage:
//
//	tests-conformance --items N [--mode standalone|leg] [--spec-dir DIR] [--timeout 120s] -- COMMAND [ARGS...]
//
// It prints one line per check and exits 0, 1, 2 or 3 (spec/TEST_RUNNER_V1.md, "Running the suite").
package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"example.com/tests-studio/conformance/internal/harness"
	"example.com/tests-studio/conformance/internal/schemas"
	"example.com/tests-studio/conformance/internal/stubs"
)

func main() { os.Exit(run(os.Args[1:])) }

func run(args []string) int {
	fs := flag.NewFlagSet("tests-conformance", flag.ContinueOnError)
	items := fs.Int("items", 0, "number of test items in the adapter's sample project (required; the project needs a passing, a failing and a skipped test)")
	mode := fs.String("mode", string(stubs.ModeStandalone), "standalone: the adapter opens its own run; leg: it attaches to a leg's run named in its environment")
	specDir := fs.String("spec-dir", "", "the spec/ directory; found by searching upwards from the working directory when empty")
	timeout := fs.Duration("timeout", harness.DefaultTimeout, "how long the adapter's command may run")
	if err := fs.Parse(args); err != nil {
		return harness.ExitUsage
	}
	if fs.NArg() == 0 || (*mode != string(stubs.ModeStandalone) && *mode != string(stubs.ModeLeg)) {
		fmt.Fprintln(os.Stderr, "usage: tests-conformance --items N [--mode standalone|leg] [--spec-dir DIR] -- COMMAND [ARGS...]")
		return harness.ExitUsage
	}
	dir, err := schemas.FindSpecDir(*specDir, ".")
	if err != nil {
		fmt.Fprintln(os.Stderr, "tests-conformance:", err)
		return harness.ExitUsage
	}
	rep, err := harness.Run(context.Background(), harness.Config{
		Command: fs.Args(), Mode: stubs.Mode(*mode), Items: *items, SpecDir: dir, Timeout: *timeout,
		Hosts: &harness.LoopbackHosts{}, Launcher: harness.ExecLauncher{},
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
