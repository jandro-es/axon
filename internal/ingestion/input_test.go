package ingestion

import "testing"

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
