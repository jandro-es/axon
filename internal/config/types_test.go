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

func TestResearchConfigDefaults(t *testing.T) {
	if got := (ResearchConfig{}).MaxFetchesOr(); got != 8 {
		t.Fatalf("MaxFetchesOr() = %d, want 8", got)
	}
	if got := (ResearchConfig{}).BudgetTokensOr(); got != 120_000 {
		t.Fatalf("BudgetTokensOr() = %d, want 120000", got)
	}
	if got := (ResearchConfig{MaxFetches: 3, BudgetTokens: 5}).MaxFetchesOr(); got != 3 {
		t.Fatalf("MaxFetchesOr override = %d, want 3", got)
	}
	if got := (ResearchConfig{MaxFetches: 3, BudgetTokens: 5}).BudgetTokensOr(); got != 5 {
		t.Fatalf("BudgetTokensOr override = %d, want 5", got)
	}
}
