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
	// ProviderAppleFM serves the macOS 27 `fm serve` backend (ADR-038):
	// "apple:system" (on-device) and "apple:pcc" (Private Cloud Compute).
	ProviderAppleFM = "apple-fm"
)

// FM-backed apple variants (the ModelRef.Model under ProviderAppleFM).
const (
	AppleFMSystem = "system"
	AppleFMPCC    = "pcc"
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
	if rest, ok := strings.CutPrefix(s, ProviderApple+":"); ok {
		if rest == AppleFMSystem || rest == AppleFMPCC {
			return ModelRef{Provider: ProviderAppleFM, Model: rest}
		}
		// Unknown variants keep parsing as Claude strings so validation —
		// not routing — is what rejects them with an actionable message.
	}
	if rest, ok := strings.CutPrefix(s, ProviderOllama+":"); ok {
		return ModelRef{Provider: ProviderOllama, Model: rest}
	}
	return ModelRef{Provider: ProviderClaude, Model: s}
}

// reservedAppleRef reports whether a tier string uses the permanently reserved
// `apple-fm` forms (FR-192, narrowed by FR-194: the live `apple:` variants
// resolve via ParseModelRef instead). Without this gate such strings parse as
// Claude model strings and silently misroute to `claude -p`.
func reservedAppleRef(s string) bool {
	return s == ProviderAppleFM || strings.HasPrefix(s, ProviderAppleFM+":")
}

// unknownAppleVariant reports an `apple:<x>` form that is neither of the
// fm-backed variants — rejected at validation, never routed (FR-194).
func unknownAppleVariant(s string) bool {
	rest, ok := strings.CutPrefix(s, ProviderApple+":")
	return ok && rest != AppleFMSystem && rest != AppleFMPCC
}

// validateVision applies the FR-197 cross-field rule: the PCC vision mode
// needs the same explicit opt-in as the apple:pcc tier (PCC receives
// unredacted image bytes — selecting it must always be deliberate).
func validateVision(p Profile) error {
	mode := p.Ingestion.VisionMode()
	if !strings.HasPrefix(mode, ProviderApple+":") {
		return nil
	}
	if mode != ProviderApple+":"+AppleFMPCC {
		return fmt.Errorf("ingestion.vision %q names an unknown apple variant — use apple (on-device) or apple:pcc", mode)
	}
	if !p.Models.PCCEnabled {
		return fmt.Errorf("ingestion.vision %q requires models.pcc_enabled: true — Private Cloud Compute vision sends unredacted image bytes off-device and is opt-in (FR-197)", mode)
	}
	return nil
}

// Fallback returns the local-failure policy, defaulting to "claude"
// (fall forward through the normal budget path — FR-79).
func (m ModelsConfig) Fallback() string {
	if m.LocalFallback == "" {
		return "claude"
	}
	return m.LocalFallback
}

// VerifyMode returns the configured verifier ref, or "off" when unset/"off".
func (m ModelsConfig) VerifyMode() string {
	if m.Verify == "" {
		return "off"
	}
	return m.Verify
}

// VerifyMinScoreOr returns the escalation floor, defaulting to 6.
func (m ModelsConfig) VerifyMinScoreOr() int {
	if m.VerifyMinScore <= 0 {
		return 6
	}
	return m.VerifyMinScore
}

// validateLocalRouting applies the ADR-015 cross-field rules that struct tags
// can't express. Empty tier strings are skipped (profiles are partial
// overrides); struct-tag `required` covers the top-level config.
func validateLocalRouting(m ModelsConfig) error {
	for _, t := range []struct{ key, val string }{
		{"classify", m.Classify}, {"routine", m.Routine}, {"synthesis", m.Synthesis},
	} {
		if reservedAppleRef(t.val) {
			return fmt.Errorf("models.%s %q is reserved and not supported — use %q (on-device), apple:system, apple:pcc, ollama:<model>, or a Claude model", t.key, t.val, ProviderApple)
		}
		if unknownAppleVariant(t.val) {
			return fmt.Errorf("models.%s %q names an unknown apple variant — the fm-backed variants are apple:%s (on-device) and apple:%s (Private Cloud Compute, requires models.pcc_enabled)", t.key, t.val, AppleFMSystem, AppleFMPCC)
		}
		if ref := ParseModelRef(t.val); ref.Provider == ProviderAppleFM && ref.Model == AppleFMPCC && !m.PCCEnabled {
			return fmt.Errorf("models.%s %q requires models.pcc_enabled: true — Private Cloud Compute is opt-in (Apple-operated compute; see docs/21 M2 and FR-195)", t.key, t.val)
		}
	}
	if m.Synthesis != "" && ParseModelRef(m.Synthesis).Provider != ProviderClaude {
		return fmt.Errorf("models.synthesis must be a Claude model (got %q): local providers are classify/routine only", m.Synthesis)
	}
	if m.Routine != "" {
		if ref := ParseModelRef(m.Routine); ref.Provider == ProviderApple || (ref.Provider == ProviderAppleFM && ref.Model == AppleFMSystem) {
			return fmt.Errorf("models.routine cannot be %q: the Apple on-device model's context window limits it to the classify tier (apple:pcc is the routine-capable rung)", m.Routine)
		}
	}
	for _, tier := range []string{m.Classify, m.Routine} {
		if ref := ParseModelRef(tier); ref.Provider == ProviderOllama && ref.Model == "" {
			return fmt.Errorf("models tier %q names ollama with no model (use ollama:<model>, e.g. ollama:qwen3:8b)", tier)
		}
	}
	if m.LocalFallback != "" && m.LocalFallback != "claude" && m.LocalFallback != "fail" {
		return fmt.Errorf("models.local_fallback must be claude or fail (got %q)", m.LocalFallback)
	}
	if m.EvalMinPass < 0 || m.EvalMinPass > 100 {
		return fmt.Errorf("models.eval_min_pass must be 0..100 (got %d)", m.EvalMinPass)
	}
	if v := m.Verify; v != "" && v != "off" {
		if ref := ParseModelRef(v); ref.Provider != ProviderOllama || ref.Model == "" {
			return fmt.Errorf("models.verify must be off or a local ollama:<model> (got %q): the verifier is a cheap local judge, never Claude or apple", v)
		}
	}
	if m.VerifyMinScore < 0 || m.VerifyMinScore > 10 {
		return fmt.Errorf("models.verify_min_score must be 0..10 (got %d)", m.VerifyMinScore)
	}
	return nil
}
