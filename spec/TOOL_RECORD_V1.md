# Tool invocation record, version 1

Status: version 1. Schema identifier: `tool-invocation/v1`. Machine form: `spec/schemas/tool-invocation-v1.schema.json`.
Checker: `conformance/cmd/tool-conformance/`.

A **tool invocation record** says, in one small file, how one external tool (a secret scanner, a linter, a test
runner, a dependency auditor) is run against a repository and what its outcome means. Anyone can write one, and
anyone can check one with the runner in this repository, with no other code from this project.

The record is data. It carries no script, no shell line and no logic: the program is named, its arguments are listed
one by one, and everything the tool may read or reach is declared.

## What the public checker does and does not do

The public checker (`tool-conformance`) validates a record, checks a pin it can check, runs the argv against a fresh
copy of a small fixture repository, maps the exit code, and checks the output the tool wrote. It is the engine's gate
without its confinement.

remark: the public runner does NOT confine the tool and cannot observe its egress. It starts the program with your
environment and your network, and a PASS on every line it prints says nothing about which hosts the tool contacted.
Enforcing and observing the `egress` field is done by the engine's gate, never by this runner. 2026-10-31.

remark: a record that passes this checker has been run against one fixture and one expected outcome. It has not been
shown correct on any other repository.

## The record

A record is a YAML or JSON document: one mapping. A key that is not listed here is refused, and the refusal prints the
allowed keys. A required key that is missing is refused, and the refusal names it. A key written twice in YAML is
refused.

| key | required | meaning |
|---|---|---|
| `schema` | no | the literal `tool-invocation/v1`; allowed so a record can state its own version |
| `id` | yes | the tool's identifier |
| `ecosystem` | yes | the kind of repository it reads (`go`, `node`, `container`, `any`, ...) |
| `version` | yes | the tool version the record was written for |
| `pin` | yes | what exactly is being run |
| `licence_class` | yes | `osi` or `paid-pack` |
| `command` | yes | the argv |
| `config` | no | a path under the repository the tool reads its configuration from |
| `extra_inputs` | no | extra typed inputs the argv may name |
| `inputs` | yes | the paths the tool reads |
| `egress` | yes | the hosts the tool may reach, or `none` |
| `exit_map` | yes | what each exit code means |
| `output` | yes | the file the tool writes and its format |
| `fixture` | yes | the repository the record is checked against, and the outcome expected |

Three optional keys (`schema`, `config`, `extra_inputs`) go beyond the field list the record was first described by.
`config` and `extra_inputs` exist because the argv may name `{{config_path}}` and extra typed inputs, and a record has to
say where those values come from. `schema` lets a record state its own version. All three are optional, so a record
without them is complete.

### `id`

Matches `^[a-z0-9][a-z0-9-]{1,63}$`.

```yaml
id: secret-scan        # valid
```
```yaml
id: Secret_Scan        # refused: capitals and an underscore are outside the pattern
```

### `ecosystem`

Matches `^[a-z0-9][a-z0-9-]{0,62}$`.

```yaml
ecosystem: container   # valid
```
```yaml
ecosystem: ""          # refused: empty
```

### `version`

A non-empty string of at most 64 characters. It is free text; this specification does not compare versions.

```yaml
version: "8.18.4"      # valid
```
```yaml
version: ""            # refused: empty
```

### `pin`

Exactly one of two shapes.

- `{image: <ref>@sha256:<64 hex>}`: a container image, pinned by digest.
- `{asset: <name>, sha256: <64 lowercase hex>}`: a file, pinned by the sha256 of its bytes.

```yaml
pin: {asset: scanner-linux-amd64, sha256: 9f2c0a7d5b1e4c38a6d0f1e2b3c4d5a69788796a5b4c3d2e1f00112233445566}   # valid
pin: {image: registry.example/tools/scanner@sha256:9f2c0a7d5b1e4c38a6d0f1e2b3c4d5a69788796a5b4c3d2e1f00112233445566}   # valid
```
```yaml
pin: {image: registry.example/tools/scanner:latest}   # refused: a tag is not a digest
pin: {asset: scanner, sha256: 9F2C}                   # refused: not 64 lowercase hex digits
```

The public runner checks an `asset` pin when you pass `--asset <file>`: it hashes that file and compares. It cannot
check an `image` pin, because it does not pull images, and it says so (`NOT APPLICABLE`) instead of passing it.

