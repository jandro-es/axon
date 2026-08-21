package config

import (
	"strings"
	"testing"
)

func TestValidateSTT(t *testing.T) {
	ok := func(mode string, mins int) IngestionConfig {
		return IngestionConfig{STT: STTConfig{Mode: mode, MaxMinutes: mins}}
	}
	if err := validateSTT(IngestionConfig{}); err != nil {
		t.Fatalf("omitted stt must be valid: %v", err)
	}
	if err := validateSTT(ok("off", 0)); err != nil {
		t.Fatalf("off must be valid: %v", err)
	}
	if err := validateSTT(ok("whisper:base", 120)); err != nil {
		t.Fatalf("whisper:base must be valid: %v", err)
	}

	cases := []struct{ name, mode, want string }{
		{"unknown provider", "vosk:small", "off or whisper:"},
		{"bare whisper", "whisper", "off or whisper:"},
		{"empty model", "whisper:", "model"},
	}
	for _, c := range cases {
		err := validateSTT(ok(c.mode, 60))
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: want error containing %q, got %v", c.name, c.want, err)
		}
	}
	for _, mins := range []int{-1, 1441} {
		if err := validateSTT(ok("whisper:base", mins)); err == nil {
			t.Errorf("max_minutes %d must be refused", mins)
		}
	}
}

func TestSTTDefaults(t *testing.T) {
	if got := (STTConfig{}).ModeOr(); got != "off" {
		t.Errorf("default mode = %q, want off", got)
	}
	if got := (STTConfig{}).MaxMinutesOr(); got != 120 {
		t.Errorf("default max_minutes = %d, want 120", got)
	}
	if got := (STTConfig{MaxMinutes: 30}).MaxMinutesOr(); got != 30 {
		t.Errorf("explicit max_minutes = %d, want 30", got)
	}
	if got := (STTConfig{Mode: "whisper:small"}).WhisperModel(); got != "small" {
		t.Errorf("WhisperModel = %q, want small", got)
	}
	if got := (STTConfig{Mode: "off"}).WhisperModel(); got != "" {
		t.Errorf("off must name no model, got %q", got)
	}
}
