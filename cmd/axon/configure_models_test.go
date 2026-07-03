package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jandro-es/axon/internal/config"
)

func TestConfigureModelsRejectsLocalSynthesis(t *testing.T) {
	dir := t.TempDir()
	cfgPath := writeTempConfig(t, dir)
	if _, err := run(t, "configure", "models", "synthesis", "ollama:qwen3:8b", "--config", cfgPath); err == nil {
		t.Fatal("local synthesis must be rejected (ADR-015)")
	}
	if _, err := run(t, "configure", "models", "routine", "apple", "--config", cfgPath); err == nil {
		t.Fatal("apple routine must be rejected (classify only)")
	}
	// The invalid values were not persisted.
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	_, p, _ := cfg.ResolveProfile("")
	if strings.HasPrefix(p.Models.Synthesis, "ollama:") || p.Models.Routine == "apple" {
		t.Errorf("invalid values persisted: %+v", p.Models)
	}
}

func TestConfigureModelsClaudeStringStillWorks(t *testing.T) {
	dir := t.TempDir()
	cfgPath := writeTempConfig(t, dir)
	out, err := run(t, "configure", "models", "classify", "claude-haiku-4-5", "--config", cfgPath)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	cfg, _ := config.Load(cfgPath)
	_, p, _ := cfg.ResolveProfile("")
	if p.Models.Classify != "claude-haiku-4-5" {
		t.Errorf("classify = %q", p.Models.Classify)
	}
}

func TestConvergeModelTierProbesOllama(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"model":   "qwen3:8b",
			"message": map[string]string{"role": "assistant", "content": "ok"},
			"done":    true,
		})
	}))
	defer srv.Close()

	m := config.ModelsConfig{OllamaHost: srv.URL}
	if err := convergeModelTier(context.Background(), io.Discard, m, "ollama:qwen3:8b"); err != nil {
		t.Fatalf("converge against live server: %v", err)
	}

	srv.Close() // now unreachable → probe must fail with remediation text
	err := convergeModelTier(context.Background(), io.Discard, m, "ollama:qwen3:8b")
	if err == nil || !strings.Contains(err.Error(), "ollama pull") {
		t.Fatalf("err = %v, want remediation hint", err)
	}
}

func TestConvergeModelTierClaudeIsNoop(t *testing.T) {
	if err := convergeModelTier(context.Background(), io.Discard, config.ModelsConfig{}, "claude-opus-4-8"); err != nil {
		t.Fatal(err)
	}
}
