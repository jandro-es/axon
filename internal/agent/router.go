package agent

import (
	"fmt"

	"github.com/jandro-es/axon/internal/config"
)

// Router selects the adapter for a resolved model provider (ADR-015). A nil
// field means "not configured on this install"; Resolve surfaces that as an
// error so the token manager's fallback ladder can react. The zero value with
// only Claude set behaves exactly like the pre-ADR-015 single adapter.
type Router struct {
	Claude Agent
	Ollama Agent
	Apple  Agent
}

// Resolve returns the adapter for a provider, or an actionable error.
func (r Router) Resolve(provider string) (Agent, error) {
	switch provider {
	case "", config.ProviderClaude:
		if r.Claude == nil {
			return nil, fmt.Errorf("no claude adapter configured")
		}
		return r.Claude, nil
	case config.ProviderOllama:
		if r.Ollama == nil {
			return nil, fmt.Errorf("no ollama adapter configured (a models.* tier references ollama: but the router has no Ollama adapter)")
		}
		return r.Ollama, nil
	case config.ProviderApple:
		if r.Apple == nil {
			return nil, fmt.Errorf("no apple adapter configured (models.* references apple; darwin with the helper compiled is required)")
		}
		return r.Apple, nil
	default:
		return nil, fmt.Errorf("unknown model provider %q", provider)
	}
}
