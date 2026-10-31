# secret-synthetic

Expected outcome: **findings**. A secret scanner run over this directory with the rule in `.gitleaks.toml`
reports exactly one finding: the token in `app.conf`, rule id `synthetic-fixture-token`.

The token in `app.conf` is **synthetic**. It is not a credential for any service and was never issued by one. It is
deliberately not in the format of any provider's real key, so that the repository's own secret scanning and push
protection do not mistake it for a leak. The rule in `.gitleaks.toml` matches only this made-up shape: the literal
prefix `TSFIXTURE-SYNTHETIC-` followed by 16 lowercase hex digits. (This file does not spell the token out, so the
rule matches one file, not two.)

A tool run over this fixture without that config (default rules only) is expected to report nothing. That is part of
what the fixture shows: the finding comes from the shipped rule, not from a provider pattern.
