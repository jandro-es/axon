package ingestion

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type fakeOCR struct {
	text   string
	err    error
	called int
}

func (f *fakeOCR) Name() string { return "fake" }
func (f *fakeOCR) Recognize(ctx context.Context, pdf []byte) (string, error) {
	f.called++
	return f.text, f.err
}
func (f *fakeOCR) RecognizeImage(ctx context.Context, img []byte, mime string) (string, error) {
	f.called++
	return f.text, f.err
}

func TestOCRFallbackRecoversEmptyBody(t *testing.T) {
	f := &fakeOCR{text: strings.Repeat("recovered text ", 20)}
	ex, err := ocrFallback(context.Background(), Extracted{Title: "scan", Markdown: ""}, []byte("%PDF"), f)
	if err != nil {
		t.Fatal(err)
	}
	if f.called != 1 {
		t.Fatalf("OCR should run on an empty body, called=%d", f.called)
	}
	if !strings.Contains(ex.Markdown, "recovered text") || ex.Title != "scan" {
		t.Fatalf("body not replaced: %+v", ex)
	}
}

func TestOCRFallbackSkipsBornDigital(t *testing.T) {
	f := &fakeOCR{text: "should not be used"}
	long := strings.Repeat("real pdf text ", 20)
	ex, err := ocrFallback(context.Background(), Extracted{Markdown: long}, []byte("%PDF"), f)
	if err != nil {
		t.Fatal(err)
	}
	if f.called != 0 {
		t.Fatal("OCR must not run when the text layer is already sufficient")
	}
	if ex.Markdown != long {
		t.Fatal("born-digital body must be untouched")
	}
}

func TestOCRFallbackNilProviderIsNoop(t *testing.T) {
	ex, err := ocrFallback(context.Background(), Extracted{Markdown: ""}, []byte("%PDF"), nil)
	if err != nil || ex.Markdown != "" {
		t.Fatalf("nil OCR should be a no-op: %+v %v", ex, err)
	}
}

func TestOCRFallbackPropagatesError(t *testing.T) {
	f := &fakeOCR{err: errors.New("helper crashed")}
	if _, err := ocrFallback(context.Background(), Extracted{Markdown: ""}, []byte("%PDF"), f); err == nil {
		t.Fatal("OCR error must propagate")
	}
}

func TestOCRFallbackBelowThresholdLeavesEmpty(t *testing.T) {
	f := &fakeOCR{text: "tiny"} // < minExtractedChars
	ex, err := ocrFallback(context.Background(), Extracted{Markdown: ""}, []byte("%PDF"), f)
	if err != nil {
		t.Fatal(err)
	}
	if ex.Markdown != "" {
		t.Fatalf("sub-threshold OCR result should not replace the body: %q", ex.Markdown)
	}
}
