//go:build unix

package main

import (
	"fmt"
	"os"
	"syscall"
)

// checkNotRoot refuses to start the daemon as root over directories owned by
// somebody else. Root writes succeed everywhere, so nothing fails at the time:
// the daemon simply creates each new vault note, review queue and Claude config
// file owned by root and mode 0600, and the user's own daemon — the one launchd
// runs — is then locked out of its own notes with "permission denied". The
// damage outlives the process and has to be undone with chown, so this is a
// start-time refusal rather than a warning.
func checkNotRoot(dirs ...string) error {
	return rootGuardError(os.Geteuid(), dirs)
}

// rootGuardError holds the decision, separated from os.Geteuid so it is
// testable without actually being root.
func rootGuardError(euid int, dirs []string) error {
	if euid != 0 {
		return nil
	}
	for _, dir := range dirs {
		owner, ok := ownerUID(dir)
		if !ok || owner == 0 {
			// Unstattable, or a genuine root-owned install: nothing to protect.
			continue
		}
		return fmt.Errorf("refusing to run as root: %s belongs to uid %d, not root — "+
			"every note, queue entry and config file this daemon created would be root-owned "+
			"and unreadable by that user. Re-run `axon start` without sudo", dir, owner)
	}
	return nil
}

// ownerUID returns the uid owning path, and whether it could be determined.
func ownerUID(path string) (uint32, bool) {
	fi, err := os.Stat(path)
	if err != nil {
		return 0, false
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}
	return st.Uid, true
}
