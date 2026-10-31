# dockerfile-misconfigured

Expected outcome: **findings**. Any Dockerfile linter or misconfiguration scanner is expected to report at least
these two well-known problems in `Dockerfile`:

1. The image runs as root: the Dockerfile has no `USER` instruction, so the container's process keeps the default
   root user.
2. The Dockerfile `ADD`s a remote URL: `ADD <url>` fetches a file at build time with no checksum, where an explicit
   download step with a verified digest is the usual advice.

Rule identifiers differ per tool. One is named here for the first problem: Trivy's misconfiguration rule `DS002`
(image user should not be root). It is expected; confirmed when a record for that tool is admitted by the gate. No
identifier is claimed for the second problem. This fixture's contract is the two problems above, not any identifier.

The URL uses the reserved `example.com` domain and is never fetched by the fixture or by the conformance runner.
