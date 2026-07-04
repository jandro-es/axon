package ingestion

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	neturl "net/url"
	"os"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/jandro-es/axon/internal/config"
)

// maxFetchBytes caps a fetched/read body to guard memory (NFR-05 "cap response
// size"). 16 MiB comfortably covers articles and most PDFs.
const maxFetchBytes = 16 << 20

// userAgent identifies AXON politely to origin servers.
const userAgent = "axon/0.2 (+local-first knowledge ingestion)"

// HTTPFetcher fetches URLs over HTTP(S). The pipeline enforces policy on the
// initial host, and this fetcher RE-validates policy on every redirect hop so a
// redirect cannot escape the egress allowlist to an internal/metadata host
// (SSRF). It executes no JavaScript. Local files are read by ReadFile.
type HTTPFetcher struct {
	client *http.Client
}

// fetchRetries and fetchBackoff bound the retry loop for transient failures
// (network blips, 429, 5xx). Small on purpose: ingestion is interactive.
const fetchRetries = 2
const fetchBackoff = 700 * time.Millisecond

// errNotModified marks a 304 inside the retry loop: success, not failure.
var errNotModified = errors.New("not modified")

// authHeaderTransport attaches configured per-domain auth headers. It runs on
// EVERY request the client makes — including each redirect hop — and matches
// the hop's own host against the rule domain, so a credential can never leak
// to a different site via redirect (NFR-05).
type authHeaderTransport struct {
	base  http.RoundTripper
	rules []config.IngestAuth
}

func (t *authHeaderTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	host := req.URL.Hostname()
	for _, rule := range t.rules {
		if !matchesDomain(rule.Domain, host) {
			continue
		}
		value, err := config.ResolveSecret(rule.Value)
		if err != nil || value == "" {
			return nil, fmt.Errorf("ingestion auth for domain %q: credential not resolvable (%v) — check the secret reference in ingestion.auth", rule.Domain, err)
		}
		header := rule.Header
		if header == "" {
			header = "Authorization"
		}
		req = req.Clone(req.Context())
		req.Header.Set(header, value)
	}
	return t.base.RoundTrip(req)
}

// NewHTTPFetcher returns a fetcher that enforces the profile's ingest egress
// policy on every redirect hop (not just the initial request) and refuses, at
// dial time, connections to loopback/private/link-local addresses — so a
// hostname that *resolves* to an internal IP (DNS rebinding) is blocked even
// when it passes the name-based policy. Optional auth rules attach per-domain
// credentials (SSO'd sources like Confluence); each rule applies only to its
// own domain, on every hop.
func NewHTTPFetcher(policy config.PolicyConfig, auth ...config.IngestAuth) *HTTPFetcher {
	dialer := &net.Dialer{
		Timeout: 10 * time.Second,
		// Control runs after DNS resolution, once per connection attempt, with
		// the concrete "ip:port" — the only place the resolved IP is knowable.
		Control: func(network, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return fmt.Errorf("refusing dial to unparseable address %q: %w", address, err)
			}
			ip := net.ParseIP(host)
			if ip == nil {
				return fmt.Errorf("refusing dial to non-IP address %q", host)
			}
			if reason := BlockedIPReason(ip); reason != "" {
				return &PolicyError{Host: host, Reason: reason}
			}
			return nil
		},
	}
	var transport http.RoundTripper = &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           dialer.DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          10,
		IdleConnTimeout:       30 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: time.Second,
	}
	if len(auth) > 0 {
		transport = &authHeaderTransport{base: transport, rules: auth}
	}
	client := &http.Client{
		Timeout:   30 * time.Second,
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return fmt.Errorf("stopped after 10 redirects")
			}
			if req.URL.Scheme != "http" && req.URL.Scheme != "https" {
				return fmt.Errorf("refusing redirect to non-http(s) scheme %q", req.URL.Scheme)
			}
			if err := CheckIngestPolicy(policy, req.URL.Hostname()); err != nil {
				return fmt.Errorf("refusing redirect to disallowed host: %w", err)
			}
			return nil
		},
	}
	return &HTTPFetcher{client: client}
}

