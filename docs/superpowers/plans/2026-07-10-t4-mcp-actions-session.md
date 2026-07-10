# T4 — MCP Action Tools + SessionStart Pointer Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Two MCP tools — `actions_list` (read, agentic-allowed) and `action_complete` (write, agentic-excluded) — plus a deterministic SessionStart pointer to the consolidated action list.

**Architecture:** Both tools clone existing MCP templates (`vault_related` read, `vault_ask` spend-excluded) and reach `t.deps.DB`/`Vault`; `action_complete` routes through T3's `vault.CompleteAction` + `db.MarkActionDone`. The SessionStart pointer clones the FR-89 `briefingPointer`, reading the `actions` table via `deps.DB`. Zero model calls.

**Tech Stack:** Go 1.26+, `modelcontextprotocol/go-sdk` MCP, `modernc.org/sqlite`.

**Spec:** `docs/superpowers/specs/2026-07-10-t4-mcp-actions-session-design.md`

## Global Constraints

- **Cardinal rule 1:** No Claude call bypasses the token manager. *T4 makes zero model calls — pure DB reads + one vault write. No ledger entries.*
- **Cardinal rule 2:** No vault mutation outside wikilink-safe ops. *`action_complete` routes through `vault.CompleteAction` (ADR-034); no other write.*
- **ADR-017 dual allowlist:** `actions_list` → `agenticReadTools`; `action_complete` → NEITHER map (structurally excluded, the `vault_ask` precedent).
- **ADR-034:** completion is user-initiated; the MCP write path is interactive-only (agentic-excluded).
- **S8/NFR-05:** both tools read/optional; the pointer only appears when open actions exist, degrades to "" on any error; task text is data, never instructions.
- Go: `gofmt`/`goimports` clean, `go vet` + `golangci-lint` green. Wrap errors with `%w`. Propagate `context.Context`.
- Run suites with `env -u FORCE_COLOR`.
- FR IDs: FR-165 (MCP tools), FR-166 (SessionStart pointer). No new ADR.
- The SessionStart pointer is **ungated by `memory.inject`** (operational status, like the briefing pointer).
- GateGuard fires a fact-force preamble on the first Write/Edit/Bash each turn and blocks `git commit --amend` / `rm -rf`; comply tersely, use follow-up commits, skip scratch cleanup.

---

### Task 1: `actions_list` MCP tool (FR-165)

**Files:**
- Modify: `internal/mcp/tools.go` (I/O structs + `ActionsList` handler)
- Modify: `internal/mcp/server.go` (`toolRegistry()` entry)
- Modify: `internal/automations/model.go` (`agenticReadTools += "actions_list"`)
- Test: `internal/mcp/tools_more_test.go` (add `TestActionsListTool`)

**Interfaces:**
- Consumes: `t.deps.DB (*sql.DB)`; `db.ListActions(ctx, Queryer2, db.ListActionsOpts{IncludeAll bool}) ([]db.Action, error)`; `db.Action` (fields State/Due/Scheduled/Start/Tags/DoneDate/SourcePath/Section/Text/Priority/Hash/Archived); `actions.BucketFields(state, due, scheduled, start string, tags []string, today time.Time) string`.
- Produces (for Task 3): a registered tool named `actions_list`; `func (t *Tools) ActionsList(ctx, ActionsListIn) (ActionsListOut, error)`.

- [ ] **Step 1: Write the failing test**

Add to `internal/mcp/tools_more_test.go`:

