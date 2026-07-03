package main

import (
	"context"
	"fmt"
	"io"
	"runtime"

	"github.com/jandro-es/axon/internal/agent"
	"github.com/jandro-es/axon/internal/config"
	"github.com/jandro-es/axon/internal/tui"
)

// convergeModelTier verifies the just-configured tier can actually serve:
// ollama → one-token chat round trip; apple → compile the helper (if needed)
// and probe on-device availability; claude → nothing to converge (the plan
// tier governs availability, docs/07). Mirrors switchEmbeddings' convergence.
func convergeModelTier(ctx context.Context, out io.Writer, m config.ModelsConfig, tierValue string) error {
	ref := config.ParseModelRef(tierValue)
	switch ref.Provider {
	case config.ProviderOllama:
		return tui.Spin(out, "probing ollama model "+ref.Model+"…", func() (string, error) {
			if err := agent.NewOllama(m.OllamaHost).Healthcheck(ctx, ref.Model); err != nil {
				return "", fmt.Errorf("%w — is Ollama running and the model pulled? (ollama pull %s)", err, ref.Model)
			}
			return "ollama model " + ref.Model + " responds", nil
		})
	case config.ProviderApple:
		if runtime.GOOS != "darwin" {
			fmt.Fprintln(out, "warning: apple tier configured on a non-mac — calls on this machine will use models.local_fallback")
			return nil
		}
		if !agent.SwiftAvailable() {
			return fmt.Errorf("swiftc not found — install Xcode Command Line Tools (xcode-select --install)")
		}
		helper := m.AppleHelper
		if helper == "" {
			helper = config.DefaultAppleLMHelperPath()
		}
		return tui.Spin(out, "compiling + probing the Apple Foundation Models helper…", func() (string, error) {
			if _, err := agent.EnsureAppleLMHelper(ctx, helper); err != nil {
				return "", err
			}
			if err := agent.NewAppleFM(helper).CheckAvailability(ctx); err != nil {
				return "", err
			}
			return "on-device model available", nil
		})
	default:
		return nil
	}
}
