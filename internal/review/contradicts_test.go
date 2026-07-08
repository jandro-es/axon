package review

import (
	"context"
	"strings"
	"testing"

	"github.com/jandro-es/axon/internal/vault"
)

func writeQueue(t *testing.T, v *vault.FS, body string) {
	t.Helper()
	if err := v.RewriteSystemFile(queuePath, body); err != nil {
		t.Fatal(err)
	}
}

func TestLoadContradicts(t *testing.T) {
	v := testVault(t)
	writeQueue(t, v, "## Resurfaced connections\n"+
		"- [ ] contradicts [[Notes/New]] ⚡ [[Notes/Old]] — A says X, B says not-X (sim 0.81)\n")
	items, err := Load(context.Background(), v)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Kind != "contradicts" {
		t.Fatalf("kind = %+v", items)
	}
	if items[0].Note != "Notes/New" || items[0].Target != "Notes/Old" {
		t.Fatalf("note/target = %q/%q", items[0].Note, items[0].Target)
	}
}

func TestAcceptContradictsLinks(t *testing.T) {
	v := testVault(t)
	if _, err := v.Create("Notes/New.md", "---\ntitle: New\n---\n\nbody\n"); err != nil {
		t.Fatal(err)
	}
	writeQueue(t, v, "## S\n- [ ] contradicts [[Notes/New]] ⚡ [[Notes/Old]] — clash (sim 0.81)\n")
	items, _ := Load(context.Background(), v)
	if _, err := Accept(context.Background(), v, items[0].ID); err != nil {
		t.Fatal(err)
	}
	n, _ := v.Read(context.Background(), "Notes/New.md")
	if !strings.Contains(n.Body, "[[Notes/Old]]") {
		t.Fatalf("expected link in axon:links block, got:\n%s", n.Body)
	}
}

func TestOutcomesFromQueueAndArchive(t *testing.T) {
	v := testVault(t)
	writeQueue(t, v, "## S\n"+
		"- [x] resurface [[Notes/Old]] — related to recent [[Notes/New]] (sim 0.80) — ✓ applied 2026-01-05\n"+
		"- [ ] contradicts [[Notes/A]] ⚡ [[Notes/B]] — clash (sim 0.9)\n")
	if err := v.Append(archivePath, "## S\n- [x] contradicts [[Notes/C]] ⚡ [[Notes/D]] — clash (sim 0.7) — ✗ dismissed 2026-01-02\n"); err != nil {
		t.Fatal(err)
	}
	outs, err := Outcomes(context.Background(), v)
	if err != nil {
		t.Fatal(err)
	}
	if len(outs) != 2 {
		t.Fatalf("outcomes = %+v", outs)
	}
	byKey := map[string]Outcome{}
	for _, o := range outs {
		byKey[o.Recent+"|"+o.Dormant] = o
	}
	if o := byKey["Notes/New|Notes/Old"]; !o.Applied || o.Date != "2026-01-05" || o.Kind != "resurface" {
		t.Fatalf("resurface outcome = %+v", o)
	}
	if o := byKey["Notes/C|Notes/D"]; o.Applied || o.Date != "2026-01-02" || o.Kind != "contradicts" {
		t.Fatalf("contradicts outcome = %+v", o)
	}
}