```go
func TestActionsListTool(t *testing.T) {
	ctx := context.Background()
	tools, _, _ := newTestTools(t, map[string]string{})
	d := tools.deps.DB
	if err := db.ReplaceActions(ctx, d, []db.Action{
		{Hash: "h-over", SourcePath: "01-Projects/w.md", Text: "fix bug", State: "open", Checkbox: " ", Due: "2000-01-01", Priority: "high", Updated: "u"},
		{Hash: "h-some", SourcePath: "Ideas.md", Text: "learn rust", State: "open", Checkbox: " ", Tags: []string{"someday"}, Updated: "u"},
		{Hash: "h-done", SourcePath: "01-Projects/w.md", Text: "shipped", State: "done", Checkbox: "x", DoneDate: "2000-01-02", Updated: "u"},
	}); err != nil {
		t.Fatal(err)
	}
	// default: open buckets only (someday IS open; done excluded)
	out, err := tools.ActionsList(ctx, ActionsListIn{})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Actions) != 2 {
		t.Fatalf("default list = %d, want 2 (open only): %+v", len(out.Actions), out.Actions)
	}
	if out.Counts["open"] != 2 || out.Counts["overdue"] != 1 {
		t.Errorf("counts = %+v", out.Counts)
	}
	// every row carries a hash (for action_complete) + a bucket
	for _, a := range out.Actions {
		if a.Hash == "" || a.Bucket == "" {
			t.Errorf("row missing hash/bucket: %+v", a)
		}
	}
	// status filter
	od, _ := tools.ActionsList(ctx, ActionsListIn{Status: "overdue"})
	if len(od.Actions) != 1 || od.Actions[0].Text != "fix bug" {
		t.Errorf("status=overdue = %+v", od.Actions)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `env -u FORCE_COLOR go test ./internal/mcp/ -run TestActionsListTool -v`
Expected: FAIL — `tools.ActionsList undefined` / `ActionsListIn undefined`.

- [ ] **Step 3: Write minimal implementation**

In `internal/mcp/tools.go`, add (ensure `time` + `github.com/jandro-es/axon/internal/actions` + `github.com/jandro-es/axon/internal/db` are imported — `db` almost certainly already is):

```go
// ActionsListIn filters the action index. All fields optional; default = open.
type ActionsListIn struct {
	Status  string `json:"status,omitempty"  jsonschema:"filter: a bucket (overdue|today|scheduled|next|waiting|someday) or 'open' (default); done/cancelled excluded"`
	Project string `json:"project,omitempty" jsonschema:"filter by project (wikilink target or source-path substring)"`
	Limit   int    `json:"limit,omitempty"   jsonschema:"max rows (default 50)"`
}

// ActionView is one action, compact, with the hash to pass to action_complete.
type ActionView struct {
	Text     string `json:"text"`
	Source   string `json:"source"`
	Section  string `json:"section,omitempty"`
	Bucket   string `json:"bucket"`
	Due      string `json:"due,omitempty"`
	Priority string `json:"priority,omitempty"`
	Hash     string `json:"hash"`
}

type ActionsListOut struct {
	Actions []ActionView   `json:"actions"`
	Counts  map[string]int `json:"counts"`
}

var actionsBucketOrder = map[string]int{
	"overdue": 0, "today": 1, "scheduled": 2, "next": 3, "waiting": 4, "someday": 5,
}

// ActionsList returns actions from the derived index (read-only, zero tokens).
func (t *Tools) ActionsList(ctx context.Context, in ActionsListIn) (ActionsListOut, error) {
	rows, err := db.ListActions(ctx, t.deps.DB, db.ListActionsOpts{IncludeAll: true})
	if err != nil {
		return ActionsListOut{}, err
	}
	today := time.Now()
	weekAgo := today.AddDate(0, 0, -7).Format("2006-01-02")
	counts := map[string]int{"open": 0, "overdue": 0, "today": 0, "waiting": 0, "someday": 0, "done7": 0}
	var views []ActionView
	for _, r := range rows {
		if r.Archived {
			continue
		}
		b := actions.BucketFields(r.State, r.Due, r.Scheduled, r.Start, r.Tags, today)
		switch b {
		case "done":
			if r.DoneDate >= weekAgo {
				counts["done7"]++
			}
			continue
		case "cancelled":
			continue
		}
		counts["open"]++
		if _, ok := counts[b]; ok {
			counts[b]++
		}
		// filter
		if in.Status != "" && in.Status != "open" && b != in.Status {
			continue
		}
		if in.Project != "" && r.Project != in.Project && !strings.Contains(r.SourcePath, in.Project) {
			continue
		}
		views = append(views, ActionView{
			Text: r.Text, Source: r.SourcePath, Section: r.Section, Bucket: b,
			Due: r.Due, Priority: r.Priority, Hash: r.Hash,
		})
	}
	sort.SliceStable(views, func(i, j int) bool {
		if bi, bj := actionsBucketOrder[views[i].Bucket], actionsBucketOrder[views[j].Bucket]; bi != bj {
			return bi < bj
		}
		return views[i].Due < views[j].Due
	})
	limit := in.Limit
	if limit <= 0 {
		limit = 50
	}
	if len(views) > limit {
		views = views[:limit]
	}
	return ActionsListOut{Actions: views, Counts: counts}, nil
}
```

> Ensure `sort` and `strings` are imported in `tools.go` (they very likely already are; add if `go build` complains).

In `internal/mcp/server.go`, add to the `toolRegistry()` slice after the `vault_related` entry:

```go
	{"actions_list", func(s *mcp.Server, t *Tools) {
		mcp.AddTool(s, &mcp.Tool{Name: "actions_list", Description: "List actionable tasks (checkbox items) across the vault, grouped by GTD bucket (overdue/today/next/waiting/someday). Read-only and spends NO tokens. Each row includes a hash to pass to action_complete."},
			func(ctx context.Context, _ *mcp.CallToolRequest, in ActionsListIn) (*mcp.CallToolResult, ActionsListOut, error) {
				out, err := t.ActionsList(ctx, in)
				return nil, out, err
			})
	}},
