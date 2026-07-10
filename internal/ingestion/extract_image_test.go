package ingestion

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type fakeVision struct {
	text   string
	err    error
	called int
}

func (f *fakeVision) Name() string { return "fake-vision" }
func (f *fakeVision) Describe(ctx context.Context, img []byte, mime string) (string, error) {
	f.called++
	return f.text, f.err
}

func TestExtractImageOCRRichSkipsVision(t *testing.T) {
	ocr := &fakeOCR{text: strings.Repeat("recovered screen text ", 20)}
	vis := &fakeVision{text: "should not be used"}
	ex, err := extractImage(context.Background(), []byte{1}, "image/png", ocr, vis)
	if err != nil {
		t.Fatal(err)
	}
	if vis.called != 0 {
		t.Fatalf("vision should be skipped when OCR is rich, called=%d", vis.called)
	}
	if !strings.Contains(ex.Markdown, "recovered screen text") {
		t.Fatalf("expected OCR text, got %q", ex.Markdown)
	}
}

func TestExtractImageSparseOCRUsesVision(t *testing.T) {
	ocr := &fakeOCR{text: "hi"} // below minExtractedChars
	vis := &fakeVision{text: strings.Repeat("a detailed visual description ", 10)}
	ex, err := extractImage(context.Background(), []byte{1}, "image/png", ocr, vis)
	if err != nil {
		t.Fatal(err)
	}
	if vis.called != 1 {
		t.Fatalf("vision should run when OCR is sparse, called=%d", vis.called)
	}
	if !strings.Contains(ex.Markdown, "detailed visual description") {
		t.Fatalf("expected vision text, got %q", ex.Markdown)
	}
}

func TestExtractImageBothEmptyNoError(t *testing.T) {
	ex, err := extractImage(context.Background(), []byte{1}, "image/png", nil, nil)
	if err != nil {
		t.Fatalf("both-absent must not error: %v", err)
	}
	if ex.Markdown != "" {
		t.Fatalf("expected empty markdown, got %q", ex.Markdown)
	}
}

func TestExtractImageVisionErrorWithOCRTextStands(t *testing.T) {
	ocr := &fakeOCR{text: "short"} // sparse → vision attempted
	vis := &fakeVision{err: errors.New("ollama down")}
	ex, err := extractImage(context.Background(), []byte{1}, "image/png", ocr, vis)
	if err != nil {
		t.Fatalf("vision error must be swallowed when OCR gave text: %v", err)
	}
	if ex.Markdown != "short" {
		t.Fatalf("expected OCR text to stand, got %q", ex.Markdown)
	}
}

func TestExtractImageVisionErrorNoOCRTextFails(t *testing.T) {
	vis := &fakeVision{err: errors.New("ollama down")}
	if _, err := extractImage(context.Background(), []byte{1}, "image/png", nil, vis); err == nil {
		t.Fatal("expected error when nothing was recovered and vision failed")
	}
}
