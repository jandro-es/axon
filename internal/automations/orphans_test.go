package automations

import (
	"context"
	"strings"
	"testing"
)

func TestOrphanReportRendersBothSections(t *testing.T) {
	rc, _ := newRC(t, map[string]string{
		"03-Resources/Vault Health.md": "# Vault Health\n\nMy own prose.\n",
	})
	seedNote(t, rc, "03-Resources/Zettelkasten.md", "Zettelkasten", "2026-06-20")
	seedNote(t, rc, "03-Resources/Old Idea.md", "Old Idea", "2020-01-01")

	res, err := (OrphanReport{}).Run(context.Background(), rc)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(res.Summary, "orphan") {
		t.Fatalf("summary should name the finding: %q", res.Summary)
	}
	n, err := rc.Vault.Read(context.Background(), "03-Resources/Vault Health.md")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(n.Body, "## Orphans (2)") {
		t.Fatalf("orphan section/count missing:\n%s", n.Body)
	}
	if !strings.Contains(n.Body, "[[03-Resources/Zettelkasten]] (updated 2026-06-20)") {
		t.Fatalf("orphan line shape wrong:\n%s", n.Body)
	}
	if !strings.Contains(n.Body, "## Dormant (1)") {
		t.Fatalf("dormant section/count missing:\n%s", n.Body)
	}
	if !strings.Contains(n.Body, "[[03-Resources/Old Idea]] (updated 2020-01-01)") {
		t.Fatalf("dormant line shape wrong:\n%s", n.Body)
	}
	if !strings.Contains(n.Body, "My own prose.") {
		t.Fatalf("human prose clobbered:\n%s", n.Body)
	}
	if strings.Count(n.Body, "<!-- axon:orphans:end -->") != 1 {
		t.Fatalf("want exactly one end marker:\n%s", n.Body)
	}
}

func TestOrphanReportCreatesStubWhenAbsent(t *testing.T) {
	rc, _ := newRC(t, nil)
	seedNote(t, rc, "03-Resources/Zettelkasten.md", "Zettelkasten", "2026-06-20")
	if _, err := (OrphanReport{}).Run(context.Background(), rc); err != nil {
		t.Fatalf("run: %v", err)
	}
	n, err := rc.Vault.Read(context.Background(), "03-Resources/Vault Health.md")
	if err != nil {
		t.Fatalf("stub not created: %v", err)
	}
	if !strings.Contains(n.Body, "orphan-report") {
		t.Fatalf("stub preamble should name the automation:\n%s", n.Body)
	}
}

func TestOrphanReportChangeGate(t *testing.T) {
	rc, _ := newRC(t, nil)
	seedNote(t, rc, "03-Resources/Zettelkasten.md", "Zettelkasten", "2026-06-20")

	ch, err := (OrphanReport{}).DetectChange(context.Background(), rc)
	if err != nil || !ch.Changed || ch.Cursor == "" {
		t.Fatalf("first detect: %+v err=%v", ch, err)
	}
	rc.LastCursor = ch.Cursor
	again, err := (OrphanReport{}).DetectChange(context.Background(), rc)
	if err != nil {
		t.Fatal(err)
	}
	if again.Changed {
		t.Fatalf("unchanged topology must skip, got %+v", again)
	}
	// A new orphan re-arms the gate.
	seedNote(t, rc, "03-Resources/Another.md", "Another", "2026-06-21")
	rearmed, err := (OrphanReport{}).DetectChange(context.Background(), rc)
	if err != nil {
		t.Fatal(err)
	}
	if !rearmed.Changed {
		t.Fatalf("a new orphan must re-arm the gate, got %+v", rearmed)
	}
}

func TestOrphanReportDryRunWritesNothing(t *testing.T) {
	rc, _ := newRC(t, nil)
	rc.DryRun = true
	seedNote(t, rc, "03-Resources/Zettelkasten.md", "Zettelkasten", "2026-06-20")
	res, err := (OrphanReport{}).Run(context.Background(), rc)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(res.Summary, "would") {
		t.Fatalf("dry-run summary wrong: %q", res.Summary)
	}
	if rc.Vault.Exists("03-Resources/Vault Health.md") {
		t.Fatal("dry-run must not create the report note")
	}
}