```

In `internal/automations/model.go`, add `"actions_list": true` to the `agenticReadTools` map:

```go
var agenticReadTools = map[string]bool{
	"vault_search": true, "vault_read": true, "vault_links": true,
	"knowledge_search": true, "tokens_status": true, "vault_related": true,
	"actions_list": true,
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `env -u FORCE_COLOR go test ./internal/mcp/ -run TestActionsListTool -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/mcp/tools.go internal/mcp/server.go internal/automations/model.go internal/mcp/tools_more_test.go
git commit -m "feat(T4): actions_list MCP tool (read, agentic-allowed) (FR-165)

Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>"
```

---

### Task 2: `action_complete` MCP tool (FR-165)

**Files:**
- Modify: `internal/mcp/tools.go` (I/O structs + `ActionComplete` handler)
- Modify: `internal/mcp/server.go` (`toolRegistry()` entry — NEITHER agentic map)
- Test: `internal/mcp/tools_more_test.go` (add `TestActionCompleteTool` + allowlist-exclusion test)

**Interfaces:**
- Consumes: `t.deps.Vault (*vault.FS).CompleteAction(ctx, path, hash, date) error`; `vault.ErrActionNotFound`; `db.MarkActionDone(ctx, Execer, hash, doneDate) (int64, error)`; `t.deps.DryRun`; `actions.Extract` (test only, to derive a hash).
- Produces (for Task 3): a registered tool named `action_complete`; `func (t *Tools) ActionComplete(ctx, ActionCompleteIn) (ActionCompleteOut, error)`.

- [ ] **Step 1: Write the failing test**

Add to `internal/mcp/tools_more_test.go` (needs `github.com/jandro-es/axon/internal/actions` + `github.com/jandro-es/axon/internal/vault` + `errors`):

```go
func TestActionCompleteTool(t *testing.T) {
	ctx := context.Background()
	tools, _, _ := newTestTools(t, map[string]string{"p.md": "- [ ] finish spec\n"})
	d := tools.deps.DB
	var hash string
	for _, a := range actions.Extract("p.md", "- [ ] finish spec\n", false) {
		hash = a.Hash()
	}
	if err := db.ReplaceActions(ctx, d, []db.Action{
		{Hash: hash, SourcePath: "p.md", LineNo: 0, Text: "finish spec", Raw: "- [ ] finish spec", State: "open", Checkbox: " ", Updated: "u"},
	}); err != nil {
		t.Fatal(err)
	}

	// stale hash → ErrActionNotFound
	if _, err := tools.ActionComplete(ctx, ActionCompleteIn{Path: "p.md", Hash: "bogus"}); !errors.Is(err, vault.ErrActionNotFound) {
		t.Fatalf("stale hash: want ErrActionNotFound, got %v", err)
	}
	// real completion
	out, err := tools.ActionComplete(ctx, ActionCompleteIn{Path: "p.md", Hash: hash})
	if err != nil {
		t.Fatal(err)
	}
	if !out.Applied {
		t.Error("Applied should be true")
	}
	n, _ := tools.deps.Vault.Read(ctx, "p.md")
	if !strings.Contains(n.Body, "- [x] finish spec ✅ ") {
		t.Errorf("source line not flipped:\n%s", n.Body)
	}
	got, _ := db.ListActions(ctx, d, db.ListActionsOpts{IncludeAll: true})
	if got[0].State != "done" {
		t.Errorf("DB row not marked done: %+v", got[0])
	}
}

func TestActionCompleteDryRun(t *testing.T) {
	ctx := context.Background()
	tools, _, _ := newTestTools(t, map[string]string{"p.md": "- [ ] x\n"})
	tools.deps.DryRun = true
	out, err := tools.ActionComplete(ctx, ActionCompleteIn{Path: "p.md", Hash: "anything"})
	if err != nil {
		t.Fatal(err)
	}
	if out.Applied {
		t.Error("dry-run must not apply")
	}
	n, _ := tools.deps.Vault.Read(ctx, "p.md")
	if !strings.Contains(n.Body, "- [ ] x") {
		t.Error("dry-run must not flip the line")
	}
}
```

> If `newTestTools` returns a `Tools` whose `deps.DryRun` isn't directly settable (it's `tools.deps.DryRun` — an unexported field accessed within the same package `mcp`, so it IS settable in-package), the assignment `tools.deps.DryRun = true` works because the test is `package mcp`. Confirm `tools_more_test.go` is `package mcp` (it is).

