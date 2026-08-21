package config

import (
	"fmt"
	"strings"
)

// STTConfig configures local speech-to-text (FR-212, ADR-042).
type STTConfig struct {
	// Mode is "off" (default) or "whisper:<model>".
	Mode string `yaml:"mode,omitempty"`
	// Binary optionally pins the executable; empty looks up "whisper" on PATH,
	// the OCR/vision helper convention.
	Binary string `yaml:"binary,omitempty"`
	// MaxMinutes refuses recordings longer than this (default 120). Config
	// rather than a Go const because transcription speed varies enormously by
	// machine and model, so the right ceiling genuinely differs per install.
	MaxMinutes int `yaml:"max_minutes,omitempty"`
}

// ModeOr returns the configured mode, defaulting to off.
func (s STTConfig) ModeOr() string {
	if strings.TrimSpace(s.Mode) == "" {
		return "off"
	}
	return strings.TrimSpace(s.Mode)
}

// MaxMinutesOr returns the duration cap, defaulting to 120.
func (s STTConfig) MaxMinutesOr() int {
	if s.MaxMinutes == 0 {
		return 120
	}
	return s.MaxMinutes
}

// WhisperModel returns the model named by a "whisper:<model>" mode, or "".
func (s STTConfig) WhisperModel() string {
	mode := s.ModeOr()
	if !strings.HasPrefix(mode, "whisper:") {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(mode, "whisper:"))
}

// validateSTT applies the ADR-042 rules, run beside validateVision.
func validateSTT(cfg IngestionConfig) error {
	s := cfg.STT
	mode := s.ModeOr()
	if mode != "off" {
		if !strings.HasPrefix(mode, "whisper:") {
			return fmt.Errorf("ingestion.stt.mode must be off or whisper:<model> (got %q)", s.Mode)
		}
		if s.WhisperModel() == "" {
			return fmt.Errorf("ingestion.stt.mode %q names no model — use whisper:<model>, e.g. whisper:base", s.Mode)
		}
	}
	if s.MaxMinutes < 0 || s.MaxMinutes > 1440 {
		return fmt.Errorf("ingestion.stt.max_minutes must be 0–1440 (got %d; 0 means the default 120)", s.MaxMinutes)
	}
	return nil
}
