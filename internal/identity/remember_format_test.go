package identity

import (
	"strings"
	"testing"
)

func TestFormatEntryRoundTrip(t *testing.T) {
	e := Entry{Text: "Lives in Tokyo", Kind: "fact", Source: "[[2026-07-05]]", ValidFrom: "2026-07-05"}
	line := FormatEntry(e)
	if line != "- 2026-07-05 — Lives in Tokyo [fact] (source: [[2026-07-05]])" {
		t.Fatalf("FormatEntry = %q", line)
	}
	f, ok := ParseFact(line)
	if !ok || f.Text != e.Text || f.Kind != e.Kind || f.Source != e.Source || f.ValidFrom != e.ValidFrom {
		t.Fatalf("round-trip lost fields: %+v", f)
	}
}

func TestFormatEntryValidFromPreferredOverDate(t *testing.T) {
	// ValidFrom wins as the leading date when both are set.
	line := FormatEntry(Entry{Text: "x", Date: "2026-01-01", ValidFrom: "2026-07-05"})
	if !strings.HasPrefix(line, "- 2026-07-05 — ") {
		t.Fatalf("ValidFrom should be the leading date: %q", line)
	}
}

func TestFormatEntryFallsBackToDate(t *testing.T) {
	// Existing callers set only Date; output must be unchanged.
	line := FormatEntry(Entry{Text: "x", Date: "2026-01-01"})
	if line != "- 2026-01-01 — x" {
		t.Fatalf("Date fallback broken: %q", line)
	}
}

func TestTombstoneIntervalForm(t *testing.T) {
	line := "- 2026-07-05 — Lives in Tokyo [fact] (source: [[2026-07-05]])"
	got := tombstone(line, "2026-08-01", `Lives in "Osaka"`)
	want := `- ~~2026-07-05 — Lives in Tokyo [fact] (source: [[2026-07-05]])~~ (until 2026-08-01; superseded by "Lives in 'Osaka'")`
	if got != want {
		t.Fatalf("tombstone interval form:\n got  %q\n want %q", got, want)
	}
	f, ok := ParseFact(got)
	if !ok || !f.Struck || f.ValidUntil != "2026-08-01" || f.SupersededBy != "Lives in 'Osaka'" {
		t.Fatalf("tombstone did not round-trip: %+v", f)
	}
}

func TestTombstoneLegacyFallback(t *testing.T) {
	// Empty superseded-by keeps the legacy form so hand-authored tombstones and
	// existing tests stay valid.
	got := tombstone("- 2026-06-01 — Prefers Go for daemons (source: session)", "2026-07-05", "")
	want := "- ~~2026-06-01 — Prefers Go for daemons (source: session)~~ (superseded 2026-07-05)"
	if got != want {
		t.Fatalf("legacy fallback:\n got  %q\n want %q", got, want)
	}
}
