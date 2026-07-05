package ingestion

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// TesseractOCR performs cross-platform OCR by rasterising a PDF with pdftoppm
// (poppler) and recognising each page image with the tesseract binary. Both are
// local system tools (ADR-026); a missing one yields an actionable error.
type TesseractOCR struct {
	tmpRoot   string
	lookup    func(string) (string, error)
	rasterize func(ctx context.Context, pdfPath, outDir string) ([]string, error)
	ocrImage  func(ctx context.Context, imgPath string) (string, error)
}

// NewTesseractOCR wires the real pdftoppm/tesseract executors.
func NewTesseractOCR() *TesseractOCR {
	return &TesseractOCR{
		lookup:    exec.LookPath,
		rasterize: rasterizePDF,
		ocrImage:  tesseractImage,
	}
}

func (t *TesseractOCR) Name() string { return "tesseract" }

// Recognize rasterises the PDF and OCRs each page, in order.
func (t *TesseractOCR) Recognize(ctx context.Context, pdf []byte) (string, error) {
	for _, bin := range []string{"pdftoppm", "tesseract"} {
		if _, err := t.lookup(bin); err != nil {
			return "", fmt.Errorf("tesseract OCR needs %q on PATH (install poppler + tesseract): %w", bin, err)
		}
	}
	dir, err := os.MkdirTemp(t.tmpRoot, "axon-ocr-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(dir)
	pdfPath := filepath.Join(dir, "in.pdf")
	if err := os.WriteFile(pdfPath, pdf, 0o600); err != nil {
		return "", err
	}
	imgs, err := t.rasterize(ctx, pdfPath, dir)
	if err != nil {
		return "", fmt.Errorf("tesseract OCR: rasterise: %w", err)
	}
	var pages []string
	for _, img := range imgs {
		txt, err := t.ocrImage(ctx, img)
		if err != nil {
			return "", fmt.Errorf("tesseract OCR: recognise %s: %w", filepath.Base(img), err)
		}
		pages = append(pages, strings.TrimSpace(txt))
	}
	return strings.Join(pages, "\n\n"), nil
}

// rasterizePDF renders each page to a PNG at ~200 dpi and returns the image
// paths in page order.
func rasterizePDF(ctx context.Context, pdfPath, outDir string) ([]string, error) {
	prefix := filepath.Join(outDir, "page")
	cmd := exec.CommandContext(ctx, "pdftoppm", "-png", "-r", "200", pdfPath, prefix)
	cmd.WaitDelay = 5 * time.Second
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("pdftoppm: %w: %s", err, bytes.TrimSpace(out))
	}
	imgs, err := filepath.Glob(prefix + "-*.png")
	if err != nil {
		return nil, err
	}
	sort.Strings(imgs) // pdftoppm zero-pads suffixes, so lexical == page order
	return imgs, nil
}

// tesseractImage OCRs a single image to stdout text.
func tesseractImage(ctx context.Context, imgPath string) (string, error) {
	cmd := exec.CommandContext(ctx, "tesseract", imgPath, "stdout")
	cmd.WaitDelay = 5 * time.Second
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%w: %s", err, bytes.TrimSpace(stderr.Bytes()))
	}
	return stdout.String(), nil
}

var _ OCR = (*TesseractOCR)(nil)
