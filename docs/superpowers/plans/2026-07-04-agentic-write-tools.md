# Agentic Write Tools Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let opted-in agentic automations call the managed-block-safe write MCP tools, with a server-enforced report-only dry-run and both-sides allowlisting, and wire compaction as the demonstrator (ADR-022, FR-105/106/107).

**Architecture:** Five slices. (1) Add `Deps.DryRun` report-only mode to the four write tool methods. (2) Thread a `--dry-run` flag from `axon mcp` up through `agent.Request.DryRunTools` / `tokens.AgentCall.DryRunTools` so a write-capable agentic dry-run spawns the agent with report-only writes instead of the Authorize-only short-circuit. (3) Add the fixed agentic tool allowlist + validation in `internal/automations` so only read + managed-block-safe write tools can ever be requested. (4) Wire compaction's agentic path to write `axon:summary` via `vault_patch`, archive-first unchanged, with a Go verify-and-fallback. (5) Docs + smoke + finish.

**Tech Stack:** Go 1.26; existing MCP server (`internal/mcp`), agent adapter (`internal/agent`), token chokepoint (`internal/tokens`), automation engine (`internal/automations`). No new dependencies.

**Spec:** `docs/superpowers/specs/2026-07-04-agentic-write-tools-design.md`
**ADR:** ADR-022 (`docs/02-architecture.md`, already committed)

## Global Constraints

- Branch: `feature/agentic-write-tools` (already created; spec + ADR + FR rows committed).
- Cardinal rule 1: every model call through `tokens.Manager.Run` (unchanged).
- Cardinal rule 2: managed-block-safe writes only; **`vault_move` never enters an agentic allowlist**.
- Report-only suppression is **server-side** (the subprocess cannot mutate), not model-trusted.
- Kill/defer degrades to the `agentic:false` one-shot path (ADR-017 / FR-85), unchanged.
- A write-capable agentic dry-run spends tokens (a real preview needs a real run) — surface it in the run summary.
- Run tests with `env -u FORCE_COLOR`. `gofmt` clean; `go vet ./...` green.
- Fixed agentic tool allowlist (constants, not config): read = `vault_search, vault_read, vault_links, knowledge_search, tokens_status`; write = `vault_patch, vault_write, daily_append, memory_remember`.

## File Structure

- `internal/mcp/tools.go` — `Deps.DryRun`; report-only branch + `Applied`/`Would` fields on `WriteOut`/`PatchOut`/`DailyAppendOut`/`RememberOut`.
- `cmd/axon/mcp_cmd.go` — `--dry-run` flag → `mcpDeps.DryRun`.
- `internal/agent/claudecode.go` — `Request.DryRunTools`; append `--dry-run` to the subprocess MCP args when set.
- `internal/tokens/manager.go` — `AgentCall.DryRunTools`; map into `req.DryRunTools`.
- `internal/automations/model.go` — agentic tool allowlist + `validateAgenticTools`; `runAgentic` validation + dry-run-tools wiring; `runModel` dry-run guard; compaction agentic-write path.
- Docs: `docs/06-component-automation-engine.md`, `docs/08-component-agent-bridge-mcp.md`, `CHANGELOG.md`.

---

### Task 1: Report-only mode for the write tools (FR-106 server half)

**Files:**
- Modify: `internal/mcp/tools.go` (`Deps` struct ~:29; `WriteOut`/`Write` ~:125; `PatchOut`/`Patch` ~:184; `DailyAppendOut`/`DailyAppend` ~:267; `RememberOut`/`Remember` ~:302)
- Test: `internal/mcp/tools_test.go` (append; create if absent)

**Interfaces:**
- Consumes: `Tools`/`Deps` (`internal/mcp/tools.go`); `NewTools(Deps) *Tools`; `vault.FS` write ops.
- Produces: `Deps.DryRun bool`; `WriteOut{OK, Path, Applied bool, Would string}`, `PatchOut{OK, Applied bool, Would string}`, `DailyAppendOut{OK, Path, Applied bool, Would string}`, `RememberOut{OK, Entry, Path, Applied bool, Would string}`. When `deps.DryRun`, each write method returns `Applied:false` with a `Would` string and performs **no** vault mutation; validation still runs.

- [ ] **Step 1: Write the failing test**

Create/append `internal/mcp/tools_test.go`:

