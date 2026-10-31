package gopassfail

import "testing"

func TestAddPasses(t *testing.T) {
	if got := Add(2, 3); got != 5 {
		t.Fatalf("Add(2, 3) = %d, want 5", got)
	}
}

// TestSubFails fails on purpose: Sub is wrong. The fixture's expected outcome is "findings".
func TestSubFails(t *testing.T) {
	if got := Sub(5, 3); got != 2 {
		t.Fatalf("Sub(5, 3) = %d, want 2", got)
	}
}