- [ ] **Step 2: Run test to verify it fails**

Run: `env -u FORCE_COLOR go test ./internal/mcp/ -run TestActionComplete -v`
Expected: FAIL — `tools.ActionComplete undefined`.

- [ ] **Step 3: Write minimal implementation**

In `internal/mcp/tools.go`, add (needs `errors`, `fmt`, `time`, `github.com/jandro-es/axon/internal/vault`, `github.com/jandro-es/axon/internal/db` — most already imported; add `vault`/`errors` if absent):

```go
type ActionCompleteIn struct {
	Path string `json:"path" jsonschema:"vault-relative note containing the checkbox line"`
	Hash string `json:"hash" jsonschema:"the action's identity hash (from actions_list)"`
}
type ActionCompleteOut struct {
	Applied bool   `json:"applied"`
	Message string `json:"message"`
}

// ActionComplete marks one open checkbox line done (ADR-034: byte-precise,
// hash-addressed, [ ]→[x] + ✅ date). Interactive-only — excluded from the
// agentic allowlists. DryRun-aware. Zero tokens.
func (t *Tools) ActionComplete(ctx context.Context, in ActionCompleteIn) (ActionCompleteOut, error) {
	if in.Path == "" || in.Hash == "" {
		return ActionCompleteOut{}, fmt.Errorf("path and hash are required")
	}
	date := time.Now().Format("2006-01-02")
	if t.deps.DryRun {
		return ActionCompleteOut{Applied: false, Message: "would complete action " + in.Hash + " in " + in.Path}, nil
	}
	if err := t.deps.Vault.CompleteAction(ctx, in.Path, in.Hash, date); err != nil {
		return ActionCompleteOut{}, err
	}
	if t.deps.DB != nil {
		_, _ = db.MarkActionDone(ctx, t.deps.DB, in.Hash, date)
	}
	return ActionCompleteOut{Applied: true, Message: "completed action in " + in.Path + " (✅ " + date + ")"}, nil
}
```

In `internal/mcp/server.go`, add to `toolRegistry()` after the `actions_list` entry (do NOT touch either agentic map — absence is the exclusion):

```go
	{"action_complete", func(s *mcp.Server, t *Tools) {
		mcp.AddTool(s, &mcp.Tool{Name: "action_complete", Description: "Mark a task done: flips its checkbox [ ]→[x] and stamps the completion date in the source note (hash-addressed, byte-precise; the hash comes from actions_list). Interactive use only — spends NO tokens, edits one human-authored line, refuses on a stale hash."},
			func(ctx context.Context, _ *mcp.CallToolRequest, in ActionCompleteIn) (*mcp.CallToolResult, ActionCompleteOut, error) {
				out, err := t.ActionComplete(ctx, in)
				return nil, out, err
			})
	}},
```

- [ ] **Step 4: Run test to verify it passes**

Run: `env -u FORCE_COLOR go test ./internal/mcp/ -run TestActionComplete -v`
Expected: PASS.

