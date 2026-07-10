package ingestion

import (
	"context"
	"strings"
	"testing"
)

func TestTesseractRecognizeImage(t *testing.T) {
	var gotPath string
	tt := &TesseractOCR{
		lookup: func(string) (string, error) { return "/usr/bin/tesseract", nil },
		ocrImage: func(ctx context.Context, imgPath string) (string, error) {
			gotPath = imgPath
			return "hello world", nil
		},
	}
	got, err := tt.RecognizeImage(context.Background(), []byte{0x89, 0x50, 0x4e, 0x47}, "image/png")
	if err != nil {
		t.Fatal(err)
	}
	if got != "hello world" {
		t.Fatalf("got %q", got)
	}
	if !strings.HasSuffix(gotPath, ".png") {
		t.Fatalf("temp image path %q should keep the .png extension", gotPath)
	}
}

func TestTesseractRecognizeImageMissingBinary(t *testing.T) {
	tt := &TesseractOCR{
		lookup:   func(string) (string, error) { return "", context.DeadlineExceeded },
		ocrImage: func(ctx context.Context, imgPath string) (string, error) { return "", nil },
	}
	if _, err := tt.RecognizeImage(context.Background(), []byte{1}, "image/png"); err == nil {
		t.Fatal("expected error when tesseract is absent")
	}
}

func TestExtFromMime(t *testing.T) {
	if got := extFromMime("image/jpeg"); got != ".jpg" {
		t.Fatalf("extFromMime(image/jpeg) = %q", got)
	}
	if got := extFromMime("application/octet-stream"); got != ".png" {
		t.Fatalf("extFromMime fallback = %q, want .png", got)
	}
}
