package config

import (
	"fmt"
	"strings"
)

// Model providers (ADR-015). Claude is the default; local providers are
// selected by prefixing the tier's model string.
const (
	ProviderClaude = "claude"
	ProviderOllama = "ollama"
	ProviderApple  = "apple"
)

// ModelRef is a parsed models.* tier value: which adapter serves it and the
// concrete model string that adapter receives.
type ModelRef struct {
	Provider string
	Model    string
}

// ParseModelRef resolves a tier string to its provider: "ollama:<model>" →
// Ollama, "apple" → the on-device Foundation Models system model, anything
// else → a Claude model string exactly as before (backward compatible).
func ParseModelRef(s string) ModelRef {
	if s == ProviderApple {
		return ModelRef{Provider: ProviderApple, Model: AppleFoundationModel}
	}
	if rest, ok := strings.CutPrefix(s, ProviderOllama+":"); ok {
		return ModelRef{Provider: ProviderOllama, Model: rest}
	}
	return ModelRef{Provider: ProviderClaude, Model: s}
}

// Fallback returns the local-failure policy, defaulting to "claude"
// (fall forward through the normal budget path — FR-79).
func (m ModelsConfig) Fallback() string {
	if m.LocalFallback == "" {
		return "claude"
	}
	return m.LocalFallback
}

// validateLocalRouting applies the ADR-015 cross-field rules that struct tags
// can't express. Empty tier strings are skipped (profiles are partial
// overrides); struct-tag `required` covers the top-level config.
func validateLocalRouting(m ModelsConfig) error {
	if m.Synthesis != "" && ParseModelRef(m.Synthesis).Provider != ProviderClaude {
		return fmt.Errorf("models.synthesis must be a Claude model (got %q): local providers are classify/routine only", m.Synthesis)
	}
	if m.Routine != "" && ParseModelRef(m.Routine).Provider == ProviderApple {
		return fmt.Errorf("models.routine cannot be %q: the Apple on-device model's context window limits it to the classify tier", m.Routine)
	}
	for _, tier := range []string{m.Classify, m.Routine} {
		if ref := ParseModelRef(tier); ref.Provider == ProviderOllama && ref.Model == "" {
			return fmt.Errorf("models tier %q names ollama with no model (use ollama:<model>, e.g. ollama:qwen3:8b)", tier)
		}
	}
	if m.LocalFallback != "" && m.LocalFallback != "claude" && m.LocalFallback != "fail" {
		return fmt.Errorf("models.local_fallback must be claude or fail (got %q)", m.LocalFallback)
	}
	return nil
}