remark: the runner hashes the file you name; it does not check that the first element of `command` resolves to that
file. A matching hash proves you handed over the pinned bytes, not that those bytes are what ran.

### `licence_class`

`osi` (an OSI-approved licence) or `paid-pack` (a tool shipped in a paid pack). Nothing else.

```yaml
licence_class: osi        # valid
```
```yaml
licence_class: freeware   # refused: allowed values are osi, paid-pack
```

### `command`

An array of strings: the **argv**. It is exec'd directly. There is no shell, so no quoting, no globbing, no pipes, no
redirects and no variable expansion happen. Each element is one argument.

- The first element is the program and must be a literal.
- An element is a literal, or contains one or more `{{name}}` placeholders, replaced by the value of a typed input.
- **A shell metacharacter in any element is refused when the record is loaded**, naming the element and the character.
  The characters are `;` `|` `&` `$` `` ` `` `<` `>` and the newline.
- A placeholder names one of the **closed** inputs `repo_root`, `output_path`, `config_path`, or a name declared in
  `extra_inputs`. Any other name is refused.

| input | value at run time |
|---|---|
| `repo_root` | the absolute path of the repository the tool runs in (a fresh copy of the fixture, in the public runner) |
| `output_path` | `repo_root` joined with `output.path`, absolute |
| `config_path` | `repo_root` joined with `config`, absolute; needs `config` in the record |
| an `extra_inputs` name | the declared value; a `path` is joined under `repo_root` |

The tool is started with `repo_root` as its working directory.

```yaml
command: [scanner, detect, --source, "{{repo_root}}", --report-path, "{{output_path}}"]   # valid
```
```yaml
command: [sh, -c, "scanner detect | tee report"]   # refused: command[2] contains the shell metacharacter '|'
command: [scanner, "--out={{home}}"]               # refused: command[1]: placeholder {{home}} names no input
```

A value substituted for a placeholder is not scanned for metacharacters: it is a path or a declared value, and no
shell reads it.

### `config`

Optional. A relative path under the repository. Same path rule as `inputs`, without a glob.

```yaml
config: .gitleaks.toml          # valid
```
```yaml
config: ../outside.toml         # refused: command and config paths may not climb out of the repository
```

### `extra_inputs`

Optional. A mapping from an input name to `{type, value}`, where `type` is `string`, `int` or `path` and `value` is a
string. A name matches `^[a-z][a-z0-9_]{0,31}$` and may not be `repo_root`, `output_path` or `config_path`. An `int`
value must parse as an integer. A `string` value gets the same metacharacter refusal as `command`.

```yaml
extra_inputs:
  min_severity: {type: string, value: high}      # valid
  jobs: {type: int, value: "4"}                  # valid
```
```yaml
extra_inputs:
  jobs: {type: int, value: many}                 # refused: extra_inputs.jobs.value: "many" is not an integer
