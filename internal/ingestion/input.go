package ingestion

import (
	"net/url"
	"strings"
)

// InputKind classifies an ingest input.
type InputKind string

const (
	KindURL  InputKind = "url"
	KindPDF  InputKind = "pdf"
	KindFile InputKind = "file"
)

// Input is a classified, normalised ingest target.
type Input struct {
	Kind InputKind
	Raw  string // original argument
	URL  string // canonical identifier stored in sources.url (http(s):// or file://path)
	Host string // URL host, empty for local files
	Path string // local filesystem path, empty for URLs
}

// ClassifyInput determines whether arg is a URL, a PDF file or another local
// file, and normalises it into an Input. Local paths are identified by a .pdf
// extension or by simply not parsing as an http(s) URL.
func ClassifyInput(arg string) Input {
	if u, err := url.Parse(arg); err == nil && (u.Scheme == "http" || u.Scheme == "https") {
		return Input{Kind: KindURL, Raw: arg, URL: arg, Host: u.Hostname()}
	}
	path := strings.TrimPrefix(arg, "file://")
	kind := KindFile
	if strings.EqualFold(filepathExt(path), ".pdf") {
		kind = KindPDF
	}
	return Input{Kind: kind, Raw: arg, URL: "file://" + path, Path: path}
}

// filepathExt returns the lowercase extension including the dot, or "".
func filepathExt(p string) string {
	if i := strings.LastIndexByte(p, '.'); i >= 0 && i > strings.LastIndexByte(p, '/') {
		return strings.ToLower(p[i:])
	}
	return ""
}
