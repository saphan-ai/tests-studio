# tests-go-test — the Go test runner adapter

`tests-go-test` runs the tests of a Go package with the ordinary `go test` and reports every test
to a Tests Studio core, following the [test runner specification v1](../../spec/TEST_RUNNER_V1.md).
Your tests stay plain Go tests. It uses the standard library only; the module's single dependency
(a JSON Schema validator) is used by its tests and nothing else.

It is accepted by the conformance suite in [`conformance/`](../../conformance): see
[Checking it against the conformance suite](#checking-it-against-the-conformance-suite).

## Install

```
go install github.com/saphan-ai/tests-studio/adapters/go-test/cmd/tests-go-test@latest
```

This needs Go 1.26 or newer (the `go` directive of `go.mod`).

## Run

```
TESTS_CORE_URL=<base URL of the core> tests-go-test ./...
```

```
tests-go-test [-project NAME] [-mock NAME]... [packages] [-- go test flags]
```

- `packages` are `go test` package patterns; the default is `.`.
- `-project` names the project the core records (default: the Go module path).
- `-mock` asks for a mock when a standalone test run is opened; repeat it for several.
- Everything after `--` is added to every `go test` the adapter starts, for example
  `tests-go-test ./... -- -race -tags integration`.

What it does, in the order the specification gives:

1. `GET /v1/spec`; it **refuses by name** (exit 2) when the core serves no version it speaks (`v1`).
2. Lists the tests with `go test -list`. Test, example and fuzz functions are items; a benchmark is
   not. A package that does not build is one item that reports `error` with the compiler's output,
   so a broken package is never a silent absence.
3. Opens a test run, or **attaches** to the leg's run when `MOCKS_RUN_ID` is set (it never opens a
   second run inside a leg).
4. For each test: starts a step (`step_id` is `<package>.<TestName>`), starts the test with the
   step's trace context in its environment, attaches what the test printed as `log` evidence,
   finishes the step with `pass`, `fail`, `error` or `skip`, a duration and, for a failure, text.
   A test that panics, or that never reports a result, is `error`; a failed assertion is `fail`.
5. Emits `run.opened`, `step.started`, `step.finished`, `evidence.stored` and, **before** closing,
   `run.closed`; closes the run; writes the run result to `TESTS_RESULT_FILE` when set; prints the
   evidence line on standard output.

Exit status: `0` all tests passed or were skipped; `1` the verdict is `fail` or `error`, or a call
to the core was refused; `2` it did not start (no `TESTS_CORE_URL`, no tests, a version the core does
not serve); `3` the core could not be reached or would not open or close the run.

### The environment it reads

| variable | meaning |
|---|---|
| `TESTS_CORE_URL` | base URL of the Tests Studio core (required) |
| `TESTS_RESULT_FILE` | file the run result is written to |
| `MOCKS_RUN_ID`, `MOCKS_RUN_TOKEN` | inside a Saphan leg: attach to this run with this credential |

The credential never reaches a command line, an event, a summary, the run result or the adapter's
own output: the credentials of the environment (`MOCKS_RUN_TOKEN`, `TESTS_RUN_TOKEN`) are removed
from the environment of every `go test` it starts, and any credential it has held is replaced by
`[redacted]` in failure text, evidence and messages before they are sent or printed.

## The helper for test authors: `tracectx`

The system under test must see the step's `traceparent` and `baggage`, so that a mock's event and
your test's step meet on one correlation id. The adapter puts them in the test's environment;
the package `github.com/saphan-ai/tests-studio/adapters/go-test/tracectx` puts them on a request:

```go
req, _ := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/orders/42", nil)
if !tracectx.Apply(req) {
	t.Fatal("not running under tests-go-test")
}
resp, err := http.DefaultClient.Do(req)
```

- `tracectx.Apply(req)` sets both headers and reports whether the test is running under the adapter.
- `tracectx.Transport(base)` wraps a transport so every request of a client carries them.
- `tracectx.Get()` returns the two values, for code that builds its own requests.

Under a plain `go test` the helper does nothing (`Apply` returns `false`), so the same tests run
with or without the adapter. The environment variables are `TESTS_STEP_TRACEPARENT` and
`TESTS_STEP_BAGGAGE`.

## Checking it against the conformance suite

The sample project the suite runs it over is [`testdata/sample`](testdata/sample): a passing, a
failing and a skipped test, and one that calls `$TESTS_CONFORMANCE_SUT_URL` through `tracectx`
(4 items). The suite starts its stubs on loopback, so this needs a host that allows listening on
`127.0.0.1`. From the repository root:

```
go -C conformance build -o /tmp/tests-conformance ./cmd/tests-conformance
go -C adapters/go-test build -o /tmp/tests-go-test ./cmd/tests-go-test
cd adapters/go-test
/tmp/tests-conformance --items 4 --mode standalone -- /tmp/tests-go-test ./testdata/sample
/tmp/tests-conformance --items 4 --mode leg        -- /tmp/tests-go-test ./testdata/sample
```

Both must exit 0 with every check `PASS`. `go -C adapters/go-test test ./suite/ -count=1` does the
same from a test; it skips, naming itself, where loopback is denied, and fails when
`TESTS_CONFORMANCE_REQUIRE_SOCKETS=1` is set.

## Honest limits

- **One process per test.** A `go test -json` run alone cannot give each test its own
  `traceparent`: the tests are already running when their events stream out. So the adapter
  starts every test itself, `go test -run '^Name$'` once per item. The cost is one `go test`
  start (link and launch of the test binary; the build itself is cached) per test, and
  `TestMain`, package `init` and any setup run once per test rather than once per package. A test
  that depends on another test having run first, or on state a previous test left behind, will
  not work under the adapter.
- **Only top-level tests are items.** A subtest is part of its parent: it is not a step of its own,
  and a failing subtest fails the parent step with the subtest's output as the failure text.
- **Tests run one after another**, not in parallel with each other (`t.Parallel` still works inside
  one test binary).
- **Mocks are not controlled from the test.** The credentials stay with the adapter, so a Go test
  cannot call the Mocks Studio core's `setScenario` through this adapter. Scenario control is not
  part of this version.
- A line that a test prints and that looks like a test name can be mistaken for one while listing;
  keep `TestMain` and `init` quiet.
