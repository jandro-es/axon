package ingestion

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/url"
	"path/filepath"
	"strings"

	htmltomarkdown "github.com/JohannesKaufmann/html-to-markdown/v2"
	readability "github.com/go-shiori/go-readability"
	"github.com/ledongthuc/pdf"
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

// minExtractedChars is the floor below which an extraction is considered
// "found nothing" — pages whose real content is smaller than a tweet are
// almost always an app shell or an error page, not an article.
const minExtractedChars = 80

// ExtractHTML runs readability over raw HTML to isolate the main article, then
// converts that to clean Markdown (strips nav/ads/scripts; keeps headings,
// lists, code, links). pageURL helps readability resolve relative links.
//
// Readability is tuned for article-shaped pages and can come back (near) empty
// on wiki/app layouts (Confluence, Notion exports…). When that happens but the
// HTML clearly has content, the whole document is converted instead — noisier,
// but complete beats empty. If there is still nothing, ExtractHTML errors so
// the pipeline never writes a junk note silently.
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
	if len(md) < minExtractedChars {
		// Fall back to readability's plain text first (unusual markup)…
		if txt := normalizeMarkdown(article.TextContent); len(txt) > len(md) {
			md = txt
		}
	}
	if len(md) < minExtractedChars {
		// …then to converting the ENTIRE document, which rescues wiki-style
		// layouts readability rejects.
		if whole, werr := htmltomarkdown.ConvertString(string(raw)); werr == nil {
			if whole = normalizeMarkdown(whole); len(whole) > len(md) {
				md = whole
			}
		}
	}
	if len(md) < minExtractedChars {
		return Extracted{}, fmt.Errorf("no extractable content at %s (%d chars) — the page is likely rendered by JavaScript or behind a login; for SSO'd sources configure ingestion.auth (Confluence pages then use the REST API automatically)", pageURL, len(md))
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

// ExtractPDF extracts the plain text from a PDF's bytes and turns it into
// Extracted (FR-21), routed through the same enrich/chunk/embed pipeline as URLs
// and text files. The PDF parser can panic on malformed input, so extraction is
// wrapped in a recover — a bad PDF yields a clear error, never a daemon crash.
// The content is treated strictly as data, never instructions (NFR-05).
func ExtractPDF(raw []byte, path string) (ex Extracted, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("parse PDF %q: %v", path, r)
		}
	}()
	reader, perr := pdf.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if perr != nil {
		return Extracted{}, fmt.Errorf("read PDF %q: %w", path, perr)
	}
	textReader, perr := reader.GetPlainText()
	if perr != nil {
		return Extracted{}, fmt.Errorf("extract PDF text %q: %w", path, perr)
	}
	var buf bytes.Buffer
	if _, perr := io.Copy(&buf, textReader); perr != nil {
		return Extracted{}, fmt.Errorf("read PDF text %q: %w", path, perr)
	}
	md := normalizeMarkdown(buf.String())
	title := firstHeading(md)
	if title == "" {
		base := filepath.Base(path)
		title = strings.TrimSuffix(base, filepath.Ext(base))
	}
	return Extracted{Title: title, Markdown: md}, nil
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

// extractImage recovers text from a raster image: OCR first, then a local
// vision description only when OCR came back sparse (mirrors ocrFallback for
// PDFs). Vision REPLACES sparse OCR text. A vision error is returned only when
// OCR also produced nothing; if OCR gave usable text, a vision error is
// swallowed and the OCR text stands. Both providers absent (or both empty)
// yields empty Markdown with no error — the caller still writes the note with
// the archived image embed (the acceptance gate: no crash).
func extractImage(ctx context.Context, img []byte, mime string, ocr OCR, vision Vision) (Extracted, error) {
	var text string
	if ocr != nil {
		if t, err := ocr.RecognizeImage(ctx, img, mime); err == nil {
			text = normalizeMarkdown(t)
		}
	}
	if len(text) < minExtractedChars && vision != nil {
		vt, verr := vision.Describe(ctx, img, mime)
		if verr != nil {
			if text == "" {
				return Extracted{}, fmt.Errorf("vision (%s): %w", vision.Name(), verr)
			}
			// OCR text stands; swallow the vision error.
		} else if vt = normalizeMarkdown(vt); vt != "" {
			text = vt
		}
	}
	return Extracted{Title: firstHeading(text), Markdown: text}, nil
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
