package ingestion

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/jandro-es/axon/internal/config"
)

func TestOllamaVisionDescribe(t *testing.T) {
	v := NewOllamaVision("", "qwen2.5vl")
	v.post = func(ctx context.Context, url string, body []byte) (int, []byte, error) {
		if !strings.Contains(string(body), "\"images\"") {
			t.Fatalf("request body missing images field: %s", body)
		}
		return http.StatusOK, []byte(`{"response":"A login screen for Acme."}`), nil
	}
	got, err := v.Describe(context.Background(), []byte{0x89, 0x50}, "image/png")
	if err != nil {
		t.Fatal(err)
	}
	if got != "A login screen for Acme." {
		t.Fatalf("got %q", got)
	}
}

func TestOllamaVisionTransportError(t *testing.T) {
	v := NewOllamaVision("", "m")
	v.post = func(ctx context.Context, url string, body []byte) (int, []byte, error) {
		return 0, nil, errors.New("connection refused")
	}
	if _, err := v.Describe(context.Background(), []byte{1}, "image/png"); err == nil {
		t.Fatal("expected error on transport failure")
	}
}

func TestOllamaVisionErrorResponse(t *testing.T) {
	v := NewOllamaVision("", "m")
	v.post = func(ctx context.Context, url string, body []byte) (int, []byte, error) {
		return http.StatusOK, []byte(`{"error":"model not found"}`), nil
	}
	if _, err := v.Describe(context.Background(), []byte{1}, "image/png"); err == nil {
		t.Fatal("expected error when response carries error field")
	}
}

func TestVisionFor(t *testing.T) {
	tests := []struct {
		mode    string
		wantNil bool
		wantErr bool
	}{
		{"", true, false},
		{"off", true, false},
		{"ollama:qwen2.5vl", false, false},
		{"ollama:", true, true},
		{"apple", true, true},
		{"garbage", true, true},
	}
	for _, tt := range tests {
		t.Run(tt.mode, func(t *testing.T) {
			got, err := VisionFor(config.IngestionConfig{Vision: tt.mode}, "darwin")
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if (got == nil) != tt.wantNil {
				t.Fatalf("nil = %v, wantNil %v", got == nil, tt.wantNil)
			}
		})
	}
}
