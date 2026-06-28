package ingestion

import (
	"context"
	"errors"
	"testing"
)

func TestFakeFetchKnownURL(t *testing.T) {
	f := NewFake()
	f.Docs["https://example.com"] = "<html>body</html>"

	doc, err := f.Fetch(context.Background(), "https://example.com")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if string(doc.Body) != "<html>body</html>" {
		t.Errorf("body = %q", doc.Body)
	}
	if doc.URL != "https://example.com" {
		t.Errorf("url = %q", doc.URL)
	}
}

func TestFakeFetchUnknownURL(t *testing.T) {
	f := NewFake()
	if _, err := f.Fetch(context.Background(), "https://nope.com"); err == nil {
		t.Error("expected error for unknown URL")
	}

	f.Err = errors.New("denied")
	if _, err := f.Fetch(context.Background(), "https://nope.com"); err == nil {
		t.Error("expected configured error for unknown URL")
	}
}

func TestFakeFetchRespectsContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := NewFake().Fetch(ctx, "https://example.com"); err == nil {
		t.Error("expected context error")
	}
}
