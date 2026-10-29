# Security

This repository holds a specification and a program, `tests-conformance`, that runs **someone else's code** to check it: your test-runner adapter, started as a child process, and the sample project it runs. It also starts two small servers on your machine's loopback address that play the Tests Studio core and the Mocks Studio core. Each of those parts is a place something could reach further than it should: the adapter could read your secrets or outlive its run, a recording could be forged, a result could pass without being measured.

This page says what the suite treats as untrusted, what it protects, how, what has been found and fixed, and what is still being checked. It was read from the code at commit `fa260b4` on `main`. The core of Tests Studio is not in this repository and is not covered here.

## What it treats as untrusted

| input | why it is untrusted |
|---|---|
| the adapter's process | it is a program you or someone else wrote; it runs with your user's rights and can do anything that user can do |
| the adapter's output (standard output, standard error, the run result file) | it is whatever the adapter chose to print; it may be enormous, malformed, or carry a credential |
| the sample project | it is code the adapter's test framework runs; the suite does not read it, but it runs under the adapter |
| the stand-in servers' recordings | each recording is built from what the adapter sent; every body is parsed and validated against the published schemas before a check trusts it |
| the command line you give the suite | it names the program to run; the suite starts exactly that and nothing else |

## What it protects

- **Your secrets.** The adapter does not inherit your environment. A host secret in a variable such as a cloud token or an API key cannot reach the adapter or its output.
- **Your machine.** An adapter that never stops, or that leaves a child holding the output open, cannot keep the suite or its processes running past the timeout.
- **The honesty of the result.** A measurement that was skipped, or that had nothing to measure, is never a pass.

## How

### The environment is scrubbed

The adapter receives the variables the specification lists for it (the core's addresses and credentials), plus a short list of host variables a toolchain needs (`PATH`, `HOME`, `TMPDIR`, `LANG`, the Go, Python and Node settings, and on Windows the system folders). Nothing else of the host is passed. `--pass-env NAME[,NAME...]` adds a name on purpose; nothing adds one by accident. The list is in specification section 12 and has been in the code since `a94b9af`; a test that sets a secret in the host and looks for it in the adapter's environment has pinned it since `991ebbb`.

### The credential does not leak

The suite hands the adapter a credential, then checks that it appears in no event, no run result and nothing the adapter printed (`credential-not-leaked`). The specification forbids an adapter to log it or put it on a command line.

### The run is bounded

On Unix the adapter starts in a process group of its own and the whole group is killed at `--timeout` (default 120 seconds), so a test worker or a browser dies with it. After the command ends or is killed, the suite waits at most `--wait-delay` (default 5 seconds) for a descendant still holding the output, then closes the pipes. The adapter's output is capped at 1 MiB per stream.

### The adapter never reaches a mock directly

An adapter may change a mock's behaviour only through the Mocks Studio core's operations. The check `mock-control-bypass` fails if any call reaches a mock's own address.

### A check that has nothing to measure fails

A check that measures something the adapter must do fails when there is nothing to measure: no events, no evidence, no request at the system under test. Tests that need real sockets skip themselves where loopback is denied; with `TESTS_CONFORMANCE_REQUIRE_SOCKETS=1` they fail instead, and continuous integration must set it.

## Weaknesses found and fixed

Each was found before the first release.

- **A data race in the stand-in core.** `SetURLs` and `LegEnv` wrote and read shared state without the lock every handler holds. Under the race detector over real sockets this gave 16 reports and 3 failing socket tests on a fresh clone. Fixed in `d94c070`: both take the recorder's lock.
- **A timeout that did not bound children.** The suite killed the adapter at the timeout but not what the adapter had started; a grandchild holding standard output kept the run alive past `--timeout`. Fixed in `bfec51c`: the adapter runs in its own process group, the group is killed, and a wait delay closes the pipes.
- **Four false passes.** The suite said `PASS` where the rule was broken: a step result with no `duration_ms`; an event the core had refused still counted as emitted; a step event whose `correlation_id` was not the one the core issued; evidence attached to a step the core never started. Fixed in `84ffe36`, each with a recording that varies exactly that field and expects a `FAIL`.

## What is still being checked

- **Windows.** Only the adapter's own process is killed at the timeout, not its descendants. The suite waits and closes the pipes, but a runaway grandchild can outlive the run there. No fix has landed; the suite does not claim one.
- **Running untrusted adapters.** The suite does not sandbox the adapter: it runs with your rights, in your working directory. If you run an adapter you did not write, run the suite in a container or a throwaway account. A sandbox is not planned in version 1.
- **Other users on the same machine.** The stand-in servers listen on loopback, which any local user can reach. The suite is not designed to defend against a hostile local user during a run.
- **Sizes and rates.** The adapter's output is capped at 1 MiB per stream and each request body the stand-in servers read or record at 8 MiB. The number of calls they record is not capped, so the suite's memory use under an adapter that makes calls without end is bounded only by the timeout.

## Reporting a vulnerability

Report a vulnerability **privately**, through GitHub's private vulnerability reporting, which is on for this repository: <https://github.com/saphan-ai/tests-studio/security/advisories/new>. Never open a public issue for a vulnerability.

Say what you ran and what you saw: the command, the adapter or project that triggered it, and the output. A minimal reproduction is the most useful thing you can send. You will get an answer in the advisory thread; we do not publish a deadline we cannot promise, and we will tell you what we find.
