// Package gotest is the reporter.Runner that uses the `go` command: `go test -list` enumerates the
// test items, and one `go test -json -run '^Name$'` per item runs them, so that each test is a
// process of its own and can be handed its own step's trace context (see the README for the cost).
package gotest

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/saphan-ai/tests-studio/adapters/go-test/reporter"
)

// Runner runs Go tests with the go command.
type Runner struct {
	GoBin    string   // the go command; "go" when empty
	Packages []string // package patterns, as `go test` takes them
	GoArgs   []string // flags added to every go test invocation, for example -race or -tags=x
	Environ  []string // the environment of every go command, KEY=value; already scrubbed of credentials
	Dir      string   // working directory of the go commands; the current one when empty
}

// List implements reporter.Runner.
func (r Runner) List(ctx context.Context) ([]reporter.Item, error) {
	args := append([]string{"test", "-list", ".*"}, r.GoArgs...)
	args = append(args, r.Packages...)
	out, err := r.command(ctx, args...).CombinedOutput()
	items := ParseList(string(out))
	if len(items) == 0 && err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			return nil, fmt.Errorf("the go command could not be run: %w", err)
		}
		return nil, fmt.Errorf("go test -list found nothing and exited with an error: %s", strings.TrimSpace(string(out)))
	}
	return items, nil
}

// Run implements reporter.Runner.
func (r Runner) Run(ctx context.Context, it reporter.Item, env []string) reporter.Outcome {
	if it.ListFailure != "" {
		return reporter.Outcome{Result: reporter.ResultError, Failure: it.ListFailure}
	}
	args := append([]string{"test", "-json", "-count=1", "-run", "^" + regexp.QuoteMeta(it.Name) + "$"}, r.GoArgs...)
	args = append(args, it.Package)
	cmd := r.command(ctx, args...)
	cmd.Env = append(cmd.Env, env...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	start := time.Now()
	runErr := cmd.Run()
	out := Classify(it.Name, stdout.Bytes(), time.Since(start))
	if out.Result == reporter.ResultError && out.Failure == "" {
		out.Failure = strings.TrimSpace(stderr.String())
		if out.Failure == "" && runErr != nil {
			out.Failure = "go test " + it.Package + " reported no result for " + it.Name + ": " + runErr.Error()
		}
	}
	return out
}

func (r Runner) command(ctx context.Context, args ...string) *exec.Cmd {
	bin := r.GoBin
	if bin == "" {
		bin = "go"
	}
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = r.Dir
	cmd.Env = append([]string(nil), r.Environ...)
	return cmd
}

var (
	okLine   = regexp.MustCompile(`^ok\s+(\S+)`)
	failLine = regexp.MustCompile(`^FAIL\s+(\S+)`)
	noTests  = regexp.MustCompile(`^\?\s+(\S+)\s+\[no test files\]`)
	testName = regexp.MustCompile(`^(Test|Example|Fuzz)\S*$`)
)

// ParseList reads the output of `go test -list '.*' <packages>`. For each package the names come
// first and a line `ok <package> <time>` closes the block; `FAIL <package> …` closes a block of
// failure text instead, and the package becomes one item that stands for the whole package, so a
// package that does not build is a reported error and never a silent absence. Benchmarks are not
// test items. A package without tests yields nothing.
func ParseList(output string) []reporter.Item {
	var items []reporter.Item
	var names, text []string
	sc := bufio.NewScanner(strings.NewReader(output))
	sc.Buffer(make([]byte, 0, 64<<10), 4<<20)
	for sc.Scan() {
		line := sc.Text()
		switch {
		case okLine.MatchString(line):
			pkg := okLine.FindStringSubmatch(line)[1]
			for _, n := range names {
				items = append(items, reporter.Item{Package: pkg, Name: n})
			}
			names, text = nil, nil
		case failLine.MatchString(line):
			pkg := failLine.FindStringSubmatch(line)[1]
			text = append(text, line)
			items = append(items, reporter.Item{Package: pkg, ListFailure: strings.TrimSpace(strings.Join(text, "\n"))})
			names, text = nil, nil
		case noTests.MatchString(line):
			names, text = nil, nil
		default:
			text = append(text, line)
			if testName.MatchString(line) {
				names = append(names, line)
			}
		}
	}
	return items
}

type testEvent struct {
	Action  string  `json:"Action"`
	Package string  `json:"Package"`
	Test    string  `json:"Test"`
	Output  string  `json:"Output"`
	Elapsed float64 `json:"Elapsed"`
}

// Classify turns the `go test -json` output of one `-run '^name$'` invocation into an outcome.
// The result of the item is the pass, fail or skip event of the test named name. The result is
// `error`, not `fail`, when the test panicked or produced no result at all (the package did not
// build, timed out or crashed): a failed assertion is a test saying no; those are the test not
// getting to say anything. Output of subtests is part of their parent's failure text.
func Classify(name string, jsonOutput []byte, wall time.Duration) reporter.Outcome {
	var testOut, pkgOut []string
	result, elapsed, haveElapsed := "", 0.0, false
	sc := bufio.NewScanner(bytes.NewReader(jsonOutput))
	sc.Buffer(make([]byte, 0, 64<<10), 4<<20)
	for sc.Scan() {
		var ev testEvent
		if err := json.Unmarshal(sc.Bytes(), &ev); err != nil {
			pkgOut = append(pkgOut, sc.Text()) // a line that is not an event: build output of an older go command
			continue
		}
		switch {
		case ev.Test == name && (ev.Action == "pass" || ev.Action == "fail" || ev.Action == "skip"):
			result, elapsed, haveElapsed = ev.Action, ev.Elapsed, true
		case ev.Action == "output" && (ev.Test == name || strings.HasPrefix(ev.Test, name+"/")):
			testOut = append(testOut, ev.Output)
		case ev.Action == "output" && ev.Test == "":
			pkgOut = append(pkgOut, ev.Output)
		}
	}
	duration := wall
	if haveElapsed {
		duration = time.Duration(elapsed * float64(time.Second))
	}
	text := strings.TrimSpace(strings.Join(testOut, ""))
	switch {
	case result == "":
		pkg := strings.TrimSpace(strings.Join(pkgOut, ""))
		return reporter.Outcome{Result: reporter.ResultError, Duration: duration, Failure: pkg, Output: pkg}
	case result == "skip":
		return reporter.Outcome{Result: reporter.ResultSkip, Duration: duration, Output: text}
	case result == "pass":
		return reporter.Outcome{Result: reporter.ResultPass, Duration: duration, Output: text}
	case panicked(testOut):
		return reporter.Outcome{Result: reporter.ResultError, Duration: duration, Failure: text, Output: text}
	default:
		return reporter.Outcome{Result: reporter.ResultFail, Duration: duration, Failure: text, Output: text}
	}
}

func panicked(output []string) bool {
	for _, chunk := range output {
		for _, line := range strings.Split(chunk, "\n") {
			if strings.HasPrefix(line, "panic: ") || strings.HasPrefix(line, "fatal error: ") {
				return true
			}
		}
	}
	return false
}
