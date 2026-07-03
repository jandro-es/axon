package core

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jandro-es/axon/internal/config"
)

func localProfile(models config.ModelsConfig) config.Profile {
	return config.Profile{Models: models}
}

func findLocalCheck(t *testing.T, checks []Check, name string) Check {
	t.Helper()
	for _, c := range checks {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("check %q not found in %+v", name, checks)
	return Check{}
}

func TestLocalModelsCheckOllama(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// /api/tags shape: the pulled-model library.
		_ = json.NewEncoder(w).Encode(map[string]any{
			"models": []map[string]any{{"name": "qwen3:8b"}},
		})
	}))
	defer srv.Close()

	p := localProfile(config.ModelsConfig{
		Classify: "ollama:qwen3:8b", Routine: "claude-sonnet-4-6", Synthesis: "claude-opus-4-8",
		OllamaHost: srv.URL,
	})
	c := findLocalCheck(t, localModelsCheck(p), "local-model:classify")
	if c.Status != StatusOK {
		t.Fatalf("check = %+v, want ok", c)
	}

	// Model not pulled → warn with remediation.
	p.Models.Classify = "ollama:missing:latest"
	c = findLocalCheck(t, localModelsCheck(p), "local-model:classify")
	if c.Status != StatusWarn || !strings.Contains(c.Detail, "ollama pull") {
		t.Fatalf("check = %+v, want warn with pull hint", c)
	}

	// Server down → warn naming the fallback.
	srv.Close()
	c = findLocalCheck(t, localModelsCheck(p), "local-model:classify")
	if c.Status != StatusWarn || !strings.Contains(c.Detail, "local_fallback") {
		t.Fatalf("check = %+v, want warn naming local_fallback", c)
	}
}

func TestLocalModelsCheckAppleHelperMissing(t *testing.T) {
	p := localProfile(config.ModelsConfig{
		Classify: "apple", Routine: "claude-sonnet-4-6", Synthesis: "claude-opus-4-8",
		AppleHelper: filepath.Join(t.TempDir(), "nonexistent-helper"),
	})
	checks := localModelsCheck(p)
	c := findLocalCheck(t, checks, "local-model:classify")
	// darwin: helper missing → warn; other OS: not-a-mac warn. Both warn.
	if c.Status != StatusWarn {
		t.Fatalf("check = %+v, want warn", c)
	}
}

func TestLocalModelsCheckAllClaudeIsSilent(t *testing.T) {
	p := localProfile(config.ModelsConfig{
		Classify: "claude-haiku-4-5", Routine: "claude-sonnet-4-6", Synthesis: "claude-opus-4-8",
	})
	if checks := localModelsCheck(p); len(checks) != 0 {
		t.Fatalf("all-claude profile should add no local-model checks, got %+v", checks)
	}
}

func TestAppleLMStepUsesInjectedConverge(t *testing.T) {
	ctx := context.Background()
	helper := filepath.Join(t.TempDir(), "axon-apple-lm")
	opts := InitOptions{
		Profile: localProfile(config.ModelsConfig{
			Classify: "apple", Routine: "claude-sonnet-4-6", Synthesis: "claude-opus-4-8",
			AppleHelper: helper,
		}),
	}

	opts.ConvergeAppleLM = func(ctx context.Context, got string) error {
		if got != helper {
			t.Errorf("helper path = %q, want %q", got, helper)
		}
		return nil
	}
	if res := appleLMStep(ctx, opts); res.Status != StepDone {
		t.Fatalf("converged step = %+v, want done", res)
	}

	opts.ConvergeAppleLM = func(context.Context, string) error {
		return errors.New("swiftc not found")
	}
	res := appleLMStep(ctx, opts)
	if res.Status != StepWarn || !strings.Contains(res.Detail, "local_fallback") {
		t.Fatalf("failed converge = %+v, want warn naming local_fallback", res)
	}

	// No converge injected → stat-only, helper absent → warn (never blocks).
	opts.ConvergeAppleLM = nil
	if res := appleLMStep(ctx, opts); res.Status != StepWarn {
		t.Fatalf("stat-only step = %+v, want warn", res)
	}
}
