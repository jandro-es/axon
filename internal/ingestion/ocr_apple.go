package ingestion

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// AppleOCR performs on-device OCR by invoking a compiled Swift helper (Apple
// Vision + PDFKit). JSON over stdout keeps the Go host pure Go — no cgo
// (ADR-013/ADR-026). Safe for concurrent use: each call spawns its own process
// and temp file.
type AppleOCR struct {
	helper  string
	timeout time.Duration
	goos    string
	run     func(ctx context.Context, bin string, args []string) (stdout, stderr []byte, err error)
}

// NewAppleOCR constructs the provider from the compiled helper path.
func NewAppleOCR(helperPath string) *AppleOCR {
	return &AppleOCR{helper: helperPath, timeout: 180 * time.Second, goos: runtime.GOOS, run: execOCRHelper}
}

func (a *AppleOCR) Name() string { return "apple" }

// Recognize writes the PDF to a temp file and runs the helper over it.
func (a *AppleOCR) Recognize(ctx context.Context, pdf []byte) (string, error) {
	if a.goos != "darwin" {
		return "", fmt.Errorf("apple OCR requires macOS (running on %s) — set ingestion.ocr: tesseract or off", a.goos)
	}
	f, err := os.CreateTemp("", "axon-ocr-*.pdf")
	if err != nil {
		return "", err
	}
	defer func() { _ = os.Remove(f.Name()) }()
	if _, err := f.Write(pdf); err != nil {
		f.Close()
		return "", err
	}
	f.Close()

	ctx, cancel := context.WithTimeout(ctx, a.timeout)
	defer cancel()
	stdout, stderr, err := a.run(ctx, a.helper, []string{f.Name()})
	if err != nil {
		return "", fmt.Errorf("apple OCR helper %s: %w: %s", a.helper, err, ocrSubprocessTail(stdout, stderr))
	}
	var out struct {
		Pages []string `json:"pages"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(stdout), &out); err != nil {
		return "", fmt.Errorf("apple OCR: decode helper response: %w", err)
	}
	return strings.Join(out.Pages, "\n\n"), nil
}

// RecognizeImage writes the image to a temp file and runs the helper in image
// mode (VNRecognizeTextRequest directly on the CGImage, skipping PDFKit).
func (a *AppleOCR) RecognizeImage(ctx context.Context, img []byte, mime string) (string, error) {
	if a.goos != "darwin" {
		return "", fmt.Errorf("apple OCR requires macOS (running on %s) — set ingestion.ocr: tesseract or off", a.goos)
	}
	f, err := os.CreateTemp("", "axon-ocr-img-*"+extFromMime(mime))
	if err != nil {
		return "", err
	}
	defer func() { _ = os.Remove(f.Name()) }()
	if _, err := f.Write(img); err != nil {
		f.Close()
		return "", err
	}
	f.Close()

	ctx, cancel := context.WithTimeout(ctx, a.timeout)
	defer cancel()
	stdout, stderr, err := a.run(ctx, a.helper, []string{"--image", f.Name()})
	if err != nil {
		return "", fmt.Errorf("apple OCR helper %s: %w: %s", a.helper, err, ocrSubprocessTail(stdout, stderr))
	}
	var out struct {
		Pages []string `json:"pages"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(stdout), &out); err != nil {
		return "", fmt.Errorf("apple OCR: decode helper response: %w", err)
	}
	return strings.Join(out.Pages, "\n\n"), nil
}

// ocrSubprocessTail assembles capped helper output for a failure message.
func ocrSubprocessTail(stdout, stderr []byte) string {
	const capPerStream = 1024
	trunc := func(b []byte) string {
		s := strings.TrimSpace(string(b))
		if len(s) > capPerStream {
			s = s[:capPerStream] + "… (truncated)"
		}
		return s
	}
	parts := make([]string, 0, 2)
	if s := trunc(stderr); s != "" {
		parts = append(parts, s)
	}
	if s := trunc(stdout); s != "" {
		parts = append(parts, "stdout: "+s)
	}
	return strings.Join(parts, "; ")
}

// execOCRHelper is the real subprocess executor (mirrors embeddings.execAppleHelper).
func execOCRHelper(ctx context.Context, bin string, args []string) ([]byte, []byte, error) {
	cmd := exec.CommandContext(ctx, bin, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.WaitDelay = 5 * time.Second
	err := cmd.Run()
	return stdout.Bytes(), stderr.Bytes(), err
}

var _ OCR = (*AppleOCR)(nil)
