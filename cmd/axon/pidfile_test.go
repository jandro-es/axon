package main

import (
	"os"
	"strconv"
	"strings"
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
