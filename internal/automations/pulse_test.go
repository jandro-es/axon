package automations

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestParseGoals(t *testing.T) {
	cases := []struct {
		body string
		want []string
	}{
		{"## Now\n- goals: [ship Axon 1.1, learn Rust]\n- people: [[Ada]]\n", []string{"ship Axon 1.1", "learn Rust"}},
		{"- goals: (current objectives)\n", nil},
		{"- goals: []\n", nil},
		{"- goals: [[Axon]], [[Rust]]\n", []string{"Axon", "Rust"}},
		{"no goals line here\n", nil},
	}
	for i, c := range cases {
		if got := parseGoals(c.body); !reflect.DeepEqual(got, c.want) {
			t.Errorf("case %d parseGoals = %#v, want %#v", i, got, c.want)
		}
	}
}

func TestBuildProjectFactsStalenessAndGoals(t *testing.T) {
	now := time.Date(2026, 6, 28, 12, 0, 0, 0, time.UTC)
	updated := map[string]string{
		"01-Projects/Axon.md":     "2026-06-26", // 2d → active
		"01-Projects/Old Idea.md": "2026-06-01", // 27d → stale (3wk)
		"01-Projects/New.md":      "",           // unknown
	}
	facts := buildProjectFacts(
		[]string{"01-Projects/Axon.md", "01-Projects/Old Idea.md", "01-Projects/New.md"},
		updated, []string{"ship Axon 1.1"}, now, pulseStaleDays)

	// Newest-touched first, unknown last.
	if facts[0].Path != "01-Projects/Axon.md" || facts[2].Path != "01-Projects/New.md" {
		t.Fatalf("order wrong: %+v", facts)
	}
	if facts[0].Stale || facts[0].Goal != "ship Axon 1.1" {
		t.Errorf("Axon should be active with goal: %+v", facts[0])
	}
	if !facts[1].Stale || facts[1].DaysAgo/7 != 3 {
		t.Errorf("Old Idea should be stale 3wk: %+v", facts[1])
	}

	text, active, stale := renderProjectFacts(facts)
	if active != 1 || stale != 1 {
		t.Errorf("counts = %d active, %d stale", active, stale)
	}
	if !strings.Contains(text, "⚠ stale 3wk") || !strings.Contains(text, "goal: ship Axon 1.1") {
		t.Errorf("rendered facts wrong:\n%s", text)
	}
}

// pulseVault seeds a project vault: one active project, one stale, USER goals.
func pulseVault() map[string]string {
	return map[string]string{
		"01-Projects/Axon.md":      "---\ntype: project\nupdated: 2026-06-26\n---\nShipping AXON 1.1: project pulse is the last slice.\n",
		"01-Projects/Old Idea.md":  "---\ntype: project\nupdated: 2026-06-01\n---\nAn idea I have not touched in weeks.\n",
		"01-Projects/README.md":    "---\ntype: readme\nupdated: 2026-06-26\n---\nfolder readme, not a project\n",
		"02-Areas/Profile/USER.md": "---\ntype: user\n---\n## Now\n- goals: [ship Axon 1.1]\n- people: [[Ada]]\n",
	}
}

func readReviewQueue(t *testing.T, rc RunCtx) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(rc.Vault.Root(), ".axon", "review-queue.md"))
	if err != nil {
		return ""
	}
	return string(data)
}

func TestProjectPulseWritesBlock(t *testing.T) {
	ctx := context.Background()
	rc, fake := newRC(t, pulseVault())
	mustReindex(t, rc)
	fake.Reply = "AXON is progressing; Old Idea has stalled."

	res, err := ProjectPulse{}.Run(ctx, rc)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Summary, "project pulse written") || res.EstimatedTokens == 0 {
		t.Errorf("summary/est = %q / %d", res.Summary, res.EstimatedTokens)
	}
	n, err := rc.Vault.Read(ctx, pulseNotePath)
	if err != nil {
		t.Fatalf("pulse note not created: %v", err)
	}
	// Human preamble intact; managed block holds narrative + both projects; stale
	// marker present; README excluded.
	if !strings.Contains(n.Body, "never overwrites them") {
		t.Error("human preamble missing")
	}
	for _, want := range []string{"axon:pulse:start", "AXON is progressing", "[[01-Projects/Axon]]", "[[01-Projects/Old Idea]]", "⚠ stale", "goal: ship Axon 1.1"} {
		if !strings.Contains(n.Body, want) {
			t.Errorf("block missing %q:\n%s", want, n.Body)
		}
	}
	if strings.Contains(n.Body, "[[01-Projects/README]]") {
		t.Error("README should not be a project")
	}
}

