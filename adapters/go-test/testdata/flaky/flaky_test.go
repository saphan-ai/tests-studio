// Package flaky is a fixture: TestFirstRunFails fails on the first run inside one test process and
// passes on every later one, which is what `-count=3` makes visible only to the first repetition.
package flaky

import "testing"

var runs int

func TestFirstRunFails(t *testing.T) {
	runs++
	if runs == 1 {
		t.Fatal("failed on the first run")
	}
}
