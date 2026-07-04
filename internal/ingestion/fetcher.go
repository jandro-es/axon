// Package ingestion turns external sources (URLs, articles, PDFs) into clean,
// linked vault notes. Phase 0 defines only the Fetcher seam and a fake; the
// fetch->extract->clean->enrich->embed->index pipeline arrives in Phase 2.
//
// Security posture (NFR-05): fetched content is DATA, never instructions. The
// Fetcher returns bytes; nothing downstream executes what it finds inside them.
package ingestion

import (
	"context"
	"time"
)

// Document is raw fetched content plus provenance, before extraction/cleaning.
type Document struct {
	URL         string
	ContentType string
	Body        []byte
	FetchedAt   time.Time
	// ETag / LastModified are the response's cache validators (RFC 9110
	// §13), captured so recurring fetchers (feed polling) can make the
	// next request conditional. One-shot callers ignore them.
	ETag         string
	LastModified string
}

// Fetcher retrieves a document from a URL. Production implementations enforce
// the egress allowlist and ingest-domain policy before any network call; the
// fake performs no IO. Implementations must be safe for concurrent use.
type Fetcher interface {
	Fetch(ctx context.Context, url string) (*Document, error)
}

// Validators are a document's HTTP cache validators, echoed back verbatim
// on the next conditional request for the same URL.
type Validators struct {
	ETag         string `json:"etag,omitempty"`
	LastModified string `json:"last_modified,omitempty"`
}

// ConditionalFetcher is implemented by fetchers that support HTTP
// conditional requests. notModified reports a 304 — success with no
// document; the caller's cached view still stands.
type ConditionalFetcher interface {
	FetchConditional(ctx context.Context, url string, v Validators) (doc *Document, notModified bool, err error)
}