func TestProjectPulseDegradesOnBudget(t *testing.T) {
	ctx := context.Background()
	rc, _ := newRC(t, pulseVault())
	mustReindex(t, rc)
	rc.BudgetTokens = 1 // force the narrative call to defer

	if _, err := (ProjectPulse{}).Run(ctx, rc); err != nil {
		t.Fatalf("must degrade, not fail: %v", err)
	}
	n, _ := rc.Vault.Read(ctx, pulseNotePath)
	if !strings.Contains(n.Body, "narrative skipped: budget") {
		t.Errorf("degradation marker missing:\n%s", n.Body)
	}
	if !strings.Contains(n.Body, "[[01-Projects/Axon]]") {
		t.Error("facts must still be present under budget pressure")
	}
}

func TestProjectPulseStaleNudgeOnceAndDryRun(t *testing.T) {
	ctx := context.Background()
	rc, fake := newRC(t, pulseVault())
	mustReindex(t, rc)
	fake.Reply = "pulse"

	if _, err := (ProjectPulse{}).Run(ctx, rc); err != nil {
		t.Fatal(err)
	}
	q := readReviewQueue(t, rc)
	if strings.Count(q, "- [ ] pulse: [[01-Projects/Old Idea]]") != 1 {
		t.Fatalf("expected one stale nudge:\n%s", q)
	}
	if strings.Contains(q, "[[01-Projects/Axon]]") {
		t.Error("active project must not be nudged")
	}
	// Second run: proposal memory suppresses a repeat nudge.
	if _, err := (ProjectPulse{}).Run(ctx, rc); err != nil {
		t.Fatal(err)
	}
	if strings.Count(readReviewQueue(t, rc), "Old Idea") != 1 {
		t.Fatalf("stale project re-nudged:\n%s", readReviewQueue(t, rc))
	}
}

func TestProjectPulseDryRunWritesNothing(t *testing.T) {
	ctx := context.Background()
	rc, fake := newRC(t, pulseVault())
	mustReindex(t, rc)
	rc.DryRun = true
	fake.Reply = "pulse"

	res, err := ProjectPulse{}.Run(ctx, rc)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(res.Summary, "would") {
		t.Errorf("dry-run summary = %q", res.Summary)
	}
	if rc.Vault.Exists(pulseNotePath) {
		t.Error("dry-run must not create the pulse note")
	}
	if readReviewQueue(t, rc) != "" {
		t.Error("dry-run must not append nudges")
	}
}

func TestProjectPulseChangeGate(t *testing.T) {
	ctx := context.Background()
	rc, _ := newRC(t, pulseVault())
	mustReindex(t, rc)

	ch, err := ProjectPulse{}.DetectChange(ctx, rc)
	if err != nil || !ch.Changed || ch.Cursor == "" {
		t.Fatalf("first DetectChange = %+v, %v", ch, err)
	}
	rc.LastCursor = ch.Cursor
	ch2, _ := (ProjectPulse{}).DetectChange(ctx, rc)
	if ch2.Changed {
		t.Error("unchanged projects+goals in same week must not re-run")
	}
}

func TestProjectPulseNoProjectsInactive(t *testing.T) {
	ctx := context.Background()
	rc, _ := newRC(t, nil)
	ch, _ := ProjectPulse{}.DetectChange(ctx, rc)
	if ch.Changed {
		t.Error("no projects → feature inactive")
	}
	res, err := (ProjectPulse{}).Run(ctx, rc)
	if err != nil || !strings.Contains(res.Summary, "no projects") {
		t.Errorf("run with no projects = %q, %v", res.Summary, err)
	}
}
