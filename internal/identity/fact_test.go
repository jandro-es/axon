package identity

import "testing"

func TestParseFact(t *testing.T) {
	tests := []struct {
		name string
		line string
		ok   bool
		want Fact
	}{
		{
			name: "open fact with kind and wikilink source",
			line: "- 2026-07-05 — Lives in Tokyo [fact] (source: [[2026-07-05]])",
			ok:   true,
			want: Fact{Text: "Lives in Tokyo", Kind: "fact", Source: "[[2026-07-05]]", ValidFrom: "2026-07-05"},
		},
		{
			name: "open fact with token source, no kind",
			line: "- 2026-06-01 — Prefers Go for daemons (source: session)",
			ok:   true,
			want: Fact{Text: "Prefers Go for daemons", Source: "session", ValidFrom: "2026-06-01"},
		},
		{
			name: "untyped legacy line (no kind, no source)",
			line: "- 2026-06-01 — An unrelated fact",
			ok:   true,
			want: Fact{Text: "An unrelated fact", ValidFrom: "2026-06-01"},
		},
		{
			name: "closed fact — new interval form",
			line: `- ~~2026-07-05 — Lives in Tokyo~~ (until 2026-08-01; superseded by "Lives in Osaka")`,
			ok:   true,
			want: Fact{Text: "Lives in Tokyo", ValidFrom: "2026-07-05", ValidUntil: "2026-08-01", SupersededBy: "Lives in Osaka", Struck: true},
		},
		{
			name: "closed fact — legacy tombstone form, source inside strike",
			line: "- ~~2026-06-01 — Prefers Go for daemons (source: session)~~ (superseded 2026-07-05)",
			ok:   true,
			want: Fact{Text: "Prefers Go for daemons", Source: "session", ValidFrom: "2026-06-01", ValidUntil: "2026-07-05", Struck: true},
		},
		{
			name: "decision kind",
			line: "- 2026-07-01 — Adopt ADR-028 [decision] (source: reconcile)",
			ok:   true,
			want: Fact{Text: "Adopt ADR-028", Kind: "decision", Source: "reconcile", ValidFrom: "2026-07-01"},
		},
		{
			name: "embedded quotes in superseded-by are preserved verbatim",
			line: `- ~~2026-07-05 — Old~~ (until 2026-08-01; superseded by "New 'quoted' text")`,
			ok:   true,
			want: Fact{Text: "Old", ValidFrom: "2026-07-05", ValidUntil: "2026-08-01", SupersededBy: "New 'quoted' text", Struck: true},
		},
		{
			name: "non-entry line",
			line: "## Memory",
			ok:   false,
		},
		{
			name: "blank line",
			line: "   ",
			ok:   false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := ParseFact(tt.line)
			if ok != tt.ok {
				t.Fatalf("ok = %v, want %v", ok, tt.ok)
			}
			if !ok {
				return
			}
			if got != tt.want {
				t.Fatalf("ParseFact(%q)\n got  %+v\n want %+v", tt.line, got, tt.want)
			}
		})
	}
}
