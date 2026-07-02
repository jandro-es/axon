package main

import (
	"strings"
	"testing"

	"github.com/jandro-es/axon/internal/config"
)

func TestConfigureModelsSubcommand(t *testing.T) {
	dir := t.TempDir()
	cfgPath := writeTempConfig(t, dir)
	if out, err := run(t, "configure", "models", "synthesis", "claude-opus-4-8", "--config", cfgPath); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	_, p, _ := cfg.ResolveProfile("")
	if p.Models.Synthesis != "claude-opus-4-8" {
		t.Errorf("synthesis = %q", p.Models.Synthesis)
	}
}

func TestConfigureEmbeddingsSwitchPersistsAndReportsPendingReindex(t *testing.T) {
	dir := t.TempDir()
	cfgPath := writeTempConfig(t, dir)
	out, err := run(t, "configure", "embeddings", "apple", "--config", cfgPath) // no --reindex
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	cfg, _ := config.Load(cfgPath)
	_, p, _ := cfg.ResolveProfile("")
	if p.Embeddings.Provider != "apple" || p.Embeddings.Dim != config.AppleEmbeddingDim || p.Embeddings.Model != config.AppleEmbeddingModel {
		t.Errorf("not persisted: %+v", p.Embeddings)
	}
	if !strings.Contains(out, "reindex") {
		t.Errorf("must announce the pending re-embed:\n%s", out)
	}
}

func TestConfigureEmbeddingsToOllamaRequiresModelAndDim(t *testing.T) {
	dir := t.TempDir()
	cfgPath := writeTempConfig(t, dir)
	if _, err := run(t, "configure", "embeddings", "apple", "--config", cfgPath); err != nil {
		t.Fatal(err)
	}
	if _, err := run(t, "configure", "embeddings", "ollama", "--config", cfgPath); err == nil {
		t.Error("switching to ollama non-interactively must require --model and --dim")
	}
	if out, err := run(t, "configure", "embeddings", "ollama", "--model", "nomic-embed-text", "--dim", "768", "--config", cfgPath); err != nil {
		t.Errorf("%v\n%s", err, out)
	}
	cfg, _ := config.Load(cfgPath)
	_, p, _ := cfg.ResolveProfile("")
	if p.Embeddings.Provider != "ollama" || p.Embeddings.Dim != 768 {
		t.Errorf("switch back not persisted: %+v", p.Embeddings)
	}
}

func TestConfigureEmbeddingsRejectsUnknownProvider(t *testing.T) {
	dir := t.TempDir()
	cfgPath := writeTempConfig(t, dir)
	if _, err := run(t, "configure", "embeddings", "banana", "--config", cfgPath); err == nil {
		t.Error("unknown provider must be rejected")
	}
}

func TestConfigureAutomationsToggle(t *testing.T) {
	dir := t.TempDir()
	cfgPath := writeTempConfig(t, dir)
	if out, err := run(t, "configure", "automations", "heartbeat", "off", "--config", cfgPath); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	cfg, _ := config.Load(cfgPath)
	_, p, _ := cfg.ResolveProfile("")
	if a, ok := p.Automations["heartbeat"]; !ok || a.Enabled {
		t.Errorf("heartbeat not disabled: %+v", p.Automations["heartbeat"])
	}
}

func TestConfigureLimits(t *testing.T) {
	dir := t.TempDir()
	cfgPath := writeTempConfig(t, dir)
	if _, err := run(t, "configure", "limits", "daily", "2000000", "--config", cfgPath); err != nil {
		t.Fatal(err)
	}
	cfg, _ := config.Load(cfgPath)
	_, p, _ := cfg.ResolveProfile("")
	if p.Limits.DailyTokens.Int() != 2_000_000 {
		t.Errorf("daily = %d", p.Limits.DailyTokens.Int())
	}
}

func TestConfigureMenuNeverHangsHeadless(t *testing.T) {
	dir := t.TempDir()
	cfgPath := writeTempConfig(t, dir)
	// Bare `axon configure` over a buffer (non-TTY) must return immediately
	// with guidance, not block on a menu.
	out, err := run(t, "configure", "--config", cfgPath)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, "configure") {
		t.Errorf("headless configure should print usage/guidance:\n%s", out)
	}
}
