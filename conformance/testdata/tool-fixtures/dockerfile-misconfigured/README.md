# dockerfile-misconfigured

Expected outcome: **findings**. Any Dockerfile linter or misconfiguration scanner is expected to report at least
these two well-known problems in `Dockerfile`:

1. No `USER` instruction: the container runs as root.
2. `ADD` of a remote URL: `COPY` plus an explicit download step is the usual advice, and `ADD <url>` fetches an
   unverified file at build time.

Rule identifiers differ per tool. From memory, and **UNVERIFIED** (the master checks them): Trivy's misconfiguration
scanner names the missing user `DS002`; Hadolint names `ADD` where `COPY` would do `DL3020`. This fixture's
contract is the two problems above, not those identifiers.

The URL uses the reserved `example.com` domain and is never fetched by the fixture or by the conformance runner.
