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

// KnownKinds is a best-effort list of the event kinds AXON publishes, used to
// warn (never to refuse) about a notify.events subscription that will never
// fire — "automation.failed" for "automation.fail", say.
//
// It is ADVISORY on purpose. Kinds are assembled three ways: literals at the
// emitter (tokens), passed as a parameter with literals at the callers
// (ingestion.Pipeline.emit), and built at runtime from user input (the
// dashboard publishes "review." + action). A static list is therefore correct
// only until the next emitter lands, so it drives a doctor warning rather than
// a config-load refusal — a stale list refusing valid config would be a worse
// failure than the typo it catches.
var KnownKinds = []string{
	"automation.briefing",
	"automation.compaction",
	"automation.fail",
	"automation.heartbeat",
	"automation.resurfacer.contradiction",
	"automation.run",
	"automation.skip",
	"capture.received",
	"action.done",
	"ingest.done",
	"ingest.embed.fail",
	"ingest.embed.skip",
	"ingest.enrich",
	"ingest.skip",
	"review.accept",
	"review.dismiss",
	"run.end",
	"token.defer",
	"token.deny",
	"token.downgrade",
	"token.error",
	"token.ledger",
	"token.unvetted_local",
}

// IsKnownKind reports whether kind appears in the advisory KnownKinds list.
// A false result means "probably a typo", never "invalid".
func IsKnownKind(kind string) bool {
	for _, k := range KnownKinds {
		if k == kind {
			return true
		}
	}
	return false
}
