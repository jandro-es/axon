package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jandro-es/axon/internal/config"
)

func TestEmbeddingsCheckProviderAware(t *testing.T) {
	ollama := config.Profile{Embeddings: config.EmbeddingsConfig{Provider: "ollama", Model: "m", Dim: 8}}
	if c := embeddingsCheck(ollama); c.Name != "ollama" {
		t.Errorf("ollama profile should keep the ollama binary check, got %q", c.Name)
	}

	apple := config.Profile{Embeddings: config.EmbeddingsConfig{Provider: "apple", Model: "m", Dim: 512,
		Helper: "/nonexistent/axon-apple-embed"}}
	c := embeddingsCheck(apple)
	if c.Name != "apple-embeddings" || c.Status != StatusWarn || !strings.Contains(c.Detail, "axon init") {
		t.Errorf("missing helper should warn pointing at axon init, got %+v", c)
	}

	// A present, executable helper is OK.
	dir := t.TempDir()
	helper := filepath.Join(dir, "axon-apple-embed")
	if err := os.WriteFile(helper, []byte("bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	apple.Embeddings.Helper = helper
	if c := embeddingsCheck(apple); c.Status != StatusOK {
		t.Errorf("existing helper should be OK, got %+v", c)
	}
}
