package automations

import (
	"reflect"
	"testing"
)

func TestMemoryEntryText(t *testing.T) {
	cases := map[string]string{
		"- 2026-06-01 — Prefers Go for daemons [preference] (source: session)": "Prefers Go for daemons",
		"- 2026-06-01 — Keep the store brute-force":                            "Keep the store brute-force",
		"- 2026-07-05 — Uses Rust (source: reconcile)":                         "Uses Rust",
	}
	for in, want := range cases {
		if got := memoryEntryText(in); got != want {
			t.Errorf("memoryEntryText(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseDistillOutput(t *testing.T) {
	existing := []string{"Prefers Go for daemons", "Lives in Madrid"}

	newFacts, conflicts := parseDistillOutput(
		"- A fresh durable fact\nCONFLICT 1: Uses Rust for daemons\n",
		existing,
	)
	if !reflect.DeepEqual(newFacts, []string{"A fresh durable fact"}) {
		t.Errorf("newFacts = %v", newFacts)
	}
	if len(conflicts) != 1 || conflicts[0].New != "Uses Rust for daemons" || conflicts[0].Old != "Prefers Go for daemons" {
		t.Errorf("conflicts = %+v", conflicts)
	}

	// A fact echoed as both a bullet and a CONFLICT is handled once (as a conflict).
	nf, cf := parseDistillOutput("- Uses Rust for daemons\nCONFLICT 1: Uses Rust for daemons\n", existing)
	if len(nf) != 0 {
		t.Errorf("duplicate new fact not deduped: %v", nf)
	}
	if len(cf) != 1 {
		t.Errorf("expected 1 conflict, got %+v", cf)
	}

	// Out-of-range and garbage CONFLICT lines are ignored; NONE yields nothing.
	nf2, cf2 := parseDistillOutput("CONFLICT 9: nope\nCONFLICT x: bad\nNONE\n", existing)
	if len(nf2) != 0 || len(cf2) != 0 {
		t.Errorf("out-of-range/garbage not ignored: nf=%v cf=%+v", nf2, cf2)
	}
}
