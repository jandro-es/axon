package events

import "testing"

func TestKnownKindsAreWellFormed(t *testing.T) {
	if len(KnownKinds) == 0 {
		t.Fatal("KnownKinds must not be empty")
	}
	seen := map[string]bool{}
	for _, k := range KnownKinds {
		if k == "" {
			t.Fatal("KnownKinds contains an empty string")
		}
		if seen[k] {
			t.Fatalf("KnownKinds contains a duplicate: %q", k)
		}
		seen[k] = true
	}
	// Spot-check kinds real emitters publish today.
	for _, k := range []string{"automation.fail", "automation.run", "token.deny", "ingest.done"} {
		if !IsKnownKind(k) {
			t.Errorf("%q is emitted in the codebase but missing from KnownKinds", k)
		}
	}
	if IsKnownKind("automation.failed") {
		t.Error("IsKnownKind must not recognise a near-miss typo")
	}
	// The list is ADVISORY: it drives a doctor warning, never a refusal, so an
	// unrecognised kind is "probably a typo" rather than "invalid".
	if IsKnownKind("some.future.kind") {
		t.Error("an unlisted kind must not be reported as known")
	}
}
