// Package gopassfail is a fixture: one function is right, one is wrong on purpose.
package gopassfail

// Add returns a+b.
func Add(a, b int) int { return a + b }

// Sub is deliberately wrong: it adds instead of subtracting.
func Sub(a, b int) int { return a + b }
