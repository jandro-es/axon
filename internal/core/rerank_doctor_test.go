package core

import (
	"strings"
	"testing"

	"github.com/jandro-es/axon/internal/config"
)

func TestRerankCheckMalformed(t *testing.T) {
	p := config.Profile{Retrieval: config.RetrievalConfig{Rerank: "cohere:x"}}
	c := rerankCheck(p)
	if c.Status != StatusWarn || !strings.Contains(strings.ToLower(c.Detail), "off or ollama") {
		t.Fatalf("malformed rerank check = %+v", c)
	}
}

func TestRerankCheckUnreachableWarns(t *testing.T) {
	// Point at a host that is not listening → warn (never fails doctor).
	p := config.Profile{Retrieval: config.RetrievalConfig{Rerank: "ollama:qwen2.5"},
		Embeddings: config.EmbeddingsConfig{Host: "http://127.0.0.1:1"}}
	if c := rerankCheck(p); c.Status != StatusWarn {
		t.Fatalf("unreachable reranker should warn, got %v", c.Status)
	}
}
