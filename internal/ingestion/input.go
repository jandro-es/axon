package ingestion

import (
	"net/url"
	"strings"
)

// InputKind classifies an ingest input.
type InputKind string

const (
	KindURL   InputKind = "url"
	KindPDF   InputKind = "pdf"
	KindFile  InputKind = "file"
	KindImage InputKind = "image"
	KindMedia InputKind = "media"
)

// Input is a classified, normalised ingest target.
type Input struct {
	Kind InputKind
	Raw  string // original argument
	URL  string // canonical identifier stored in sources.url (http(s):// or file://path)
	Host string // URL host, empty for local files
	Path string // local filesystem path, empty for URLs
}

// imageExts are the lowercase extensions classified as KindImage.
var imageExts = map[string]bool{
	".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".webp": true,
	".heic": true, ".heif": true, ".tiff": true, ".tif": true, ".bmp": true,
}

// builtinMediaHosts auto-classify as KindMedia (the YouTube family).
var builtinMediaHosts = map[string]bool{
	"youtube.com": true, "www.youtube.com": true, "m.youtube.com": true,
	"music.youtube.com": true, "youtu.be": true,
	"youtube-nocookie.com": true, "www.youtube-nocookie.com": true,
}

// ClassifyInput determines whether arg is a URL (article or caption-bearing
// media), a PDF, an image, or another local file, and normalises it into an
// Input. mediaHosts extends the built-in media host set; forceMedia routes ANY
// http(s) URL through the caption path. Local paths are classified by extension.
func ClassifyInput(arg string, mediaHosts []string, forceMedia bool) Input {
	if u, err := url.Parse(arg); err == nil && (u.Scheme == "http" || u.Scheme == "https") {
		host := u.Hostname()
		if forceMedia || isMediaHost(host, mediaHosts) {
			return Input{Kind: KindMedia, Raw: arg, URL: arg, Host: host}
		}
		return Input{Kind: KindURL, Raw: arg, URL: arg, Host: host}
	}
	path := strings.TrimPrefix(arg, "file://")
	kind := KindFile
	switch ext := filepathExt(path); {
	case ext == ".pdf":
		kind = KindPDF
	case imageExts[ext]:
		kind = KindImage
	}
	return Input{Kind: kind, Raw: arg, URL: "file://" + path, Path: path}
}

// isMediaHost reports whether host is a built-in or configured media host.
func isMediaHost(host string, extra []string) bool {
	host = strings.ToLower(host)
	if builtinMediaHosts[host] {
		return true
	}
	for _, h := range extra {
		if strings.EqualFold(strings.TrimSpace(h), host) {
			return true
		}
	}
	return false
}

// filepathExt returns the lowercase extension including the dot, or "".
func filepathExt(p string) string {
	if i := strings.LastIndexByte(p, '.'); i >= 0 && i > strings.LastIndexByte(p, '/') {
		return strings.ToLower(p[i:])
	}
	return ""
}
