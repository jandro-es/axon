package db

import (
	"context"
	"encoding/json"
)

// SessionPendingKey is the automation_state row holding sessions recorded by
// the Stop hook and awaiting distillation (ADR-021). Paths only — transcript
// content never enters the database (NFR-14).
const SessionPendingKey = "session-distill:pending"

// PendingSession is one recorded session awaiting distillation.
type PendingSession struct {
	TranscriptPath string `json:"transcript_path"`
	LastStop       string `json:"last_stop"` // RFC3339
	// Ended marks a SessionEnd-recorded session (FR-104): immediately ready
	// to distill, no idle wait. Sticky; absent in legacy rows (= false).
	Ended bool `json:"ended,omitempty"`
}

// LoadPendingSessions reads the pending-session map (empty on any problem —
// worst case a session is re-recorded on its next Stop).
func LoadPendingSessions(ctx context.Context, q Queryer) (map[string]PendingSession, error) {
	out := map[string]PendingSession{}
	raw, err := GetCursor(ctx, q, SessionPendingKey)
	if err != nil || raw == "" {
		return out, nil
	}
	_ = json.Unmarshal([]byte(raw), &out)
	return out, nil
}

// SavePendingSessions persists the pending-session map.
func SavePendingSessions(ctx context.Context, q Execer, m map[string]PendingSession, updated string) error {
	raw, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return SetCursor(ctx, q, SessionPendingKey, string(raw), updated)
}
