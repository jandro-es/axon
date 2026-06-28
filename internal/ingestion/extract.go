package ingestion

import (
	"bytes"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"

	htmltomarkdown "github.com/JohannesKaufmann/html-to-markdown/v2"
	readability "github.com/go-shiori/go-readability"
)

// Extracted is the cleaned, structured result of the extract+clean stages: the
// main content as Markdown plus whatever provenance the source exposed.
type Extracted struct {
	Title    string
	Author   string
	Date     string // ISO date if available
	SiteName string
	Markdown string
}

// ExtractHTML runs readability over raw HTML to isolate the main article, then
// converts that to clean Markdown (strips nav/ads/scripts; keeps headings,
// lists, code, links). pageURL helps readability resolve relative links.
func ExtractHTML(raw []byte, pageURL string) (Extracted, error) {
	var parsed *url.URL
	if pageURL != "" {
		if u, err := url.Parse(pageURL); err == nil {
			parsed = u
		}
	}
	article, err := readability.FromReader(bytes.NewReader(raw), parsed)
	if err != nil {
		return Extracted{}, fmt.Errorf("readability: %w", err)
	}
	md, err := htmltomarkdown.ConvertString(article.Content)
	if err != nil {
		return Extracted{}, fmt.Errorf("html->markdown: %w", err)
	}
	md = normalizeMarkdown(md)
	if strings.TrimSpace(md) == "" {
		// Fall back to readability's plain text if conversion yielded nothing
		// (e.g. unusual markup); never produce an empty note silently.
		md = normalizeMarkdown(article.TextContent)
	}
	ex := Extracted{
		Title:    strings.TrimSpace(article.Title),
		Author:   strings.TrimSpace(article.Byline),
		SiteName: strings.TrimSpace(article.SiteName),
		Markdown: md,
	}
	if article.PublishedTime != nil {
		ex.Date = article.PublishedTime.UTC().Format("2006-01-02")
	}
	return ex, nil
}

// ExtractFile turns a local Markdown/text file into Extracted. Markdown is kept
// as-is; the title is the first H1 or the filename.
func ExtractFile(raw []byte, path string) Extracted {
	text := normalizeMarkdown(string(raw))
	title := firstHeading(text)
	if title == "" {
		base := filepath.Base(path)
		title = strings.TrimSuffix(base, filepath.Ext(base))
	}
	return Extracted{Title: title, Markdown: text}
}

// normalizeMarkdown collapses excessive blank lines and trims trailing space.
func normalizeMarkdown(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	lines := strings.Split(s, "\n")
	var out []string
	blanks := 0
	for _, ln := range lines {
		ln = strings.TrimRight(ln, " \t")
		if ln == "" {
			blanks++
			if blanks > 1 {
				continue
			}
		} else {
			blanks = 0
		}
		out = append(out, ln)
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

// firstHeading returns the text of the first ATX H1, or "".
func firstHeading(md string) string {
	for _, ln := range strings.Split(md, "\n") {
		ln = strings.TrimSpace(ln)
		if strings.HasPrefix(ln, "# ") {
			return strings.TrimSpace(ln[2:])
		}
	}
	return ""
}
