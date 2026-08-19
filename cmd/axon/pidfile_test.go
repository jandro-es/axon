package main

import (
	"os"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
)

func TestCheckNotRunning(t *testing.T) {
	dir := t.TempDir()

	// No pidfile at all — nothing to guard against.
	if err := checkNotRunning(dir); err != nil {
		t.Errorf("no pidfile: err = %v, want nil", err)
	}

	// A pidfile pointing at a LIVE process must refuse the start (two daemons
	// on one profile double-run every automation). The test runner's parent is
	// a convenient live process that is not us.
	if err := os.WriteFile(pidFilePath(dir), []byte(strconv.Itoa(os.Getppid())+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := checkNotRunning(dir)
	if err == nil {
		t.Fatal("live pid in pidfile: want an error, got nil")
	}
	if !strings.Contains(err.Error(), "already running") {
		t.Errorf("error should say a daemon is already running, got: %v", err)
	}

	// Our own pid (e.g. re-entry) does not block.
	if err := os.WriteFile(pidFilePath(dir), []byte(strconv.Itoa(os.Getpid())+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := checkNotRunning(dir); err != nil {
		t.Errorf("own pid: err = %v, want nil", err)
	}

	// A stale pidfile (dead pid) does not block a restart after a crash.
	if err := os.WriteFile(pidFilePath(dir), []byte("999999999\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := checkNotRunning(dir); err != nil {
		t.Errorf("stale pid: err = %v, want nil", err)
	}
}

// TestSignalErrMeansAlive pins the interpretation of signal 0's result. The
// case that matters is EPERM: a daemon started with sudo is very much alive,
// and reading it as dead lets a second daemon start alongside it.
func TestSignalErrMeansAlive(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"no error — ours and alive", nil, true},
		{"EPERM — alive, owned by another user", syscall.EPERM, true},
		{"wrapped EPERM", &os.SyscallError{Syscall: "kill", Err: syscall.EPERM}, true},
		{"ESRCH — no such process", syscall.ESRCH, false},
		{"process already released", os.ErrProcessDone, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := signalErrMeansAlive(tt.err); got != tt.want {
				t.Errorf("signalErrMeansAlive(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

// TestProcessAliveForeignProcess is the end-to-end version of the above: pid 1
// is always running and always owned by root, so a non-root test process can
// only see it as alive if EPERM is handled.
func TestProcessAliveForeignProcess(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("pid 1 is not a stable init process on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("running as root — signalling pid 1 would not return EPERM")
	}
	if !processAlive(1) {
		t.Error("processAlive(1) = false, want true (init is running but not ours)")
	}
}
