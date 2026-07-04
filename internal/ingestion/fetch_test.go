package ingestion

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/jandro-es/axon/internal/config"
)

// permissivePolicy mirrors the personal-profile default: wildcard everything.
// The SSRF guards must hold even under this policy.
var permissivePolicy = config.PolicyConfig{
	EgressAllowlist:    []string{"localhost", "*"},
	IngestDomainsAllow: []string{"*"},
}

// TestFetchRefusesLoopbackAtDialTime proves the dial-time IP check: a server
// on 127.0.0.1 must be refused even though the wildcard policy allows the
// name, because the resolved IP is loopback. This is the DNS-rebinding
// defense — the name-based policy never sees the IP, the dialer does.
func TestFetchRefusesLoopbackAtDialTime(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("request must never reach the server")
	}))
	defer srv.Close()

	f := NewHTTPFetcher(permissivePolicy)
	_, err := f.Fetch(context.Background(), srv.URL)
	if err == nil {
		t.Fatal("Fetch to loopback succeeded, want SSRF refusal")
	}
	if !strings.Contains(err.Error(), "SSRF guard") {
		t.Errorf("error should name the SSRF guard, got: %v", err)
	}
}

// TestCheckRedirectRevalidatesPolicy exercises the security control on the
// redirect path directly: every hop is re-checked against the ingest policy,
// non-http(s) schemes are refused, and the hop count is bounded.
func TestCheckRedirectRevalidatesPolicy(t *testing.T) {
	strict := config.PolicyConfig{
		IngestDomainsAllow: []string{"allowed.example.com"},
		IngestDomainsDeny:  []string{"*"},
	}
	f := NewHTTPFetcher(strict)
	check := f.client.CheckRedirect

	mkReq := func(rawurl string) *http.Request {
		req, err := http.NewRequest(http.MethodGet, rawurl, nil)
		if err != nil {
			t.Fatal(err)
		}
		return req
	}
	via := []*http.Request{mkReq("https://allowed.example.com/start")}

	if err := check(mkReq("https://allowed.example.com/next"), via); err != nil {
		t.Errorf("redirect to allowed host refused: %v", err)
	}
	if err := check(mkReq("https://evil.example.net/"), via); err == nil {
		t.Error("redirect to a denied host must be refused (SSRF via redirect)")
	}
	if err := check(mkReq("https://169.254.169.254/latest/meta-data/"), via); err == nil {
		t.Error("redirect to the metadata IP must be refused")
	}
	if err := check(mkReq("file:///etc/passwd"), via); err == nil {
		t.Error("redirect to a non-http(s) scheme must be refused")
	}
	tenHops := make([]*http.Request, 10)
	for i := range tenHops {
		tenHops[i] = mkReq("https://allowed.example.com/hop")
	}
	if err := check(mkReq("https://allowed.example.com/final"), tenHops); err == nil {
		t.Error("more than 10 redirects must be refused")
	}
}

// plainFetcher builds an HTTPFetcher whose client skips the production
// dial-time loopback block, so tests can exercise fetch behaviour (auth,
// retries, login detection) against local httptest servers.
func plainFetcher(rules ...config.IngestAuth) *HTTPFetcher {
	var transport = http.DefaultTransport
	if len(rules) > 0 {
		transport = &authHeaderTransport{base: http.DefaultTransport, rules: rules}
	}
	return &HTTPFetcher{client: &http.Client{Transport: transport}}
}

