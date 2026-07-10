package ingestion

import (
	"context"
	"fmt"
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

// VisionFor builds the configured vision provider, or nil when vision is off.
// "apple" is accepted by the seam but not yet available; it returns an
// actionable error so wiring falls back to OCR-only and doctor can report it.
// goos is runtime.GOOS (kept for the future Apple tier; unused today).
func VisionFor(cfg config.IngestionConfig, goos string) (Vision, error) {
	mode := cfg.VisionMode()
	switch {
	case mode == "off":
		return nil, nil
	case mode == "apple":
		return nil, fmt.Errorf(`ingestion.vision: "apple" requires macOS 27 on-device image input (not yet available) — use ollama:<model> or off`)
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
