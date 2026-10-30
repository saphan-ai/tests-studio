# Test runner specification, version 1.1

This document tells you how to write a **runner adapter**: a small piece of code that makes a test
framework (pytest, Playwright, Go's `testing`, anything) report to a Tests Studio core. You do not
need the core's code, and you do not need our permission. You implement what is written here, run
the conformance suite in `conformance/` against your adapter, and **an adapter is accepted when the
suite passes against it**.

The key words MUST, MUST NOT and MAY are used in their usual sense.

**Version 1.1 clarifies version 1; it does not replace it.** The wire protocol is still spec
version `v1` (the string in `Tests-Spec-Version`, `emitter.spec` and `adapter.spec` does not
change), every v1.1 rule is additive except where section 14 says otherwise, and every rule
a v1 adapter already followed still holds. Section 14 lists what v1.1 states, with the number of
each ruling.

## 1. What an adapter does

A test framework discovers test items (a test function, a test case, a scenario). The adapter turns
a run of them into a conversation with the core:

1. ask which spec versions the core serves, and pick one it speaks;
2. open a test run, or — inside a Saphan leg — **attach** to the leg's run;
3. for every test item, **start a step** (the core answers with a W3C `traceparent`), hand that
   `traceparent` to the test so the system under test carries it, then **finish the step** with
   one result;
4. attach **evidence** (screenshots, logs, artifacts) to steps;
5. emit **live events** as things happen;
6. **close** the run, and publish the **run result**.

If a test needs a mock to behave in a certain way (a slow reply, an error), the adapter sets that
through the **Mocks Studio core**, never through the mock itself (section 8).

## 2. Ids and formats

| name | meaning | format |
|---|---|---|
| `run_id` | the run, and the scope mocks keep their state in | `^[a-z0-9][a-z0-9._-]{2,63}$` |
| `test_run_id` | one test run attached to or opening a run | same |
| `step_id` | one step; chosen by the adapter, unique within a test run | 1 to 128 characters |
| `correlation_id` | one transaction; the trace-id of a W3C Trace Context | 32 lowercase hex, not all zero |
| `event_id` | one event | `^[A-Za-z0-9._:-]{8,128}$` |
| timestamps | UTC with millisecond precision | `2026-10-29T04:10:00.123Z` |

A test item becomes exactly one step. A step MAY span several correlation ids in the system
under test; a correlation id never belongs to two steps.

An `event_id` is unique **within its test run**: it names one event, and the events of a test run
form a **set** keyed by it, as the `events_digest` of section 7 already treats them. Sending the
same document under the same `event_id` again is the same event; sending a different event under
an `event_id` already used is a defect (ruling 16).

## 3. Versions

The core serves a list of spec versions (this document is `v1`). The adapter:

1. calls `GET /v1/spec` first, before anything else;
2. chooses a version it speaks from that list; if there is none, it **refuses by name** — it stops
   and says which versions the core serves and which it speaks;
3. sends the chosen version in the `Tests-Spec-Version` header of the call that opens or attaches
   the test run, and writes it into the `emitter.spec` of every event and the `adapter.spec` of the
   run result.

A core refuses a version it does not serve with `422 spec-version-unsupported`, naming the served
versions in `allowed`. A new optional field is a minor change and keeps `v1`; a new required field,
a removed field or a changed meaning is `v2`.

## 4. The environment an adapter reads

| variable | when | meaning |
|---|---|---|
| `TESTS_CORE_URL` | always | base URL of the Tests Studio core |
| `TESTS_RESULT_FILE` | when set | path the adapter writes the run result to (section 7) |
| `MOCKS_RUN_ID` | inside a leg | the leg's run id; its presence means "attach, never open" |
| `MOCKS_RUN_TOKEN` | inside a leg | the leg's run credential |
| `MOCKS_CORE_URL` | inside a leg | base URL of the Mocks Studio core |
| `TESTS_RUN_TOKEN` | optional | a test-run credential a parent process hands to a child process of the adapter; the adapter MUST NOT put it on a command line |

The adapter takes the leg's run id and credential from the environment and **never opens a second
run inside a leg**: the leg's code was started with the leg's endpoints, so a run opened inside it
could not have endpoints of its own. A core refuses an open inside a leg with `403
run-open-not-allowed` (section 6); an adapter that finds `MOCKS_RUN_ID` set attaches and never
sends the open call at all (ruling 11).

**Handing a test run to a child process (ruling 8).** An adapter whose test workers or framework
plugins run in child processes passes them what they need to report into the same test run. The
ids (`run_id`, `test_run_id`, `step_id`) are **not secrets** and may travel in the environment,
a command line or a file. Only the credential is a secret: it travels in `TESTS_RUN_TOKEN` in the
child's environment, never on a command line and never in a file the child does not own.

**One Mocks Studio core (ruling 9).** Inside a leg the adapter finds the Mocks Studio core in two
places: `MOCKS_CORE_URL` and the `mocks_core_url` of the attach answer. Both name **one**
service. When they differ the adapter does not choose: it stops and refuses by the name
`mocks-core-mismatch`, saying both values. (This is an adapter-side refusal, not a core reason
code; section 10 lists it apart.) Standalone, the open answer's `mocks_core_url` is the only
source.

## 5. The credential

A **test-run credential** is an HTTP bearer token scoped to exactly one `test_run_id`. It is
returned once, by the call that opens or attaches the test run, in that response only.

- Every call under `/v1/test-runs/{test_run_id}` carries `Authorization: Bearer <credential>`.
- Attaching carries the leg's run credential (`MOCKS_RUN_TOKEN`) instead.
- Calls to the Mocks Studio core carry the run credential: `MOCKS_RUN_TOKEN` inside a leg, or the
  `run_credential` the open answer returned (standalone).
- A missing credential is refused `401 run-credential-missing`. A credential that is another
  run's, expired, revoked, or of a closed test run is refused `403 run-credential-invalid`. Neither
  refusal says whether the other run exists.
- The adapter MUST NOT write a credential to a log, a standard stream, an event, a summary, an
  artifact or the run result. Closing the test run revokes its credential.

## 6. The calls

The full shape is `runner-v1.openapi.yaml` (OpenAPI 3.1). In order:

| call | operation | purpose |
|---|---|---|
| `GET /v1/spec` | `getSpec` | the served spec versions; no credential |
| `POST /v1/test-runs` | `openTestRun` | open a standalone test run: `{project, mocks[]?, test_run_id?}` (`mocks` is optional: a run without mocks is legal); answers ids, endpoints and credentials; no credential needed |
| `POST /v1/runs/{run_id}/test-runs` | `attachTestRun` | attach to a leg's run: `{project, test_run_id?, reset?}`; carries the leg's run credential; answers like `openTestRun` without `run_credential` |
| `POST /v1/test-runs/{id}/steps` | `startStep` | `{step_id, name}`; answers `{step_id, correlation_id, traceparent, baggage}` |
| `POST /v1/test-runs/{id}/step-results` | `finishStep` | `{step_id, result, duration_ms, failure?}`; `result` is `pass`, `fail`, `error` or `skip`; `failure` text is required for `fail` and `error` |
| `PUT /v1/test-runs/{id}/evidence/{sha256}` | `attachEvidence` | the body is the evidence bytes; headers `Tests-Evidence-Kind` (`screenshot`, `log`, `artifact`) and `Tests-Step-Id` |
| `POST /v1/test-runs/{id}/events` | `emitEvent` | one `tests.event/v1` document |
| `POST /v1/test-runs/{id}/close` | `closeTestRun` | `{verdict, counts, started_at, ended_at, adapter}`; answers the run result |

An adapter never calls the attach operation of the Mocks Studio core. It asks the Tests Studio core
(`attachTestRun` above), which attaches to the leg's run on the Mocks Studio core with the run
credential the adapter presented (ecosystem contract sections 1 and 6). A Tests Studio core
therefore verifies the leg's run credential by presenting it to the Mocks Studio core, and neither
keeps nor logs it.

Opening **inside a leg** is refused `403 run-open-not-allowed`; this is the one rule and the one
code for it (section 4 says only that the adapter does not try).

**One test run attached at a time, any number in sequence (ruling 12).** A leg's run may hold
several test runs one after another (a retry after a fix), each with a clean history. A second
attach **while one is attached** is refused `409 run-attached`. After `closeTestRun` the run is no
longer attached: a new attach with a **new** `test_run_id` is accepted. The conformance check
`no-second-run` counts only attaches made while a test run is attached.

**One opener per test run, and parallel workers (ruling 3).** Exactly one party opens or attaches
a test run: the adapter's coordinating process. Workers of a parallel framework (pytest-xdist,
Playwright workers, `go test` packages) do not open or attach of their own; the adapter hands each
the test run's id and credential (section 4) and serialises or coordinates their calls. Every
`step_id` is unique within the test run, whichever worker starts it. If two workers start the
same `step_id`, the core accepts the first and refuses the second `409 step-already-started`; the
adapter treats that as its own defect and reports the item that lost the race as `error`, with
failure text saying so, under a `step_id` of its own that is unique (section 6.1).

