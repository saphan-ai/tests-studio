# Tests Studio

**Tests Studio turns a run of tests into a verdict you can check: every test step is tied to what the system under test, and the mocks standing in for its neighbours, really did.**
This repository holds the public part: the specification any test framework follows to report to Tests Studio, and the conformance suite that tells you whether your adapter follows it.
If the suite passes against your adapter, your adapter is accepted. You do not need our code, and you do not need our permission.

## The problem

A test run says "42 passed". It does not say what the system did while the tests ran, whether the mock that should have answered slowly really answered slowly, or whether the report was written by the run it claims to describe.

A test framework also reports in its own shape. Every framework you add means another reporter, written again, drifting from the others.

## How it works

Tests Studio is one of three parts of one platform. Saphan Studio's fleet writes the code. Mocks Studio supplies mocks that stand in for the real systems around it. Tests Studio runs the tests against those mocks and gives the verdict.

The three meet on one number: the **correlation id**. For each test the adapter asks Tests Studio to start a step; the answer carries a W3C `traceparent`. The test hands that header to the system under test, the system passes it on, and the mocks record it. At the end the run result carries a digest of every mock event that belongs to the run, so anyone can ask the mocks for those events and recompute it. A mismatch is a refusal, never a pass.

The piece that makes this work for any framework is the **runner adapter**: a small program or plugin that sits inside a test framework and makes these calls:

1. ask which specification versions the core serves, and pick one it speaks;
2. open a test run (or attach to the run of a Saphan leg);
3. for each test item, start a step, give the `traceparent` to the test, and finish the step with one result;
4. attach evidence, emit live events, close the run, and publish the run result.

The specification in [`spec/TEST_RUNNER_V1.md`](spec/TEST_RUNNER_V1.md) says exactly what each call carries and which refusals exist. The conformance suite plays the core and the mocks, watches every call the adapter makes, and prints one `PASS` or `FAIL` line per rule.

## See a real example

The suite ships with a tiny adapter of its own, `sample-adapter`, that reads a three-test sample project (one test passes, one fails, one is skipped). Build both, then run the suite against it, in both modes:

```sh
go -C conformance build -o ../bin/tests-conformance ./cmd/tests-conformance
go -C conformance build -o ../bin/sample-adapter ./cmd/sample-adapter
./bin/tests-conformance --items 3 --mode standalone -- ./bin/sample-adapter --project conformance/testdata/sample-project/tests.json  # needs: listener
./bin/tests-conformance --items 3 --mode leg -- ./bin/sample-adapter --project conformance/testdata/sample-project/tests.json  # needs: listener
```

You need Go 1.26 or newer. The suite starts two stand-in servers on your own machine's loopback address, so it needs permission to listen there.

What you should see is one line per check, ending in exit code 0:

<!-- UNRUN: the run below was not made. The machine this page was written on denies loopback
     (see "Honest limits"). Replace this block with the real, unedited output of the two
     commands above, and the date and machine kind of the run. Do not write it by hand. -->

```text
UNRUN — output of ./bin/tests-conformance --items 3 --mode standalone -- ./bin/sample-adapter ...
(to be pasted from a machine that allows loopback; never typed in)
```

What the suite prints on a machine that denies loopback was measured and is real. It says so and stops with exit code 4, before any check:

```text
tests-conformance: cannot listen on loopback: listen tcp 127.0.0.1:0: bind: operation not permitted
```

The exit codes are 0 (every check passed), 1 (a check failed), 2 (the adapter declared a spec version the core does not serve), 3 (the command could not start or never reached the core) and 4 (the suite could not run).

## Make your test framework report to Tests Studio

1. Read [`spec/TEST_RUNNER_V1.md`](spec/TEST_RUNNER_V1.md). It is the rules for you, written for someone who has never seen our core.
2. Write the adapter in your framework's own language. Its behaviour is the specification; nothing here is a library to link.
3. Make a small sample project in that framework with a passing, a failing and a skipped test. At least one test must call the stand-in system under test at `$TESTS_CONFORMANCE_SUT_URL` with the `traceparent` and `baggage` headers its step was given.
4. Build the suite and run it against your adapter, once in each mode:

```sh
go -C conformance build -o ../bin/tests-conformance ./cmd/tests-conformance
./bin/tests-conformance --items 3 --mode standalone -- <your adapter command> <its arguments>  # needs: adapter
./bin/tests-conformance --items 3 --mode leg -- <your adapter command> <its arguments>  # needs: adapter
```

`--items` is the number of tests in your sample project. The adapter runs in a scrubbed environment: only the variables the specification lists reach it. If your framework needs another (`PYTHONPATH`, say), name it with `--pass-env PYTHONPATH`.

5. **Your adapter is accepted when every check passes in both modes.** Open an issue or a pull request to have it listed here; see [`CONTRIBUTING.md`](CONTRIBUTING.md).

Reference adapters for Go's `testing`, pytest and Playwright are planned. None has landed in this repository yet.

## What is measured

Each number below is recomputed from its command by the documentation check on every run; if the specification changes and the number does not, that check goes red. Measured 2026-10-30 on the commit this page was written against.

