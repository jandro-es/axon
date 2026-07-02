package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jandro-es/axon/internal/config"
)

func TestSetupProvisionsFreshInstallHeadless(t *testing.T) {
	home := t.TempDir()
	t.Setenv("AXON_HOME", home)
	cfgPath := filepath.Join(home, "config.yaml")
	envPath := filepath.Join(home, ".env")
	vault := filepath.Join(home, "vault")

	out, err := run(t, "setup", "--vault", vault, "--embeddings", "ollama",
		"--config", cfgPath, "--env", envPath)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("starter config invalid: %v", err)
	}
	_, p, _ := cfg.ResolveProfile("")
	if p.Embeddings.Provider != "ollama" {
		t.Errorf("provider = %q", p.Embeddings.Provider)
	}
	st, err := os.Stat(envPath)
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Errorf(".env: err=%v mode=%v (want 0600)", err, st.Mode().Perm())
	}
	if _, err := os.Stat(filepath.Join(vault, "Templates", "Daily Note.md")); err != nil {
		t.Errorf("init did not scaffold the vault: %v", err)
	}
	if !strings.Contains(out, "setup complete") {
		t.Errorf("missing completion summary:\n%s", out)
	}

	// Idempotency: a second run keeps everything and reports it.
	before, _ := os.ReadFile(cfgPath)
	out2, err := run(t, "setup", "--config", cfgPath, "--env", envPath)
	if err != nil {
		t.Fatalf("second setup: %v\n%s", err, out2)
	}
	after, _ := os.ReadFile(cfgPath)
	if string(before) != string(after) {
		t.Error("second setup must not rewrite the config")
	}
	if !strings.Contains(out2, "config exists") {
		t.Errorf("second run should report kept config:\n%s", out2)
	}
}

func TestSetupHeadlessFreshRequiresVault(t *testing.T) {
	home := t.TempDir()
	t.Setenv("AXON_HOME", home)
	cfgPath := filepath.Join(home, "config.yaml")
	if _, err := run(t, "setup", "--config", cfgPath, "--env", filepath.Join(home, ".env")); err == nil {
		t.Error("fresh headless setup without --vault must error, not hang")
	}
}

func TestSetupAppleProviderLandsInStarter(t *testing.T) {
	home := t.TempDir()
	t.Setenv("AXON_HOME", home)
	cfgPath := filepath.Join(home, "config.yaml")
	out, err := run(t, "setup", "--vault", filepath.Join(home, "v"), "--embeddings", "apple",
		"--config", cfgPath, "--env", filepath.Join(home, ".env"))
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	cfg, _ := config.Load(cfgPath)
	_, p, _ := cfg.ResolveProfile("")
	if p.Embeddings.Provider != "apple" || p.Embeddings.Dim != config.AppleEmbeddingDim {
		t.Errorf("embeddings = %+v", p.Embeddings)
	}
}
