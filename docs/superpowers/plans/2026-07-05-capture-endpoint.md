# Browser Capture Endpoint Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A guarded localhost `POST /api/capture` endpoint (plus a served same-origin `GET /capture` page) that drops a URL/selection into `00-Inbox/`, where the existing capture automation ingests it — no new ingestion path, no browser-triggered model spend.

**Architecture:** Two dashboard handlers behind the existing ADR-020/023 guard (loopback bind + Host guard + `application/json` + a custom `X-Axon-Capture` header that forces a CORS preflight no cross-origin page can pass). `POST /api/capture` decodes `{url,title,text}` and writes a fresh `00-Inbox/capture-<UTC>.md` via the wikilink-safe `vault.FS.Create`, emitting a `capture.received` event; it makes no model call. `GET /capture` serves a small inline HTML page whose JS reads `location.hash` and does the same-origin guarded POST, so a cross-origin bookmarklet can reach the guarded endpoint by navigating to `/capture` (it can only navigate, not read the response or forge the same-origin POST). A `dashboard.capture_enabled` pointer-default-ON toggle gates both routes.

**Tech Stack:** Go 1.26 (`net/http`, stdlib only), `internal/dashboard`, `internal/config`, `internal/vault`, `internal/events`. No new dependencies.

## Global Constraints

- Go 1.26+; `gofmt`/`goimports` clean, `go vet` + `golangci-lint` green.
- Run Go tests with `env -u FORCE_COLOR go test ./...` (ambient `FORCE_COLOR=3` breaks color-sensitive output).
- Cardinal rule 2: no vault mutation that isn't wikilink-safe. Writes go through `vault.FS.Create` only, confined to `00-Inbox/`. No delete, no overwrite.
- Cardinal rule 1: no Claude call. This endpoint makes none; the capture automation's existing egress-policied path handles any eventual fetch.
- Toggle pattern: `*bool` yaml field, pointer-default-ON, `XxxAllowed()` helper returning true when nil.
- Guard template (verbatim shape from `handleAsk`): 404 when disabled/surface-absent; 403 unless `X-Axon-Capture: 1` and `Content-Type: application/json`; `MaxBytesReader` on the body.
- Custom header name: `X-Axon-Capture`, value `1`.
- Inbox dir: `00-Inbox` (matches `internal/automations/capture.go` `inboxDir`).
- Filename stamp: UTC `20060102-150405.000` (millisecond, collision-safe).
- Provisional IDs already committed: FR-121 (`POST /api/capture`), FR-122 (served `/capture` + recipes), ADR-024.

---

### Task 1: `dashboard.capture_enabled` config toggle

**Files:**
- Modify: `internal/config/types.go:244-254` (DashboardConfig)
- Test: `internal/config/types_test.go` (add a test; create if the file is absent — check first with `ls internal/config/*_test.go`)

**Interfaces:**
- Produces: `DashboardConfig.CaptureEnabled *bool` (yaml `capture_enabled,omitempty`) and method `func (d DashboardConfig) CaptureAllowed() bool`.

- [ ] **Step 1: Write the failing test**

Add to `internal/config` a test (in whichever `_test.go` file holds config tests, e.g. `types_test.go`):

```go
func TestCaptureAllowedDefaultsOn(t *testing.T) {
	var d DashboardConfig // CaptureEnabled nil
	if !d.CaptureAllowed() {
		t.Fatal("CaptureAllowed() = false with nil pointer, want true (default-ON)")
	}
	f := false
	d.CaptureEnabled = &f
	if d.CaptureAllowed() {
		t.Fatal("CaptureAllowed() = true with *false, want false")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `env -u FORCE_COLOR go test ./internal/config/ -run TestCaptureAllowedDefaultsOn -v`
Expected: FAIL — `d.CaptureEnabled` / `d.CaptureAllowed` undefined (build error).

- [ ] **Step 3: Add the field and helper**

In `internal/config/types.go`, extend `DashboardConfig` (place `CaptureEnabled` right after `AskEnabled`) and add the helper after `AskAllowed`:

```go
	// AskEnabled gates the browser-triggered ask endpoint (ADR-023). Pointer
	// default-ON: unset = enabled; set false to forbid dashboard token spend.
	AskEnabled *bool `yaml:"ask_enabled,omitempty"`
	// CaptureEnabled gates the browser capture endpoint (ADR-024). Pointer
	// default-ON: unset = enabled; set false to forbid browser vault writes.
	CaptureEnabled *bool `yaml:"capture_enabled,omitempty"`
}

