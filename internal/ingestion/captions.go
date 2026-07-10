package ingestion

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// ErrNoCaptions signals that a media URL has no usable captions (or yt-dlp is
// absent). The pipeline turns this into a flagged 00-Inbox capture, never a
// failure.
var ErrNoCaptions = errors.New("no captions available")

// Captioner fetches a plain-text transcript + title for a media URL.
type Captioner interface {
	Fetch(ctx context.Context, url string) (transcript, title string, err error)
}

// captionFetcher pulls native/auto captions via a detected yt-dlp binary
// (ADR-026 detected-binary precedent). lookup/run are injectable for tests.
type captionFetcher struct {
	langs  string
	lookup func(string) (string, error)
	run    func(ctx context.Context, url, langs, outDir string) (subFiles []string, title string, err error)
}

// newCaptionFetcher wires the real yt-dlp executor. langs defaults to "en.*".
func newCaptionFetcher(langs string) *captionFetcher {
	if strings.TrimSpace(langs) == "" {
		langs = "en.*"
	}
	return &captionFetcher{langs: langs, lookup: exec.LookPath, run: ytDlpRun}
}

// Fetch returns the transcript + title, or ErrNoCaptions when yt-dlp is absent
// or produces no usable subtitle track.
func (c *captionFetcher) Fetch(ctx context.Context, url string) (string, string, error) {
	if _, err := c.lookup("yt-dlp"); err != nil {
		return "", "", ErrNoCaptions
	}
	dir, err := os.MkdirTemp("", "axon-captions-*")
	if err != nil {
		return "", "", err
	}
	defer func() { _ = os.RemoveAll(dir) }()

	subs, title, err := c.run(ctx, url, c.langs, dir)
	if err != nil {
		return "", "", fmt.Errorf("yt-dlp captions: %w", err)
	}
	if len(subs) == 0 {
		return "", "", ErrNoCaptions
	}
	raw, err := os.ReadFile(subs[0])
	if err != nil {
		return "", "", err
	}
	text := stripVTT(string(raw))
	if strings.TrimSpace(text) == "" {
		return "", "", ErrNoCaptions
	}
	if strings.TrimSpace(title) == "" {
		title = url
	}
	return text, title, nil
}

// ytDlpRun invokes yt-dlp to write VTT subtitles (native + auto) without
// downloading media, and prints the title. Returns the .vtt files in the temp
// dir (page order) and the title.
func ytDlpRun(ctx context.Context, url, langs, outDir string) ([]string, string, error) {
	tmpl := filepath.Join(outDir, "sub.%(ext)s")
	cmd := exec.CommandContext(ctx, "yt-dlp",
		"--skip-download", "--write-subs", "--write-auto-subs",
		"--sub-format", "vtt", "--sub-langs", langs,
		"--print", "title", "-o", tmpl, url)
	cmd.WaitDelay = 5 * time.Second
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, "", fmt.Errorf("%w: %s", err, bytes.TrimSpace(stderr.Bytes()))
	}
	subs, _ := filepath.Glob(filepath.Join(outDir, "*.vtt"))
	sort.Strings(subs)
	return subs, strings.TrimSpace(stdout.String()), nil
}

var (
	vttCueNumRe = regexp.MustCompile(`^\d+$`)
	vttTagRe    = regexp.MustCompile(`</?[^>]+>`)
)

// stripVTT converts a WebVTT subtitle file to plain text: drops the header,
// cue timings, cue indices and inline markup, and collapses the consecutive
// duplicate lines that auto-captions emit for rolling display.
func stripVTT(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	var out []string
	var last string
	for _, ln := range strings.Split(s, "\n") {
		t := strings.TrimSpace(ln)
		switch {
		case t == "", t == "WEBVTT":
			continue
		case strings.HasPrefix(t, "NOTE"), strings.HasPrefix(t, "Kind:"), strings.HasPrefix(t, "Language:"):
			continue
		case strings.Contains(t, "-->"):
			continue
		case vttCueNumRe.MatchString(t):
			continue
		}
		t = strings.TrimSpace(vttTagRe.ReplaceAllString(t, ""))
		if t == "" || t == last {
			continue
		}
		last = t
		out = append(out, t)
	}
	return normalizeMarkdown(strings.Join(out, "\n"))
}

var _ Captioner = (*captionFetcher)(nil)