- [ ] **Step 5: Add the allowlist-exclusion test + commit**

Add to `internal/automations/model_test.go` (or wherever `agenticReadTools`/`agenticWriteTools` live — same package `automations`; create `internal/automations/agentic_tools_test.go` if no obvious home):

```go
func TestActionToolsAgenticDisposition(t *testing.T) {
	if !agenticReadTools["actions_list"] {
		t.Error("actions_list must be an agentic READ tool")
	}
	if agenticReadTools["action_complete"] || agenticWriteTools["action_complete"] {
		t.Error("action_complete must be excluded from BOTH agentic maps (ADR-034)")
	}
}
```

Run: `env -u FORCE_COLOR go test ./internal/mcp/ ./internal/automations/ -run 'TestActionComplete|TestActionToolsAgentic' -v`
Expected: PASS.

```bash
git add internal/mcp/tools.go internal/mcp/server.go internal/mcp/tools_more_test.go internal/automations/agentic_tools_test.go
git commit -m "feat(T4): action_complete MCP tool (write, agentic-excluded) (FR-165, ADR-034)

Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>"
```

---

### Task 3: Count assertions (filter_test + server_test)

**Files:**
- Modify: `internal/mcp/filter_test.go` (16 → 18)
- Modify: `internal/mcp/server_test.go` (want-list +2)

**Interfaces:** none (test bumps for the two tools added in Tasks 1–2).

- [ ] **Step 1: Update the assertions**

In `internal/mcp/filter_test.go`, change `if len(all) != 16 {` → `if len(all) != 18 {` and the message `want 16` → `want 18`.

In `internal/mcp/server_test.go`, add `"action_complete"` and `"actions_list"` to the `want` slice in sorted position (they sort before `automations_list`):

```go
	want := []string{
		"action_complete", "actions_list",
		"automations_list", "automations_run", "daily_append", "knowledge_ingest",
		"knowledge_search", "memory_remember", "metrics_query", "tokens_status",
		"vault_ask", "vault_links", "vault_move", "vault_patch", "vault_read", "vault_related", "vault_search", "vault_write",
	}
```

- [ ] **Step 2: Run tests to verify they pass**

Run: `env -u FORCE_COLOR go test ./internal/mcp/ -run 'TestRegisteredToolNamesFilter|TestServer' -v`
Expected: PASS (18 tools; sorted list matches).

- [ ] **Step 3: Commit**

```bash
git add internal/mcp/filter_test.go internal/mcp/server_test.go
git commit -m "test(T4): bump MCP tool-count assertions 16→18 (actions_list + action_complete)

Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>"
```

---

### Task 4: SessionStart open-actions pointer (FR-166)

**Files:**
- Modify: `internal/hooks/hooks.go` (`openActionsPointer` helper + wire into `sessionStart`)
- Test: `internal/hooks/hooks_test.go` (add `TestSessionStartActionsPointer`)

**Interfaces:**
- Consumes: `deps.DB (*sql.DB)`; `db.ListActions`; `actions.BucketFields`; the `sessionStart` builder (`hooks.go:101`) and its `deps.Vault != nil` block.
- Produces: a `- Actions: N open (M due today, K overdue) → [[01-Projects/Actions.md]]` line when open actions exist.

- [ ] **Step 1: Write the failing test**

Add to `internal/hooks/hooks_test.go` (mirror `TestSessionStartBriefingPointer`; uses `testDeps`, `sessionContext`, `db.ReplaceActions`):

