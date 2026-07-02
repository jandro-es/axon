package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/jandro-es/axon/internal/config"
)

func TestVaultMoveEndToEnd(t *testing.T) {
	dir := t.TempDir()
	cfgPath := writeTempConfig(t, dir)

	// Provision so the vault (and some content) exists.
	if out, err := run(t, "init", "--config", cfgPath); err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}
	newVault := filepath.Join(dir, "relocated-vault")

	out, err := run(t, "vault", "move", newVault, "--config", cfgPath)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(newVault, "Templates", "Daily Note.md")); err != nil {
		t.Error("vault content did not move:", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "vault")); !os.IsNotExist(err) {
		t.Error("old vault location should be gone")
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	_, p, _ := cfg.ResolveProfile("")
	if p.VaultPath != newVault {
		t.Errorf("vault_path = %q, want %q", p.VaultPath, newVault)
	}
	if !strings.Contains(out, "Obsidian") {
		t.Errorf("must tell the user about the Obsidian bookmark:\n%s", out)
	}

	// The moved vault still reindexes cleanly (paths are vault-relative).
	if out, err := run(t, "reindex", "--config", cfgPath); err != nil {
		t.Errorf("reindex after move: %v\n%s", err, out)
	}
}

func TestVaultMoveRefusesWhileDaemonRuns(t *testing.T) {
	dir := t.TempDir()
	cfgPath := writeTempConfig(t, dir)
	if _, err := run(t, "init", "--config", cfgPath); err != nil {
		t.Fatal(err)
	}
	// Simulate a live daemon: pidfile with OUR pid (definitely alive).
	dataDir := filepath.Join(dir, "data")
	if err := os.WriteFile(filepath.Join(dataDir, "axon.pid"),
		[]byte(strconv.Itoa(os.Getpid())+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := run(t, "vault", "move", filepath.Join(dir, "x"), "--config", cfgPath); err == nil {
		t.Error("must refuse to move while the daemon runs (headless, no --stop-daemon)")
	}
}
