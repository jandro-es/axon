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
	base := "version: 1\nproject_name: t\nactive_profile: p\nprofiles:\n  p:\n    vault_path: /v\n    data_dir: /d\n    ingestion:\n      ocr: %s\n"
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