- **13** checks the suite runs against an adapter: `sed -n '/^\*\*Checks\*\*/,/^\*\*Mutants/p' spec/TEST_RUNNER_V1.md | grep -c '^| [^ a-z|][a-z0-9-]*[^ a-z|] |'`
- **8** mutants of the sample adapter, each breaking one rule and failing exactly one check: `sed -n '/^\*\*Mutants/,/^## 13/p' spec/TEST_RUNNER_V1.md | grep -c '^| [^ a-z|][a-z0-9-]*[^ a-z|] |'`
- **18** refusal reason codes the specification names for the core: `sed -n '/Reason codes of the Tests Studio core/,/^A call made/p' spec/TEST_RUNNER_V1.md | grep -o '[^ a-z|][a-z]*-[a-z-]*[^ a-z|]' | wc -l`
- **3** tests in the sample project (pass, fail, skip): `grep -c '"outcome"' conformance/testdata/sample-project/tests.json`

What is not here, because it is not measured on this page: how fast the suite runs, and how many adapters pass it. The count of tests in the suite's own test run depends on whether the machine allows loopback, so it is not a fixed number and none is printed here.

## Honest limits

Updated with every push to this repository. Last measured 2026-10-30.

- **No reference adapter has landed.** The Go test reporter, the pytest plugin and the Playwright reporter are planned; today the only adapter here is the suite's own `sample-adapter`, which reads a JSON file rather than a real framework's tests.
- **The suite needs loopback.** It starts its stand-in servers on `127.0.0.1`. Where that is denied it exits 4, and the suite's own tests that use real sockets skip themselves. Continuous integration must set `TESTS_CONFORMANCE_REQUIRE_SOCKETS=1` so that a skipped measurement fails instead of passing quietly.
- **The real-output example above was not run for this page.** The machine it was written on denies loopback. The marked place stays marked until a run on a machine that allows loopback is pasted in.
- **On Windows the timeout kills only the adapter's own process.** On Unix the suite kills the whole process group (specification section 12), so a test worker or browser the adapter started dies with it. Anywhere else only the direct child is killed; the suite then waits at most `--wait-delay` for any descendant that still holds the output, and closes the pipes. A runaway grandchild can outlive the run there.
- **The suite checks the calls an adapter makes, not whether it is a good adapter.** An adapter that passes makes the right calls with the right shapes; it can still report a framework's results wrongly. The checks are listed in the specification.
- **The core is not here.** The suite plays a recording core and a stand-in Mocks Studio core. A pass shows your adapter speaks the specification, not that it works against the real core.
- **Specification v1 only.** A new optional field keeps `v1`; a new required field, a removed one or a changed meaning is `v2`.

## Built in public

The specification and its conformance suite are public so that anyone can write an adapter and know, without asking us, whether it is right. They were written by Saphan Studio's own fleet of agents under a human owner, and reviewed before they landed. Every weakness found in the suite, and the commit that fixed it, is named in [`SECURITY.md`](SECURITY.md).

Problems and questions go to the issue tracker of this repository. A vulnerability goes to the private channel in [`SECURITY.md`](SECURITY.md), never to a public issue.

## Repository layout

```text
spec/                       the specification and its schemas
  TEST_RUNNER_V1.md         the rules an adapter follows
  runner-v1.openapi.yaml    the calls of section 6, as OpenAPI 3.1
  schemas/                  tests.event/v1 and tests.run-result/v1
  ecosystem-v1/             copies of the shared contract with Mocks Studio; do not edit
conformance/                the Go module of the conformance suite
  cmd/tests-conformance/    the suite's command
  cmd/sample-adapter/       the fixture adapter every check passes against
  internal/                 the harness, the stand-in servers, the checks
  testdata/sample-project/  the three-test sample project
docscheck/                  the test that keeps these documents honest
LICENSE, LICENSE-docs       the two licences
```

The core of Tests Studio (storage, scheduling, the console) is closed and not in this repository.

**Licences.** The code in this repository (`conformance/`, `docscheck/`) is Apache-2.0: see [`LICENSE`](LICENSE). The documents and the specification (`README.md`, `SECURITY.md`, `CONTRIBUTING.md`, `spec/`) are CC BY 4.0: see [`LICENSE-docs`](LICENSE-docs). The files under `spec/ecosystem-v1/` are copies of a shared contract and are covered by the same CC BY 4.0 licence.

## Documentation

- [`spec/TEST_RUNNER_V1.md`](spec/TEST_RUNNER_V1.md): the specification, start to finish.
- [`SECURITY.md`](SECURITY.md): what the suite treats as untrusted and what it protects, and how to report a vulnerability.
- [`CONTRIBUTING.md`](CONTRIBUTING.md): how to report a problem, add an adapter or change this repository.

## For technical readers

- The suite is a Go module, `github.com/saphan-ai/tests-studio/conformance`, whose only dependencies are a JSON Schema validator and a YAML reader. It needs Go 1.26 or newer.
- The adapter is started as a child process in its own process group, with a scrubbed environment, and bounded by `--timeout` (default 120 seconds).
- The checks read a recording of every call the adapter made to the two stand-in servers. A check that measures something the adapter must do fails when there is nothing to measure; a check that measures something it must not do passes only after counting the calls it looked at.
- The tests of the suite itself run with the commands below. The first runs the suite's own tests; the second runs the documentation check that reads this page.

```sh
go -C conformance test ./... -count=1
go -C docscheck test ./... -count=1  # needs: self
```
