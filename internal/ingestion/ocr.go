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
	// RecognizeImage returns the recovered text for a single raster image's raw
	// bytes. mime is the source content type (e.g. "image/png").
	RecognizeImage(ctx context.Context, img []byte, mime string) (string, error)
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

// mimeForImage maps a local image path's extension to a MIME type.
func mimeForImage(path string) string {
	switch filepathExt(path) {
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".heic":
		return "image/heic"
	case ".heif":
		return "image/heif"
	case ".tif", ".tiff":
		return "image/tiff"
	case ".bmp":
		return "image/bmp"
	default:
		return "application/octet-stream"
	}
}

// extFromMime maps an image MIME type back to a file extension (for temp files
// handed to OCR binaries). Unknown types default to .png.
func extFromMime(mime string) string {
	switch mime {
	case "image/jpeg":
		return ".jpg"
	case "image/gif":
		return ".gif"
	case "image/webp":
		return ".webp"
	case "image/heic", "image/heif":
		return ".heic"
	case "image/tiff":
		return ".tiff"
	case "image/bmp":
		return ".bmp"
	default:
		return ".png"
	}
}
