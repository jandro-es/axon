package ingestion

import (
	"context"
	"strings"
	"testing"
)

func newFakeAppleOCR(stdout string, runErr error) *AppleOCR {
	a := NewAppleOCR("/fake/helper")
	a.goos = "darwin"
	a.run = func(ctx context.Context, bin string, args []string) ([]byte, []byte, error) {
		return []byte(stdout), nil, runErr
	}
	return a
}

func TestAppleOCRJoinsPages(t *testing.T) {
	a := newFakeAppleOCR(`{"pages":["page one","page two"]}`, nil)
	got, err := a.Recognize(context.Background(), []byte("%PDF-1.4"))
	if err != nil {
		t.Fatal(err)
	}
	if got != "page one\n\npage two" {
		t.Fatalf("joined = %q", got)
	}
}

func TestAppleOCRRefusesNonDarwin(t *testing.T) {
	a := NewAppleOCR("/fake/helper")
	a.goos = "linux"
	if _, err := a.Recognize(context.Background(), []byte("%PDF")); err == nil || !strings.Contains(err.Error(), "macOS") {
		t.Fatalf("non-darwin err = %v", err)
	}
}

func TestAppleOCRBadJSON(t *testing.T) {
	a := newFakeAppleOCR("not json", nil)
	if _, err := a.Recognize(context.Background(), []byte("%PDF")); err == nil {
		t.Fatal("malformed helper output should error")
	}
}