// Fetch retrieves url and returns the (size-capped) body, treated strictly as
// data. Confluence page URLs are fetched via the Confluence REST API first
// (clean storage HTML, no JS shell), falling back to the plain page. Transient
// failures (network, 429, 5xx) are retried a couple of times; auth walls and
// login redirects produce actionable errors instead of junk notes.
func (f *HTTPFetcher) Fetch(ctx context.Context, url string) (*Document, error) {
	if apiURL, ok := confluenceAPIURL(url); ok {
		if doc, err := f.fetchConfluenceAPI(ctx, apiURL, url); err == nil {
			return doc, nil
		}
		// API refused or unavailable (anonymous instance, odd URL): fall back
		// to the regular page fetch below.
	}

	doc, _, err := f.fetchWithRetry(ctx, url, Validators{})
	return doc, err
}

// FetchConditional implements ConditionalFetcher. The Confluence API path
// is skipped — it never applies to feed URLs, the only recurring callers.
func (f *HTTPFetcher) FetchConditional(ctx context.Context, url string, v Validators) (*Document, bool, error) {
	return f.fetchWithRetry(ctx, url, v)
}

// fetchWithRetry is the bounded retry loop shared by Fetch and
// FetchConditional; a 304 short-circuits as (nil, true, nil).
func (f *HTTPFetcher) fetchWithRetry(ctx context.Context, url string, v Validators) (*Document, bool, error) {
	var lastErr error
	for attempt := 0; attempt <= fetchRetries; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, false, ctx.Err()
			case <-time.After(time.Duration(attempt) * fetchBackoff):
			}
		}
		doc, retryable, err := f.fetchOnce(ctx, url, v)
		if err == nil {
			return doc, false, nil
		}
		if errors.Is(err, errNotModified) {
			return nil, true, nil
		}
		lastErr = err
		if !retryable {
			break
		}
	}
	return nil, false, lastErr
}

