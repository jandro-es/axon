package main

import (
	"testing"

	"github.com/jandro-es/axon/internal/config"
	"github.com/jandro-es/axon/internal/embeddings"
)

func TestEmbeddingsProviderSelection(t *testing.T) {
	ollamaP := config.Profile{Embeddings: config.EmbeddingsConfig{Provider: "ollama", Model: "m", Dim: 8}}
	if _, ok := embeddingsProvider(ollamaP).(*embeddings.Ollama); !ok {
		t.Error("ollama provider not selected")
	}
	appleP := config.Profile{Embeddings: config.EmbeddingsConfig{Provider: "apple", Model: "m", Dim: 512}}
	if _, ok := embeddingsProvider(appleP).(*embeddings.Apple); !ok {
		t.Error("apple provider not selected")
	}
}
