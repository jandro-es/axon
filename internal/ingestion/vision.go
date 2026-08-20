package ingestion

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	"github.com/jandro-es/axon/internal/config"
)

// Vision produces a plain-language description (including transcribed text) of
// an image. Implementations are strictly local (ADR-035); output is content,
// never instructions (NFR-05). A nil Vision on the Pipeline means the feature
// is off. Vision is a perception primitive: budget-exempt, NOT routed through
// the token-manager chokepoint (an ADR-015 amendment, like OCR and rerank).
type Vision interface {
	Describe(ctx context.Context, img []byte, mime string) (string, error)
	Name() string
}

// fmLookPath locates the macOS 27 fm CLI; a package seam for tests.
var fmLookPath = exec.LookPath

// VisionFor builds the configured vision provider, or nil when vision is off.
// "apple" (on-device) and "apple:pcc" (FR-197, gated by models.pcc_enabled at
// validation) resolve to the fm-backed provider on macOS 27; anywhere the fm
// CLI is missing they return an actionable error so wiring falls back to
// OCR-only and doctor can report it. goos is runtime.GOOS.
func VisionFor(cfg config.IngestionConfig, goos string) (Vision, error) {
	mode := cfg.VisionMode()
	switch {
	case mode == "off":
		return nil, nil
	case mode == "apple" || mode == "apple:pcc":
		if goos != "darwin" {
			return nil, fmt.Errorf("ingestion.vision: %q requires macOS 27 (running on %s) — use ollama:<model> or off", mode, goos)
		}
		bin, err := fmLookPath("fm")
		if err != nil {
			return nil, fmt.Errorf("ingestion.vision: %q requires macOS 27's fm CLI (not found on PATH) — use ollama:<model> or off", mode)
		}
		variant := "system"
		if mode == "apple:pcc" {
			variant = "pcc"
		}
		return NewAppleVision(bin, variant), nil
	case strings.HasPrefix(mode, "apple:"):
		return nil, fmt.Errorf("ingestion.vision: unknown apple variant %q — use apple (on-device) or apple:pcc", mode)
	case strings.HasPrefix(mode, "ollama:"):
		model := strings.TrimPrefix(mode, "ollama:")
		if model == "" {
			return nil, fmt.Errorf("ingestion.vision: ollama provider needs a model name (ollama:<model>)")
		}
		return NewOllamaVision("", model), nil
	default:
		return nil, fmt.Errorf("ingestion.vision: unknown provider %q — use off, ollama:<model>, or apple", mode)
	}
}
