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
	fmt.Fprintf(&b, "\n<!-- captured %s via /api/capture -->\n", now.Format(time.RFC3339))
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
