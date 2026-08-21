package core

import (
	"context"

	"github.com/jandro-es/axon/internal/config"
)

// DriftSeams carries the probes TierDriftKey needs. Both are injectable so
// tests never reach the network or the OS; nil fields fall back to the real
// implementations.
type DriftSeams struct {
	OllamaDigestFn func(ctx context.Context, host, model string) (string, bool)
	OSVersionFn    func(ctx context.Context) (string, bool)
}

func (s DriftSeams) ollamaDigest(ctx context.Context, host, model string) (string, bool) {
	if s.OllamaDigestFn != nil {
		return s.OllamaDigestFn(ctx, host, model)
	}
	return OllamaDigest(ctx, host, model)
}

func (s DriftSeams) osVersion(ctx context.Context) (string, bool) {
	if s.OSVersionFn != nil {
		return s.OSVersionFn(ctx)
	}
	return MacOSProductVersion(ctx)
}

// TierDriftKey returns the value that identifies which model a gated local
// tier is actually running, so a later check can tell whether the model has
// changed underneath a passing eval.
//
// One helper, three callers — the eval-drift automation, the doctor vetting
// check, and `axon eval` when it records a run. They MUST agree: if the key
// stored at eval time is computed differently from the key compared later,
// every tier reports drift forever, or none ever does.
//
//   - ollama    → the pulled model's content digest.
//   - apple-fm  → "macos:<version>". Apple ships the on-device model with the
//     OS, so the OS version IS the model version and a minor update swaps the
//     model underneath the tier (FR-194).
//   - anything else (including claude, which is never gated) → ok=false, no
//     claim made. A false ok is never a silent "no drift": callers must treat
//     it as "unknown", not as a match.
func TierDriftKey(ctx context.Context, ref config.ModelRef, ollamaHost string, seams DriftSeams) (string, bool) {
	switch ref.Provider {
	case config.ProviderOllama:
		return seams.ollamaDigest(ctx, ollamaHost, ref.Model)
	case config.ProviderAppleFM, config.ProviderApple:
		v, ok := seams.osVersion(ctx)
		if !ok {
			return "", false
		}
		return "macos:" + v, true
	default:
		return "", false
	}
}