### 6.1 Steps and the traceparent

`startStep` returns a `traceparent` (`00-<trace-id>-<span-id>-01`) and a `baggage` value
(`mocks.run=<run_id>,mocks.step=<step_id>`). The adapter exposes both to the test (for example as
environment variables or a fixture) so that the test's calls to the system under test carry them
as the `traceparent` and `baggage` HTTP headers, unchanged. The trace-id **is** the step's
`correlation_id`; this is how a mock's event and a test's step meet on one id.

Every test item becomes exactly one step: one `startStep`, one `finishStep`, a `step_id` that is
unique in the test run. The result of a step is exactly one of `pass`, `fail`, `error`, `skip`.

**Retries (ruling 4).** A framework that retries an item (a flaky-test plugin, Playwright's
`retries`) still reports **one step per item**, finished **once** with the item's **final**
outcome. Earlier attempts are not steps. The adapter MAY keep them visible: as evidence of the
step (section 6.2) or as events of its own kind `x-<adapter>.attempt` (section 9.2). This keeps
`--items` equal to the number of items and the counts equal to the number of steps.

**An item whose body never ran (ruling 5).** A fixture that failed, a timeout before the body
started, an abort of the run: the item is still accounted for. It becomes one step, finished with
result `error` and failure text that names why (fixture, timeout, abort). It is never left
without a step and never reported as `skip`, which means the test chose not to run.

