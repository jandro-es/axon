// Package events provides AXON's in-process event bus and structured logger.
// Every run, token, ingest and error is emitted here, then fanned out to the
// dashboard (SSE) and persisted to the events table. Nothing in the system does
// silent work — observability is a requirement, not a nicety.
package events

import "time"

// Level classifies an event's severity, mirroring the slog levels.
type Level string

const (
	LevelInfo  Level = "info"
	LevelWarn  Level = "warn"
	LevelError Level = "error"
)

// Event is a single observable occurrence. It maps onto the events table in
// docs/04 (ts, level, kind, message, data).
type Event struct {
	TS      time.Time      `json:"ts"`
	Level   Level          `json:"level"`
	Kind    string         `json:"kind"`    // e.g. "ingest.done", "automation.run", "token.ledger"
	Message string         `json:"message"` // human-readable summary
	Data    map[string]any `json:"data,omitempty"`
}
