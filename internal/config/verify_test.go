package config

import "testing"

func TestVerifyModeDefaultsOff(t *testing.T) {
	if got := (ModelsConfig{}).VerifyMode(); got != "off" {
		t.Errorf("empty VerifyMode = %q, want off", got)
	}
	if got := (ModelsConfig{Verify: "ollama:judge"}).VerifyMode(); got != "ollama:judge" {
		t.Errorf("VerifyMode = %q", got)
	}
}

func TestVerifyMinScoreOr(t *testing.T) {
	if got := (ModelsConfig{}).VerifyMinScoreOr(); got != 6 {
		t.Errorf("default VerifyMinScoreOr = %d, want 6", got)
	}
	if got := (ModelsConfig{VerifyMinScore: 8}).VerifyMinScoreOr(); got != 8 {
		t.Errorf("VerifyMinScoreOr = %d, want 8", got)
	}
}