```go
func TestSessionStartActionsPointer(t *testing.T) {
	ctx := context.Background()
	deps, fake := testDeps(t)

	// No open actions → no pointer.
	if got := sessionContext(t, deps); strings.Contains(got, "→ [[01-Projects/Actions.md]]") {
		t.Errorf("no-actions vault should have no pointer:\n%s", got)
	}

	// Seed open + overdue + today rows.
	if err := db.ReplaceActions(ctx, deps.DB, []db.Action{
		{Hash: "h1", SourcePath: "01-Projects/w.md", Text: "overdue", State: "open", Checkbox: " ", Due: "2000-01-01", Updated: "u"},
		{Hash: "h2", SourcePath: "01-Projects/w.md", Text: "today", State: "open", Checkbox: " ", Due: time.Now().Format("2006-01-02"), Updated: "u"},
		{Hash: "h3", SourcePath: "01-Projects/w.md", Text: "loose", State: "open", Checkbox: " ", Updated: "u"},
	}); err != nil {
		t.Fatal(err)
	}
	got := sessionContext(t, deps)
	if !strings.Contains(got, "- Actions: 3 open (1 due today, 1 overdue) → [[01-Projects/Actions.md]]") {
		t.Errorf("actions pointer wrong:\n%s", got)
	}
	if fake.CallCount() != 0 {
		t.Errorf("pointer must make no model call, got %d", fake.CallCount())
	}
}

func TestSessionStartActionsPointerUngatedByInject(t *testing.T) {
	ctx := context.Background()
	deps, _ := testDeps(t)
	off := false
	deps.Memory.Inject = &off // identity injection off
	if err := db.ReplaceActions(ctx, deps.DB, []db.Action{
		{Hash: "h1", SourcePath: "w.md", Text: "x", State: "open", Checkbox: " ", Updated: "u"},
	}); err != nil {
		t.Fatal(err)
	}
	// Operational status → still shown even with inject:false.
	if got := sessionContext(t, deps); !strings.Contains(got, "→ [[01-Projects/Actions.md]]") {
		t.Errorf("actions pointer should show regardless of memory.inject:\n%s", got)
	}
}
```

> Confirm `time`, `strings`, and `github.com/jandro-es/axon/internal/db` are imported in `hooks_test.go` (add if not). `testDeps(t)` returns `(Deps, *agent.Fake)` per the existing tests.

- [ ] **Step 2: Run tests to verify they fail**

Run: `env -u FORCE_COLOR go test ./internal/hooks/ -run TestSessionStartActionsPointer -v`
Expected: FAIL — the pointer line is absent.

- [ ] **Step 3: Write minimal implementation**

In `internal/hooks/hooks.go`, add the helper (near `briefingPointer`, needs `context`, `database/sql`, `time`, `github.com/jandro-es/axon/internal/actions`, `github.com/jandro-es/axon/internal/db` — most already imported):

```go
// openActionsPointer returns a one-line pointer to the consolidated action list
// when open actions exist, else "" (best-effort — any error yields no line, never
// a broken hook). Operational status, like the briefing pointer; no model call.
func openActionsPointer(ctx context.Context, d *sql.DB) string {
	if d == nil {
		return ""
	}
	rows, err := db.ListActions(ctx, d, db.ListActionsOpts{State: "open"})
	if err != nil || len(rows) == 0 {
		return ""
	}
	today := time.Now()
	open, todayN, overdue := 0, 0, 0
	for _, r := range rows {
		open++
		switch actions.BucketFields(r.State, r.Due, r.Scheduled, r.Start, r.Tags, today) {
		case "today":
			todayN++
		case "overdue":
			overdue++
		}
	}
	extra := ""
	if todayN > 0 || overdue > 0 {
		extra = fmt.Sprintf(" (%d due today, %d overdue)", todayN, overdue)
	}
	return fmt.Sprintf("- Actions: %d open%s → [[01-Projects/Actions.md]]\n", open, extra)
}
```

Wire it into `sessionStart`, immediately after the briefing-pointer block (inside `if deps.Vault != nil`, before the `Conventions` line). It reads `deps.DB`, not the vault, and is **ungated by `memory.inject`**:

```go
		if line := briefingPointer(deps.Vault); line != "" {
			b.WriteString(line)
		}
		if line := openActionsPointer(ctx, deps.DB); line != "" {
			b.WriteString(line)
		}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `env -u FORCE_COLOR go test ./internal/hooks/ -run TestSessionStartActionsPointer -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/hooks/hooks.go internal/hooks/hooks_test.go
