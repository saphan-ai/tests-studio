//go:build !unix

package harness

import "os/exec"

// ownProcessGroup does nothing where process groups are not available: only the direct child is
// killed at the timeout, and WaitDelay still bounds the wait for any output pipe a descendant holds.
func ownProcessGroup(*exec.Cmd) {}
