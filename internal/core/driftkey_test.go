package core

import (
	"context"
	"testing"

	"github.com/jandro-es/axon/internal/config"
	"github.com/jandro-es/axon/internal/db"
)

func TestTierDriftKey(t *testing.T) {
	ctx := context.Background()
	seams := DriftSeams{
		OllamaDigestFn: func(context.Context, string, string) (string, bool) { return "sha256:abc", true },
		OSVersionFn:    func(context.Context) (string, bool) { return "27.1", true },
	}

	// Ollama: the content digest.
	got, ok := TierDriftKey(ctx, config.ModelRef{Provider: config.ProviderOllama, Model: "qwen2.5"}, "http://x", seams)
	if !ok || got != "sha256:abc" {
		t.Fatalf("ollama key = %q, %v; want sha256:abc, true", got, ok)
	}

	// fm-backed: the OS version IS the model version — Apple ships the model
	// with the OS, so a minor update swaps the model underneath the tier.
	got, ok = TierDriftKey(ctx, config.ModelRef{Provider: config.ProviderAppleFM, Model: "on-device"}, "", seams)
	if !ok || got != "macos:27.1" {
		t.Fatalf("apple-fm key = %q, %v; want macos:27.1, true", got, ok)
	}

	// An unknown provider makes no claim — never a silent "no drift".
	if _, ok := TierDriftKey(ctx, config.ModelRef{Provider: "mlx", Model: "x"}, "", seams); ok {
		t.Fatal("an unknown provider must report ok=false, not a bogus key")
	}

	// Claude is never gated, so it has no drift key.
	if _, ok := TierDriftKey(ctx, config.ModelRef{Provider: config.ProviderClaude, Model: "sonnet"}, "", seams); ok {
		t.Fatal("claude must have no drift key")
	}

	// A probe that cannot answer reports ok=false rather than an empty key
	// that would compare equal to a stored empty value.
	quiet := DriftSeams{
		OllamaDigestFn: func(context.Context, string, string) (string, bool) { return "", false },
		OSVersionFn:    func(context.Context) (string, bool) { return "", false },
	}
	if _, ok := TierDriftKey(ctx, config.ModelRef{Provider: config.ProviderAppleFM}, "", quiet); ok {
		t.Fatal("an unavailable OS probe must report ok=false")
	}
	if _, ok := TierDriftKey(ctx, config.ModelRef{Provider: config.ProviderOllama, Model: "m"}, "h", quiet); ok {
		t.Fatal("an unreachable Ollama must report ok=false")
	}
}

// The gap this slice closes: before TierDriftKey, the vetting check computed a
// current fingerprint only for Ollama, so an fm-backed tier that passed evals
// stayed "vetted" forever — even after an OS update swapped the on-device
// model underneath it. These assert the drift arm now fires for apple-fm.
func TestVettingCheckDetectsAppleFMDrift(t *testing.T) {
	passed := db.EvalRun{PassPct: 90, Digest: "macos:27.0"}

	drifted := vettingCheck("m", "classify", "apple-fm:on-device", 70, passed, true, "macos:27.1", true)
	if drifted.Status != StatusWarn {
		t.Fatalf("an OS update under a vetted fm tier must warn: %+v", drifted)
	}

	same := vettingCheck("m", "classify", "apple-fm:on-device", 70, passed, true, "macos:27.0", true)
	if same.Status != StatusOK {
		t.Fatalf("an unchanged OS must stay vetted: %+v", same)
	}

	// An unavailable probe (digestKnown=false) must not fabricate drift.
	unknown := vettingCheck("m", "classify", "apple-fm:on-device", 70, passed, true, "", false)
	if unknown.Status != StatusOK {
		t.Fatalf("an unknown current key must not report drift: %+v", unknown)
	}
}
