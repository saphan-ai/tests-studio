# secret-synthetic

Expected outcome: **findings**. A secret scanner run over this directory with the rule in `.gitleaks.toml`
reports exactly one finding: the token in `app.conf`, rule id `synthetic-fixture-token` (the id this fixture's own
`.gitleaks.toml` defines; expected; confirmed when a record for that tool is admitted by the gate).

The token in `app.conf` is **synthetic**. It is not a credential for any service and was never issued by one. It is
deliberately not in the format of any provider's real key, so that the repository's own secret scanning and push
protection do not mistake it for a leak. The rule in `.gitleaks.toml` matches only this made-up shape: the literal
prefix `TSFIXTURE-SYNTHETIC-` followed by 16 lowercase hex digits. (This file does not spell the token out, so the
rule matches one file, not two.)

What is known: the rule in `.gitleaks.toml` matches the token in `app.conf` once, and the token matches no provider's
key format that its author knows of. What is not measured: whether a scanner's own default rules also report it.
gitleaks' default `generic-api-key` rule looks at lines such as `api_token = ...` and may well match this one; nobody has
run gitleaks over this fixture. Treat the expected outcome as "at least the finding from the shipped rule" until a
record for that tool is admitted and measured.
