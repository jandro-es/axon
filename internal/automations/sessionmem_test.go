package automations

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jandro-es/axon/internal/config"
	"github.com/jandro-es/axon/internal/db"
	"github.com/jandro-es/axon/internal/identity"
)

// writeTranscript writes fixture JSONL in the verified real shapes.
func writeTranscript(t *testing.T, dir, name string) string {
	t.Helper()
	lines := []string{
		`{"type":"queue-operation","op":"x"}`,
		`{"type":"user","message":{"role":"user","content":"Should we use SQLite or Postgres?"}}`,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"thinking","thinking":"secret reasoning"},{"type":"text","text":"SQLite fits the local-first design; we decided to use it."}]}}`,
		`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","content":"ignored"},{"type":"text","text":"Agreed, SQLite it is."}]}}`,
		`{"type":"ai-title","title":"x"}`,
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func seedPending(t *testing.T, rc RunCtx, id, path string, age time.Duration) {
	t.Helper()
	ctx := context.Background()
	pending, _ := db.LoadPendingSessions(ctx, rc.DB)
	pending[id] = db.PendingSession{
		TranscriptPath: path,
		LastStop:       rc.now().UTC().Add(-age).Format(time.RFC3339),
	}
	if err := db.SavePendingSessions(ctx, rc.DB, pending, rc.now().UTC().Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
}

func TestExtractTranscript(t *testing.T) {
	dir := t.TempDir()
	p := writeTranscript(t, dir, "s.jsonl")
	text, err := extractTranscript(p, 8000)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"User: Should we use SQLite", "Assistant: SQLite fits", "User: Agreed, SQLite it is."} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q in:\n%s", want, text)
		}
	}
	if strings.Contains(text, "secret reasoning") || strings.Contains(text, "ignored") {
		t.Fatalf("thinking/tool content leaked:\n%s", text)
	}
	// Tail cap keeps the newest end.
	capped, _ := extractTranscript(p, 30)
	if !strings.HasSuffix(strings.TrimSpace(text), strings.TrimSpace(capped)) {
		t.Fatalf("cap must keep the tail: %q", capped)
	}
}

func TestParseSessionItems(t *testing.T) {
	items, err := parseSessionItems("- decision: use SQLite\nlesson: WAL enables multi-process reads\n")
	if err != nil || len(items) != 2 || items[0].Kind != "decision" || items[1].Kind != "lesson" {
		t.Fatalf("items = %+v err=%v", items, err)
	}
	if items, err := parseSessionItems("NONE"); err != nil || len(items) != 0 {
		t.Fatalf("NONE: %+v %v", items, err)
	}
	if _, err := parseSessionItems("opinion: tabs are better"); err == nil {
		t.Fatal("bad kind must fail validation")
	}
	if _, err := parseSessionItems(""); err == nil {
		t.Fatal("empty must fail validation")
	}
}

func TestSessionDistillWritesMemory(t *testing.T) {
	rc, fake := newRC(t, nil)
	fake.Reply = "- decision: use SQLite for local-first storage\n- preference: user prefers WAL mode explained tersely"
	p := writeTranscript(t, t.TempDir(), "s.jsonl")
	seedPending(t, rc, "sess-1", p, time.Hour)
	ctx := context.Background()

	ch, err := (SessionDistill{}).DetectChange(ctx, rc)
	if err != nil || !ch.Changed {
		t.Fatalf("detect = %+v err=%v", ch, err)
	}
	res, err := (SessionDistill{}).Run(ctx, rc)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Summary, "2 entr") {
		t.Fatalf("summary = %q", res.Summary)
	}
	entries, _ := identity.RecentEntries(ctx, rc.Vault, 10)
	joined := strings.Join(entries, "\n")
	if !strings.Contains(joined, "use SQLite for local-first storage") || !strings.Contains(joined, "(source: session)") {
		t.Fatalf("memory entries = %v", entries)
	}
	// Once ever: second run has nothing ready.
	ch2, _ := (SessionDistill{}).DetectChange(ctx, rc)
	if ch2.Changed {
		t.Fatalf("session re-considered: %+v", ch2)
	}
	if fake.CallCount() != 1 {
		t.Fatalf("calls = %d, want 1", fake.CallCount())
	}
}

