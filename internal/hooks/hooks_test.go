package hooks

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jandro-es/axon/internal/agent"
	"github.com/jandro-es/axon/internal/config"
	"github.com/jandro-es/axon/internal/db"
	"github.com/jandro-es/axon/internal/identity"
	"github.com/jandro-es/axon/internal/search"
	"github.com/jandro-es/axon/internal/tokens"
	"github.com/jandro-es/axon/internal/vault"
)

func testDeps(t *testing.T) (Deps, *agent.Fake) {
	t.Helper()
	d, err := db.Open(db.MemoryDSN)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	if _, err := db.Migrate(d); err != nil {
		t.Fatal(err)
	}
	fake := agent.NewFake()
	mgr := tokens.New(d, fake, search.New(d, nil), nil, tokens.Config{
		Profile: "test", AuthMode: "subscription",
		Limits: config.LimitsConfig{DailyTokens: 1000, WeeklyTokens: 5000, GuardPauseAtPct: 80},
	})
	return Deps{Profile: "test", DB: d, Vault: vault.NewFS(t.TempDir()), Manager: mgr}, fake
}

func TestSessionStartInjectsStatusNoModel(t *testing.T) {
	deps, fake := testDeps(t)
	// Seed an inbox item.
	_, _ = deps.Vault.Create("00-Inbox/thought.md", "a captured idea")

	res, err := Handle(context.Background(), SessionStart, []byte(`{"hook_event_name":"SessionStart"}`), deps)
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		HookSpecificOutput struct {
			HookEventName     string `json:"hookEventName"`
			AdditionalContext string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal(res.Stdout, &out); err != nil {
		t.Fatalf("output not valid JSON: %v\n%s", err, res.Stdout)
	}
	ctx := out.HookSpecificOutput.AdditionalContext
	if !strings.Contains(ctx, "Budget:") || !strings.Contains(ctx, "Inbox: 1") {
		t.Errorf("status block missing budget/inbox:\n%s", ctx)
	}
	if !strings.Contains(ctx, "vault_move") {
		t.Error("status should remind about wikilink-safe conventions")
	}
	if fake.CallCount() != 0 {
		t.Error("SessionStart made a model call (must be cheap, no model)")
	}
}

func TestPreToolUseBlocksAndAllows(t *testing.T) {
	deps, _ := testDeps(t)
	tests := []struct {
		name     string
		input    Input
		wantDeny bool
	}{
		{"rm note", Input{ToolName: "Bash", ToolInput: map[string]any{"command": "rm 01-Projects/x.md"}}, true},
		{"unlink", Input{ToolName: "Bash", ToolInput: map[string]any{"command": "unlink notes/a.md"}}, true},
		{"raw mv note", Input{ToolName: "Bash", ToolInput: map[string]any{"command": "mv a.md b.md"}}, true},
		{"mv without .md", Input{ToolName: "Bash", ToolInput: map[string]any{"command": "mv 02-Areas/old 04-Archive/old"}}, true},
		{"git mv", Input{ToolName: "Bash", ToolInput: map[string]any{"command": "git mv a.md b.md"}}, true},
		{"find -delete", Input{ToolName: "Bash", ToolInput: map[string]any{"command": "find . -name '*.md' -delete"}}, true},
		{"find -exec rm", Input{ToolName: "Bash", ToolInput: map[string]any{"command": "find Daily -name '*.md' -exec rm {} +"}}, true},
		{"shred", Input{ToolName: "Bash", ToolInput: map[string]any{"command": "shred -u secret.md"}}, true},
		{"truncate", Input{ToolName: "Bash", ToolInput: map[string]any{"command": "truncate -s0 note.md"}}, true},
		{"redirect overwrite md", Input{ToolName: "Bash", ToolInput: map[string]any{"command": "echo hi > 01-Projects/x.md"}}, true},
		{"redirect append md", Input{ToolName: "Bash", ToolInput: map[string]any{"command": "cat a >> notes/b.md"}}, true},
		{"dd of=", Input{ToolName: "Bash", ToolInput: map[string]any{"command": "dd if=/dev/zero of=note.md bs=1"}}, true},
		{"write into .obsidian", Input{ToolName: "Write", ToolInput: map[string]any{"file_path": ".obsidian/app.json"}}, true},
		{"write into .OBSIDIAN (case)", Input{ToolName: "Write", ToolInput: map[string]any{"file_path": "vault/.OBSIDIAN/app.json"}}, true},
		{"write into .git via ..", Input{ToolName: "Edit", ToolInput: map[string]any{"file_path": "a/b/../../.git/config"}}, true},
		{"safe ls", Input{ToolName: "Bash", ToolInput: map[string]any{"command": "ls -la"}}, false},
		{"safe grep", Input{ToolName: "Bash", ToolInput: map[string]any{"command": "grep -r foo 01-Projects"}}, false},
		{"normal note edit", Input{ToolName: "Edit", ToolInput: map[string]any{"file_path": "01-Projects/x.md"}}, false},
		{"redirect to non-md", Input{ToolName: "Bash", ToolInput: map[string]any{"command": "echo hi > /tmp/scratch.txt"}}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw, _ := json.Marshal(tt.input)
			res, err := Handle(context.Background(), PreToolUse, raw, deps)
			if err != nil {
				t.Fatal(err)
			}
			denied := strings.Contains(string(res.Stdout), `"permissionDecision":"deny"`)
			if denied != tt.wantDeny {
				t.Errorf("deny = %v, want %v (output: %s)", denied, tt.wantDeny, res.Stdout)
			}
		})
	}
}

