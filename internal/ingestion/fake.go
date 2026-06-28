package ingestion

import (
	"context"
	"fmt"
	"time"
)

// Fake is a Fetcher that returns canned documents from an in-memory map, for
// tests and the Phase 0 skeleton. It makes no network calls.
type Fake struct {
	// Docs maps URL -> body returned by Fetch.
	Docs map[string]string
	// FetchedAt stamps returned documents; zero means time.Time{}.
	FetchedAt time.Time
	// Err, if set, is returned for any URL not present in Docs.
	Err error
}

// NewFake returns an empty fake fetcher.
func NewFake() *Fake {
	return &Fake{Docs: make(map[string]string)}
}

// Fetch returns the canned document for url, or an error if unknown.
func (f *Fake) Fetch(ctx context.Context, url string) (*Document, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	body, ok := f.Docs[url]
	if !ok {
		if f.Err != nil {
			return nil, f.Err
		}
		return nil, fmt.Errorf("no canned document for %q", url)
	}
	return &Document{
		URL:         url,
		ContentType: "text/html",
		Body:        []byte(body),
		FetchedAt:   f.FetchedAt,
	}, nil
}

// compile-time assertion that Fake satisfies Fetcher.
var _ Fetcher = (*Fake)(nil)
