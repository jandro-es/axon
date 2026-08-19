//go:build unix

package main

import (
	"os"
	"strings"
	"testing"
)

func TestRootGuardError(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root — a temp dir would be root-owned, defeating the fixture")
	}
	userDir := t.TempDir() // owned by whoever runs the tests, which is not root

	t.Run("not root — never blocks", func(t *testing.T) {
		if err := rootGuardError(os.Geteuid(), []string{userDir}); err != nil {
			t.Errorf("err = %v, want nil", err)
		}
	})

	t.Run("root over a user-owned dir — refuses", func(t *testing.T) {
		err := rootGuardError(0, []string{userDir})
		if err == nil {
			t.Fatal("want an error, got nil")
		}
		// The message has to name the directory and point at the way out,
		// because the user hits this while typing `sudo axon start`.
		for _, want := range []string{userDir, "sudo"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error should mention %q, got: %v", want, err)
			}
		}
	})

	t.Run("root over a root-owned dir — allowed", func(t *testing.T) {
		// "/" is root-owned on every unix; a genuine root install is fine.
		if err := rootGuardError(0, []string{"/"}); err != nil {
			t.Errorf("err = %v, want nil", err)
		}
	})

	t.Run("unstattable dir does not block", func(t *testing.T) {
		if err := rootGuardError(0, []string{userDir + "/does-not-exist"}); err != nil {
			t.Errorf("err = %v, want nil", err)
		}
	})

	t.Run("checks every dir, not just the first", func(t *testing.T) {
		if err := rootGuardError(0, []string{"/", userDir}); err == nil {
			t.Error("want an error from the second dir, got nil")
		}
	})
}