// TestAuthHeaderScopedToDomain: the configured credential reaches its own
// domain and never any other host (NFR-05 — no cross-site credential leak).
func TestAuthHeaderScopedToDomain(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("Authorization")
		_, _ = w.Write([]byte("<html><body><p>" + strings.Repeat("real content here. ", 20) + "</p></body></html>"))
	}))
	defer srv.Close()
	host := strings.TrimPrefix(srv.URL, "http://")
	hostname, _, _ := strings.Cut(host, ":")

	// Rule matches the server's host → header attached.
	f := plainFetcher(config.IngestAuth{Domain: hostname, Value: "Bearer sekrit-123"})
	if _, err := f.Fetch(context.Background(), srv.URL); err != nil {
		t.Fatal(err)
	}
	if got != "Bearer sekrit-123" {
		t.Errorf("matching domain: Authorization = %q, want the configured value", got)
	}

	// Rule for a DIFFERENT domain → header absent.
	got = ""
	f = plainFetcher(config.IngestAuth{Domain: "confluence.example.com", Value: "Bearer sekrit-123"})
	if _, err := f.Fetch(context.Background(), srv.URL); err != nil {
		t.Fatal(err)
	}
	if got != "" {
		t.Errorf("non-matching domain still received the credential: %q", got)
	}
}

// TestFetchRetriesTransientAndSurfacesAuthErrors: 5xx retries then succeeds;
// 401 fails immediately with an actionable ingestion.auth hint.
func TestFetchRetriesTransientAndSurfacesAuthErrors(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls <= 2 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte("<html><body><p>" + strings.Repeat("finally worked. ", 20) + "</p></body></html>"))
	}))
	defer srv.Close()

	f := plainFetcher()
	if _, err := f.Fetch(context.Background(), srv.URL); err != nil {
		t.Fatalf("expected success after transient 503s, got %v (calls=%d)", err, calls)
	}
	if calls != 3 {
		t.Errorf("calls = %d, want 3 (two 503s then success)", calls)
	}

	var authCalls int
	authSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authCalls++
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer authSrv.Close()
	_, err := f.Fetch(context.Background(), authSrv.URL)
	if err == nil || !strings.Contains(err.Error(), "ingestion.auth") {
		t.Errorf("401 error should point at ingestion.auth, got: %v", err)
	}
	if authCalls != 1 {
		t.Errorf("401 was retried (%d calls); auth failures are not transient", authCalls)
	}
}