git commit -m "feat(T4): SessionStart open-actions pointer (FR-166)

Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>"
```

---

### Task 5: Full green + docs + live smoke

**Files:**
- Modify: `docs/03-requirements.md` (FR-165/166), `docs/08-component-agent-bridge-mcp.md` (both tools + agentic dispositions), `docs/12-component-personal-memory-and-onboarding.md` (SessionStart pointer), `docs/16-roadmap-1.2.5.md` (T4 built), `CLAUDE.md` (FR range → FR-166)

**Interfaces:** none (verification + docs).

- [ ] **Step 1: Whole-suite green + lint**

Run: `env -u FORCE_COLOR go build ./... && env -u FORCE_COLOR go test ./... && golangci-lint run`
Expected: all pass. Fix any drift (`gofmt -w`; unused imports; the `strings`/`sort` imports in `tools.go`).

- [ ] **Step 2: Write the docs**

- `docs/03-requirements.md`: FR-165 (the two MCP tools — `actions_list` read/agentic-allowed/zero-spend; `action_complete` write via `vault.CompleteAction`, in the default set but excluded from both agentic allowlists, interactive-only, ADR-034); FR-166 (the ungated SessionStart open-actions pointer, deterministic, no model call).
- `docs/08-component-agent-bridge-mcp.md`: add `actions_list` and `action_complete` to the MCP tool table, noting the agentic read-allowlist membership of `actions_list` and the explicit exclusion of `action_complete` (like `vault_ask`).
- `docs/12-component-personal-memory-and-onboarding.md`: note the SessionStart injection now includes the open-actions pointer (operational status line, ungated by `memory.inject`).
- `docs/16-roadmap-1.2.5.md`: mark **T4** ✅ BUILT 2026-07-10 (FR-165/166, no ADR).
- `CLAUDE.md`: `docs/03` FR range → FR-166; update the 1.2.5 doc-pack line (T4 shipped; remaining T5/T6).

- [ ] **Step 3: Live smoke (isolated AXON_HOME — never :7777)**

```bash
go build -o /tmp/axon-t4 ./cmd/axon
SMOKE=$(mktemp -d)
# T1-shape config; seed tasks; reindex.
/tmp/axon-t4 init --config "$SMOKE/config.yaml"
mkdir -p "$SMOKE/vault/01-Projects"
printf '## Sprint\n- [ ] fix bug 📅 2000-01-01 ⏫\n- [ ] plan 📅 2999-01-01\n' > "$SMOKE/vault/01-Projects/work.md"
/tmp/axon-t4 reindex --config "$SMOKE/config.yaml"
# SessionStart pointer:
/tmp/axon-t4 hook session-start --config "$SMOKE/config.yaml" 2>/dev/null | python3 -c 'import sys,json; print([l for l in json.load(sys.stdin)["hookSpecificOutput"]["additionalContext"].splitlines() if "Actions:" in l])'
# MCP tool listing (both present):
/tmp/axon-t4 mcp --tools actions_list,action_complete --config "$SMOKE/config.yaml" </dev/null 2>&1 | head -c 200; echo
```

Expected: the `hook session-start` output contains `- Actions: 2 open (0 due today, 1 overdue) → [[01-Projects/Actions.md]]`; the MCP handshake with the two-tool filter starts. (Full MCP call behavior is unit-covered; the CLI handshake just proves registration.) `env -u FORCE_COLOR`; skip scratch cleanup (GateGuard).

- [ ] **Step 4: Commit docs**

```bash
git add docs/ CLAUDE.md
git commit -m "docs(T4): FR-165/166, MCP action tools + SessionStart pointer, roadmap T4 built

Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>"
```

---

## Notes for the implementer

- **Read one neighbor first:** `internal/mcp/tools_more_test.go` (`newTestTools`, `TestRelatedTool`), `internal/mcp/server.go` (`vault_related`/`vault_ask` registry entries), `internal/automations/model.go` (the two agentic maps), `internal/hooks/hooks.go` (`sessionStart`/`briefingPointer`) + `hooks_test.go` (`testDeps`/`sessionContext`/`TestSessionStartBriefingPointer`).
- **`action_complete` in NEITHER agentic map** is the whole ADR-034 guarantee — do not add it to `agenticReadTools`/`agenticWriteTools`. The Task-2 exclusion test enforces this.
- **Zero model calls** — nothing here touches the token manager. `action_complete` is a vault write, not a Claude call.
- **The pointer is ungated** — read the DB, emit one line, best-effort; do NOT wrap it in `if deps.Memory.InjectEnabled()`.
- **GateGuard:** first Write/Edit/Bash each turn triggers a fact-force preamble (comply tersely); `git commit --amend`/`rm -rf` blocked (follow-up commits; leave scratch dirs).
```