```go
package mcp

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/jandro-es/axon/internal/vault"
)

// newDryTools builds a Tools over a temp vault with report-only writes on.
func newDryTools(t *testing.T) (*Tools, *vault.FS, string) {
	t.Helper()
	dir := t.TempDir()
	v := vault.NewFS(dir)
	return NewTools(Deps{Vault: v, DryRun: true}), v, dir
}

func TestReportOnlyWriteDoesNotMutate(t *testing.T) {
	ctx := context.Background()
	tl, v, dir := newDryTools(t)

	// Seed a managed note so Patch has a target.
	if _, err := v.Create("Note.md", "---\ntitle: n\n---\nprose\n\n<!-- axon:summary:start -->\nold\n<!-- axon:summary:end -->\n"); err != nil {
		t.Fatal(err)
	}
	before := snapshot(t, dir)

	pOut, err := tl.Patch(ctx, PatchIn{Path: "Note.md", Marker: "summary", Content: "new summary"})
	if err != nil {
		t.Fatal(err)
	}
	if pOut.Applied || pOut.Would == "" {
		t.Fatalf("patch dry-run = %+v, want Applied=false and a Would", pOut)
	}
	wOut, err := tl.Write(ctx, WriteIn{Path: "New.md", Body: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	if wOut.Applied || wOut.Would == "" {
		t.Fatalf("write dry-run = %+v", wOut)
	}
	dOut, err := tl.DailyAppend(ctx, DailyAppendIn{Content: "log line"}, "2026-07-04")
	if err != nil {
		t.Fatal(err)
	}
	if dOut.Applied || dOut.Would == "" {
		t.Fatalf("daily dry-run = %+v", dOut)
	}

	if after := snapshot(t, dir); !equalMaps(before, after) {
		t.Fatalf("vault mutated under report-only:\nbefore=%v\nafter=%v", before, after)
	}
}

func equalMaps(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// snapshot returns a stable map of vault-relative path → content.
func snapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	_ = filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		b, _ := os.ReadFile(p)
		rel, _ := filepath.Rel(root, p)
		out[rel] = string(b)
		return nil
	})
	return out
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `env -u FORCE_COLOR go test ./internal/mcp/ -run TestReportOnlyWrite -v 2>&1 | tail -8`
Expected: compile FAIL — `unknown field DryRun in struct literal of type Deps` and `pOut.Applied`/`pOut.Would` undefined.

- [ ] **Step 3: Add `Deps.DryRun` + output fields**

In `internal/mcp/tools.go`, add to `Deps` (after `ToolFilter`):

```go
	// DryRun puts the write tools in report-only mode (ADR-022 / FR-106):
	// each validates and computes its change, returns Applied=false with a
	// Would string, and performs no vault mutation. Read tools are unaffected.
	DryRun bool