// fetchOnce is a single GET; the bool reports whether the failure is transient.
// Non-empty validators make the request conditional (If-None-Match /
// If-Modified-Since); a 304 surfaces as errNotModified.
func (f *HTTPFetcher) fetchOnce(ctx context.Context, url string, v Validators) (*Document, bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, false, err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,text/plain;q=0.8,*/*;q=0.5")
	req.Header.Set("Accept-Language", "en")
	if v.ETag != "" {
		req.Header.Set("If-None-Match", v.ETag)
	}
	if v.LastModified != "" {
		req.Header.Set("If-Modified-Since", v.LastModified)
	}
	resp, err := f.client.Do(req)
	if err != nil {
		return nil, true, fmt.Errorf("fetch %q: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotModified {
		return nil, false, errNotModified
	}

	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return nil, false, fmt.Errorf("fetch %q: status %d — the page requires authentication; add an ingestion.auth entry for this domain (e.g. a Confluence PAT: header Authorization, value \"Bearer <token>\" or \"Basic <base64 email:api-token>\")", url, resp.StatusCode)
	case resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500:
		return nil, true, fmt.Errorf("fetch %q: status %d", url, resp.StatusCode)
	case resp.StatusCode < 200 || resp.StatusCode >= 300:
		return nil, false, fmt.Errorf("fetch %q: status %d", url, resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxFetchBytes))
	if err != nil {
		return nil, true, fmt.Errorf("read %q: %w", url, err)
	}

	// A 200 that is actually a login page (SSO redirect chains end that way)
	// must fail loudly, not become a junk "Log in" note.
	finalURL := url
	if resp.Request != nil && resp.Request.URL != nil {
		finalURL = resp.Request.URL.String()
	}
	if reason := loginPageReason(finalURL, resp.Header.Get("Content-Type"), body); reason != "" {
		return nil, false, fmt.Errorf("fetch %q: %s — add an ingestion.auth entry for this domain (browser sessions are not reused)", url, reason)
	}

	return &Document{
		URL:          url,
		ContentType:  resp.Header.Get("Content-Type"),
		Body:         body,
		FetchedAt:    time.Now().UTC(),
		ETag:         resp.Header.Get("ETag"),
		LastModified: resp.Header.Get("Last-Modified"),
	}, false, nil
}

// loginPageRe spots sign-in shells: a password field or an SSO/login form URL.
var (
	loginURLRe  = regexp.MustCompile(`(?i)/(login|signin|sign-in|sso|saml|authorize|oauth2?)([/?.]|$)`)
	loginBodyRe = regexp.MustCompile(`(?i)(type=["']password["']|log in to continue|sign in to continue|id=["']login|name=["']os_password["'])`)
)

// loginPageReason returns a non-empty explanation when the fetched document is
// a login/SSO page rather than the requested content.
func loginPageReason(finalURL, contentType string, body []byte) string {
	if loginURLRe.MatchString(finalURL) {
		return "redirected to a sign-in page (" + finalURL + ")"
	}
	if strings.Contains(strings.ToLower(contentType), "html") && loginBodyRe.Match(body[:min(len(body), 64<<10)]) {
		return "received a sign-in page instead of the content"
	}
	return ""
}

// confluencePagesRe matches Confluence page URLs that carry a numeric content
// id: /spaces/<KEY>/pages/<id>[/<slug>] (Cloud and recent Server/DC).
var confluencePagesRe = regexp.MustCompile(`^(.*?)/spaces/[^/]+/pages/(\d+)(?:/|$)`)

// confluenceAPIURL maps a Confluence page URL to its REST API content URL, or
// reports false when the URL doesn't carry a page id. The API returns the
// page's clean storage HTML — immune to the JS app shell that makes the
// rendered page extract as empty.
func confluenceAPIURL(raw string) (string, bool) {
	u, err := neturl.Parse(raw)
	if err != nil || u.Host == "" {
		return "", false
	}
	if m := confluencePagesRe.FindStringSubmatch(u.Path); m != nil {
		return u.Scheme + "://" + u.Host + m[1] + "/rest/api/content/" + m[2] + "?expand=body.storage,space", true
	}
	// Server/DC form: /pages/viewpage.action?pageId=<id>
	if strings.HasSuffix(u.Path, "/pages/viewpage.action") {
		id := u.Query().Get("pageId")
		if id != "" && !strings.ContainsFunc(id, func(r rune) bool { return r < '0' || r > '9' }) {
			prefix := strings.TrimSuffix(u.Path, "/pages/viewpage.action")
			return u.Scheme + "://" + u.Host + prefix + "/rest/api/content/" + id + "?expand=body.storage,space", true
		}
	}
	return "", false
}

// fetchConfluenceAPI GETs a Confluence REST content URL and synthesizes an
// HTML document from the page's storage body, so extraction sees real content
// with a real title. Same client, so policy, SSRF guards and per-domain auth
// all apply unchanged.
func (f *HTTPFetcher) fetchConfluenceAPI(ctx context.Context, apiURL, pageURL string) (*Document, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/json")
	resp, err := f.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("confluence api %q: status %d", apiURL, resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxFetchBytes))
	if err != nil {
		return nil, err
	}
	var page struct {
		Title string `json:"title"`
		Body  struct {
			Storage struct {
				Value string `json:"value"`
			} `json:"storage"`
		} `json:"body"`
		Space struct {
			Name string `json:"name"`
		} `json:"space"`
	}
	if err := json.Unmarshal(raw, &page); err != nil {
		return nil, fmt.Errorf("confluence api %q: decode: %w", apiURL, err)
	}
	if strings.TrimSpace(page.Body.Storage.Value) == "" {
		return nil, fmt.Errorf("confluence api %q: empty storage body", apiURL)
	}
	html := "<html><head><title>" + htmlEscape(page.Title) + "</title></head><body><h1>" +
		htmlEscape(page.Title) + "</h1>" + page.Body.Storage.Value + "</body></html>"
	return &Document{
		URL:         pageURL,
		ContentType: "text/html; charset=utf-8",
		Body:        []byte(html),
		FetchedAt:   time.Now().UTC(),
	}, nil
}

func htmlEscape(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")
	return r.Replace(s)
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