// TestPreToolUseBlocksNativeWriteOverwrite: Claude Code's built-in Write tool
// is a whole-file overwrite — exactly the operation vault_write refuses on
// existing notes — so the hook must deny it deterministically for existing
// vault notes while still allowing new-note creation and surgical Edits.
func TestPreToolUseBlocksNativeWriteOverwrite(t *testing.T) {
	deps, _ := testDeps(t)
	if _, err := deps.Vault.Create("01-Projects/existing.md", "human prose here"); err != nil {
		t.Fatal(err)
	}
	root := deps.Vault.Root()

	tests := []struct {
		name     string
		input    Input
		wantDeny bool
	}{
		{"Write over existing vault note (abs path)",
			Input{ToolName: "Write", ToolInput: map[string]any{"file_path": filepath.Join(root, "01-Projects", "existing.md")}}, true},
		{"Write over existing vault note (cwd-relative)",
			Input{ToolName: "Write", CWD: root, ToolInput: map[string]any{"file_path": "01-Projects/existing.md"}}, true},
		{"Write NEW vault note allowed",
			Input{ToolName: "Write", ToolInput: map[string]any{"file_path": filepath.Join(root, "01-Projects", "brand-new.md")}}, false},
		{"Edit existing vault note allowed (surgical)",
			Input{ToolName: "Edit", ToolInput: map[string]any{"file_path": filepath.Join(root, "01-Projects", "existing.md")}}, false},
		{"Write .md outside the vault allowed",
			Input{ToolName: "Write", ToolInput: map[string]any{"file_path": filepath.Join(t.TempDir(), "outside.md")}}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw, _ := json.Marshal(tt.input)
			res, err := Handle(context.Background(), PreToolUse, raw, deps)
			if err != nil {
				t.Fatal(err)
			}
			denied := strings.Contains(string(res.Stdout), `"permissionDecision":"deny"`)
			if denied != tt.wantDeny {
				t.Errorf("deny = %v, want %v (output: %s)", denied, tt.wantDeny, res.Stdout)
			}
		})
	}
}

func TestPostToolUseAndStopAreAdvisory(t *testing.T) {
	deps, _ := testDeps(t)
	for _, ev := range []string{PostToolUse, Stop} {
		res, err := Handle(context.Background(), ev, []byte(`{}`), deps)
		if err != nil {
			t.Fatalf("%s: %v", ev, err)
		}
		if res.ExitCode != 0 {
			t.Errorf("%s exit = %d, want 0 (advisory, never blocks)", ev, res.ExitCode)
		}
	}
}

