package ingestion

import (
	"context"
	"strings"
	"testing"

	"github.com/jandro-es/axon/internal/config"
)

func TestSTTForOffReturnsNilNil(t *testing.T) {
	got, err := STTFor(config.IngestionConfig{}, "darwin")
	if err != nil || got != nil {
		t.Fatalf("omitted stt must be (nil, nil), got (%v, %v)", got, err)
	}
	got, err = STTFor(config.IngestionConfig{STT: config.STTConfig{Mode: "off"}}, "linux")
	if err != nil || got != nil {
		t.Fatalf("explicit off must be (nil, nil), got (%v, %v)", got, err)
	}
}

func TestSTTForMissingBinaryIsAnActionableError(t *testing.T) {
	cfg := config.IngestionConfig{STT: config.STTConfig{
		Mode: "whisper:base", Binary: "/nonexistent/whisper"}}
	_, err := STTFor(cfg, "darwin")
	if err == nil {
		t.Fatal("a missing binary must be an error the caller can degrade on")
	}
	if !strings.Contains(err.Error(), "whisper") {
		t.Fatalf("the error must name what is missing: %v", err)
	}
}

func TestSTTForBuildsWhisperProvider(t *testing.T) {
	// A binary that exists is enough to construct; nothing is executed here.
	cfg := config.IngestionConfig{STT: config.STTConfig{
		Mode: "whisper:base", Binary: "/bin/echo"}}
	got, err := STTFor(cfg, "darwin")
	if err != nil {
		t.Fatalf("construct: %v", err)
	}
	if got == nil {
		t.Fatal("a configured provider must not be nil")
	}
}

// Probe must not transcribe: a provider that cannot tell the duration cheaply
// reports (0, nil) meaning "unknown, proceed" rather than erroring.
func TestWhisperProbeUnknownIsNotAnError(t *testing.T) {
	w := &whisperSTT{bin: "/bin/echo", model: "base"}
	d, err := w.Probe(context.Background(), "/nonexistent/audio.m4a")
	if err != nil {
		t.Fatalf("an unknown duration must not be an error: %v", err)
	}
	if d != 0 {
		t.Fatalf("unknown duration must be 0, got %v", d)
	}
}
