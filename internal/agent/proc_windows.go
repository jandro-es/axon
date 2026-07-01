//go:build windows

package agent

import "os/exec"

// killProcessGroup is a no-op on Windows: exec.CommandContext's default kill
// applies, and WaitDelay (set by the caller) bounds Wait regardless.
func killProcessGroup(_ *exec.Cmd) {}