// sessionContext runs SessionStart and returns the injected additionalContext.
func sessionContext(t *testing.T, deps Deps) string {
	t.Helper()
	res, err := Handle(context.Background(), SessionStart, []byte(`{"hook_event_name":"SessionStart"}`), deps)
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		HookSpecificOutput struct {
			AdditionalContext string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal(res.Stdout, &out); err != nil {
		t.Fatalf("output not valid JSON: %v", err)
	}
	return out.HookSpecificOutput.AdditionalContext
}

func TestSessionStartInjectsIdentityNoModel(t *testing.T) {
	deps, fake := testDeps(t)
	if _, err := identity.Generate(deps.Vault, identity.Values{Name: "Jandro", AgentName: "Axon", Date: "2026-06-28"}); err != nil {
		t.Fatal(err)
	}
	if _, err := identity.Remember(context.Background(), deps.Vault, identity.Entry{Text: "Prefers Go", Date: "2026-06-28"}); err != nil {
		t.Fatal(err)
	}
	ctx := sessionContext(t, deps)
	for _, want := range []string{"AXON profile", "Jandro", "Prefers Go"} {
		if !strings.Contains(ctx, want) {
			t.Errorf("injection missing %q:\n%s", want, ctx)
		}
	}
	if fake.CallCount() != 0 {
		t.Error("identity injection must make no model call")
	}
}

func TestSessionStartRespectsInjectOff(t *testing.T) {
	deps, _ := testDeps(t)
	if _, err := identity.Generate(deps.Vault, identity.Values{Name: "Jandro", Date: "2026-06-28"}); err != nil {
		t.Fatal(err)
	}
	off := false
	deps.Memory.Inject = &off
	if ctx := sessionContext(t, deps); strings.Contains(ctx, "AXON profile") {
		t.Errorf("inject:false should suppress identity injection:\n%s", ctx)
	}
}

func TestSessionStartRedactsInjectedProfile(t *testing.T) {
	deps, _ := testDeps(t)
	deps.Redaction = []string{"Jandro"}
	if _, err := identity.Generate(deps.Vault, identity.Values{Name: "Jandro", Date: "2026-06-28"}); err != nil {
		t.Fatal(err)
	}
	if ctx := sessionContext(t, deps); strings.Contains(ctx, "Jandro") {
		t.Errorf("redaction not applied to injected profile:\n%s", ctx)
	}
}

func TestReviewQueueCount(t *testing.T) {
	deps, _ := testDeps(t)
	root := deps.Vault.Root()
	_ = os.MkdirAll(filepath.Join(root, ".axon"), 0o755)
	_ = os.WriteFile(filepath.Join(root, ".axon", "review-queue.md"), []byte("- [ ] one\n- [ ] two\n- [x] done\n"), 0o644)
	if n := reviewQueueCount(deps.Vault); n != 2 {
		t.Errorf("reviewQueueCount = %d, want 2", n)
	}
}

func TestSessionStartBriefingPointer(t *testing.T) {
	deps, _ := testDeps(t)
	today := time.Now().UTC().Format("2006-01-02")

	// No daily note → no pointer.
	res, err := Handle(context.Background(), SessionStart, nil, deps)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(res.Stdout), "Briefing:") {
		t.Fatal("pointer must be absent without a briefing block")
	}

	// Daily note WITHOUT the block → still no pointer.
	daily := filepath.Join(deps.Vault.Root(), "Daily")
	if err := os.MkdirAll(daily, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(daily, today+".md"), []byte("---\ntitle: x\n---\nplain\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, _ = Handle(context.Background(), SessionStart, nil, deps)
	if strings.Contains(string(res.Stdout), "Briefing:") {
		t.Fatal("pointer must be absent without the axon:briefing block")
	}

	// Daily note WITH an axon:briefing block → pointer present.
	body := "---\ntitle: x\n---\n<!-- axon:briefing:start -->\nhello\n<!-- axon:briefing:end -->\n"
	if err := os.WriteFile(filepath.Join(daily, today+".md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err = Handle(context.Background(), SessionStart, nil, deps)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(res.Stdout), "- Briefing: Daily/"+today+".md (axon:briefing)") {
		t.Fatalf("pointer missing:\n%s", res.Stdout)
	}
}

func stopPayload(session, transcript string) []byte {
	b, _ := json.Marshal(map[string]any{
		"hook_event_name": "Stop", "session_id": session, "transcript_path": transcript,
	})
	return b
}

func TestStopRecordsSession(t *testing.T) {
	deps, _ := testDeps(t)
	res, err := Handle(context.Background(), Stop, stopPayload("sess-abc", "/tmp/t.jsonl"), deps)
	if err != nil || res.ExitCode != 0 {
		t.Fatalf("stop: %v %d", err, res.ExitCode)
	}
	if !strings.Contains(string(res.Stdout), "Reminder") {
		t.Fatal("advisory line lost")
	}
	pending, _ := db.LoadPendingSessions(context.Background(), deps.DB)
	p, ok := pending["sess-abc"]
	if !ok || p.TranscriptPath != "/tmp/t.jsonl" || p.LastStop == "" {
		t.Fatalf("pending = %+v", pending)
	}

	// Second Stop for the same session upserts (fresher LastStop, same size).
	if _, err := Handle(context.Background(), Stop, stopPayload("sess-abc", "/tmp/t.jsonl"), deps); err != nil {
		t.Fatal(err)
	}
	pending, _ = db.LoadPendingSessions(context.Background(), deps.DB)
	if len(pending) != 1 {
		t.Fatalf("upsert broke: %d entries", len(pending))
	}
}

func TestStopRespectsToggleAndMissingFields(t *testing.T) {
	deps, _ := testDeps(t)
	f := false
	deps.Memory.CaptureSessions = &f
	_, _ = Handle(context.Background(), Stop, stopPayload("sess-off", "/tmp/t.jsonl"), deps)
	pending, _ := db.LoadPendingSessions(context.Background(), deps.DB)
	if len(pending) != 0 {
		t.Fatal("toggle off must not record")
	}

	deps2, _ := testDeps(t)
	_, _ = Handle(context.Background(), Stop, stopPayload("", "/tmp/t.jsonl"), deps2)
	_, _ = Handle(context.Background(), Stop, stopPayload("sess-x", ""), deps2)
	_, _ = Handle(context.Background(), Stop, nil, deps2) // garbage stdin tolerated
	pending2, _ := db.LoadPendingSessions(context.Background(), deps2.DB)
	if len(pending2) != 0 {
		t.Fatalf("incomplete payloads recorded: %v", pending2)
	}
}