### 6.2 Evidence

Evidence is content-addressed. The path carries the lowercase hex SHA-256 of the exact bytes in
the body. The core recomputes the digest and refuses bytes that do not match with `422
evidence-digest-mismatch`. Evidence belongs to a step (`Tests-Step-Id`).

**The step must be started and accepted; it may be finished (ruling 6).** The core accepts
evidence for any step it accepted a start for, whether or not that step already has its result.
Videos and traces are complete only after the test ends, so an adapter MAY attach them after
`finishStep`. Evidence for a step the core never accepted is refused `422 step-unknown`.

**Evidence is mandatory (ruling 14).** A conforming run attaches at least one evidence item
(a log is enough). An adapter that has nothing else to attach attaches the log of the run.

## 7. The run result

`closeTestRun` answers a `tests.run-result/v1` document
(`schemas/tests-run-result-v1.schema.json`). The adapter publishes it unchanged:

- it writes the document to the file named by `TESTS_RESULT_FILE`, when that variable is set;
- it prints the document's `evidence_line` on its standard output, as a line of its own.

Fields: `run_id`, `test_run_id`, `project`, `verdict` (`pass`, `fail`, `error`), `counts`
(`total`, `passed`, `failed`, `errored`, `skipped`), `started_at`, `ended_at`, `events_digest`,
`adapter` (`{name, version, spec}`), `evidence_line`.

- **Counts.** `total` is the number of steps; the four others are the number of results of that
  kind; they sum to `total`.
- **Verdict.** `error` if any step errored, else `fail` if any failed, else `pass`. A skipped step
  never fails a run.
- **The core is the authority (ruling 1).** At `closeTestRun` the core **re-tallies** the test run
  from what it accepted: `total` is the steps whose start it accepted, and each of the four others
  is the results it accepted of that kind. A result the core refused (a `422`, a `409`) is **not**
  counted. The core compares the adapter's `verdict` and `counts` with its tally and refuses a close
  that differs, **`422 run-result-mismatch`**, naming its own tally in the message; it does the same
  while a started step still has no accepted result. A refused close leaves the test run open and
  its credential live, so the adapter can close again with the right tally. The adapter therefore
  counts what the core accepted, not what it meant to send. Only a close the core accepted yields a
  run result.
