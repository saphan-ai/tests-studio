package harness

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"time"

	"example.com/tests-studio/conformance/internal/checks"
	"example.com/tests-studio/conformance/internal/proto"
	"example.com/tests-studio/conformance/internal/schemas"
	"example.com/tests-studio/conformance/internal/stubs"
)

// Exit codes of the runner (specification section "Running the suite").
const (
	ExitPass      = 0 // every check passed
	ExitFail      = 1 // a check failed
	ExitRefused   = 2 // the adapter was refused before checking: unknown spec version
	ExitNoAdapter = 3 // the adapter's command could not be started, or never reached the core
	ExitUsage     = 4 // the suite itself could not run: bad flags, no specification directory
)

// DefaultTimeout bounds one run of the adapter's command.
const DefaultTimeout = 120 * time.Second

// Config is one run of the suite.
type Config struct {
	Command  []string
	Mode     stubs.Mode
	Items    int
	SpecDir  string
	Timeout  time.Duration
	Hosts    Hosts
	Launcher Launcher
	WorkDir  string // where the run result file is placed; a temporary directory when empty
}

// Report is the outcome of a run.
type Report struct {
	Lines   []string // the lines the runner prints, in order
	Exit    int
	Results []checks.Result
	Calls   []stubs.Call
	Input   checks.Input // what the checks read, for callers that want to re-run or vary them
}

// Run starts the stubs, starts the adapter's command against them, and checks the recording.
func Run(ctx context.Context, cfg Config) (Report, error) {
	if cfg.Items < 1 {
		return Report{Exit: ExitUsage}, errors.New("--items must be the number of test items in the adapter's sample project, 1 or more")
	}
	val, err := schemas.Load(cfg.SpecDir)
	if err != nil {
		return Report{Exit: ExitUsage}, err
	}
	workDir := cfg.WorkDir
	if workDir == "" {
		if workDir, err = os.MkdirTemp("", "tests-conformance-"); err != nil {
			return Report{Exit: ExitUsage}, err
		}
		defer os.RemoveAll(workDir) // a scratch directory of this run only
	}
	stack := stubs.New(cfg.Mode, val)
	coreH, mocksH, worldH := stack.Handlers()
	defer cfg.Hosts.Close()
	coreURL, err := cfg.Hosts.Serve("core", coreH)
	if err != nil {
		return Report{Exit: ExitUsage}, err
	}
	mocksURL, err := cfg.Hosts.Serve("mocks", mocksH)
	if err != nil {
		return Report{Exit: ExitUsage}, err
	}
	worldURL, err := cfg.Hosts.Serve("world", worldH)
	if err != nil {
		return Report{Exit: ExitUsage}, err
	}
	stack.SetURLs(coreURL, mocksURL, worldURL)

	resultPath := filepath.Join(workDir, "run-result.json")
	env := []string{
		proto.EnvCoreURL + "=" + coreURL, proto.EnvResultFile + "=" + resultPath,
		proto.EnvSutURL + "=" + worldURL + "/sut",
	}
	legRun, legToken := stack.LegEnv()
	if cfg.Mode == stubs.ModeLeg {
		env = append(env, proto.EnvLegRunID+"="+legRun, proto.EnvLegToken+"="+legToken, proto.EnvMocksURL+"="+mocksURL)
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	out, err := cfg.Launcher.Launch(runCtx, Launch{Args: cfg.Command, Env: env})
	if err != nil {
		return Report{Exit: ExitNoAdapter, Lines: []string{"REFUSED adapter-not-started — the adapter command could not be started: " + err.Error()}}, nil
	}
	calls := stack.Rec.Calls()
	if !anyCore(calls) {
		return Report{Exit: ExitNoAdapter, Calls: calls, Lines: []string{
			fmt.Sprintf("REFUSED adapter-never-reached-core — the adapter command ran (exit %d) and made no call to %s=%s", out.ExitCode, proto.EnvCoreURL, coreURL)}}, nil
	}
	var resultRaw []byte
	if b, err := os.ReadFile(resultPath); err == nil {
		resultRaw = b
	}
	input := checks.Input{
		Calls: calls, Mode: cfg.Mode, Items: cfg.Items, LegRunID: legRun, LegToken: legToken,
		Served: stack.Served(), Stdout: out.Stdout, Stderr: out.Stderr, ResultRaw: resultRaw, Validator: val,
	}
	rep := render(cfg, out, calls, checks.All(input))
	rep.Input = input
	return rep, nil
}

func anyCore(calls []stubs.Call) bool {
	for _, c := range calls {
		if c.Service == stubs.ServiceCore {
			return true
		}
	}
	return false
}

func render(cfg Config, out Output, calls []stubs.Call, results []checks.Result) Report {
	rep := Report{Calls: calls, Results: results}
	if declared, refused := checks.DeclaredVersionRefused(calls); refused {
		for _, r := range results {
			if r.ID == checks.IDSpecVersion {
				rep.Results = []checks.Result{r}
				rep.Lines = append(rep.Lines, r.Line())
			}
		}
		rep.Lines = append(rep.Lines, fmt.Sprintf("REFUSED spec-version-unsupported — the adapter declared spec version %q and the core serves %s; no other check was made", declared, proto.SpecVersion))
		rep.Exit = ExitRefused
		return rep
	}
	failed := 0
	for _, r := range results {
		rep.Lines = append(rep.Lines, r.Line())
		if !r.Pass {
			failed++
		}
	}
	rep.Lines = append(rep.Lines, fmt.Sprintf("SUMMARY %d checks, %d passed, %d failed (%s mode)", len(results), len(results)-failed, failed, cfg.Mode))
	if out.TimedOut {
		rep.Lines = append(rep.Lines, "remark: the adapter command was stopped at the timeout; checks that needed what it had not yet done failed for that reason")
	}
	rep.Lines = append(rep.Lines,
		"remark: this run measured one adapter run in "+string(cfg.Mode)+" mode only; run it again in the other mode before claiming either",
		"remark: the stubs serve one request at a time, so the suite does not measure an adapter's behaviour under concurrent steps")
	rep.Exit = ExitPass
	if failed > 0 {
		rep.Exit = ExitFail
	}
	return rep
}
