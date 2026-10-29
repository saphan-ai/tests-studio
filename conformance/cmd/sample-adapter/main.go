// Command sample-adapter is the conformance suite's fixture adapter (see internal/sampleadapter).
package main

import (
	"os"

	"example.com/tests-studio/conformance/internal/sampleadapter"
)

func main() {
	os.Exit(sampleadapter.Main(os.Args[1:], os.Environ(), nil, os.Stdout, os.Stderr))
}