- **`events_digest` is copied (ruling 2).** SHA-256 over the event ids of the Mocks Studio core's
  events that carry this `test_run_id`, as that core holds them (ecosystem contract section 6).
  The core computes it and returns it in the close answer; the adapter copies it **verbatim** into
  the run result and MUST NOT compute, reformat or change it. There is one producer of the digest.
  The byte layout, which every product that computes or verifies a digest MUST follow:
  1. the ids are a **set**: an id counted once however many times it was ingested;
  2. sort them ascending by their UTF-8 bytes (so `B` sorts before `a`, and `-` before `.`);
  3. write each id followed by exactly one LF byte (0x0A), and no other byte (no separator, no
     header, no final trailer);
  4. SHA-256 that whole byte string as one input;
  5. write the result as `sha256:` and 64 lowercase hex characters.

  The empty set digests the empty input. The set is frozen when the test run closes: an event that
  carries the `test_run_id` and is ingested later does not change the digest the close returned,
  and a verifier that later recomputes a different digest reports a mismatch.

  Known answers (computed with Python's `hashlib`, outside this repository's Go code, and checked
  by `proto.EventsDigest`'s test):

  | event ids (as given) | digest |
  |---|---|
  | none | `sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855` |
  | `evt-0001` | `sha256:d5c51f60239f4d48591a58b5a6aad406fef54315c27eea24f38d800b922d38e4` |
  | `evt-0001`, `evt-0001` | `sha256:d5c51f60239f4d48591a58b5a6aad406fef54315c27eea24f38d800b922d38e4` |
  | `evt-0003`, `evt-0001`, `evt-0002` | `sha256:80e903cace39e18ffd3b42682fa4a5ac5cc3399b3570ddde071daefc295bb7c5` |
  | `evt-b`, `evt-B`, `evt-a.1`, `evt-a-1` | `sha256:d6b0186f9a60e0bd8323617e33d6494898e1ac37cb7150defd589a1b727351f1` |

  The fourth row hashes the bytes `evt-0001\nevt-0002\nevt-0003\n`; the fifth, after sorting to
  `evt-B`, `evt-a-1`, `evt-a.1`, `evt-b`, hashes `evt-B\nevt-a-1\nevt-a.1\nevt-b\n`.
- **`evidence_line`.** The line a Saphan leg cites in its return, verbatim:
  `- evidence: tests-studio-run <run_id>/<test_run_id> verdict=<verdict> events=sha256:<hex>`.
  Whoever verifies it asks the Mocks Studio core for the test run's event ids and recomputes the
  digest; a mismatch or an absent run is a refusal, never a pass.

## 8. Mock control

An adapter sets a mock's scenario **only** through the Mocks Studio core's operations
(`ecosystem-v1/mocks-runs-v1.openapi.yaml`, Appendix C of the ecosystem contract):
`setScenario` (`PUT /v1/runs/{run_id}/mocks/{mock}/settings`), `resetRun`
(`POST /v1/runs/{run_id}/reset`) and `announceCorrelation`
(`POST /v1/runs/{run_id}/correlations`). The core validates the setting against the mock's manifest
and applies it. The adapter MUST NOT call a mock's own control surface, nor any other URL of a
mock: a mock's URL is for the system under test, not for the adapter. The conformance suite checks
that no call reaches a mock except through the stub Mocks Studio core (`mock-control-bypass`).

`announceCorrelation` is **optional** and exists for protocols that cannot carry the `traceparent`
and `baggage` headers (ruling 13). An adapter whose test reaches the system under test over HTTP
with those headers never calls it; one whose protocol has no header (a raw TCP or SMTP exchange)
calls it to tell the Mocks Studio core which correlation id a step uses.

## 9. Live events: `tests.event/v1`

Events are the live timeline. One console merges this stream and the mocks' `mocks.event/v1`
stream by `run_id`, `correlation_id` and `at`. The schema is
`schemas/tests-event-v1.schema.json` (JSON Schema 2020-12).

### 9.1 The shared header

These fields have **exactly the same schema** in `tests.event/v1` and in `mocks.event/v1`
(`ecosystem-v1/mocks-event-v1.schema.json`), byte for byte after canonical JSON: `run_id`,
`test_run_id` (both `$defs/run_id`), `step_id`, `correlation_id` (32 lowercase hex, not all
zero), `at`, and the `payload` redaction shape. Tests Studio does not redefine them; the
`shared-header` test in the conformance module compiles both schemas with the same validator and
asserts it.

### 9.2 Tests Studio's own fields

`schema` (`tests.event/v1`), `event_id`, `kind`, `emitter` (`{name, version, spec}`), `summary`,
`outcome`.

- `kind`: `run.opened`, `step.started`, `step.finished`, `evidence.stored`, `run.closed`, or
  `x-<name>.<kind>` for an adapter's own kind.
- `step.started`, `step.finished` and `evidence.stored` carry `step_id` and `correlation_id`;
  `step.finished` carries `outcome` (the step's result); `run.closed` carries `outcome` (the
  verdict).
- `summary` is one human line and never a secret. `payload`, when present, is redacted by default:
  `{redacted: true, digest, size}`, or `{redacted: false, fields}` holding only fields that are safe
  to export.

The `correlation_id` of a `step.started`, `step.finished` or `evidence.stored` event MUST equal the
one `startStep` returned for that `step_id`.

An adapter emits at least `run.opened`, `step.started`, `step.finished` and `run.closed`, and a
required event counts only when the core accepted it (`202`). Closing the test run revokes its
credential (section 5), so `run.closed` MUST be emitted **before** `closeTestRun`: one emitted
afterwards is refused, and the run has no `run.closed` event. This is the only reading of the
rule (ruling 7).

**Order.** Beyond that one constraint, v1.1 specifies no order among events: the timeline is merged
by `at`, and a consumer MUST NOT rely on the order in which a core ingested them.

**Attempts.** An adapter MAY emit `x-<adapter>.attempt` events for the earlier attempts of a
retried item (section 6.1). They carry the item's `step_id` and `correlation_id` like any step event.

## 10. Refusals

Every refusal is a JSON document: `message` (its first sentence says what was measured, what was
refused and what to do next), `reason_code` (the public key; integrate on it), `allowed` (the
permitted values, when a set applies). Reason codes of the Tests Studio core:

`spec-version-unsupported` · `run-credential-missing` · `run-credential-invalid` ·
`run-open-not-allowed` · `run-attached` · `run-already-open` · `run-id-invalid` ·
`request-invalid` · `step-id-missing` · `step-unknown` · `step-already-started` ·
`step-already-finished` · `result-invalid` · `failure-missing` · `evidence-digest-mismatch` ·
`evidence-kind-unknown` · `event-invalid` · `event-schema-unknown` · `run-result-mismatch` (v1.1:
the close's verdict or counts differ from the core's own tally, section 7).

**Refusals of the adapter itself.** A refusal is also something an adapter issues, to its own
caller, when it cannot proceed. These names are the adapter's, never a core's answer:
`mocks-core-mismatch` (`MOCKS_CORE_URL` and the answer's `mocks_core_url` name different services,
section 4). An adapter's refusal follows the same shape: it says what was measured, what it refuses,
and what to do next.

A call made with the credential of a test run that has been closed is refused `403
run-credential-invalid` (section 5): closing revokes the credential, and there is no separate code
for it.

The Mocks Studio core's reason codes are in Appendix C.

### 10.1 A refused call is never retried (ruling 15)

A **refusal** is a `4xx` answer carrying a refusal document. It is a decision: the adapter MUST NOT
send the same request again, and it reports the refusal (its message names the next act). A
**transport failure** (no answer, a reset connection, a `5xx`) is not a decision and MAY be retried,
with backoff bounded by a documented setting of the adapter (a count and a total time); an adapter
that retries says its bound in its documentation. A request changed to follow a refusal's advice is a
new call, not a retry. `closeTestRun` is **always attempted**, whatever else was refused, so that
the credential is revoked and the run result exists. The check `refusal-not-retried` measures this
from the recording.

## 11. Schemas and the ecosystem contract

| file | what |
|---|---|
| `schemas/tests-event-v1.schema.json` | `tests.event/v1` |
| `schemas/tests-run-result-v1.schema.json` | `tests.run-result/v1` |
| `runner-v1.openapi.yaml` | the calls of section 6 |
| `ecosystem-v1/mocks-event-v1.schema.json` | `mocks.event/v1` (Appendix A of the ecosystem contract) |
| `ecosystem-v1/mocks-event-v1.examples.json` | its valid and invalid examples (Appendix B) |
| `ecosystem-v1/mocks-runs-v1.openapi.yaml` | the run and scenario operations (Appendix C) |

The three files of `ecosystem-v1/` are the contract's appendices, copied byte for byte; do not edit
them here. The schemas use only regular-expression syntax of the RE2 dialect (no lookahead), so
they compile in Go and in the regular-expression dialects most JSON Schema validators share.

## 12. Running the conformance suite

Building the suite needs **Go 1.26 or newer** (the `go` directive of `conformance/go.mod`).

```
go -C conformance build -o /path/to/tests-conformance ./cmd/tests-conformance
/path/to/tests-conformance --items 4 --mode standalone -- <your adapter command> <its arguments>
/path/to/tests-conformance --items 4 --mode leg        -- <your adapter command> <its arguments>
```

**Continuous integration MUST set `TESTS_CONFORMANCE_REQUIRE_SOCKETS=1` when it runs `go test` in
`conformance/`.** The tests that start the stubs on loopback skip themselves where loopback is
denied, and `go test` still exits 0; with the variable set they fail instead, so a skipped
measurement cannot read as a pass.

`--items` is the number of test items in your sample project (the suite's own sample project has
four; the number `4` above is that one). The sample project MUST hold a passing, a failing and a
skipped test, and at least one test MUST call the stand-in system under test at
`$TESTS_CONFORMANCE_SUT_URL` (any path under it, say `/sut/ping`) with the `traceparent` and
`baggage` headers the step was given; one of them SHOULD set a scenario through the Mocks Studio
core. It SHOULD also hold an item whose body never runs (a fixture that fails), which the adapter
reports as one `error` step (section 6.1), and the run attaches at least one evidence item. The
suite judges the composition from the results it receives (check `sample-composition`): at least one
accepted `pass`, one `fail`, one `skip`, and one request at the stand-in system. Run it in both modes. `--spec-dir` names the `spec/` directory (found by
searching upwards otherwise); `--timeout` bounds the adapter's run (default 120 s).

**The timeout bounds the whole process tree.** On Unix the runner starts your command in a process
group of its own and kills the whole group at the timeout, so a test worker, a browser or a shell's
child dies with it. Anywhere else only the command itself is killed. In both cases the runner then
waits at most `--wait-delay` (default 5 s) for any descendant that still holds the command's
output, and closes the pipes.

The runner starts a **recording stub core** (section 6) and a **stub Mocks Studio core**
(Appendix C) on loopback, starts your command with their URLs and credentials in its environment
(section 4, plus `TESTS_CONFORMANCE_SUT_URL`), and checks every recorded call.

**The adapter's environment is scrubbed.** Your command receives the variables of section 4, then
only these variables of the host, when they are set: `PATH`, `HOME`, `TMPDIR`, `LANG`, `GOCACHE`,
`GOMODCACHE`, `GOFLAGS`, `VIRTUAL_ENV`, `NODE_PATH`, `PLAYWRIGHT_BROWSERS_PATH`, and, for Windows,
`SystemRoot`, `SystemDrive`, `ComSpec`, `PATHEXT`, `USERPROFILE`, `TEMP`, `TMP`, `APPDATA`,
`LOCALAPPDATA`. Nothing else of the host reaches it, so a host secret cannot leak into the adapter
or its output. If your framework needs another variable, name it with `--pass-env NAME[,NAME...]`
(for example `--pass-env PYTHONPATH,PYTEST_ADDOPTS`). It prints one line
per check, `PASS <check-id> — <what was measured>` or `FAIL <check-id> — <what was measured>`.
Lines that begin `remark: ` are measurement caveats, not checks.

**Exit codes**

| code | meaning |
|---|---|
| 0 | every check passed |
| 1 | a check failed |
| 2 | the adapter was refused before checking: it declared a spec version the core does not serve |
| 3 | the adapter's command could not be started, or it never reached the core |
| 4 | the suite could not run (bad flags, no `spec/` directory, no listener) |

The adapter's own exit status is not checked: a test framework exits non-zero when a test fails.

**Checks**

| id | what must hold |
|---|---|
| `spec-version-known` | the adapter asked `GET /v1/spec` first and declared a version the core serves |
| `run-opened` | standalone: one test run opened; leg: one attach to the leg's run with the leg's credential; one invocation judges one test run |
| `no-second-run` | leg: no run opened of any kind, and no attach made while a test run is attached (a new attach after the close is allowed, ruling 12); standalone: exactly one open |
| `step-per-item` | each of the `--items` test items became exactly one step with a unique `step_id` (one start each, even a refused second start counts) and exactly one accepted result carrying it; no result is sent without its `step_id` |
| `step-result-valid` | every result is `pass`, `fail`, `error` or `skip`, carries a `duration_ms` of 0 or more (an absent one fails), and carries failure text where failing |
| `traceparent-to-sut` | every request the system under test received carried exactly a `traceparent` and `baggage` the core handed out, and at least one arrived |
| `evidence-digest` | every evidence upload declares the SHA-256 of its own bytes, with a known kind, and its `Tests-Step-Id` names a step the core accepted |
| `events-valid` | every emitted event validates against `tests.event/v1` and carries this run's ids; each step event carries the `correlation_id` `startStep` returned; the four required kinds were each accepted (`202`); no `event_id` names two different events (ruling 16) |
| `run-result-schema` | the published run result validates, repeats the core's `events_digest`, and its evidence line is on standard output |
| `run-result-counts` | the counts equal the steps and the results the core accepted, the verdict follows from them, and the core never refused a close as `run-result-mismatch` (ruling 1) |
| `credential-presented` | every call that needs a credential presented its own run's |
| `credential-not-leaked` | no credential appears in an event, the run result or the adapter's output |
| `mock-control-bypass` | no call reached a mock's own URL |
| `sample-composition` | the accepted results hold at least one `pass`, one `fail` and one `skip`, and at least one request reached the stand-in system (ruling 14) |
| `refusal-not-retried` | no request the core or the Mocks Studio core refused by name was sent again unchanged (ruling 15) |

A check that measures something the adapter must DO fails when there is nothing to measure: no
events, no evidence, no request at the system under test, no step result, no run result. The four
checks that measure something the adapter must NOT do (`no-second-run`, `credential-not-leaked`,
`mock-control-bypass`, `refusal-not-retried`) pass when they observe no violation; their output says
how many calls they looked at, and a run that made no call at all never reaches them (exit 3).

**What the suite cannot see.** It serves one request at a time, so it cannot observe parallel
workers racing (ruling 3 is checked through its effects: one start and one result per `step_id`); it
cannot tell a retried item's attempts from separate items except by the count of steps (ruling 4);
and it cannot tell an item whose body never ran from one that failed, so the `error` and its failure
text (ruling 5) are checked only as far as `step-result-valid` and `step-per-item` go.

**Mutants.** The suite's own fixture adapter (`conformance/internal/sampleadapter`, run over the
sample project in `conformance/testdata/sample-project`) passes every check. Each mutant breaks one
rule and fails exactly one check:

