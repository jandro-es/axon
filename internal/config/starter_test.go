package config

import (
	"strings"
	"testing"
)

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
