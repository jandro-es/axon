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