func TestOrphanReportTruncates(t *testing.T) {
	rc, _ := newRC(t, nil)
	for i := 0; i < orphanListMax+5; i++ {
		seedNote(t, rc, "03-Resources/Note"+string(rune('A'+i%26))+string(rune('a'+i/26))+".md", "n", "2026-06-20")
	}
	if _, err := (OrphanReport{}).Run(context.Background(), rc); err != nil {
		t.Fatalf("run: %v", err)
	}
	n, _ := rc.Vault.Read(context.Background(), "03-Resources/Vault Health.md")
	if !strings.Contains(n.Body, "…and ") {
		t.Fatalf("a truncated section must say so:\n%s", n.Body)
	}
}

// The dormant section is informational only — this automation never proposes.
func TestOrphanReportNeverTouchesReviewQueue(t *testing.T) {
	rc, _ := newRC(t, nil)
	seedNote(t, rc, "03-Resources/Old Idea.md", "Old Idea", "2020-01-01")
	if _, err := (OrphanReport{}).Run(context.Background(), rc); err != nil {
		t.Fatalf("run: %v", err)
	}
	if rc.Vault.Exists(".axon/review-queue.md") {
		t.Fatal("orphan-report must never write proposals")
	}
}

// FR-205: link-suggester used to walk the vault alphabetically and stop at
// MaxSuggestions, so an orphan late in the alphabet never received proposals.
// Orphans must now come first.
func TestLinkSuggesterVisitsOrphansFirst(t *testing.T) {
	files := map[string]string{
		"Zzz Orphan.md": "quantum entanglement notes about physics and measurement\n",
		"Hub.md":        "the hub note about physics and measurement\n",
	}
	// Several connected notes that sort BEFORE the orphan alphabetically.
	for _, n := range []string{"Aaa", "Bbb", "Ccc", "Ddd"} {
		files[n+".md"] = "physics and measurement notes linking [[Hub]]\n"
	}
	rc, _ := newRC(t, files)
	ctx := context.Background()
	mustReindex(t, rc)

	res, err := (LinkSuggester{MaxSuggestions: 2}).Run(ctx, rc)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	joined := strings.Join(res.Changes, "\n")
	if !strings.Contains(joined, "Zzz Orphan") {
		t.Fatalf("the orphan must be proposed first, got:\n%s\nsummary=%s", joined, res.Summary)
	}
}

// The report note links to every note it lists. Without excluding its own
// edges, the second run would find zero orphans, empty the block, and the
// notes would become orphans again — an oscillation. Found in live smoke.
func TestOrphanReportDoesNotHideItsOwnSubjects(t *testing.T) {
	rc, _ := newRC(t, map[string]string{
		"03-Resources/Zzz Orphan.md": "nothing points here\n",
		"03-Resources/Hub.md":        "see [[Spoke]]\n",
		"03-Resources/Spoke.md":      "back to [[Hub]]\n",
	})
	ctx := context.Background()
	mustReindex(t, rc)
	if _, err := (OrphanReport{}).Run(ctx, rc); err != nil {
		t.Fatalf("first run: %v", err)
	}
	// Reindex so the report note's own wikilinks enter the link graph.
	mustReindex(t, rc)
	if _, err := (OrphanReport{}).Run(ctx, rc); err != nil {
		t.Fatalf("second run: %v", err)
	}
	n, err := rc.Vault.Read(ctx, "03-Resources/Vault Health.md")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(n.Body, "Zzz Orphan") {
		t.Fatalf("the orphan vanished on the second run — the report hid its own subject:\n%s", n.Body)
	}
	if strings.Contains(n.Body, "## Orphans (0)") {
		t.Fatalf("report emptied itself:\n%s", n.Body)
	}
}
