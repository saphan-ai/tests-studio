# Contributing

Thank you for looking. This repository is small on purpose: a specification, and a suite that checks adapters against it.

## Reporting

- **A problem or a question:** open an issue at <https://github.com/saphan-ai/tests-studio/issues>. Say what you ran, what you expected and what you saw; paste the command and its output.
- **A vulnerability:** never in a public issue. Use the private channel in [`SECURITY.md`](SECURITY.md).
- **A mistake in the specification** (two sentences that disagree, a rule you cannot implement): an issue is welcome. Quote the section number.

## Your test framework is missing?

Write an adapter. You do not need our permission and you do not need our code.

1. Read [`spec/TEST_RUNNER_V1.md`](spec/TEST_RUNNER_V1.md).
2. Write the adapter for your framework, with a small sample project that has a passing, a failing and a skipped test.
3. Run the conformance suite against it **in both modes**, `standalone` and `leg`. The commands are in the README, under "Make your test framework report to Tests Studio".
4. **An adapter is accepted when every check passes in both modes.** Open a pull request that adds a line to the README's list of adapters, with the suite's output pasted in, the version of the adapter you ran and the date.

Where your adapter lives is up to you; it does not have to be in this repository.

## Building and testing

You need Go 1.26 or newer.

```sh
go -C conformance build ./...
go -C conformance vet ./...
go -C conformance test ./... -count=1
go -C docscheck test ./... -count=1  # needs: self
```

The suite's own tests that use real sockets skip themselves where the machine denies loopback, and `go test` still exits 0. Set `TESTS_CONFORMANCE_REQUIRE_SOCKETS=1` when you run them to make a skip a failure. Set `TESTS_DOCS_REQUIRE_SOCKETS=1` for the same on the documentation check.

The documentation check in `docscheck/` reads the README, `SECURITY.md` and `CONTRIBUTING.md`. It fails when a required section is missing or out of order, when a command in the README does not run, when a number under "What is measured" differs from what its command prints, when a licence file is not the standard text, and when a document names a host or an address outside the examples and documentation ranges. If you change a document, run it.

## How a change is made here

- **One change, one pull request.** A refactor and a change of behaviour are separate.
- **A test first.** A change to the suite or the specification comes with a test that fails before it and passes after it. A fix to a check shows the recording that used to pass wrongly.
- **Never weaken a check to make something pass.** A check that measures something an adapter must do fails when there is nothing to measure; keep it that way. A skipped measurement is never a pass.
- **The specification and the suite change together.** A rule added to one is added to the other in the same pull request. `spec/ecosystem-v1/` is a copy of a shared contract and is not edited here.
- **Commit messages** read `<area>: <what>` in English, with the reason in the body. No `wip` and no `fixup!` commits on a branch under review.
- **Nothing confidential.** Do not put a customer name, a private address, a key or an internal host in a commit, an issue or a pull request. Use `example.com`, `.example`, `.test` and the documentation address ranges.
- **Licences.** Code is Apache-2.0 and documents are CC BY 4.0 (see the README). By contributing you agree your contribution is licensed the same way.
- A maintainer reviews every pull request and merges it; opening one is the last step you take.