// AskAllowed reports whether the dashboard Ask endpoint is enabled (default true).
func (d DashboardConfig) AskAllowed() bool { return d.AskEnabled == nil || *d.AskEnabled }

// CaptureAllowed reports whether the browser capture endpoint is enabled (default true).
func (d DashboardConfig) CaptureAllowed() bool { return d.CaptureEnabled == nil || *d.CaptureEnabled }
```

- [ ] **Step 4: Run test to verify it passes**

Run: `env -u FORCE_COLOR go test ./internal/config/ -run TestCaptureAllowedDefaultsOn -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/config/types.go internal/config/*_test.go
git commit -m "feat(config): dashboard.capture_enabled toggle (default-ON, FR-121)"
```

---

### Task 2: `POST /api/capture` handler

**Files:**
- Modify: `internal/dashboard/server.go` (add `CaptureEnabled bool` to `Config`; register `POST /api/capture` route)
- Create: `internal/dashboard/capture.go` (the handler — keep server.go focused; server.go only gets the Config field + route registration)
- Test: `internal/dashboard/capture_api_test.go`

**Interfaces:**
- Consumes: `Config.Vault *vault.FS` (existing), `Config.Bus *events.Bus` (existing), `vault.FS.Create(rel, content string) (bool, error)`, `events.Event{Level, Kind, Message, Data}`, `events.LevelInfo`.
- Produces: `Config.CaptureEnabled bool`; `func (s *Server) handleCapture(w http.ResponseWriter, r *http.Request)`; POST response JSON `{"ok": true, "path": "<rel>"}`; package const `inboxDir = "00-Inbox"`.

- [ ] **Step 1: Write the failing test**

Create `internal/dashboard/capture_api_test.go`. This mirrors `ask_api_test.go` but needs no DB/searcher/manager — only a vault and a bus.

```go
package dashboard

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jandro-es/axon/internal/events"
	"github.com/jandro-es/axon/internal/vault"
)

func captureTestServer(t *testing.T, enabled bool) (*httptest.Server, *vault.FS, *events.Bus) {
	t.Helper()
	v := vault.NewFS(t.TempDir())
	bus := events.NewBus()
	s := New(Config{
		Profile: "test", Vault: v, Bus: bus, CaptureEnabled: enabled,
	})
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)
	return srv, v, bus
}

func postCapture(t *testing.T, url string, payload map[string]any, withHeader bool) *http.Response {
	t.Helper()
	body, _ := json.Marshal(payload)
	req, _ := http.NewRequest("POST", url+"/api/capture", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if withHeader {
		req.Header.Set("X-Axon-Capture", "1")
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestCaptureURLHappyPath(t *testing.T) {
	srv, v, _ := captureTestServer(t, true)

	res := postCapture(t, srv.URL, map[string]any{"url": "https://example.com/a", "title": "A"}, true)
	if res.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", res.StatusCode)
	}
	var out struct {
		OK   bool   `json:"ok"`
		Path string `json:"path"`
	}
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if !out.OK || !strings.HasPrefix(out.Path, "00-Inbox/capture-") {
		t.Fatalf("out = %+v", out)
	}
	data, err := os.ReadFile(filepath.Join(v.Root(), filepath.FromSlash(out.Path)))
	if err != nil {
		t.Fatalf("note not written: %v", err)
	}
	if !strings.HasPrefix(string(data), "https://example.com/a\n") {
		t.Fatalf("URL not on its own first line:\n%s", data)
	}
}

func TestCaptureGuardAndToggle(t *testing.T) {
	srv, _, _ := captureTestServer(t, true)
	if res := postCapture(t, srv.URL, map[string]any{"url": "https://x"}, false); res.StatusCode != http.StatusForbidden {
		t.Fatalf("no-header status = %d, want 403", res.StatusCode)
	}
	if res := postCapture(t, srv.URL, map[string]any{}, true); res.StatusCode != http.StatusBadRequest {
		t.Fatalf("empty-payload status = %d, want 400", res.StatusCode)
	}
	off, _, _ := captureTestServer(t, false)
	if res := postCapture(t, off.URL, map[string]any{"url": "https://x"}, true); res.StatusCode != http.StatusNotFound {
		t.Fatalf("disabled status = %d, want 404", res.StatusCode)
	}
}

func TestCaptureTextOnly(t *testing.T) {
	srv, v, _ := captureTestServer(t, true)
	res := postCapture(t, srv.URL, map[string]any{"text": "a clipped paragraph"}, true)
	if res.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", res.StatusCode)
	}
	var out struct {
		Path string `json:"path"`
	}
	_ = json.NewDecoder(res.Body).Decode(&out)
	data, _ := os.ReadFile(filepath.Join(v.Root(), filepath.FromSlash(out.Path)))
	if !strings.Contains(string(data), "a clipped paragraph") {
		t.Fatalf("text missing:\n%s", data)
	}
}

// _ = io keeps the import used by page tests added in Task 3; remove if lint complains
var _ = io.Discard
```

Before running, verify the event bus constructor name: `grep -nE 'func NewBus|func .*Bus.*Publish' internal/events/*.go`. The handler in Step 3 uses only `Bus.Publish` (confirmed in `handleAsk`); the test uses `events.NewBus()` — if the constructor differs, adjust the one call in `captureTestServer`.

- [ ] **Step 2: Run test to verify it fails**

Run: `env -u FORCE_COLOR go test ./internal/dashboard/ -run TestCapture -v`
Expected: FAIL — `CaptureEnabled` field and `handleCapture`/`/api/capture` route undefined (build error).

- [ ] **Step 3: Add the Config field, route, and handler**

In `internal/dashboard/server.go`, add to `Config` (right after `AskEnabled bool`):

```go
	AskEnabled bool
	// CaptureEnabled + Vault power POST /api/capture and GET /capture (ADR-024).
	// A nil Vault or CaptureEnabled=false disables both (404).
	CaptureEnabled bool
```

In `Handler()`, register the route after the `POST /api/ask` line:

```go
	mux.HandleFunc("POST /api/ask", s.handleAsk)
	mux.HandleFunc("POST /api/capture", s.handleCapture)
```

Create `internal/dashboard/capture.go`:

```go
package dashboard

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/jandro-es/axon/internal/events"
)

// inboxDir is where captured notes land; the capture automation ingests from
// here on its next tick (mirrors internal/automations/capture.go).
const inboxDir = "00-Inbox"

// handleCapture writes a captured URL/selection into 00-Inbox/ as a plain
// Markdown note (ADR-024). It makes no model call — the capture automation's
// existing egress-policied path handles any eventual fetch. Guarded identically
// to review/ask actions (loopback + Host guard + JSON content type +
// X-Axon-Capture header force a CORS preflight no cross-origin page can pass)
// and gated by dashboard.capture_enabled.
func (s *Server) handleCapture(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.CaptureEnabled || s.cfg.Vault == nil {
		http.Error(w, "capture is disabled for this profile", http.StatusNotFound)
		return
	}
	if r.Header.Get("X-Axon-Capture") != "1" ||
		!strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	var in struct {
		URL   string `json:"url"`
		Title string `json:"title"`
		Text  string `json:"text"`
	}
	// 16 KB: a selection can be a paragraph.
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16*1024)).Decode(&in); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	in.URL, in.Title, in.Text = strings.TrimSpace(in.URL), strings.TrimSpace(in.Title), strings.TrimSpace(in.Text)
	if in.URL == "" && in.Title == "" && in.Text == "" {
		http.Error(w, "nothing to capture", http.StatusBadRequest)
		return
	}

	now := time.Now().UTC()
	body := captureBody(in.URL, in.Title, in.Text, now)
	rel, err := s.writeCapture(body, now)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if s.cfg.Bus != nil {
		s.cfg.Bus.Publish(events.Event{
			Level: events.LevelInfo, Kind: "capture.received",
			Message: "captured: " + captureLabel(in.URL, in.Title),
			Data:    map[string]any{"profile": s.cfg.Profile, "path": rel},
		})
	}
	writeJSON(w, map[string]any{"ok": true, "path": rel})
}

// writeCapture writes the note to 00-Inbox/capture-<UTC ms>.md, retrying once
// with a suffix on the vanishingly rare collision (Create never overwrites).
func (s *Server) writeCapture(body string, now time.Time) (string, error) {
	stamp := now.Format("20060102-150405.000")
	rel := fmt.Sprintf("%s/capture-%s.md", inboxDir, stamp)
	created, err := s.cfg.Vault.Create(rel, body)
	if err != nil {
		return "", err
	}
	if !created {
		rel = fmt.Sprintf("%s/capture-%s-1.md", inboxDir, stamp)
		if _, err := s.cfg.Vault.Create(rel, body); err != nil {
			return "", err
		}
	}
	return rel, nil
}

// captureBody builds the note: the URL on its own first line (so the capture
// automation's captureURLLine regex matches it), then an H1 and any text.
func captureBody(url, title, text string, now time.Time) string {
	var b strings.Builder
	if url != "" {
		b.WriteString(url)
		b.WriteString("\n\n")
	}
	h1 := title
	if h1 == "" {
		h1 = "Captured note"
	}
	b.WriteString("# ")
	b.WriteString(h1)
	b.WriteString("\n")
	if text != "" {
		b.WriteString("\n")
		b.WriteString(text)
		b.WriteString("\n")
	}
	b.WriteString(fmt.Sprintf("\n<!-- captured %s via /api/capture -->\n", now.Format(time.RFC3339)))
	return b.String()
}

func captureLabel(url, title string) string {
	if title != "" {
		return title
	}
	if url != "" {
		return url
	}
	return "note"
}
```

Note: `events.LevelInfo` is the same constant `handleAsk` uses — confirmed present.

- [ ] **Step 4: Run test to verify it passes**

Run: `env -u FORCE_COLOR go test ./internal/dashboard/ -run TestCapture -v`
Expected: PASS (the three TestCapture* tests).

- [ ] **Step 5: Commit**

```bash
git add internal/dashboard/server.go internal/dashboard/capture.go internal/dashboard/capture_api_test.go
git commit -m "feat(dashboard): POST /api/capture writes URL/selection to 00-Inbox (FR-121, ADR-024)"
```

---

### Task 3: Served `GET /capture` page

**Files:**
- Modify: `internal/dashboard/server.go` (register `GET /capture` route)
- Modify: `internal/dashboard/capture.go` (add `handleCapturePage` + `capturePageHTML`)
- Test: `internal/dashboard/capture_api_test.go` (add page tests)

**Interfaces:**
- Consumes: `Config.CaptureEnabled`, `Config.Vault`.
- Produces: `func (s *Server) handleCapturePage(w http.ResponseWriter, r *http.Request)`; serves `text/html` referencing `/api/capture` and `X-Axon-Capture`.

- [ ] **Step 1: Write the failing test**

Add to `internal/dashboard/capture_api_test.go` (remove the temporary `var _ = io.Discard` line from Task 2 — `io` is now used by `io.ReadAll` below):

```go
func TestCapturePageServed(t *testing.T) {
	srv, _, _ := captureTestServer(t, true)
	res, err := http.Get(srv.URL + "/capture")
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", res.StatusCode)
	}
	if ct := res.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Fatalf("content-type = %q, want text/html", ct)
	}
	body, _ := io.ReadAll(res.Body)
	if !strings.Contains(string(body), "/api/capture") ||
		!strings.Contains(string(body), "X-Axon-Capture") {
		t.Fatalf("page does not reference the guarded POST:\n%s", body)
	}
}

func TestCapturePageDisabled404(t *testing.T) {
	off, _, _ := captureTestServer(t, false)
	res, err := http.Get(off.URL + "/capture")
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("disabled page status = %d, want 404", res.StatusCode)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `env -u FORCE_COLOR go test ./internal/dashboard/ -run TestCapturePage -v`
Expected: FAIL — compile error (`handleCapturePage` undefined once the route is registered) or, before registration, `GET /capture` falls through to the static/SPA handler and the body lacks `/api/capture`.

- [ ] **Step 3: Register route and add the page handler**

In `internal/dashboard/server.go` `Handler()`, add the `GET /capture` route immediately after `POST /api/capture` (Go's `ServeMux` matches the more specific pattern regardless of registration order relative to the `/` catch-all, but placing it here documents intent):

```go
	mux.HandleFunc("POST /api/capture", s.handleCapture)
	mux.HandleFunc("GET /capture", s.handleCapturePage)
```

Add to `internal/dashboard/capture.go`:

```go
// handleCapturePage serves the same-origin capture page (ADR-024). A
// cross-origin bookmarklet opens this URL with the payload in location.hash;
// the page's JS reads the hash and does the guarded same-origin POST to
// /api/capture. Because the page is same-origin with the dashboard, the guarded
// POST succeeds; a cross-origin opener can only navigate here, not read the
// response or forge the same-origin request.
func (s *Server) handleCapturePage(w http.ResponseWriter, _ *http.Request) {
	if !s.cfg.CaptureEnabled || s.cfg.Vault == nil {
		http.Error(w, "capture is disabled for this profile", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(capturePageHTML))
}

// capturePageHTML is a self-contained page: no external assets, no inline
// interpolation of request data (the payload comes from location.hash at
// runtime and is treated as data, never executed — NFR-05).
const capturePageHTML = `<!doctype html><meta charset="utf-8">
<title>AXON capture</title>
<meta name="viewport" content="width=device-width,initial-scale=1">
<style>body{font:15px system-ui;margin:3rem auto;max-width:32rem;color:#222;background:#fafafa;text-align:center}
.ok{color:#137333}.err{color:#c5221f;white-space:pre-wrap;text-align:left}</style>
<h1>AXON capture</h1>
<p id="status">Capturing…</p>
<script>
(function () {
  var status = document.getElementById('status');
  var h = new URLSearchParams((location.hash || '').replace(/^#/, ''));
  var payload = { url: h.get('u') || '', title: h.get('t') || '', text: h.get('s') || '' };
  if (!payload.url && !payload.title && !payload.text) {
    status.className = 'err'; status.textContent = 'Nothing to capture.'; return;
  }
  fetch('/api/capture', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', 'X-Axon-Capture': '1' },
    body: JSON.stringify(payload)
  }).then(function (r) {
    if (r.ok) return r.json();
    return r.text().then(function (t) { throw new Error(t || ('HTTP ' + r.status)); });
  }).then(function (j) {
    status.className = 'ok'; status.textContent = 'Captured ✓ ' + (j.path || '');
    setTimeout(function () { window.close(); }, 800);
  }).catch(function (e) {
    status.className = 'err'; status.textContent = 'Capture failed: ' + e.message;
  });
})();
</script>`
```

- [ ] **Step 4: Run test to verify it passes**

Run: `env -u FORCE_COLOR go test ./internal/dashboard/ -run TestCapture -v`
Expected: PASS (page + disabled + the Task 2 tests).

- [ ] **Step 5: Commit**

```bash
git add internal/dashboard/server.go internal/dashboard/capture.go internal/dashboard/capture_api_test.go
git commit -m "feat(dashboard): served same-origin GET /capture page for bookmarklet (FR-122)"
```

---

### Task 4: Health field + daemon wiring

**Files:**
- Modify: `internal/dashboard/health.go:24` (add `capture_enabled`)
- Modify: `cmd/axon/start_cmd.go` (pass `CaptureEnabled`)
- Test: `internal/dashboard/capture_api_test.go` (assert health field)

**Interfaces:**
- Consumes: `Config.CaptureEnabled`, `DashboardConfig.CaptureAllowed()`.
- Produces: `/health` JSON gains `"capture_enabled": <bool>`.

- [ ] **Step 1: Write the failing test**

Add to `internal/dashboard/capture_api_test.go`:

```go
func TestHealthReportsCaptureEnabled(t *testing.T) {
	srv, _, _ := captureTestServer(t, true)
	res, err := http.Get(srv.URL + "/health")
	if err != nil {
		t.Fatal(err)
	}
	var h map[string]any
	if err := json.NewDecoder(res.Body).Decode(&h); err != nil {
		t.Fatal(err)
	}
	if v, ok := h["capture_enabled"].(bool); !ok || !v {
		t.Fatalf("health capture_enabled = %v (ok=%v), want true", h["capture_enabled"], ok)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `env -u FORCE_COLOR go test ./internal/dashboard/ -run TestHealthReportsCaptureEnabled -v`
Expected: FAIL — `capture_enabled` absent from health map (nil, type assertion `ok=false`).

- [ ] **Step 3: Add the health field and wire the daemon**

In `internal/dashboard/health.go`, after the `ask_enabled` line:

```go
	out["ask_enabled"] = s.cfg.AskEnabled
	out["capture_enabled"] = s.cfg.CaptureEnabled
```

In `cmd/axon/start_cmd.go`, in the `dashboard.New(dashboard.Config{...})` literal, after the `AskEnabled:` line:

```go
			AskEnabled:     deps.profile.Dashboard.AskAllowed(),
			CaptureEnabled: deps.profile.Dashboard.CaptureAllowed(),
```

- [ ] **Step 4: Run tests + build to verify they pass**

Run: `env -u FORCE_COLOR go build ./... && env -u FORCE_COLOR go test ./internal/dashboard/`
Expected: build clean; dashboard package PASS (including `TestHealthReportsCaptureEnabled`).

- [ ] **Step 5: Commit**

```bash
git add internal/dashboard/health.go cmd/axon/start_cmd.go internal/dashboard/capture_api_test.go
git commit -m "feat(dashboard): health capture_enabled + daemon wiring (FR-121)"
```

---

### Task 5: Docs — config reference, GUIDE recipes, example, roadmap, changelog

**Files:**
- Modify: `axon.config.example.yaml:62` (add commented `capture_enabled`)
- Modify: `docs/04-data-model-and-config.md` (document `dashboard.capture_enabled`)
- Modify: `docs/GUIDE.md` (add "Capture from anywhere" section)
- Modify: `docs/14-roadmap-1.1.md` (mark D1 built)
- Modify: `CHANGELOG.md` (entry)

No code; no tests. Verify by grep, not `go test`.

- [ ] **Step 1: Example config**

In `axon.config.example.yaml`, under the personal-profile `dashboard:` block, after the `ask_enabled` comment line (~line 62):

```yaml
      # ask_enabled: true                     # dashboard Ask panel / POST /api/ask (ADR-023); set false to forbid browser-triggered token spend
      # capture_enabled: true                 # browser capture: POST /api/capture + served /capture page (ADR-024); set false to forbid browser vault writes
```

- [ ] **Step 2: Config reference**

In `docs/04-data-model-and-config.md`, find the dashboard section (`grep -n ask_enabled docs/04-data-model-and-config.md`) and add a sibling entry for `capture_enabled`, matching the surrounding format:

> `dashboard.capture_enabled` (bool, default `true`) — gates the browser capture endpoint (`POST /api/capture`) and the served `/capture` page (ADR-024). When on, a bookmarklet or macOS Shortcut can drop a URL/selection into `00-Inbox/`, where the capture automation ingests it. Set `false` to forbid all browser-triggered vault writes. Writes are non-destructive (`00-Inbox/` only, never overwrite) and make no model call.

- [ ] **Step 3: GUIDE "Capture from anywhere" section**

In `docs/GUIDE.md`, near the ingestion/capture material (`grep -n '00-Inbox\|[Cc]apture' docs/GUIDE.md`), add:

````markdown
### Capture from anywhere (browser + Shortcuts)

With the dashboard running (`dashboard.capture_enabled` is on by default), you
can drop a page or selection straight into your inbox. AXON writes it to
`00-Inbox/` and the capture automation ingests it on its next tick — no model
call happens at capture time.

**Bookmarklet.** Create a new bookmark with this as the URL (adjust the port if
you changed `dashboard.port`). Clicking it on any page opens a small AXON tab
that captures the URL, title, and any selected text, then closes itself:

```
javascript:(function(){var p=7777;window.open('http://127.0.0.1:'+p+'/capture#u='+encodeURIComponent(location.href)+'&t='+encodeURIComponent(document.title)+'&s='+encodeURIComponent((''+getSelection()).slice(0,2000)));})()
```

The bookmarklet opens AXON's own `/capture` page, which performs the actual
guarded request same-origin — an arbitrary web page cannot POST to the endpoint
itself (the guard requires a same-origin request with a custom header).

**macOS Shortcuts / curl.** For a share-sheet Shortcut, add a *Get Contents of
URL* action:

- URL: `http://127.0.0.1:7777/api/capture`
- Method: `POST`
- Headers: `Content-Type: application/json`, `X-Axon-Capture: 1`
- Request Body (JSON): `{ "url": <Shortcut Input> }`

The same request from a terminal:

```bash
curl -sS http://127.0.0.1:7777/api/capture \
  -H 'Content-Type: application/json' -H 'X-Axon-Capture: 1' \
  -d '{"url":"https://example.com/article","title":"Something to read"}'
```

Set `dashboard.capture_enabled: false` to turn the endpoint off entirely.
````

- [ ] **Step 4: Mark D1 built in the roadmap**

Run `grep -n 'D1' docs/14-roadmap-1.1.md` to locate the slice, then append ` *(built)*` to the D1 heading/row exactly as A1 and A2 were annotated (`grep -n 'built' docs/14-roadmap-1.1.md` shows the existing style).

- [ ] **Step 5: Changelog**

In `CHANGELOG.md`, under the current unreleased/1.1 section (match existing format):

```markdown
- **Browser capture endpoint (FR-121/122, ADR-024).** `POST /api/capture` and a
  served same-origin `/capture` page drop a URL/selection into `00-Inbox/` for
  the capture automation to ingest — guarded like review/ask actions, gated by
  `dashboard.capture_enabled`, no model call. Ships a bookmarklet + macOS
  Shortcuts recipe.
```

- [ ] **Step 6: Verify docs and commit**

Run: `grep -rn 'capture_enabled' axon.config.example.yaml docs/04-data-model-and-config.md docs/GUIDE.md` — expect a hit in each.
Run: `grep -in 'd1' docs/14-roadmap-1.1.md | grep -i built` — expect the D1 line marked.

```bash
git add axon.config.example.yaml docs/04-data-model-and-config.md docs/GUIDE.md docs/14-roadmap-1.1.md CHANGELOG.md
git commit -m "docs: browser capture endpoint — config, GUIDE recipes, roadmap D1 built, changelog (FR-121/122)"
```

---

### Task 6: Full suite + live smoke

**Files:** none (verification only).

- [ ] **Step 1: Full test suite**

Run: `env -u FORCE_COLOR go test ./...`
Expected: all packages PASS. (No MCP tool added this cycle, so `filter_test.go`/`server_test.go` tool-count assertions are unaffected.)

- [ ] **Step 2: Vet + lint**

Run: `env -u FORCE_COLOR go vet ./... && golangci-lint run ./internal/dashboard/... ./internal/config/...`
Expected: clean.

- [ ] **Step 3: Live smoke in a scratch env**

Build and run the daemon against an isolated `AXON_HOME`, then exercise the endpoint end-to-end. Use the scratchpad dir (established capture-smoke pattern). Do not attempt `rm -rf` cleanup — the GateGuard hook blocks it and the scratch dir is ephemeral `/private/tmp`.

```bash
SMOKE=/private/tmp/claude-501/-Users-jandro-Projects-axon/84f7638b-ccf6-4b6b-872c-136d5674130c/scratchpad/capture-smoke
mkdir -p "$SMOKE"
env -u FORCE_COLOR go build -o "$SMOKE/axon" ./cmd/axon
# init an isolated profile following the pattern used in prior smokes
# (AXON_HOME="$SMOKE/home" "$SMOKE/axon" init ... ), then start the daemon:
# AXON_HOME="$SMOKE/home" "$SMOKE/axon" start   (run in background)
```

With the daemon up:

```bash
# guarded POST succeeds
curl -sS -o /dev/null -w '%{http_code}\n' http://127.0.0.1:7777/api/capture \
  -H 'Content-Type: application/json' -H 'X-Axon-Capture: 1' \
  -d '{"url":"https://go.dev/blog/","title":"The Go Blog"}'     # expect 200

# header-less request is rejected
curl -sS -o /dev/null -w '%{http_code}\n' http://127.0.0.1:7777/api/capture \
  -H 'Content-Type: application/json' -d '{"url":"https://x"}'  # expect 403

# served page references the guarded POST
curl -sS http://127.0.0.1:7777/capture | grep -c 'X-Axon-Capture' # expect >=1

# the note landed in 00-Inbox (path layout: confirm the vault dir under AXON_HOME first)
find "$SMOKE/home" -path '*00-Inbox/capture-*.md'                 # expect a file
```

Confirm the captured note has `https://go.dev/blog/` on its own first line and the `<!-- captured … via /api/capture -->` marker. Stop the daemon.

- [ ] **Step 4: Report results**

Report the smoke outcomes (status codes, note path, note contents) to the user before finishing the branch.

- [ ] **Step 5: Finish the branch**

**REQUIRED SUB-SKILL:** Use superpowers:finishing-a-development-branch. Standing choice (memory `feature-cycle-workflow`): **merge to main + push**. Verify the full suite is green (Step 1), then `--no-ff` merge `feature/capture-endpoint` into `main`, push, delete the branch. After merging, update the `feature-cycle-workflow` memory with the D1 completion + next roadmap pick (B1 per build order).

---

## Self-review notes

- **Spec coverage:** FR-121 → Tasks 1, 2, 4 (toggle, handler, health, wiring). FR-122 → Tasks 3, 5 (served page, bookmarklet/Shortcuts docs). ADR-024 write-envelope + guard → Tasks 2–3. Docs (config, GUIDE, roadmap, changelog) → Task 5. Every spec "Testing" bullet maps to a test in Task 2/3/4.
- **Guard shape** matches `handleAsk` verbatim (404 disabled → 403 header/CT → 400 empty → write). No CORS headers are ever sent, so the custom-header preflight cannot succeed cross-origin.
- **Wikilink safety:** only `vault.FS.Create`; confined to `00-Inbox/`; never overwrites (collision retry with `-1` suffix).
- **No new MCP tool** → MCP count assertions untouched.
- **Execution-time checks:** (1) event bus constructor name in the test (`grep` in Task 2 Step 1); (2) the vault path layout under `AXON_HOME` for the smoke `find` (Task 6 Step 3). Both are noted inline.
- **Type consistency:** `CaptureEnabled *bool`/`CaptureAllowed()` (config) vs `CaptureEnabled bool` (dashboard.Config) are deliberately different types — the daemon converts via `CaptureAllowed()` in Task 4. `handleCapture`, `handleCapturePage`, `writeCapture`, `captureBody`, `captureLabel`, `inboxDir`, `capturePageHTML` names are consistent across Tasks 2–3.
