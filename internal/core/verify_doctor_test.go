package core

import (
	"strings"
	"testing"

	"github.com/jandro-es/axon/internal/config"
)

func TestVerifyCheckMalformed(t *testing.T) {
	p := config.Profile{Models: config.ModelsConfig{Verify: "cohere:x", Routine: "ollama:qwen"}}
	c := verifyCheck(p)
	if c.Status != StatusWarn || !strings.Contains(strings.ToLower(c.Detail), "off or ollama") {
		t.Fatalf("malformed verify check = %+v", c)
	}
}

func TestVerifyCheckRoutineNotLocalWarns(t *testing.T) {
	p := config.Profile{Models: config.ModelsConfig{Verify: "ollama:judge", Routine: "claude-sonnet-5"}}
	c := verifyCheck(p)
	if c.Status != StatusWarn || !strings.Contains(c.Detail, "never triggers") {
		t.Fatalf("routine-not-local verify check = %+v", c)
	}
}

func TestVerifyCheckUnreachableWarns(t *testing.T) {
	p := config.Profile{Models: config.ModelsConfig{
		Verify: "ollama:judge", Routine: "ollama:qwen", OllamaHost: "http://127.0.0.1:1"}}
	if c := verifyCheck(p); c.Status != StatusWarn {
		t.Fatalf("unreachable verify should warn, got %v", c.Status)
	}
}
