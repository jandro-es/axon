package ingestion

import (
	"context"
	"fmt"

	"github.com/jandro-es/axon/internal/config"
)

// OCR recovers text from a PDF whose text layer is empty (scanned pages).
// Implementations are strictly local (ADR-026); recovered text is content,
// never instructions (NFR-05). A nil OCR on the Pipeline means the feature
// is off.
type OCR interface {
	// Recognize returns the recovered text (page order preserved) for a PDF's
	// raw bytes, or an error.
	Recognize(ctx context.Context, pdf []byte) (string, error)
	// Name identifies the provider for diagnostics/errors.
	Name() string
}

// ocrFallback replaces ex.Markdown with OCR-recovered text when the text-layer
// extraction came back below the min-content threshold and an OCR provider is
// configured. Born-digital PDFs (sufficient text) and the nil-provider case are
// no-ops. A provider error is returned so the ingest is recorded as failed.
func ocrFallback(ctx context.Context, ex Extracted, pdf []byte, o OCR) (Extracted, error) {
	if len(ex.Markdown) >= minExtractedChars || o == nil {
		return ex, nil
	}
	text, err := o.Recognize(ctx, pdf)
	if err != nil {
		return Extracted{}, fmt.Errorf("ocr (%s): %w", o.Name(), err)
	}
	if text = normalizeMarkdown(text); len(text) >= minExtractedChars {
		ex.Markdown = text
	}
	return ex, nil
}

// OCRFor builds the configured OCR provider, or nil when OCR is off. apple is
// macOS-only; goos is runtime.GOOS (injectable in tests).
func OCRFor(cfg config.IngestionConfig, goos string) (OCR, error) {
	switch cfg.OCRMode() {
	case "off":
		return nil, nil
	case "apple":
		if goos != "darwin" {
			return nil, fmt.Errorf("ingestion.ocr: apple requires macOS (running on %s) — use tesseract or off", goos)
		}
		helper := cfg.OCRHelper
		if helper == "" {
			helper = config.DefaultOCRHelperPath()
		}
		return NewAppleOCR(helper), nil
	case "tesseract":
		return NewTesseractOCR(), nil
	default:
		return nil, fmt.Errorf("ingestion.ocr: unknown provider %q", cfg.OCRMode())
	}
}