// TestFetchDetectsLoginPage: a 200 that is actually a sign-in shell must fail
// loudly instead of becoming a junk note.
func TestFetchDetectsLoginPage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><body><form><input type="password" name="pw"/></form>Log in to continue</body></html>`))
	}))
	defer srv.Close()

	_, err := plainFetcher().Fetch(context.Background(), srv.URL)
	if err == nil || !strings.Contains(err.Error(), "sign-in page") {
		t.Errorf("login shell not detected, err = %v", err)
	}
}

// TestConfluenceAPIURL maps page URLs to REST content URLs (Cloud + DC forms).
func TestConfluenceAPIURL(t *testing.T) {
	tests := []struct {
		in   string
		want string
		ok   bool
	}{
		{"https://acme.atlassian.net/wiki/spaces/ENG/pages/123456/My+Page",
			"https://acme.atlassian.net/wiki/rest/api/content/123456?expand=body.storage,space", true},
		{"https://acme.atlassian.net/wiki/spaces/ENG/pages/123456",
			"https://acme.atlassian.net/wiki/rest/api/content/123456?expand=body.storage,space", true},
		{"https://confluence.corp.example/pages/viewpage.action?pageId=98765",
			"https://confluence.corp.example/rest/api/content/98765?expand=body.storage,space", true},
		{"https://confluence.corp.example/confluence/pages/viewpage.action?pageId=5",
			"https://confluence.corp.example/confluence/rest/api/content/5?expand=body.storage,space", true},
		{"https://example.com/blog/some-article", "", false},
		{"https://acme.atlassian.net/wiki/spaces/ENG/overview", "", false},
	}
	for _, tt := range tests {
		got, ok := confluenceAPIURL(tt.in)
		if ok != tt.ok || got != tt.want {
			t.Errorf("confluenceAPIURL(%q) = (%q, %v), want (%q, %v)", tt.in, got, ok, tt.want, tt.ok)
		}
	}
}

// TestFetchConfluenceViaAPI: a Confluence page URL is served from the REST
// API's storage HTML (title + real content), not the JS app shell.
func TestFetchConfluenceViaAPI(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/wiki/rest/api/content/42", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"title":"Design Doc","body":{"storage":{"value":"<p>` +
			strings.Repeat("The actual page content. ", 20) + `</p>"}},"space":{"name":"ENG"}}`))
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<html><body><div id="app">JavaScript required</div></body></html>`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	pageURL := srv.URL + "/wiki/spaces/ENG/pages/42/Design+Doc"
	doc, err := plainFetcher().Fetch(context.Background(), pageURL)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(doc.Body), "The actual page content") {
		t.Errorf("API storage body not used: %.120s", doc.Body)
	}
	if !strings.Contains(string(doc.Body), "<title>Design Doc</title>") {
		t.Errorf("API title not carried into the document: %.120s", doc.Body)
	}

	ex, err := ExtractHTML(doc.Body, pageURL)
	if err != nil {
		t.Fatal(err)
	}
	if ex.Title != "Design Doc" || !strings.Contains(ex.Markdown, "The actual page content") {
		t.Errorf("extraction from API document failed: title=%q md=%.80s", ex.Title, ex.Markdown)
	}
}

// TestFetchRefusesLocalhostHostname covers the hostname → internal IP path:
// "localhost" passes any name allowlist containing it, but resolves to
// loopback and must be blocked at dial time.
func TestFetchRefusesLocalhostHostname(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("request must never reach the server")
	}))
	defer srv.Close()

	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	localhostURL := "http://localhost:" + u.Port() + "/"

	f := NewHTTPFetcher(permissivePolicy)
	if _, err := f.Fetch(context.Background(), localhostURL); err == nil {
		t.Fatal("Fetch to localhost succeeded, want dial-time SSRF refusal")
	}
}

// TestFetchConditional: first fetch captures validators without sending
// conditional headers; a conditional refetch sends them and maps 304 to
// (nil, notModified, nil) with no retries; plain Fetch never sends them.
func TestFetchConditional(t *testing.T) {
	const lastMod = "Fri, 04 Jul 2026 10:00:00 GMT"
	var calls int
	var gotINM, gotIMS string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		gotINM = r.Header.Get("If-None-Match")
		gotIMS = r.Header.Get("If-Modified-Since")
		if gotINM == `"abc"` {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", `"abc"`)
		w.Header().Set("Last-Modified", lastMod)
		w.Header().Set("Content-Type", "application/rss+xml")
		_, _ = w.Write([]byte(`<?xml version="1.0"?><rss version="2.0"><channel><title>t</title></channel></rss>`))
	}))
	defer srv.Close()
	f := plainFetcher()
	ctx := context.Background()

	// Unconditional (empty validators): no conditional headers, validators captured.
	doc, notModified, err := f.FetchConditional(ctx, srv.URL, Validators{})
	if err != nil || notModified {
		t.Fatalf("first fetch: doc=%v notModified=%v err=%v", doc, notModified, err)
	}
	if gotINM != "" || gotIMS != "" {
		t.Errorf("empty validators must send no conditional headers: INM=%q IMS=%q", gotINM, gotIMS)
	}
	if doc.ETag != `"abc"` || doc.LastModified != lastMod {
		t.Errorf("validators not captured: %q %q", doc.ETag, doc.LastModified)
	}

	// Conditional: headers sent, 304 → notModified, success, NOT retried.
	doc2, nm2, err2 := f.FetchConditional(ctx, srv.URL, Validators{ETag: `"abc"`, LastModified: lastMod})
	if err2 != nil || !nm2 || doc2 != nil {
		t.Fatalf("304: doc=%v notModified=%v err=%v", doc2, nm2, err2)
	}
	if gotIMS != lastMod {
		t.Errorf("If-Modified-Since = %q, want %q", gotIMS, lastMod)
	}
	if calls != 2 {
		t.Errorf("calls = %d, want 2 (304 must not retry)", calls)
	}

	// Plain Fetch: no conditional headers ever.
	if _, err := f.Fetch(ctx, srv.URL); err != nil {
		t.Fatal(err)
	}
	if gotINM != "" || gotIMS != "" {
		t.Errorf("plain Fetch sent conditional headers: INM=%q IMS=%q", gotINM, gotIMS)
	}
}
