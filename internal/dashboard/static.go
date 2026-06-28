package dashboard

import (
	"io/fs"
	"net/http"
	"strings"
)

// staticHandler serves the embedded SPA. Unknown non-API paths fall back to
// index.html so client-side routing works. If no embedded assets are present
// (e.g. a dev build without the SPA), a minimal built-in page is served so the
// API is still discoverable.
func (s *Server) staticHandler() http.Handler {
	if s.cfg.Static == nil {
		return http.HandlerFunc(s.fallbackPage)
	}
	fileServer := http.FileServer(http.FS(s.cfg.Static))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/")
		if path == "" {
			path = "index.html"
		}
		if _, err := fs.Stat(s.cfg.Static, path); err != nil {
			// SPA fallback: serve index.html for client routes.
			r2 := r.Clone(r.Context())
			r2.URL.Path = "/"
			http.ServeFileFS(w, r2, s.cfg.Static, "index.html")
			return
		}
		fileServer.ServeHTTP(w, r)
	})
}

// fallbackPage is shown when no SPA build is embedded.
func (s *Server) fallbackPage(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(`<!doctype html><meta charset="utf-8">
<title>AXON dashboard</title>
<style>body{font:14px system-ui;margin:2rem;color:#222;background:#fafafa}</style>
<h1>AXON dashboard</h1>
<p>The SPA build is not embedded in this binary. The API is live:</p>
<ul>
<li><a href="/health">/health</a></li>
<li><a href="/api/usage">/api/usage</a> · <a href="/api/tokens">/api/tokens</a> · <a href="/api/runs">/api/runs</a></li>
<li><a href="/api/vault">/api/vault</a> · <a href="/api/graph">/api/graph</a> · <a href="/api/activity">/api/activity</a></li>
<li><code>/events</code> (SSE live stream)</li>
</ul>`))
}
