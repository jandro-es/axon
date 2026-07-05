package config

import "testing"

func TestRerankModeDefaultsOff(t *testing.T) {
	if got := (RetrievalConfig{}).RerankMode(); got != "off" {
		t.Errorf("empty RerankMode = %q, want off", got)
	}
	if got := (RetrievalConfig{Rerank: "ollama:qwen2.5"}).RerankMode(); got != "ollama:qwen2.5" {
		t.Errorf("RerankMode = %q", got)
	}
}

func TestRerankOverfetchOr(t *testing.T) {
	if got := (RetrievalConfig{}).RerankOverfetchOr(); got != 3 {
		t.Errorf("default overfetch = %d, want 3", got)
	}
	if got := (RetrievalConfig{RerankOverfetch: 5}).RerankOverfetchOr(); got != 5 {
		t.Errorf("overfetch = %d, want 5", got)
	}
}
