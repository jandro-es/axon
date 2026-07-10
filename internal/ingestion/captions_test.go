package ingestion

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStripVTT(t *testing.T) {
	in := "WEBVTT\nKind: captions\nLanguage: en\n\n" +
		"00:00:01.000 --> 00:00:03.000\n<c>Hello</c> world\n\n" +
		"00:00:03.000 --> 00:00:05.000\nHello world\n\n" + // duplicate rolling caption
		"00:00:05.000 --> 00:00:07.000\nSecond line\n"
	got := stripVTT(in)
	if strings.Contains(got, "-->") || strings.Contains(got, "WEBVTT") || strings.Contains(got, "<c>") {
		t.Fatalf("VTT artifacts remain: %q", got)
	}
	if strings.Count(got, "Hello world") != 1 {
		t.Fatalf("duplicate lines not collapsed: %q", got)
	}
	if !strings.Contains(got, "Second line") {
		t.Fatalf("missing content: %q", got)
	}
}

func TestCaptionFetcherHappyPath(t *testing.T) {
	c := newCaptionFetcher("en.*")
	c.lookup = func(string) (string, error) { return "/usr/bin/yt-dlp", nil }
	c.run = func(ctx context.Context, url, langs, outDir string) ([]string, string, error) {
		sub := filepath.Join(outDir, "sub.en.vtt")
		_ = os.WriteFile(sub, []byte("WEBVTT\n\n00:00:01.000 --> 00:00:02.000\nHello there\n"), 0o600)
		return []string{sub}, "My Talk", nil
	}
	transcript, title, err := c.Fetch(context.Background(), "https://youtu.be/x")
	if err != nil {
		t.Fatal(err)
	}
	if title != "My Talk" || !strings.Contains(transcript, "Hello there") {
		t.Fatalf("title=%q transcript=%q", title, transcript)
	}
}

func TestCaptionFetcherNoBinary(t *testing.T) {
	c := newCaptionFetcher("en.*")
	c.lookup = func(string) (string, error) { return "", errors.New("not found") }
	if _, _, err := c.Fetch(context.Background(), "https://youtu.be/x"); !errors.Is(err, ErrNoCaptions) {
		t.Fatalf("err = %v, want ErrNoCaptions", err)
	}
}

func TestCaptionFetcherNoSubtitleFile(t *testing.T) {
	c := newCaptionFetcher("en.*")
	c.lookup = func(string) (string, error) { return "/usr/bin/yt-dlp", nil }
	c.run = func(ctx context.Context, url, langs, outDir string) ([]string, string, error) {
		return nil, "Title", nil
	}
	if _, _, err := c.Fetch(context.Background(), "https://youtu.be/x"); !errors.Is(err, ErrNoCaptions) {
		t.Fatalf("err = %v, want ErrNoCaptions", err)
	}
}