```

Extend the four output structs:

```go
type WriteOut struct {
	OK      bool   `json:"ok"`
	Path    string `json:"path"`
	Applied bool   `json:"applied"`
	Would   string `json:"would,omitempty"`
}
```
```go
type PatchOut struct {
	OK      bool   `json:"ok"`
	Applied bool   `json:"applied"`
	Would   string `json:"would,omitempty"`
}
```
```go
type DailyAppendOut struct {
	OK      bool   `json:"ok"`
	Path    string `json:"path"`
	Applied bool   `json:"applied"`
	Would   string `json:"would,omitempty"`
}
```
```go
type RememberOut struct {
	OK      bool   `json:"ok"`
	Entry   string `json:"entry"`
	Path    string `json:"path"`
	Applied bool   `json:"applied"`
	Would   string `json:"would,omitempty"`
}
```

- [ ] **Step 4: Branch each write method on `deps.DryRun`**

`Write` — insert the report-only return **after** the existing validation (the `Exists`/`force`/`isAxonManaged` checks), immediately before `t.deps.Vault.Write`:

```go
	if t.deps.DryRun {
		return WriteOut{OK: true, Path: in.Path, Applied: false,
			Would: fmt.Sprintf("create %s (%d bytes)", in.Path, len(in.Body))}, nil
	}
	if err := t.deps.Vault.Write(ctx, in.Path, &vault.Note{Body: in.Body}); err != nil {
```

`Patch` — after `guardAgentPath`, before `t.deps.Vault.Patch`:

```go
	if t.deps.DryRun {
		return PatchOut{OK: true, Applied: false,
			Would: fmt.Sprintf("patch axon:%s in %s (%d chars)", in.Marker, in.Path, len(in.Content))}, nil
	}
	if err := t.deps.Vault.Patch(ctx, in.Path, in.Marker, in.Content); err != nil {
```

`DailyAppend` — after computing `path`, before the `Exists`/`Create`/`Append` block:

```go
	if t.deps.DryRun {
		return DailyAppendOut{OK: true, Path: path, Applied: false,
			Would: fmt.Sprintf("append %d byte(s) to %s", len(in.Content), path)}, nil
	}
	if !t.deps.Vault.Exists(path) {
```

`Remember` — at the top of the method body (before `identity.Remember`):

```go
	if t.deps.DryRun {
		return RememberOut{OK: true, Path: identity.MemoryPath, Applied: false,
			Would: fmt.Sprintf("remember %s: %s", in.Kind, in.Text)}, nil
	}
```

On the **live** (non-dry-run) paths, set `Applied: true` in the success returns:
- `Write`: `return WriteOut{OK: true, Path: in.Path, Applied: true}, nil`
- `Patch`: `return PatchOut{OK: true, Applied: true}, nil`
- `DailyAppend`: `return DailyAppendOut{OK: true, Path: path, Applied: true}, nil`
- `Remember`: `return RememberOut{OK: true, Entry: line, Path: identity.MemoryPath, Applied: true}, nil`

- [ ] **Step 5: Run tests to verify they pass**

Run: `env -u FORCE_COLOR go test ./internal/mcp/ -run TestReportOnlyWrite -v 2>&1 | tail -6`
Expected: PASS.

- [ ] **Step 6: Full package + vet**

Run: `env -u FORCE_COLOR go test ./internal/mcp/ && go vet ./internal/mcp/`
Expected: PASS (existing MCP tests unaffected; `DryRun` defaults false everywhere it is constructed).

- [ ] **Step 7: Commit**

```bash
git add internal/mcp/tools.go internal/mcp/tools_test.go
git commit -m "feat(mcp): report-only mode for write tools (FR-106 server half)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 2: Thread `--dry-run` from `axon mcp` to the adapter (FR-106 wiring)

**Files:**
- Modify: `cmd/axon/mcp_cmd.go` (flag + `mcpDeps.DryRun`)
- Modify: `internal/agent/claudecode.go` (`Request.DryRunTools`; append `--dry-run` in `buildMCPConfig`)
- Modify: `internal/tokens/manager.go` (`AgentCall.DryRunTools`; map to `req.DryRunTools`)
- Test: `internal/agent/claudecode_test.go` (append)

**Interfaces:**
- Consumes: `Request` struct (`internal/agent/claudecode.go`, ~:30-48); `AgentCall` (`internal/tokens/manager.go:49`); `buildMCPConfig(tools []string)` (`internal/agent/claudecode.go:207`).
- Produces: `agent.Request.DryRunTools bool`; `tokens.AgentCall.DryRunTools bool`; `buildMCPConfig(tools []string, dryRun bool)` appending `--dry-run` when true; `mcp` subcommand accepts `--dry-run`.

- [ ] **Step 1: Write the failing test**

Append to `internal/agent/claudecode_test.go` (confirm `strings` is imported; add if missing):

```go
func TestBuildMCPConfigDryRunFlag(t *testing.T) {
	c := &ClaudeCode{mcpCommand: "axon", mcpArgs: []string{"mcp"}}

	dry, err := c.buildMCPConfig([]string{"vault_patch"}, true)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(dry, "--dry-run") || !strings.Contains(dry, "vault_patch") {
		t.Fatalf("dry-run config missing flag/tool: %s", dry)
	}

	live, err := c.buildMCPConfig([]string{"vault_patch"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(live, "--dry-run") {
		t.Fatalf("live config must not carry --dry-run: %s", live)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `env -u FORCE_COLOR go test ./internal/agent/ -run TestBuildMCPConfigDryRun -v 2>&1 | tail -6`
Expected: compile FAIL — `too many arguments in call to c.buildMCPConfig`.

- [ ] **Step 3: `Request.DryRunTools` + adapter wiring**

In `internal/agent/claudecode.go`, add to the `Request` struct (after `RunBudgetTokens`):

```go
	// DryRunTools spawns the subprocess MCP server in report-only mode
	// (axon mcp --tools <csv> --dry-run): write tools validate and report,
	// never mutate (ADR-022 / FR-106). Ignored for non-agentic calls.
	DryRunTools bool
```

Change the `buildMCPConfig` signature and body (`:207-219`):

```go
func (c *ClaudeCode) buildMCPConfig(tools []string, dryRun bool) (string, error) {
	if c.mcpCommand == "" {
		return "", fmt.Errorf("agentic run requested but no MCP command wired (ClaudeCodeOptions.MCPCommand)")
	}
	args := append(append([]string{}, c.mcpArgs...), "--tools", strings.Join(tools, ","))
	if dryRun {
		args = append(args, "--dry-run")
	}
	cfg := map[string]any{
		"mcpServers": map[string]any{
			"axon": map[string]any{"command": c.mcpCommand, "args": args},
		},
	}
	raw, err := json.Marshal(cfg)
	return string(raw), err
}
```

Update the sole caller in the agentic arg assembly (`:186`):

```go
	mcpCfg, _ := c.buildMCPConfig(req.Tools, req.DryRunTools) // runAgentic validates before calling
```

- [ ] **Step 4: `AgentCall.DryRunTools` + manager mapping**

In `internal/tokens/manager.go`, add to `AgentCall` (near `Tools`/`MaxTurns`, ~:69):

```go
	// DryRunTools requests report-only write tools for this agentic run
	// (ADR-022 / FR-106). Set by an automation only when its own dry-run is
	// active AND its tool allowlist includes a write tool.
	DryRunTools bool
```

In the `AgentCall`→`Request` mapping (`:498-501`), inside the `if len(call.Tools) > 0` block:

```go
	if len(call.Tools) > 0 {
		req.Tools = call.Tools
		req.MaxTurns = call.MaxTurns
		req.RunBudgetTokens = call.BudgetTokens
		req.DryRunTools = call.DryRunTools
	}
```

- [ ] **Step 5: `--dry-run` flag on `axon mcp`**

In `cmd/axon/mcp_cmd.go`, add a bool var and flag, and set `mcpDeps.DryRun`:

```go
	var toolsCSV string
	var dryRun bool
```

In `RunE`, after the `ToolFilter` assignment:

```go
			if toolsCSV != "" {
				mcpDeps.ToolFilter = strings.Split(toolsCSV, ",")
			}
			mcpDeps.DryRun = dryRun
```

Register the flag beside `--tools`:

```go
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "report-only: write tools validate and describe changes without mutating (agentic dry-runs)")
```

- [ ] **Step 6: Run tests to verify they pass**

Run: `env -u FORCE_COLOR go test ./internal/agent/ ./internal/tokens/ ./cmd/... 2>&1 | tail -6`
Expected: PASS — the new adapter test plus every existing agent/tokens/cmd test (the `buildMCPConfig` caller is updated; new fields default false).

- [ ] **Step 7: Vet + commit**

Run: `go vet ./internal/agent/ ./internal/tokens/ ./cmd/...`
```bash
git add cmd/axon/mcp_cmd.go internal/agent/claudecode.go internal/tokens/manager.go internal/agent/claudecode_test.go
git commit -m "feat(agent): thread --dry-run to subprocess MCP for report-only agentic writes (FR-106)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 3: Agentic tool allowlist + validation + dry-run-tools wiring (FR-105)

**Files:**
- Modify: `internal/automations/model.go` (allowlist consts + `validateAgenticTools`; `runAgentic` :50; `runModel` dry-run guard :26)
- Test: `internal/automations/standard_test.go` (append)

**Interfaces:**
- Consumes: `runAgentic(ctx, rc, call, toolsAllow []string, maxTurns int)` (`internal/automations/model.go:50`); `runModel` (:21); `tokens.AgentCall.DryRunTools` (Task 2).
- Produces: `agenticReadTools`/`agenticWriteTools map[string]bool` (package `automations`); `validateAgenticTools(tools []string) error`; `agenticContainsWriteTool(tools []string) bool`; `runAgentic` rejects out-of-allowlist tools and sets `call.DryRunTools = rc.DryRun` when the list includes a write tool; `runModel` runs (not Authorize-only) when `rc.DryRun && call.DryRunTools`.

- [ ] **Step 1: Write the failing test**

Append to `internal/automations/standard_test.go`:

```go
func TestValidateAgenticTools(t *testing.T) {
	if err := validateAgenticTools([]string{"vault_read", "vault_links", "vault_patch"}); err != nil {
		t.Fatalf("valid set rejected: %v", err)
	}
	for _, bad := range [][]string{{"vault_move"}, {"knowledge_ingest"}, {"automations_run"}, {"not_a_tool"}} {
		if err := validateAgenticTools(bad); err == nil {
			t.Fatalf("expected %v to be rejected", bad)
		}
	}
}

func TestAgenticContainsWriteTool(t *testing.T) {
	if !agenticContainsWriteTool([]string{"vault_read", "vault_patch"}) {
		t.Fatal("vault_patch should be detected as a write tool")
	}
	if agenticContainsWriteTool([]string{"vault_read", "vault_links"}) {
		t.Fatal("read-only set must not be flagged as write-capable")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `env -u FORCE_COLOR go test ./internal/automations/ -run 'TestValidateAgenticTools|TestAgenticContainsWriteTool' -v 2>&1 | tail -6`
Expected: compile FAIL — `undefined: validateAgenticTools`, `undefined: agenticContainsWriteTool`.

- [ ] **Step 3: Add the allowlist + helpers**

In `internal/automations/model.go`, near the top (after imports), add:

```go
// agenticWriteTools is the fixed set of managed-block-safe write tools an
// agentic automation may request (ADR-022 / FR-105). vault_move and every
// other mutating/model/network tool are deliberately absent.
var agenticWriteTools = map[string]bool{
	"vault_patch": true, "vault_write": true, "daily_append": true, "memory_remember": true,
}

// agenticReadTools is the read surface agentic runs have always had (ADR-017).
var agenticReadTools = map[string]bool{
	"vault_search": true, "vault_read": true, "vault_links": true,
	"knowledge_search": true, "tokens_status": true,
}

// validateAgenticTools rejects any tool outside the read + managed-block-safe
// write allowlists, so a stray vault_move or typo fails a run (and its tests)
// instead of silently granting a capability.
func validateAgenticTools(tools []string) error {
	for _, name := range tools {
		if !agenticReadTools[name] && !agenticWriteTools[name] {
			return fmt.Errorf("tool %q is not permitted in an agentic automation allowlist (ADR-022)", name)
		}
	}
	return nil
}

// agenticContainsWriteTool reports whether an allowlist includes any write tool.
func agenticContainsWriteTool(tools []string) bool {
	for _, name := range tools {
		if agenticWriteTools[name] {
			return true
		}
	}
	return false
}
```

- [ ] **Step 4: Enforce in `runAgentic` and wire dry-run-tools**

Replace the body of `runAgentic` (`:50-59`):

```go
func runAgentic(ctx context.Context, rc RunCtx, call tokens.AgentCall, toolsAllow []string, maxTurns int) (text string, est int, degraded bool, err error) {
	if verr := validateAgenticTools(toolsAllow); verr != nil {
		return "", 0, false, verr
	}
	call.Tools = toolsAllow
	call.MaxTurns = maxTurns
	// A write-capable agentic dry-run runs the agent with report-only write
	// tools (server-enforced) instead of the Authorize-only short-circuit,
	// so the operator sees what would be written (FR-106).
	if rc.DryRun && agenticContainsWriteTool(toolsAllow) {
		call.DryRunTools = true
	}
	text, est, degraded, err = runModel(ctx, rc, call)
	if err != nil && errors.Is(err, tokens.ErrRunBudgetExceeded) {
		rc.Log.Warn("agentic run killed at budget; degrading", "operation", call.Operation)
		return "", est, true, nil
	}
	return text, est, degraded, err
}
```

- [ ] **Step 5: `runModel` dry-run guard**

In `runModel` (`:26`), change the dry-run short-circuit so a report-only-tools call runs instead of only pre-flighting:

```go
	if rc.DryRun && !call.DryRunTools {
		auth, aerr := rc.Manager.Authorize(ctx, call)
		if aerr != nil {
			return "", 0, false, aerr
		}
		return "", auth.EstInput, auth.Decision == tokens.DecisionDefer || auth.Decision == tokens.DecisionDeny, nil
	}
```

(When `call.DryRunTools` is true, control falls through to `rc.Manager.Run` — a real, ledgered run whose subprocess writes are report-only.)

- [ ] **Step 6: Run tests to verify they pass**

Run: `env -u FORCE_COLOR go test ./internal/automations/ -run 'TestValidateAgenticTools|TestAgenticContainsWriteTool' -v 2>&1 | tail -6`
Expected: PASS.

- [ ] **Step 7: Full package + vet + commit**

Run: `env -u FORCE_COLOR go test ./internal/automations/ && go vet ./internal/automations/`
Expected: PASS (existing agentic compaction/digest tests still green — their read-only allowlists validate, and `rc.DryRun` without write tools keeps Authorize-only).
```bash
git add internal/automations/model.go internal/automations/standard_test.go
git commit -m "feat(automations): agentic write-tool allowlist + report-only dry-run wiring (FR-105/106)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 4: Compaction demonstrator — agentic-writes axon:summary (FR-107)

**Files:**
- Modify: `internal/automations/model.go` (compaction agentic path ~:388-427)
- Test: `internal/automations/standard_test.go` (append)

**Interfaces:**
- Consumes: `runAgentic` (Task 3, now write-aware); `rc.Vault.Patch/Read/Create/List`; `agenticEnabled(rc, "compaction", true)`.
- Produces: compaction's agentic path grants `vault_patch` and instructs the model to write `axon:summary`; Go archives first (unchanged, FR-44), then **verifies** the summary block is non-empty after the run and **falls back** to a Go `Patch` of the returned text if empty; `--dry-run` previews without mutating; `managedBlock(body, name string) string` reader helper.

- [ ] **Step 1: Write the failing test**

Append to `internal/automations/standard_test.go`. (Note: `newRC`'s `agent.Fake` returns `fake.Reply` as the final assistant text and does **not** itself perform MCP tool calls, so in unit context the summary lands via the Step-3 verify-and-fallback Go `Patch` — the assertion is *outcome* correctness. A real agent driving a real `vault_patch` is exercised by the Task 5 smoke and the existing `claudecode_agentic_e2e_test.go`.)

```go
// TestCompactionAgenticWritesSummary: on the agentic path the distilled
// summary lands in axon:summary, the original is archived first (FR-44).
func TestCompactionAgenticWritesSummary(t *testing.T) {
	seed := map[string]string{
		"03-Resources/long.md": "---\ntitle: long\n---\n" + strings.Repeat("Sentence about vectors. ", 80) + "\n",
	}
	rc, fake := newRC(t, seed)
	ctx := context.Background()
	fake.Reply = "- vectors summary\n- second point"
	mustReindex(t, rc)

	res, err := (Compaction{}).Run(ctx, rc)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Changes) == 0 {
		t.Skip("no note crossed the compaction threshold in this fixture")
	}
	n, _ := rc.Vault.Read(ctx, "03-Resources/long.md")
	if !strings.Contains(n.Body, "axon:summary:start") || !strings.Contains(n.Body, "vectors summary") {
		t.Fatalf("summary not written:\n%s", n.Body)
	}
	archived := false
	for _, p := range mustList(t, rc) {
		if strings.HasPrefix(p, ".axon/archive/") {
			archived = true
		}
	}
	if !archived {
		t.Fatal("original not archived before compaction (FR-44)")
	}
}

func TestCompactionAgenticDryRunNoMutation(t *testing.T) {
	seed := map[string]string{
		"03-Resources/long.md": "---\ntitle: long\n---\n" + strings.Repeat("Sentence about vectors. ", 80) + "\n",
	}
	rc, fake := newRC(t, seed)
	ctx := context.Background()
	fake.Reply = "- summary"
	mustReindex(t, rc)
	rc.DryRun = true

	before, _ := rc.Vault.Read(ctx, "03-Resources/long.md")
	if _, err := (Compaction{}).Run(ctx, rc); err != nil {
		t.Fatal(err)
	}
	after, _ := rc.Vault.Read(ctx, "03-Resources/long.md")
	if before.Body != after.Body {
		t.Fatalf("dry-run mutated the note:\n%s", after.Body)
	}
}

func mustList(t *testing.T, rc RunCtx) []string {
	t.Helper()
	paths, err := rc.Vault.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return paths
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `env -u FORCE_COLOR go test ./internal/automations/ -run 'TestCompactionAgentic' -v 2>&1 | tail -10`
Expected: the dry-run test fails if compaction currently writes under a write-capable dry-run; the summary test may pass or fail depending on current wiring. Goal: both green after Step 3. (If `mustList` already exists in the package, drop the duplicate and re-run.)

- [ ] **Step 3: Wire the compaction agentic path**

In `internal/automations/model.go`, replace the compaction agentic branch (grant `vault_patch`, instruct the write):

```go
		if agenticEnabled(rc, "compaction", true) {
			agCall := call
			agCall.Messages = []tokens.Message{{Role: "user", Content: prompt +
				"\n\nBefore distilling, you may use vault_links to see this note's backlinks (path: " + on.Path +
				") and vault_read to check what inbound links rely on — preserve those facts. Then write the 5-8 bullet summary into this note's `axon:summary` managed block using vault_patch (path: " + on.Path + ", marker: summary)."}}
			text, est, deferred, merr = runAgentic(ctx, rc, agCall,
				[]string{"vault_read", "vault_links", "vault_patch"}, 4)
			if merr == nil && deferred && !rc.DryRun {
				text, est, deferred, merr = runModel(ctx, rc, call)
			}
		} else {
			text, est, deferred, merr = runModel(ctx, rc, call)
		}
```

Replace the write section (after archive-first) with verify-and-fallback:

```go
		// Archive the pre-compaction body first (FR-44): unchanged.
		stamp := rc.now().UTC().Format("20060102-150405")
		archivePath := fmt.Sprintf(".axon/archive/%s-%s-%s.md", vault.BaseNoExt(on.Path), hashShort(on.Path), stamp)
		if _, err := rc.Vault.Create(archivePath, fmt.Sprintf("archived from %s by compaction at %s\n\n%s", on.Path, stamp, n.Body)); err != nil {
			return RunResult{}, fmt.Errorf("archive %s before compaction: %w", on.Path, err)
		}
		// The agentic path may have written axon:summary itself via vault_patch.
		// Verify; if the block is empty (agent skipped the tool, or one-shot
		// fallback ran), Go writes the returned text — outcome guaranteed
		// either way, and this is the only writer on the agentic:false path.
		if cur, rerr := rc.Vault.Read(ctx, on.Path); rerr == nil && strings.TrimSpace(managedBlock(cur.Body, "summary")) != "" {
			// Agent already wrote it; nothing to do.
		} else if err := rc.Vault.Patch(ctx, on.Path, "summary", strings.TrimSpace(text)); err != nil {
			return RunResult{}, err
		}
```

Add the `managedBlock` helper in the same file (reuse an existing package helper if one exists and skip this):

```go
// managedBlock returns the inner content of an axon:<name> block, or "".
func managedBlock(body, name string) string {
	start, end := "<!-- axon:"+name+":start -->", "<!-- axon:"+name+":end -->"
	i := strings.Index(body, start)
	if i < 0 {
		return ""
	}
	rest := body[i+len(start):]
	j := strings.Index(rest, end)
	if j < 0 {
		return ""
	}
	return rest[:j]
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `env -u FORCE_COLOR go test ./internal/automations/ -run 'TestCompaction' -v 2>&1 | tail -12`
Expected: PASS — summary written (via fallback in unit context), archive present, dry-run mutates nothing.

- [ ] **Step 5: Full package + vet + commit**

Run: `env -u FORCE_COLOR go test ./internal/automations/ && go vet ./...`
Expected: PASS across the module.
```bash
git add internal/automations/model.go internal/automations/standard_test.go
git commit -m "feat(automations): compaction agentic-writes axon:summary via vault_patch (FR-107)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 5: Docs + live smoke + finish

**Files:**
- Modify: `docs/06-component-automation-engine.md` (compaction paragraph)
- Modify: `docs/08-component-agent-bridge-mcp.md` (agentic allowlist + report-only mode)
- Modify: `CHANGELOG.md`
- Smoke: session scratchpad only.

**Interfaces:** docs are prose; the smoke consumes the built binary + `axon mcp --tools … --dry-run`, and (if `claude` is logged in) a real `axon run compaction --dry-run`.

- [ ] **Step 1: docs/06 compaction paragraph**

Find it (`grep -n -i 'compaction' docs/06-component-automation-engine.md`) and append to the compaction description:

```
On its agentic path (ADR-022) compaction now writes the distilled summary into the note's `axon:summary` block via the `vault_patch` tool; the original is still archived first (FR-44) and Go verifies the block, falling back to a deterministic write if the agent skipped the tool. The `agentic: false` one-shot + deterministic write is unchanged.
```

- [ ] **Step 2: docs/08 agentic tools**

Find the agentic/tool-allowlist section (`grep -n -i 'allowedTools\|agentic\|read-only' docs/08-component-agent-bridge-mcp.md`) and add:

```
Agentic write tools (ADR-022): an automation's allowlist may include the managed-block-safe write tools — `vault_patch`, `vault_write`, `daily_append`, `memory_remember` (never `vault_move`). Enforcement is the same dual allowlist as read tools (`--allowedTools` client-side, `axon mcp --tools <csv>` server-side). `axon run <automation> --dry-run` adds `--dry-run` to the subprocess so write tools validate and report `{would, applied:false}` without mutating — suppression is server-side, and such a dry-run spends tokens because the model actually runs.
```

- [ ] **Step 3: CHANGELOG**

Add under `[Unreleased] → Added`, above the heartbeat-synthesis bullet:

```markdown
- **Agentic write tools (ADR-022, FR-105…FR-107)** — opted-in agentic
  automations may now call the managed-block-safe write tools (`vault_patch`,
  `vault_write`, `daily_append`, `memory_remember`; never `vault_move`),
  enforced by the same dual allowlist as reads (client `--allowedTools` +
  server `axon mcp --tools`). `axon run <name> --dry-run` spawns the agent
  with **server-enforced report-only** write tools — each validates and
  reports what it would change without mutating (a real preview at real token
  cost). A mid-run budget kill leaves a prefix of per-tool-atomic, idempotent
  writes — never a half-edited note; a re-run converges. Compaction is the
  first user: its agentic path writes `axon:summary` via `vault_patch`
  (archive-first and the `agentic:false` deterministic write unchanged).
  Closes ADR-017's two reasons for deferring write tools.
```

- [ ] **Step 4: Commit docs**

```bash
git add docs/06-component-automation-engine.md docs/08-component-agent-bridge-mcp.md CHANGELOG.md
git commit -m "docs: agentic write tools built (ADR-022, FR-105..107)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

- [ ] **Step 5: Smoke — report-only server (no model, always runnable)**

```bash
S=/private/tmp/claude-501/-Users-jandro-Projects-axon/84f7638b-ccf6-4b6b-872c-136d5674130c/scratchpad/agwrite-smoke
mkdir -p "$S/vault" && go build -o "$S/axon" ./cmd/axon
```

Write `$S/config.yaml` as in prior smokes (ollama embeddings, vault/data under `$S`), `"$S/axon" init --config "$S/config.yaml"`, seed a managed note, then drive the MCP server directly over stdio in dry-run: pipe an MCP `initialize` + `tools/call` (name `vault_patch`) JSON-RPC pair to `"$S/axon" mcp --tools vault_patch,vault_read --dry-run --config "$S/config.yaml"`. Assert the response contains `"applied":false` and `"would"`, that the note bytes are unchanged afterward, and that `tools/list` does not include `vault_move`.

Expected: report-only write returns a `would`/`applied:false` result; the vault is untouched; `vault_move` absent.

- [ ] **Step 6: Smoke — real agentic compaction (only if `claude` is logged in)**

```bash
claude --version >/dev/null 2>&1 && echo CLAUDE-PRESENT || echo CLAUDE-ABSENT
```

If present: seed a long note over the compaction threshold, run `"$S/axon" run compaction --dry-run --config "$S/config.yaml"` (confirm a preview and that `axon:summary` is **not** written), then `"$S/axon" run compaction` (confirm the summary is written and the original archived under `.axon/archive/`). If absent, note the skip — Tasks 1-4 unit tests + the report-only server smoke + `claudecode_agentic_e2e_test.go` cover the contract.

- [ ] **Step 7: Cleanup**

```bash
rm -rf "$S"
```

- [ ] **Step 8: Finish the branch**

```bash
go vet ./... && env -u FORCE_COLOR go test ./...
```
Then invoke `superpowers:finishing-a-development-branch` (merge to `main`, push, delete branch).

---

## Self-Review Notes

- **Spec coverage:** FR-105 (allowlist + validation) → Task 3; FR-106 (report-only mode) → Task 1 (server half) + Task 2 (threading) + Task 3 (`runModel` guard); FR-107 (compaction) → Task 4; docs/ADR-022 supersession + CHANGELOG → Task 5; convergence model → idempotent `Patch` re-read in Task 4, documented in ADR-022. Both ADR-017 blockers map to concrete tasks (dry-run → 1/2/3; half-finish → per-tool atomicity, exercised by report-only non-mutation + archive-first).
- **Placeholder scan:** none — every code step carries complete code. The one harness limitation (fake agent does not itself call MCP tools) is stated explicitly with the verify-and-fallback guarantee and the e2e/smoke coverage that exercises a real tool call.
- **Type consistency:** `Deps.DryRun`, `Request.DryRunTools`, `AgentCall.DryRunTools` used identically across tasks; `buildMCPConfig(tools, dryRun)` updated at its sole caller; `validateAgenticTools`/`agenticContainsWriteTool`/`agenticWriteTools`/`agenticReadTools` names match between definition (Task 3) and use (Tasks 3/4); output structs gain `Applied bool`/`Would string` consistently; `managedBlock` reused if already present.
- **Known risk (flagged, not hidden):** the compaction verify-and-fallback means unit tests prove *outcome* correctness, not that the agent used the tool; that gap is covered by the Task 5 real-agent smoke and the existing agentic e2e harness.
