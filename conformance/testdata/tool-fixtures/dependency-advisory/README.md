# dependency-advisory

Expected outcome: **findings**. `package.json` pins `lodash` at exactly `4.17.15`, a version with published
advisories. A dependency scanner that reads npm manifests is expected to report at least one advisory against it.

Advisory identifiers, from memory and **UNVERIFIED** (the master checks them): `CVE-2020-8203`, GitHub advisory
`GHSA-p6mc-m468-83gw` (prototype pollution in `zipObjectDeep`, fixed in `4.17.19`).

There is no lock file and nothing is installed or downloaded: the fixture holds the manifest only.
