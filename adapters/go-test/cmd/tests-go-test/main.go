// Command tests-go-test runs a Go package's tests and reports every test to a Tests Studio core,
// following the test runner specification v1. Usage:
//
//	tests-go-test [-project NAME] [-mock NAME]... [packages] [-- go test flags]
package main

import (
	"bytes"
	"context"
	"flag"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"

	"github.com/saphan-ai/tests-studio/adapters/go-test/gotest"
	"github.com/saphan-ai/tests-studio/adapters/go-test/reporter"
)

type mockList []string

func (m *mockList) String() string     { return strings.Join(*m, ",") }
func (m *mockList) Set(v string) error { *m = append(*m, v); return nil }

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	code := run(ctx, os.Args[1:], os.Environ(), os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}

func run(ctx context.Context, args, environ []string, stdout, stderr io.Writer) int {
	own, goArgs := args, []string(nil)
	for i, a := range args {
		if a == "--" {
			own, goArgs = args[:i], args[i+1:]
			break
		}
	}
	fs := flag.NewFlagSet(reporter.Name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	project := fs.String("project", "", "project name reported to the core (default: the Go module path)")
	var mocks mockList
	fs.Var(&mocks, "mock", "a mock to ask for when a standalone test run is opened; repeatable")
	if err := fs.Parse(own); err != nil {
		return reporter.ExitRefused
	}
	pkgs := fs.Args()
	if len(pkgs) == 0 {
		pkgs = []string{"."}
	}
	base := reporter.Scrub(environ)
	if *project == "" {
		*project = defaultProject(ctx, base)
	}
	return reporter.Run(ctx, reporter.Config{
		Environ: environ, Project: *project, Mocks: mocks, Stdout: stdout, Stderr: stderr,
		Runner: gotest.Runner{Packages: pkgs, GoArgs: goArgs, Environ: base},
	})
}

// defaultProject is the module path of the current directory, or the directory's own name.
func defaultProject(ctx context.Context, environ []string) string {
	cmd := exec.CommandContext(ctx, "go", "list", "-m")
	cmd.Env = environ
	var out bytes.Buffer
	cmd.Stdout = &out
	if cmd.Run() == nil {
		if mod := strings.TrimSpace(out.String()); mod != "" && !strings.Contains(mod, "\n") {
			return mod
		}
	}
	if wd, err := os.Getwd(); err == nil {
		return filepath.Base(wd)
	}
	return reporter.Name
}