| mutant (`--mutant`) | breaks | exit | the one check that fails |
|---|---|---|---|
| `spec-v2` | declares a spec version the core does not serve | 2 | `spec-version-known` |
| `result-without-step-id` | a step result without `step_id` | 1 | `step-per-item` |
| `evidence-digest-mismatch` | evidence whose SHA-256 is not its bytes' | 1 | `evidence-digest` |
| `run-result-digest-malformed` | a run result whose `events_digest` is malformed | 1 | `run-result-schema` |
| `event-missing-run-id` | an event without `run_id` | 1 | `events-valid` |
| `credential-in-summary` | the credential printed in an event summary | 1 | `credential-not-leaked` |
| `mock-control-direct` | a call to a mock's control URL | 1 | `mock-control-bypass` |
| `second-run-in-leg` | a second run opened inside a leg's run | 1 | `no-second-run` |
| `counts-mismatch` | a close whose counts the core's tally contradicts (the core refuses it, the adapter then closes right) | 1 | `run-result-counts` |
| `duplicate-step-id` | a `step_id` started a second time | 1 | `step-per-item` |
| `retry-as-new-step` | a retried item reported as a second step | 1 | `step-per-item` |
| `never-ran-without-step` | an item whose body never ran, left without a step | 1 | `step-per-item` |
| `evidence-unstarted-step` | evidence for a step that was never started | 1 | `evidence-digest` |
| `run-closed-after-close` | `run.closed` emitted after `closeTestRun` | 1 | `events-valid` |
| `concurrent-attach-in-leg` | an attach while a test run is attached | 1 | `no-second-run` |
| `sample-without-skip` | a sample project with no skipped test | 1 | `sample-composition` |
| `retry-refused-call` | a refused attach sent again unchanged | 1 | `refusal-not-retried` |
| `event-id-reused` | one `event_id` on two different events | 1 | `events-valid` |

