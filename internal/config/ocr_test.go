package config

import (
	"strings"
	"testing"
)

func TestOCRModeDefaultsOff(t *testing.T) {
	if got := (IngestionConfig{}).OCRMode(); got != "off" {
		t.Errorf("empty OCRMode = %q, want off", got)
	}
	if got := (IngestionConfig{OCR: "apple"}).OCRMode(); got != "apple" {
		t.Errorf("OCRMode = %q, want apple", got)
	}
}

func TestOCRValidationRejectsUnknown(t *testing.T) {
	base := `version: 1
project_name: t
active_profile: p
profiles:
  p:
    vault_path: "/tmp/v"
    data_dir: "/tmp/d"
    claude: {auth_mode: subscription}
    dashboard: {host: "127.0.0.1", port: 7777}
    embeddings: {provider: ollama, model: nomic-embed-text, dim: 768, batch_size: 16}
    models: {classify: c, routine: r, synthesis: s}
    limits: {daily_tokens: 1, weekly_tokens: 1}
    retrieval: {top_k: 4, max_context_tokens: 1000}
    policy: {data_residency: local-only}
    ingestion: {ocr: %s}
`
	if _, err := Parse([]byte(strings.Replace(base, "%s", "apple", 1))); err != nil {
		t.Fatalf("apple should validate: %v", err)
	}
	if _, err := Parse([]byte(strings.Replace(base, "%s", "banana", 1))); err == nil {
		t.Fatal("banana should be rejected by oneof")
	}
}

func TestDefaultOCRHelperPath(t *testing.T) {
	if !strings.HasSuffix(DefaultOCRHelperPath(), "/bin/axon-apple-ocr") {
		t.Errorf("DefaultOCRHelperPath = %q", DefaultOCRHelperPath())
	}
}
