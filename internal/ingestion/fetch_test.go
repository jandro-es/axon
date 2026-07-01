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
