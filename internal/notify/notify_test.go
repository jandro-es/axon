package notify

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jandro-es/axon/internal/events"
)

func TestNtfyPostsTitleAndBody(t *testing.T) {
	var gotBody, gotTitle, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody, gotTitle, gotPath = string(b), r.Header.Get("Title"), r.URL.Path
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	n, err := NewNtfy(srv.URL+"/axon-topic", nil)
	if err != nil {
		t.Fatal(err)
	}
	err = n.Send(context.Background(), Note{
		Kind: "automation.fail", Level: events.LevelError,
		Title: "automation failed", Body: "capture failed: disk full",
	})
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if gotPath != "/axon-topic" {
		t.Errorf("path = %q, want /axon-topic", gotPath)
	}
	if gotTitle != "automation failed" {
		t.Errorf("Title header = %q", gotTitle)
	}
	if !strings.Contains(gotBody, "disk full") {
		t.Errorf("body = %q", gotBody)
	}
}

func TestNtfyRedactsBeforeSending(t *testing.T) {
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
	}))
	defer srv.Close()

	n, err := NewNtfy(srv.URL+"/t", []string{`sk-[A-Za-z0-9]+`})
	if err != nil {
		t.Fatal(err)
	}
	if err := n.Send(context.Background(), Note{Title: "t", Body: "leaked sk-ABC123 here"}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(gotBody, "sk-ABC123") {
		t.Fatalf("the secret was sent unredacted: %q", gotBody)
	}
}

// A redaction rule that will not compile must REFUSE construction, not send
// unredacted. The failure mode of a bad regex must never be "your data goes
// out unfiltered".
func TestNtfyRefusesWhenRedactorCannotCompile(t *testing.T) {
	if _, err := NewNtfy("https://example.com/t", []string{"([unclosed"}); err == nil {
		t.Fatal("a bad redaction rule must refuse construction, not send unredacted")
	}
}

func TestNtfyCapsBodyLength(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got = string(b)
	}))
	defer srv.Close()
	n, _ := NewNtfy(srv.URL+"/t", nil)
	if err := n.Send(context.Background(), Note{Title: "t", Body: strings.Repeat("x", maxBodyBytes*2)}); err != nil {
		t.Fatal(err)
	}
	if len(got) > maxBodyBytes+16 {
		t.Fatalf("body not capped: %d bytes", len(got))
	}
}

func TestNtfyReportsNon2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))
	defer srv.Close()
	n, _ := NewNtfy(srv.URL+"/t", nil)
	if err := n.Send(context.Background(), Note{Title: "t", Body: "b"}); err == nil {
		t.Fatal("a non-2xx response must be an error the caller can log")
	}
}
