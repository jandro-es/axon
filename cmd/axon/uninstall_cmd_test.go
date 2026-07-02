package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUninstallPurgeRefusedWithoutHeadlessConfirmation(t *testing.T) {
	home := t.TempDir()
	t.Setenv("AXON_HOME", home)
	sentinel := filepath.Join(home, "config.yaml")
	if err := os.WriteFile(sentinel, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := run(t, "uninstall", "--purge", "--config", sentinel)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if _, err := os.Stat(sentinel); err != nil {
		t.Error("purge must be refused without --yes-purge-all-data")
	}
	if !strings.Contains(out, "not confirmed") {
		t.Errorf("should explain the refusal:\n%s", out)
	}
}

func TestUninstallPurgeRemovesHomeNeverVault(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "axon-home")
	vault := filepath.Join(root, "vault")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(vault, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(vault, "note.md"), []byte("# mine"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AXON_HOME", home)

	out, err := run(t, "uninstall", "--purge", "--yes-purge-all-data",
		"--config", filepath.Join(home, "config.yaml"))
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if _, err := os.Stat(home); !os.IsNotExist(err) {
		t.Error("AXON home should be removed with confirmed --purge")
	}
	if _, err := os.Stat(filepath.Join(vault, "note.md")); err != nil {
		t.Error("the vault must NEVER be touched by uninstall")
	}
	if !strings.Contains(out, "vault") {
		t.Errorf("summary should reassure about the vault:\n%s", out)
	}
}

func TestUninstallNothingInstalledIsClean(t *testing.T) {
	home := t.TempDir()
	t.Setenv("AXON_HOME", home)
	out, err := run(t, "uninstall", "--config", filepath.Join(home, "nope.yaml"))
	if err != nil {
		t.Fatalf("uninstall on a clean machine must succeed: %v\n%s", err, out)
	}
	for _, want := range []string{"daemon", "service", "binary"} {
		if !strings.Contains(out, want) {
			t.Errorf("step %q missing:\n%s", want, out)
		}
	}
}
