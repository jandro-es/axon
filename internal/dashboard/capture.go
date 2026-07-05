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