```

### `inputs`

The paths the tool reads, relative to `repo_root`, each without a `..` segment and without a leading `/`. A `*` may
appear in an `inputs` path as a glob. The list is declared so a gate can show the tool only what it needs; the public
runner does not restrict reads.

```yaml
inputs: [".", "go.mod"]       # valid
```
```yaml
inputs: ["/etc/passwd"]       # refused: an absolute path is outside the pattern
inputs: ["../other"]          # refused: inputs[0] has a '..' segment
```

### `egress`

Either the literal `none`, or a non-empty list of unique lower-case host names the tool may reach.

```yaml
egress: none                                  # valid
egress: [advisories.example, mirror.example]  # valid
```
```yaml
egress: []                                    # refused: an empty list says nothing; write none
egress: ["https://advisories.example/x"]      # refused: a host name, not a URL
```

### `exit_map`

A mapping from an exit code to what it means, plus a required `default`. A key is an integer from 0 to 255 or
`default`. A value is `clean`, `findings` or `tool-error`.

- `clean`: the tool ran and found nothing.
- `findings`: the tool ran and found something. This is a result, not a failure of the tool.
- `tool-error`: the tool did not do its job (bad arguments, crash, cannot read its input).

The code the process exits with is looked up as a key. If the key is absent, `default` applies. `default` is not a
fallback for a process that never exits normally: a tool that is killed by a signal, does not start, or outlives the
runner's timeout has no exit code, and the public runner reports that as a failure of the run itself, before any
mapping.

```yaml
exit_map: {0: clean, 1: findings, default: tool-error}   # valid
```
```yaml
exit_map: {0: clean, 1: findings}                         # refused: required key "default" is missing
exit_map: {0: ok, default: tool-error}                    # refused: "ok" is not one of clean, findings, tool-error
exit_map: {256: findings, default: tool-error}            # refused: 256 is not a key
```

### `output`

`{format, path}`. `format` is one of `sarif-2.1.0`, `junit-xml`, `cyclonedx-1.5`, `json`; any other value is refused
and the refusal lists these four. `path` is relative to `repo_root`, without a `..` segment or a glob. The tool writes
the file there (the placeholder `{{output_path}}` is that location, absolute). A file already at that path in the
fixture is a defect of the fixture: the public runner refuses it, because a file that was there before the tool ran
proves nothing about the tool.

```yaml
output: {format: sarif-2.1.0, path: report.sarif}   # valid
```
```yaml
output: {format: spdx-2.3, path: sbom.json}         # refused: output.format "spdx-2.3" is not one of: sarif-2.1.0, junit-xml, cyclonedx-1.5, json
```

### `fixture`

`{repo, expect}`. `repo` is a directory under the fixtures root the checker is given (a relative path, no `..`).
`expect` is `clean` or `findings`: the class the exit code must map to when the tool runs on that fixture.

```yaml
fixture: {repo: secret-synthetic, expect: findings}   # valid
```
```yaml
fixture: {repo: secret-synthetic, expect: tool-error}   # refused: expect must be clean or findings
fixture: {repo: ../elsewhere, expect: clean}            # refused: fixture.repo has a '..' segment
```

## Which rules the schema carries and which the runner adds

The JSON Schema carries the shape of every field, the closed lists, the required keys, the refusal of unknown keys and,
as a character class on each `command` element, the metacharacter refusal. A JSON Schema validator reports where a
value was refused, not which character, and it cannot do the following, which the runner adds:

- name the element and the character of a metacharacter, and the allowed keys of an unknown-key refusal;
- refuse a `..` segment in a path, and a `*` outside `inputs`;
- refuse a placeholder that names no input, a placeholder in the program position, an unbalanced `{{`, and
  `{{config_path}}` without `config`;
- refuse an `int` extra input that is not an integer;
- refuse a key written twice in YAML.

A record is valid when it passes both. The runner prints each problem on its own line, those from the extra rules
prefixed `rule:` and those from the schema prefixed `schema:`.

## Normalisation: what an output must satisfy

The output file is checked by `format`. What the public runner measures:

**`junit-xml`.** Well-formed XML whose root element is `testsuites` or `testsuite`; at least one `testcase` below it;
every `testcase` has a non-empty `name`; and inside any one `testsuite`, no two `testcase` elements share the same
(`classname`, `name`) pair. The same pair may appear in two different suites. Pass or fail is not read from the cases.

**`sarif-2.1.0`.** Well-formed JSON; a `version` of exactly `"2.1.0"`; a non-empty `runs` array; and validity against
the SARIF 2.1.0 JSON Schema file you pass with `--output-schema`.

**`cyclonedx-1.5`.** Well-formed JSON; `bomFormat` of `"CycloneDX"`; `specVersion` of `"1.5"`; and validity against the
CycloneDX 1.5 JSON Schema file you pass with `--output-schema`.

**`json`.** Well-formed JSON that is not `null`; and, when you pass `--output-schema`, validity against that file.

The checker does not ship the SARIF or CycloneDX schema files: you fetch them from their publishers and pass the file.
It prints the sha256 of the file it used, so a reader of the result knows which bytes judged the output.

| format | published schema (fetch it yourself) | sha256 of the file |
|---|---|---|
| SARIF 2.1.0 | `https://docs.oasis-open.org/sarif/sarif/v2.1.0/errata01/os/schemas/sarif-schema-2.1.0.json` | UNRUN: this repository's author fetched nothing; compute it with `sha256sum` on the file you fetched |
| CycloneDX 1.5 | `https://raw.githubusercontent.com/CycloneDX/specification/1.5/schema/bom-1.5.schema.json` | UNRUN: same; the file refers to companion schema files that must sit beside it |

The two URLs are written from memory and are UNVERIFIED: check them before relying on them. A published sha256 is
recorded here once someone has fetched the file and read it.

remark: with no `--output-schema`, a `sarif-2.1.0`, `cyclonedx-1.5` or `json` output is reported `UNMEASURED: no schema
given`, never `PASS`: the runner then knows only that the file is JSON of the right identity, not that it is a valid
SARIF or CycloneDX document. A schema file that cannot be compiled (for example a CycloneDX file whose companions are
missing) is also `UNMEASURED`, not a pass and not a failure of the record.

remark: the JUnit check is structural. A report with one empty-named case fails; a report that lists a test as passed
when it failed is not detected.

## The waiver file

A repository may hold a file `.saphan-waivers.yaml` at its root that accepts specific findings for a limited time:

```yaml
waivers:
  - rule: synthetic-fixture-token          # the rule id of the finding (SARIF result.ruleId)
    fingerprint: 3f9a1c7e5b2d4a60          # a value of the result's partialFingerprints or fingerprints
    reason: "synthetic value in a test fixture; see its README"
    expires: 2027-03-31                    # a calendar date, UTC
```

- All four fields are required for each waiver; an entry missing one is refused, naming it.
- A waiver applies to the findings whose rule id equals `rule` and one of whose fingerprints equals `fingerprint`, and
  to no other.
- It is in force through the end of the `expires` date, UTC. After that it is **expired**.
- **An expired waiver is reported and never silently applied.** The finding it covered counts as a finding again, and the
  report names the waiver, its rule, its fingerprint, its reason and the date it expired.
- A run whose class is `findings` and whose every finding is covered by an unexpired waiver is reported as a pass; the
  report lists the waived findings.

The public conformance runner does not read waivers: a fixture is checked as it is. This section is what a gate that
does must do.

## Reporting to Tests Studio

A tool run is reported to a Tests Studio runner as **one step** of a test run, using the runner-v1 events and evidence
calls already defined by the Tests Studio runner specification (`spec/TEST_RUNNER_V1.md`, cited by file only):

1. `step.started` for the step, named after the record's `id`.
2. For a `sarif-2.1.0` output, the SARIF file is attached as **evidence of kind `artifact`**
   (`Tests-Evidence-Kind: artifact`, with the step's `Tests-Step-Id`). The file is sent as it was written.
3. `step.finished` with an `outcome` taken from the exit map: `clean` is `pass`, `findings` is `fail`, `tool-error` is
   `error`. A waived `findings` run is `pass` (see above).

An unexpired waiver changes the outcome; an exit class never does so by itself.

Replaying a `junit-xml` output as individual test cases waits for the import profile now under review. Its title is not
fixed in this repository yet, so it is not quoted here; this sentence takes the title once the profile lands. Until then, a JUnit output is attached as an `artifact`
the same way, and the step's outcome comes from the exit map.

## The conformance runner

```
tool-conformance --record <file> --fixtures-root <dir> [--output-schema <file>] [--asset <file>]
                 [--record-schema <file>] [--timeout <duration>]
```

`--record-schema` defaults to `spec/schemas/tool-invocation-v1.schema.json`, relative to the directory you run in.
`--timeout` defaults to 60 seconds. It runs these checks in order, stops at the first `FAIL`, and prints one line per
check in the form `(<letter>) <STATUS>: <text>`:

| check | what it does |
|---|---|
| (a) | the record validates against the schema, plus the runner's extra rules |
| (b) | an `asset` pin's sha256 is compared with `--asset`; an `image` pin is `NOT APPLICABLE`; an asset pin with no `--asset` is `UNMEASURED` |
| (c) | the argv runs with the inputs bound, in a fresh copy of the fixture, under the timeout |
| (d) | the exit code maps through `exit_map` and must equal `fixture.expect` |
| (e) | the output file exists and is checked by its format, as above |
| (f) | the last line, always printed, says egress was not observed |

A line is `PASS`, `FAIL`, `NOT APPLICABLE` or `UNMEASURED`. The exit status is 0 when no check failed and 1 when one did;
2 means a usage error. `UNMEASURED` and `NOT APPLICABLE` never make the exit status non-zero, and the `summary:` line
counts all four separately.

The fixtures the checker is meant to be run against are in `conformance/testdata/tool-fixtures/`, one directory per
case, each with a README that names its expected outcome.