The fixture adapter also shows the rules no mutant can break alone: it attaches evidence after the
step finished (ruling 6), reports its never-run item as one `error` step with failure text (ruling
5), counts what the core accepted (ruling 1), copies the core's digest (ruling 2), and refuses by
name when `MOCKS_CORE_URL` and the answer's `mocks_core_url` differ (ruling 9). A sequential
re-attach in a leg (ruling 12) passes `no-second-run`: the check's own test feeds it attach, close,
attach, close.

## 13. What this specification does not cover

The core itself (runs, storage, evidence, scheduling), the console, and any particular framework's
adapter. The specification and its suite are the product: nothing here is a library to link.

## 14. Changes in v1.1

Version 1.1 states, once and in the section it belongs to, rules an adapter author could not derive
from version 1. The wire protocol is unchanged (`v1`); the only new reason code of the core is
`run-result-mismatch`, and `mocks-core-mismatch` is an adapter-side refusal.

| ruling | what v1.1 states | section |
|---|---|---|
| 1 | the core re-tallies at close from the accepted results; a mismatching verdict or counts are refused `422 run-result-mismatch`; a refused result is not counted | 7, 10 |
| 2 | the adapter copies the core's `events_digest` verbatim; the byte layout is normative | 7 |
| 3 | one opener per test run; `step_id`s unique within it; a racing second start is refused `409` and its item reported `error` | 6 |
| 4 | one step per item, finished once with the final outcome; earlier attempts MAY be evidence or `x-<adapter>.attempt` events | 6.1, 9.2 |
| 5 | an item whose body never ran is one step with result `error` and failure text naming why | 6.1 |
| 6 | evidence needs a started, accepted step; the step may already be finished | 6.2 |
| 7 | `run.closed` is emitted before `closeTestRun`; there is no second reading | 9.2 |
| 8 | ids are not secrets; only the credential is, and it reaches a child process in `TESTS_RUN_TOKEN` | 4 |
| 9 | `MOCKS_CORE_URL` and the answer's `mocks_core_url` name one service; if they differ the adapter refuses by name, `mocks-core-mismatch` | 4, 10 |
| 10 | `mocks` is optional in `openTestRun`; a run without mocks is legal | 6 |
| 11 | one rule and one code for an open inside a leg: `403 run-open-not-allowed` | 4, 6 |
| 12 | one test run attached at a time, any number in sequence: after `closeTestRun` a new attach with a new `test_run_id` is accepted; `no-second-run` counts concurrent attaches only | 6, 12 |
| 13 | `announceCorrelation` is optional, for protocols that cannot carry the headers | 8 |
| 14 | at least one evidence item per conforming run; the suite checks the sample project's composition (`sample-composition`) | 6.2, 12 |
| 15 | a refusal is never retried; a transport failure MAY be, with a documented bound; `closeTestRun` is always attempted (`refusal-not-retried`) | 10.1, 12 |
| 16 | `event_id` is unique per test run with set semantics | 2, 12 |

Also stated: event order beyond `run.closed` before `closeTestRun` stays unspecified (section 9.2), and
the OpenAPI `SpecVersion` pattern `^v[0-9]+$` is unchanged.

**Suite changes an adapter feels.** The suite has fifteen checks (two new: `sample-composition`,
`refusal-not-retried`); its sample project has four items (`--items 4`), the fourth being an item
whose body never runs. One conformance invocation still opens, or attaches, exactly one test run:
`no-second-run` no longer fails a leg that attaches again after closing its test run (v1 failed
it), but `run-opened` and the checks after it judge the first test run only, so a sequence of test
runs in one invocation is not yet exercised end to end.

What v1 passed and v1.1 FAILS — behaviours v1 was silent on, each now refused:

1. a refused request sent again unchanged (`refusal-not-retried`);
2. one `event_id` on two different event documents (`events-valid`);
3. counting a result the core refused (`run-result-counts`; the core refuses such a close);
4. any close refused `run-result-mismatch`, even when a correct close follows it (`run-result-counts`);
5. closing while a started step has no accepted result (the core refuses the close);
6. a result sent without its `step_id`, whatever followed it (`step-per-item`).

Everything else v1.1 adds is either a clarification the suite does not observe or a loosening.
