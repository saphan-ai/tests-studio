//go:build unix

package harness

import (
	"os/exec"
	"syscall"
)

// ownProcessGroup puts the command in a process group of its own and makes a cancelled context
// kill the whole group, so a grandchild (a test worker, a browser, a shell's child) dies with it.
func ownProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}
