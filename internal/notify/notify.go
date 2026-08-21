// Package notify pushes selected events to an outbound destination the owner
// named in config (FR-211, ADR-041). Delivery is best-effort by decision: a
// notifier that can break the daemon is worse than no notifier.
package notify

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/jandro-es/axon/internal/events"
	"github.com/jandro-es/axon/internal/ingestion"
)

// maxBodyBytes caps a notification body. A notification is a nudge, not a
// transcript.
const maxBodyBytes = 512

// Note is one outbound notification. Deliberately narrow: Event.Data never
// reaches here, because its fields are arbitrary and emitter-defined.
type Note struct {
	Kind  string
	Level events.Level
	Title string
	Body  string
}

// Notifier delivers a Note. The seam exists so the Companion-local path
// (macOS user notifications, no egress) can land later without touching the
// subscriber, and so tests use a fake rather than a live host.
type Notifier interface {
	Send(ctx context.Context, n Note) error
}

// Ntfy posts to an ntfy topic URL.
type Ntfy struct {
	url      string
	redactor *ingestion.Redactor
	client   *http.Client
}

// NewNtfy builds a sender. A redaction rule that will not compile is a
// construction error: the failure mode of a bad regex must never be "your
// data goes out unfiltered".
func NewNtfy(url string, redactionRules []string) (*Ntfy, error) {
	r, err := ingestion.NewRedactor(redactionRules)
	if err != nil {
		return nil, fmt.Errorf("notify: redaction rules do not compile, refusing to send unredacted: %w", err)
	}
	return &Ntfy{
		url:      url,
		redactor: r,
		client: &http.Client{
			Timeout: 10 * time.Second,
			// The configured URL is the entire allow-list (ADR-041): never
			// follow a redirect to a destination the owner did not name.
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}, nil
}

func (n *Ntfy) Send(ctx context.Context, note Note) error {
	title, _ := n.redactor.Redact(note.Title)
	body, _ := n.redactor.Redact(note.Body)
	if len(body) > maxBodyBytes {
		body = body[:maxBodyBytes] + "…"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, n.url, bytes.NewReader([]byte(body)))
	if err != nil {
		return fmt.Errorf("notify: build request: %w", err)
	}
	req.Header.Set("Title", title)
	req.Header.Set("Content-Type", "text/plain")
	resp, err := n.client.Do(req)
	if err != nil {
		return fmt.Errorf("notify: post: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("notify: destination returned %s", resp.Status)
	}
	return nil
}
