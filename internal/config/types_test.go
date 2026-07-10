package config

import "testing"

func TestVisionModeDefaultsOff(t *testing.T) {
	if got := (IngestionConfig{}).VisionMode(); got != "off" {
		t.Fatalf("VisionMode() = %q, want off", got)
	}
	if got := (IngestionConfig{Vision: "ollama:qwen2.5vl"}).VisionMode(); got != "ollama:qwen2.5vl" {
		t.Fatalf("VisionMode() = %q, want ollama:qwen2.5vl", got)
	}
}

func TestCaptionLangsOrDefault(t *testing.T) {
	if got := (IngestionConfig{}).CaptionLangsOr(); got != "en.*" {
		t.Fatalf("CaptionLangsOr() = %q, want en.*", got)
	}
	if got := (IngestionConfig{CaptionLangs: "es.*"}).CaptionLangsOr(); got != "es.*" {
		t.Fatalf("CaptionLangsOr() = %q, want es.*", got)
	}
}
