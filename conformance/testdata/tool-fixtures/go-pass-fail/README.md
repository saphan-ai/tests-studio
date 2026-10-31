# go-pass-fail

Expected outcome: **findings**. `go test ./...` in this directory exits non-zero: `TestAddPasses` passes and
`TestSubFails` fails on purpose (`Sub` adds instead of subtracting). A JUnit report of the run holds two test cases,
one passed and one failed.

This is a module of its own (its `go.mod` sits here), and it lives under `testdata`, so `go test ./...` and
`go vet ./...` run from the conformance module never reach it. Run it by hand with `go -C <this directory> test ./...`.
