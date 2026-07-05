package automations

import (
	"context"
	"reflect"
	"strings"
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

func TestMentionLineAndHasTarget(t *testing.T) {
	line := mentionLine("03-Resources/note-a", "2026-06-28")
	if line != "- [[03-Resources/note-a]] (2026-06-28)" {
		t.Fatalf("mentionLine = %q", line)
	}
	block := "- [[03-Resources/note-a]] (2026-06-28)\n- [[Daily/x]] (2026-06-28)"
	if !mentionHasTarget(block, "03-Resources/note-a") {
		t.Error("should find existing target")
	}
	if mentionHasTarget(block, "03-Resources/note-b") {
		t.Error("should not find absent target")
	}
}

func TestMaterializeAndAppendMention(t *testing.T) {
	ctx := context.Background()
	rc, _ := newRC(t, nil)
	e := entityRef{Type: "person", Name: "Jane Doe"}
	if err := materializeEntity(ctx, rc, e, []string{"Daily/2026-06-27", "03-Resources/x"}, "2026-06-28"); err != nil {
		t.Fatal(err)
	}
	n, err := rc.Vault.Read(ctx, entityPagePath(e))
	if err != nil {
		t.Fatalf("page not created: %v", err)
	}
	if !strings.Contains(n.Body, "axon:mentions:start") ||
		!strings.Contains(n.Body, "[[Daily/2026-06-27]]") ||
		!strings.Contains(n.Body, "[[03-Resources/x]]") {
		t.Fatalf("page body wrong:\n%s", n.Body)
	}
	if n.Frontmatter["entity_type"] != "person" {
		t.Fatalf("entity_type frontmatter = %v", n.Frontmatter["entity_type"])
	}
	// Append a new mention → added; re-append same → dedup (not added).
	added, err := appendMention(ctx, rc, entityPagePath(e), "03-Resources/y", "2026-06-28")
	if err != nil || !added {
		t.Fatalf("append new = %v,%v", added, err)
	}
	added2, err := appendMention(ctx, rc, entityPagePath(e), "03-Resources/y", "2026-06-28")
	if err != nil || added2 {
		t.Fatalf("re-append should dedup: %v,%v", added2, err)
	}
	n, _ = rc.Vault.Read(ctx, entityPagePath(e))
	if strings.Count(n.Body, "[[03-Resources/y]]") != 1 {
		t.Fatalf("duplicate mention written:\n%s", n.Body)
	}
}

func TestPendingEntitiesRoundtrip(t *testing.T) {
	ctx := context.Background()
	rc, _ := newRC(t, nil)
	in := map[string]pendingEntity{"person|jane doe": {Type: "person", Name: "Jane Doe", Sources: []string{"a", "b"}}}
	savePendingEntities(ctx, rc, in)
	out := loadPendingEntities(ctx, rc)
	if len(out) != 1 || out["person|jane doe"].Name != "Jane Doe" || len(out["person|jane doe"].Sources) != 2 {
		t.Fatalf("roundtrip = %+v", out)
	}
}
