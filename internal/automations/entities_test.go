package automations

import (
	"reflect"
	"testing"
)

func TestNormalizeEntity(t *testing.T) {
	if e, ok := normalizeEntity("person", "  Jane   Doe "); !ok || e.Name != "Jane Doe" || e.key() != "person|jane doe" {
		t.Fatalf("normalize = %+v ok=%v", e, ok)
	}
	for _, bad := range []string{"", "x", "  ", "2026", "42"} {
		if _, ok := normalizeEntity("person", bad); ok {
			t.Errorf("normalizeEntity(%q) should be skipped", bad)
		}
	}
	if _, ok := normalizeEntity("place", "Paris"); ok {
		t.Error("unknown type should be skipped")
	}
}

func TestEntityFileNameAndPath(t *testing.T) {
	if got := entityFileName("A/B: C?"); got != "A B C" {
		t.Errorf("entityFileName = %q", got)
	}
	if got := entityPagePath(entityRef{Type: "person", Name: "Jane Doe"}); got != "Entities/People/Jane Doe.md" {
		t.Errorf("person path = %q", got)
	}
	if got := entityPagePath(entityRef{Type: "project", Name: "Phoenix"}); got != "Entities/Projects/Phoenix.md" {
		t.Errorf("project path = %q", got)
	}
}

func TestParseEntities(t *testing.T) {
	ex, err := parseEntities(`prose {"people":["Jane Doe"],"projects":["Phoenix"]} more`)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(ex.People, []string{"Jane Doe"}) || !reflect.DeepEqual(ex.Projects, []string{"Phoenix"}) {
		t.Fatalf("parsed = %+v", ex)
	}
	if _, err := parseEntities("no json here"); err == nil {
		t.Error("garbage should error")
	}
}

func TestScannableNote(t *testing.T) {
	yes := []string{"Daily/2026-06-28.md", "03-Resources/x.md"}
	no := []string{"Entities/People/Jane.md", ".axon/review-queue.md", "03-Resources/README.md", "notes.txt"}
	for _, p := range yes {
		if !scannableNote(p) {
			t.Errorf("%q should be scannable", p)
		}
	}
	for _, p := range no {
		if scannableNote(p) {
			t.Errorf("%q should NOT be scannable", p)
		}
	}
}

func TestCollectEntitiesDedupsAndNormalizes(t *testing.T) {
	got := collectEntities(entityExtract{People: []string{"Jane Doe", "jane doe", ""}, Projects: []string{"Phoenix"}})
	if len(got) != 2 {
		t.Fatalf("collect = %+v, want 2 (deduped person + project)", got)
	}
}
