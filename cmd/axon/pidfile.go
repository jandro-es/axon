package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// pidFilePath is the per-profile daemon pidfile, in the profile's data dir so it
// is isolated like everything else (NFR-04).
func pidFilePath(dataDir string) string {
	return filepath.Join(dataDir, "axon.pid")
}

// writePidFile records the current process id for `axon stop`. Best-effort: a
// failure to write must not stop the daemon from running.
func writePidFile(dataDir string) (string, error) {
	path := pidFilePath(dataDir)
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return path, err
	}
	if err := os.WriteFile(path, []byte(strconv.Itoa(os.Getpid())+"\n"), 0o644); err != nil {
		return path, err
	}
	return path, nil
}

// readPidFile returns the pid recorded in the profile's pidfile.
func readPidFile(dataDir string) (int, error) {
	data, err := os.ReadFile(pidFilePath(dataDir))
	if err != nil {
		return 0, err
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0, fmt.Errorf("malformed pidfile: %w", err)
	}
	return pid, nil
}

// removePidFile deletes the pidfile (ignoring a missing file).
func removePidFile(dataDir string) {
	_ = os.Remove(pidFilePath(dataDir))
}

// processAlive reports whether a process with pid exists. On unix it uses
// signal 0; FindProcess always succeeds there, so the signal is the real test.
func processAlive(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return proc.Signal(syscall.Signal(0)) == nil
}

// signalStop asks the process to terminate gracefully (SIGTERM), falling back to
// Kill where SIGTERM is unsupported (Windows).
func signalStop(pid int) error {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	if err := proc.Signal(syscall.SIGTERM); err != nil {
		return proc.Kill()
	}
	return nil
}
