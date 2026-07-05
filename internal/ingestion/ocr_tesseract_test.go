package ingestion

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestTesseractJoinsPages(t *testing.T) {
	tt := NewTesseractOCR()
	tt.lookup = func(string) (string, error) { return "/usr/bin/x", nil }
	tt.rasterize = func(ctx context.Context, pdfPath, outDir string) ([]string, error) {
		return []string{filepath.Join(outDir, "page-1.png"), filepath.Join(outDir, "page-2.png")}, nil
	}
	tt.ocrImage = func(ctx context.Context, img string) (string, error) {
		return "text of " + filepath.Base(img), nil
	}
	got, err := tt.Recognize(context.Background(), []byte("%PDF"))
	if err != nil {
		t.Fatal(err)
	}
	if got != "text of page-1.png\n\ntext of page-2.png" {
		t.Fatalf("joined = %q", got)
	}
}

func TestTesseractMissingBinary(t *testing.T) {
	tt := NewTesseractOCR()
	tt.lookup = func(name string) (string, error) {
		if name == "tesseract" {
			return "", context.DeadlineExceeded // any non-nil error
		}
		return "/usr/bin/pdftoppm", nil
	}
	_, err := tt.Recognize(context.Background(), []byte("%PDF"))
	if err == nil || !strings.Contains(err.Error(), "tesseract") {
		t.Fatalf("missing-binary err = %v", err)
	}
}
