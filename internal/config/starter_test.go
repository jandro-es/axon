package config

import (
	"os"
	"strings"
	"testing"
)

func TestStarterRespectsAxonHome(t *testing.T) {
	// data_dir/config_dir MUST live under AXON_HOME when it is set — otherwise
	// an isolated setup (tests, secondary installs) silently reads and writes
	// the REAL ~/.axon profile data.
	t.Setenv("AXON_HOME", "/tmp/isolated-axon-home")
	raw, err := Starter("personal", "/tmp/vault", "ollama")
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	_, p, _ := cfg.ResolveProfile("")
	if !strings.HasPrefix(p.DataDir, "/tmp/isolated-axon-home/") {
		t.Errorf("data_dir = %q, must be under AXON_HOME", p.DataDir)
	}
	if !strings.HasPrefix(p.Claude.ConfigDir, "/tmp/isolated-axon-home/") {
		t.Errorf("claude.config_dir = %q, must be under AXON_HOME", p.Claude.ConfigDir)
	}
}

func TestStarterDefaultsToTildeAxon(t *testing.T) {
	// Without AXON_HOME the config stays portable: literal ~/.axon paths.
	os.Unsetenv("AXON_HOME")
	raw, err := Starter("personal", "~/Notes/V", "ollama")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `data_dir: "~/.axon/profiles/personal"`) {
		t.Errorf("default starter should use portable ~/.axon paths:\n%s", raw)
	}
}

func TestStarterValidatesAndCarriesChoices(t *testing.T) {
	for _, tc := range []struct {
		provider  string
		wantModel string
		wantDim   int
	}{
		{"ollama", "nomic-embed-text", 768},
		{"apple", AppleEmbeddingModel, AppleEmbeddingDim},
	} {
		raw, err := Starter("personal", "~/Notes/Vault", tc.provider)
		if err != nil {
			t.Fatalf("%s: %v", tc.provider, err)
		}
		cfg, err := Parse(raw)
		if err != nil {
			t.Fatalf("%s: starter must validate: %v", tc.provider, err)
		}
		_, p, err := cfg.ResolveProfile("")
		if err != nil {
			t.Fatal(err)
		}
		if p.Embeddings.Provider != tc.provider || p.Embeddings.Model != tc.wantModel || p.Embeddings.Dim != tc.wantDim {
			t.Errorf("%s: embeddings = %+v", tc.provider, p.Embeddings)
		}
		if p.VaultPath != "~/Notes/Vault" {
			t.Errorf("vault_path = %q", p.VaultPath)
		}
		if !strings.Contains(string(raw), "axon configure") {
			t.Error("starter should point at axon configure")
		}
	}
}
