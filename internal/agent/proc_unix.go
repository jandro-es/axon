//go:build unix

package agent

import (
	"os/exec"
	"syscall"
)

// killProcessGroup places the child in its own process group and replaces the
// context-cancel action with a kill of that whole group. Killing only the
// direct child (the exec.CommandContext default) leaves any helpers it spawned
// running — and holding the stdout/stderr pipes, which stalls Wait until they
// exit on their own.
func killProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}
