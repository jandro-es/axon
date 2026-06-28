package ingestion

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

// maxFetchBytes caps a fetched/read body to guard memory (NFR-05 "cap response
// size"). 16 MiB comfortably covers articles and most PDFs.
const maxFetchBytes = 16 << 20

// userAgent identifies AXON politely to origin servers.
const userAgent = "axon/0.2 (+local-first knowledge ingestion)"

// HTTPFetcher fetches URLs over HTTP(S). It performs no policy checks itself —
// the pipeline enforces policy before calling Fetch — and executes no
// JavaScript. Local files are read by ReadFile, not this type.
type HTTPFetcher struct {
	client *http.Client
}

// NewHTTPFetcher returns a fetcher with a sane per-request timeout.
func NewHTTPFetcher() *HTTPFetcher {
	return &HTTPFetcher{client: &http.Client{Timeout: 30 * time.Second}}
}

// Fetch GETs url and returns the (size-capped) body. Treated strictly as data.
func (f *HTTPFetcher) Fetch(ctx context.Context, url string) (*Document, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := f.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch %q: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("fetch %q: status %d", url, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxFetchBytes))
	if err != nil {
		return nil, fmt.Errorf("read %q: %w", url, err)
	}
	return &Document{
		URL:         url,
		ContentType: resp.Header.Get("Content-Type"),
		Body:        body,
		FetchedAt:   time.Now().UTC(),
	}, nil
}

// ReadFile reads a local file as an ingestion Document, capped at maxFetchBytes.
func ReadFile(path string) (*Document, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("read file %q: %w", path, err)
	}
	if info.IsDir() {
		return nil, fmt.Errorf("read file %q: is a directory", path)
	}
	fh, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open %q: %w", path, err)
	}
	defer fh.Close()
	body, err := io.ReadAll(io.LimitReader(fh, maxFetchBytes))
	if err != nil {
		return nil, fmt.Errorf("read %q: %w", path, err)
	}
	return &Document{URL: "file://" + path, Body: body, FetchedAt: time.Now().UTC()}, nil
}

// compile-time assertion that *HTTPFetcher satisfies Fetcher.
var _ Fetcher = (*HTTPFetcher)(nil)
