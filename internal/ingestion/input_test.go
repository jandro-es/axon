package ingestion

import (
	"context"
	"strings"
	"testing"

	"github.com/jandro-es/axon/internal/config"
)

func TestClassifyInput(t *testing.T) {
	tests := []struct {
		name       string
		arg        string
		mediaHosts []string
		forceMedia bool
		wantKind   InputKind
		wantHost   string
	}{
		{"http url", "https://example.com/a", nil, false, KindURL, "example.com"},
		{"pdf file", "/tmp/x.pdf", nil, false, KindPDF, ""},
		{"png image", "/tmp/shot.PNG", nil, false, KindImage, ""},
		{"jpg image", "file:///tmp/a.jpeg", nil, false, KindImage, ""},
		{"heic image", "/tmp/a.heic", nil, false, KindImage, ""},
		{"plain text", "/tmp/notes.md", nil, false, KindFile, ""},
		{"youtube host", "https://www.youtube.com/watch?v=x", nil, false, KindMedia, "www.youtube.com"},
		{"youtu.be short", "https://youtu.be/abc", nil, false, KindMedia, "youtu.be"},
		{"extra media host", "https://vimeo.com/123", []string{"vimeo.com"}, false, KindMedia, "vimeo.com"},
		{"force media on any url", "https://podcast.example/ep1", nil, true, KindMedia, "podcast.example"},
		{"force media does not touch local file", "/tmp/a.md", nil, true, KindFile, ""},
		{"pdf url stays url", "https://example.com/f.pdf", nil, false, KindURL, "example.com"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ClassifyInput(tt.arg, tt.mediaHosts, tt.forceMedia)
			if got.Kind != tt.wantKind {
				t.Fatalf("kind = %q, want %q", got.Kind, tt.wantKind)
			}
			if got.Host != tt.wantHost {
				t.Fatalf("host = %q, want %q", got.Host, tt.wantHost)
			}
		})
	}
}

func TestClassifyInputAudio(t *testing.T) {
	for _, ext := range []string{".m4a", ".mp3", ".wav", ".aac", ".flac", ".ogg", ".opus"} {
		in := ClassifyInput("/tmp/recording"+ext, nil, false)
		if in.Kind != KindAudio {
			t.Errorf("%s classified as %q, want audio", ext, in.Kind)
		}
	}
	// filepathExt lowercases, so uppercase extensions classify too.
	if got := ClassifyInput("/tmp/Recording.M4A", nil, false); got.Kind != KindAudio {
		t.Errorf("uppercase extension classified as %q, want audio", got.Kind)
	}
	if got := ClassifyInput("/tmp/notes.txt", nil, false); got.Kind != KindFile {
		t.Errorf("txt classified as %q, want file", got.Kind)
	}
	if got := ClassifyInput("/tmp/scan.png", nil, false); got.Kind != KindImage {
		t.Errorf("png classified as %q, want image", got.Kind)
	}
	if got := ClassifyInput("https://example.com/ep.mp3", nil, false); got.Kind != KindURL {
		t.Errorf("http url classified as %q, want url", got.Kind)
	}
}

// The security-relevant half: the knowledge_ingest MCP tool exists to be
// called by a model, so it must never be able to transcribe host audio.
func TestAudioIngestRefusedWithoutAllowLocalFiles(t *testing.T) {
	p, _, _ := newTestPipeline(t, config.PolicyConfig{})
	_, err := p.Ingest(context.Background(), "/tmp/secret.m4a", IngestOptions{AllowLocalFiles: false})
	if err == nil || !strings.Contains(err.Error(), "not permitted") {
		t.Fatalf("agent-driven audio ingestion must be refused, got %v", err)
	}
}
