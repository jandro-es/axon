package vault

import (
	"testing"
	"time"
)

func TestSplitFrontmatter(t *testing.T) {
	tests := []struct {
		name     string
		in       string
		wantFM   string
		wantBody string
	}{
		{
			name:     "well-formed",
			in:       "---\ntitle: X\ntype: note\n---\nBody here.\n",
			wantFM:   "title: X\ntype: note\n",
			wantBody: "Body here.\n",
		},
		{
			name:     "no frontmatter",
			in:       "Just a body.\n",
			wantFM:   "",
			wantBody: "Just a body.\n",
		},
		{
			name:     "unterminated frontmatter is treated as body",
			in:       "---\ntitle: X\nno closing fence",
			wantFM:   "",
			wantBody: "---\ntitle: X\nno closing fence",
		},
		{
			name:     "empty frontmatter",
			in:       "---\n---\nBody.",
			wantFM:   "",
			wantBody: "Body.",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fm, body := splitFrontmatter(tt.in)
			if fm != tt.wantFM {
				t.Errorf("fm = %q, want %q", fm, tt.wantFM)
			}
			if body != tt.wantBody {
				t.Errorf("body = %q, want %q", body, tt.wantBody)
			}
		})
	}
}

func TestParseNoteMetadata(t *testing.T) {
	content := "---\ntitle: Hello\ntype: project\nstatus: active\ntags: [a, b/c]\n---\nThe body.\n"
	n, err := parseNote("01-Projects/x.md", content, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if n.FrontmatterString("title") != "Hello" {
		t.Errorf("title = %q", n.FrontmatterString("title"))
	}
	if n.FrontmatterString("type") != "project" {
		t.Errorf("type = %q", n.FrontmatterString("type"))
	}
	tags := n.Tags()
	if len(tags) != 2 || tags[0] != "a" || tags[1] != "b/c" {
		t.Errorf("tags = %v", tags)
	}
	if n.Body != "The body.\n" {
		t.Errorf("body = %q", n.Body)
	}
}

func TestReassembleRoundTrip(t *testing.T) {
	content := "---\ntitle: X\n---\nBody line.\n"
	fm, body := splitFrontmatter(content)
	if got := reassemble(fm, body); got != content {
		t.Errorf("round-trip mismatch:\n got %q\nwant %q", got, content)
	}
}