func TestSessionDistillIdleThresholdAndNone(t *testing.T) {
	rc, fake := newRC(t, nil)
	fake.Reply = "NONE"
	dir := t.TempDir()
	fresh := writeTranscript(t, dir, "fresh.jsonl")
	idle := writeTranscript(t, dir, "idle.jsonl")
	seedPending(t, rc, "sess-fresh", fresh, time.Minute) // too recent
	seedPending(t, rc, "sess-idle", idle, 2*time.Hour)
	ctx := context.Background()

	res, err := (SessionDistill{}).Run(ctx, rc)
	if err != nil {
		t.Fatal(err)
	}
	if fake.CallCount() != 1 {
		t.Fatalf("calls = %d, want 1 (fresh session must wait)", fake.CallCount())
	}
	if !strings.Contains(res.Summary, "1 empty") {
		t.Fatalf("summary = %q", res.Summary)
	}
	// The fresh session is still pending.
	pending, _ := db.LoadPendingSessions(ctx, rc.DB)
	if _, ok := pending["sess-fresh"]; !ok {
		t.Fatal("fresh session must remain pending")
	}
	if _, ok := pending["sess-idle"]; ok {
		t.Fatal("idle session must be drained")
	}
}

func TestSessionDistillMissingTranscriptSkipped(t *testing.T) {
	rc, fake := newRC(t, nil)
	seedPending(t, rc, "sess-gone", "/nonexistent/t.jsonl", time.Hour)
	res, err := (SessionDistill{}).Run(context.Background(), rc)
	if err != nil {
		t.Fatal(err)
	}
	if fake.CallCount() != 0 {
		t.Fatal("missing transcript must not reach the model")
	}
	if !strings.Contains(res.Summary, "1 skipped") {
		t.Fatalf("summary = %q", res.Summary)
	}
}

func TestSessionDistillBudgetDeferLeavesPending(t *testing.T) {
	rc, fake := newRC(t, nil)
	// A 1-token per-call cap (ADR-017 budget_tokens wiring) forces a defer.
	rc.BudgetTokens = 1
	p := writeTranscript(t, t.TempDir(), "s.jsonl")
	seedPending(t, rc, "sess-defer", p, time.Hour)

	res, err := (SessionDistill{}).Run(context.Background(), rc)
	if err != nil {
		t.Fatal(err)
	}
	if fake.CallCount() != 0 {
		t.Fatal("deferred call must not reach the agent")
	}
	if !strings.Contains(strings.Join(res.Changes, "\n"), "budget defer") {
		t.Fatalf("changes = %v", res.Changes)
	}
	pending, _ := db.LoadPendingSessions(context.Background(), rc.DB)
	if _, ok := pending["sess-defer"]; !ok {
		t.Fatal("deferred session must remain pending")
	}
}

func TestSessionDistillDryRun(t *testing.T) {
	rc, fake := newRC(t, nil)
	rc.DryRun = true
	p := writeTranscript(t, t.TempDir(), "s.jsonl")
	seedPending(t, rc, "sess-dry", p, time.Hour)
	res, err := (SessionDistill{}).Run(context.Background(), rc)
	if err != nil {
		t.Fatal(err)
	}
	if fake.CallCount() != 0 {
		t.Fatal("dry-run called the model")
	}
	if !strings.Contains(strings.Join(res.Changes, "\n"), "sess-dry") {
		t.Fatalf("changes = %v", res.Changes)
	}
	pending, _ := db.LoadPendingSessions(context.Background(), rc.DB)
	if len(pending) != 1 {
		t.Fatal("dry-run mutated state")
	}
}

func TestSessionDistillRegistered(t *testing.T) {
	p := config.Profile{Automations: map[string]config.Automation{
		"session-distill": {Enabled: true, Schedule: "15 */2 * * *"},
	}}
	if _, err := Get(p, "session-distill"); err != nil {
		t.Fatalf("not registered: %v", err)
	}
	if Purpose("session-distill") == "(no description)" {
		t.Fatal("no catalog purpose")
	}
}
