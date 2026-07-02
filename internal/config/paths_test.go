package config

import (
	"path/filepath"
	"testing"
)

// TestDefaultConfigPath is the gate for the standard per-user config location:
// with no override the config resolves to <home>/.axon/config.yaml, and it
// follows an AXON_HOME override so profiles/installs stay self-contained.
func TestDefaultConfigPath(t *testing.T) {
	t.Run("follows AXON_HOME", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("AXON_HOME", home)
		want := filepath.Join(home, "config.yaml")
		if got := DefaultConfigPath(); got != want {
			t.Errorf("DefaultConfigPath() = %q, want %q", got, want)
		}
	})

	t.Run("defaults under ~/.axon", func(t *testing.T) {
		t.Setenv("AXON_HOME", "")
		got := DefaultConfigPath()
		if base := filepath.Base(got); base != DefaultConfigFile {
			t.Errorf("DefaultConfigPath() base = %q, want %q", base, DefaultConfigFile)
		}
		if dir := filepath.Base(filepath.Dir(got)); dir != ".axon" {
			t.Errorf("DefaultConfigPath() parent dir = %q, want %q", dir, ".axon")
		}
	})
}
func TestDefaultEnvPath(t *testing.T) {
	t.Setenv("AXON_HOME", "/tmp/axhome-test")
	if got, want := DefaultEnvPath(), filepath.Join("/tmp/axhome-test", ".env"); got != want {
		t.Errorf("DefaultEnvPath() = %q, want %q", got, want)
	}
}
