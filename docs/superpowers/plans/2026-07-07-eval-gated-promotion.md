# R5.2 — Eval-Gated Local-Model Promotion Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make a local model earn a classify/routine tier only when it has passed the R5.1 eval harness on this machine: persist eval results, add a runtime admission gate in the token chokepoint that routes unvetted local tiers to Claude, surface vetting + version drift in `doctor`, and add a default-off automation that re-runs evals when a gated model's version drifts.

**Architecture:** A DB-only `eval_runs` table (migration `0006`) records each `axon eval` outcome. The token-manager gate in `Authorize` consults the latest row via a raw inline SELECT (no new `tokens → db` import; matches the manager's existing ledger SQL) and, when a local classify/routine tier is unvetted or below `models.eval_min_pass`, retargets to the tier's Claude fallback and emits `token.unvetted_local`. `axon eval` persists a row per family (fetching the Ollama digest once, out of hot path). `doctor` reports vetting/drift; an optional `eval-drift` automation refreshes rows on digest change. All hot-path reads are pure SQLite; live-digest fetches happen only in `doctor`/`eval`/automation.

**Tech Stack:** Go 1.26+, `modernc.org/sqlite`, existing `internal/db` (Execer/Queryer), `internal/tokens`, `internal/config`, `internal/core`, `internal/automations`, `internal/eval` (R5.1). No new third-party dependency.

## Global Constraints

Every task's requirements implicitly include this section. Copied from the spec (`docs/superpowers/specs/2026-07-07-eval-gated-promotion-design.md`) and CLAUDE.md:

- **Cardinal rule 1 — no Claude call bypasses the token manager.** The gate only redirects *which tier* serves a call; every resulting call still goes through `Manager.Run` (ledgered). The automation spends tokens only via R5.1 `eval.Run` (already chokepoint-routed). No new path reaches Claude.
- **Cardinal rule 2 — no vault mutation.** Gate reads DB; `doctor` reads config+DB+Ollama; `axon eval` prints + writes `eval_runs`. `reindex` never touches `eval_runs`.
- **`eval_runs` is DB-only and S9-exempt** — machine-local measurements with no vault source; `reindex` leaves it untouched (do NOT add it to any reindex rebuild pass).
- **Gate is a pure indexed SQLite read** on the hot path — NO Ollama/network call in `tokens.Authorize`. Live-digest fetches live only in `doctor`, `axon eval` persistence, and the `eval-drift` automation.
- **`models.eval_min_pass` defaults to 0 (gate off)** — backward compatible. The R5.1 `evalManager` sets `PromotionGateOff = true` (chicken-and-egg guard). Only `classify`/`routine` family aliases are gated; a concrete-ref `ModelKey` and `synthesis` are never gated.
- **Dependency boundary:** `internal/tokens` must NOT gain an `internal/db` import — the gate uses a raw inline `SELECT` against `m.db` like the existing ledger code. `internal/eval` stays pure (no `db`, no `agent`): digest fetch + row writes live in `cmd/axon`/`internal/core`/`internal/automations`.
- **Tests run with `env -u FORCE_COLOR`.** `db.Open(db.MemoryDSN)` then `db.Migrate` for in-memory DBs (Open does NOT auto-migrate). Fakes for agent/chokepoint; never real Ollama/Claude.
- **Every commit message ends with:** `Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>`

---

## File Structure

- `internal/db/migrations/0006_eval_runs.sql` — **new.** The `eval_runs` table + lookup index.
- `internal/db/eval.go` — **new.** `EvalRun`, `RecordEvalRun`, `LatestEvalRun`.
- `internal/db/eval_test.go` — **new.** Repo round-trip + latest-row selection.
- `internal/config/types.go` — **modify.** Add `EvalMinPass int` to `ModelsConfig`.
- `internal/config/models.go` — **modify.** `validateLocalRouting` friendly range message.
- `internal/config/models_test.go` — **modify.** `eval_min_pass` range test.
- `internal/tokens/manager.go` — **modify.** `Config.EvalMinPass`/`PromotionGateOff`; gate in `Authorize`; `latestEvalPass` inline query; `token.unvetted_local` event.
- `internal/tokens/promotion_test.go` — **new.** Gate behavior with fake router + in-memory DB.
- `cmd/axon/status_cmd.go` — **modify.** `managerConfig` maps `EvalMinPass`.
- `internal/core/init.go` — **modify.** Add `OllamaDigest(ctx, host, model) (string, bool)`.
- `cmd/axon/eval_cmd.go` — **modify.** Persist rows after `eval.Run`; `--no-save` flag; digest fetch.
- `cmd/axon/deps.go` — **modify.** `evalManager` sets `PromotionGateOff = true`.
- `cmd/axon/eval_cmd_test.go` — **modify.** Persistence + `--no-save` assertions.
- `internal/core/doctor.go` — **modify.** Extend local-model checks with vetting/drift states.
- `internal/core/doctor_eval_test.go` — **new.** Five-state table with injected row + digest.
- `internal/automations/evaldrift.go` — **new.** The `eval-drift` automation.
- `internal/automations/registry.go` — **modify.** Register `eval-drift`.
- `internal/automations/evaldrift_test.go` — **new.** Drift → re-eval; no-drift → no work; off → no work.

---

## Requirement → Task map

| FR | Requirement | Task(s) |
|----|-------------|---------|
| FR-142 | Persisted `eval_runs`; runtime gate; `eval_min_pass` config; eval persist + bypass | 1, 2, 3, 4 |
| FR-143 | `doctor` vetting/drift status; `eval-drift` automation | 5, 6 |

---

### Task 1: `eval_runs` migration + repository

**Files:**
- Create: `internal/db/migrations/0006_eval_runs.sql`
- Create: `internal/db/eval.go`
- Test: `internal/db/eval_test.go`

**Interfaces:**
- Consumes: `Execer`, `Queryer` (existing, `internal/db/notes.go`).
- Produces:
  ```go
  type EvalRun struct {
      Family   string
      ModelRef string
      Digest   string
      Passed   int
      Total    int
      PassPct  int
      RanAt    time.Time
  }
  func RecordEvalRun(ctx context.Context, ex Execer, r EvalRun) error
  func LatestEvalRun(ctx context.Context, q Queryer, family, modelRef string) (EvalRun, bool, error)
  ```

- [ ] **Step 1: Write the migration**

Create `internal/db/migrations/0006_eval_runs.sql`:
```sql
-- eval_runs: machine-local record of each `axon eval` outcome, keyed by task
-- family + model ref. Derived/operational (like automation_state), DB-only and
-- S9-exempt: there is no vault source to rebuild it from, so reindex ignores it.
CREATE TABLE eval_runs (
    id        INTEGER PRIMARY KEY AUTOINCREMENT,
    family    TEXT    NOT NULL,
    model_ref TEXT    NOT NULL,
    digest    TEXT    NOT NULL DEFAULT '',
    passed    INTEGER NOT NULL,
    total     INTEGER NOT NULL,
    pass_pct  INTEGER NOT NULL,
    ran_at    TEXT    NOT NULL
);
CREATE INDEX idx_eval_runs_lookup ON eval_runs (family, model_ref, ran_at DESC);
```

- [ ] **Step 2: Write the failing repo test**

Create `internal/db/eval_test.go`:
```go
package db

import (
	"context"
	"testing"
	"time"
)

func TestRecordAndLatestEvalRun(t *testing.T) {
	ctx := context.Background()
	sqlDB, err := Open(MemoryDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	if _, err := Migrate(sqlDB); err != nil {
		t.Fatal(err)
	}

	if _, ok, err := LatestEvalRun(ctx, sqlDB, "routine", "ollama:qwen"); err != nil || ok {
		t.Fatalf("empty table: ok=%v err=%v, want ok=false", ok, err)
	}

	older := EvalRun{Family: "routine", ModelRef: "ollama:qwen", Digest: "d1", Passed: 6, Total: 10, PassPct: 60, RanAt: time.Unix(1000, 0).UTC()}
	newer := EvalRun{Family: "routine", ModelRef: "ollama:qwen", Digest: "d2", Passed: 9, Total: 10, PassPct: 90, RanAt: time.Unix(2000, 0).UTC()}
	for _, r := range []EvalRun{older, newer} {
		if err := RecordEvalRun(ctx, sqlDB, r); err != nil {
			t.Fatal(err)
		}
	}
	got, ok, err := LatestEvalRun(ctx, sqlDB, "routine", "ollama:qwen")
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if got.PassPct != 90 || got.Digest != "d2" {
		t.Fatalf("latest = %+v, want pct 90 digest d2", got)
	}
	if _, ok, _ := LatestEvalRun(ctx, sqlDB, "classify", "ollama:qwen"); ok {
		t.Fatal("different family must not match")
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `env -u FORCE_COLOR go test ./internal/db/ -run TestRecordAndLatestEvalRun -v`
Expected: FAIL — `undefined: EvalRun`.

- [ ] **Step 4: Write `eval.go`**

Create `internal/db/eval.go`:
```go
package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// EvalRun is one row of eval_runs: the outcome of `axon eval` for one task
// family against one model ref on this machine (FR-142). DB-only, S9-exempt.
type EvalRun struct {
	Family   string
	ModelRef string
	Digest   string
	Passed   int
	Total    int
	PassPct  int
	RanAt    time.Time
}

// RecordEvalRun inserts one eval outcome.
func RecordEvalRun(ctx context.Context, ex Execer, r EvalRun) error {
	if _, err := ex.ExecContext(ctx,
		`INSERT INTO eval_runs (family, model_ref, digest, passed, total, pass_pct, ran_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?);`,
		r.Family, r.ModelRef, r.Digest, r.Passed, r.Total, r.PassPct,
		r.RanAt.UTC().Format(time.RFC3339)); err != nil {
		return fmt.Errorf("insert eval_run %s/%s: %w", r.Family, r.ModelRef, err)
	}
	return nil
}

// LatestEvalRun returns the most recent row for (family, modelRef); ok=false
// when none exists.
func LatestEvalRun(ctx context.Context, q Queryer, family, modelRef string) (EvalRun, bool, error) {
	var (
		r     EvalRun
		ranAt string
	)
	err := q.QueryRowContext(ctx,
		`SELECT family, model_ref, digest, passed, total, pass_pct, ran_at
		   FROM eval_runs
		  WHERE family = ? AND model_ref = ?
		  ORDER BY ran_at DESC, id DESC
		  LIMIT 1;`, family, modelRef).
		Scan(&r.Family, &r.ModelRef, &r.Digest, &r.Passed, &r.Total, &r.PassPct, &ranAt)
	if errors.Is(err, sql.ErrNoRows) {
		return EvalRun{}, false, nil
	}
	if err != nil {
		return EvalRun{}, false, fmt.Errorf("query latest eval_run: %w", err)
	}
	r.RanAt, _ = time.Parse(time.RFC3339, ranAt)
	return r, true, nil
}
```
Note: confirm `Queryer` exposes `QueryRowContext` (defined in `internal/db/notes.go:282`); if it only has `QueryContext`, mirror whichever single-row reader an existing `internal/db` file uses (or use `Queryer2`).

- [ ] **Step 5: Run test to verify it passes**

Run: `env -u FORCE_COLOR go test ./internal/db/ -run TestRecordAndLatestEvalRun -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/db/migrations/0006_eval_runs.sql internal/db/eval.go internal/db/eval_test.go
git commit -m "$(cat <<'EOF'
feat(db): eval_runs table + RecordEvalRun/LatestEvalRun repo (FR-142)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 2: `models.eval_min_pass` config

**Files:**
- Modify: `internal/config/types.go`
- Modify: `internal/config/models.go`
- Test: `internal/config/models_test.go`

**Interfaces:**
- Produces: `ModelsConfig.EvalMinPass int` (yaml `eval_min_pass`, validate `omitempty,min=0,max=100`).

- [ ] **Step 1: Write the failing test**

Add to `internal/config/models_test.go` inside `TestValidateLocalRouting`'s table (mirror existing rows):
```go
{"eval_min_pass in range ok", func(m *ModelsConfig) { m.EvalMinPass = 80 }, false},
{"eval_min_pass over 100 rejected", func(m *ModelsConfig) { m.EvalMinPass = 150 }, true},
```

- [ ] **Step 2: Run test to verify it fails**

Run: `env -u FORCE_COLOR go test ./internal/config/ -run TestValidateLocalRouting -v`
Expected: FAIL — `EvalMinPass` undefined.

- [ ] **Step 3: Add the field + validation**

In `internal/config/types.go`, add to `ModelsConfig` (after `LocalFallback`):
```go
	// EvalMinPass gates local-tier promotion (R5.2/FR-142): a local
	// classify/routine model serves its tier only when its latest `axon eval`
	// pass rate is >= this percent. 0 (default) disables the gate — local tiers
	// route as configured. New installs scaffold 80; doctor nudges.
	EvalMinPass int `yaml:"eval_min_pass,omitempty" validate:"omitempty,min=0,max=100"`
```

In `internal/config/models.go`, add to `validateLocalRouting` (before `return nil`):
```go
	if m.EvalMinPass < 0 || m.EvalMinPass > 100 {
		return fmt.Errorf("models.eval_min_pass must be 0..100 (got %d)", m.EvalMinPass)
	}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `env -u FORCE_COLOR go test ./internal/config/ -run TestValidateLocalRouting -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/config/types.go internal/config/models.go internal/config/models_test.go
git commit -m "$(cat <<'EOF'
feat(config): models.eval_min_pass (0..100, default 0 opt-in) (FR-142)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 3: Runtime admission gate in the chokepoint

**Files:**
- Modify: `internal/tokens/manager.go`
- Modify: `cmd/axon/status_cmd.go` (managerConfig mapping)
- Test: `internal/tokens/promotion_test.go`

**Interfaces:**
- Consumes: `Config` (existing), `resolveRef`/`resolveModel`/`fallbackClaudeKey`/`emit` (existing, `manager.go`), `config.ProviderClaude`, `events.LevelWarn`.
- Produces:
  ```go
  // new Config fields
  EvalMinPass int
  PromotionGateOff bool
  // new manager helpers
  func isGatedTier(key string) bool
  func (m *manager) latestEvalPass(ctx context.Context, family, ref string) (pct int, ok bool)
  ```

- [ ] **Step 1: Write the failing test**

Create `internal/tokens/promotion_test.go`. Mirror the existing `testManagerRouter`/`localTestConfig` helpers in `internal/tokens/local_test.go` (same package). Each test seeds `eval_runs` directly via SQL.
```go
package tokens

import (
	"context"
	"testing"

	"github.com/jandro-es/axon/internal/agent"
)

func seedEvalRun(t *testing.T, m *manager, family, ref string, pct int) {
	t.Helper()
	if _, err := m.db.Exec(
		`INSERT INTO eval_runs (family, model_ref, digest, passed, total, pass_pct, ran_at)
		 VALUES (?, ?, '', ?, 100, ?, '2026-07-07T00:00:00Z');`,
		family, ref, pct, pct); err != nil {
		t.Fatal(err)
	}
}

func gateConfig() Config {
	c := localTestConfig() // confirm classify tier is a local ollama ref
	c.Models.Classify = "ollama:qwen"
	c.EvalMinPass = 80
	return c
}

func TestGateAdmitsVettedLocal(t *testing.T) {
	ctx := context.Background()
	local := agent.NewFake()
	local.Reply = "from-local"
	claude := agent.NewFake()
	claude.Reply = "from-claude"
	m := testManagerRouter(t, gateConfig(), agent.Router{Claude: claude, Ollama: local})
	seedEvalRun(t, m, "classify", m.resolveModel("classify"), 90)

	res, err := m.Run(ctx, AgentCall{Operation: "t", ModelKey: "classify",
		Messages: []Message{{Role: "user", Content: "hi"}}})
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != "from-local" {
		t.Fatalf("vetted local should serve, got %q", res.Text)
	}
}

func TestGateRetargetsUnvetted(t *testing.T) {
	ctx := context.Background()
	local := agent.NewFake()
	local.Reply = "from-local"
	claude := agent.NewFake()
	claude.Reply = "from-claude"
	m := testManagerRouter(t, gateConfig(), agent.Router{Claude: claude, Ollama: local})
	res, err := m.Run(ctx, AgentCall{Operation: "t", ModelKey: "classify",
		Messages: []Message{{Role: "user", Content: "hi"}}})
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != "from-claude" {
		t.Fatalf("unvetted local must retarget to Claude, got %q", res.Text)
	}
	if local.CallCount() != 0 {
		t.Fatalf("local must not be called when unvetted, got %d", local.CallCount())
	}
}

func TestGateBelowThresholdRetargets(t *testing.T) {
	ctx := context.Background()
	claude := agent.NewFake()
	claude.Reply = "from-claude"
	local := agent.NewFake()
	local.Reply = "from-local"
	m := testManagerRouter(t, gateConfig(), agent.Router{Claude: claude, Ollama: local})
	seedEvalRun(t, m, "classify", m.resolveModel("classify"), 60)
	res, _ := m.Run(ctx, AgentCall{Operation: "t", ModelKey: "classify",
		Messages: []Message{{Role: "user", Content: "hi"}}})
	if res.Text != "from-claude" {
		t.Fatalf("below-threshold must retarget, got %q", res.Text)
	}
}

func TestGateOffBypasses(t *testing.T) {
	ctx := context.Background()
	local := agent.NewFake()
	local.Reply = "from-local"
	claude := agent.NewFake()
	c := localTestConfig()
	c.Models.Classify = "ollama:qwen" // EvalMinPass stays 0
	m := testManagerRouter(t, c, agent.Router{Claude: claude, Ollama: local})
	res, _ := m.Run(ctx, AgentCall{Operation: "t", ModelKey: "classify",
		Messages: []Message{{Role: "user", Content: "hi"}}})
	if res.Text != "from-local" {
		t.Fatalf("gate off (min_pass 0) must route local, got %q", res.Text)
	}
}

func TestPromotionGateOffBypasses(t *testing.T) {
	ctx := context.Background()
	local := agent.NewFake()
	local.Reply = "from-local"
	claude := agent.NewFake()
	c := gateConfig()
	c.PromotionGateOff = true
	m := testManagerRouter(t, c, agent.Router{Claude: claude, Ollama: local})
	res, _ := m.Run(ctx, AgentCall{Operation: "t", ModelKey: "classify",
		Messages: []Message{{Role: "user", Content: "hi"}}})
	if res.Text != "from-local" {
		t.Fatalf("PromotionGateOff must route local even unvetted, got %q", res.Text)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `env -u FORCE_COLOR go test ./internal/tokens/ -run 'TestGate|TestPromotionGateOff' -v`
Expected: FAIL — `Config.EvalMinPass` undefined / unvetted currently routes local.

- [ ] **Step 3: Add Config fields**

In `internal/tokens/manager.go`, add to `Config` (after `RedactionRules`):
```go
	// EvalMinPass mirrors models.eval_min_pass (percent, 0–100). 0 disables the
	// promotion gate (FR-142). Set by managerConfig from the profile.
	EvalMinPass int
	// PromotionGateOff disables the gate for this manager regardless of
	// EvalMinPass. Set ONLY by the eval harness's own manager so `axon eval`
	// measures the real local model (chicken-and-egg guard).
	PromotionGateOff bool
```

- [ ] **Step 4: Implement the gate in `Authorize`**

In `Authorize`, replace:
```go
	ref := m.resolveRef(call.ModelKey)
	model := ref.Model
```
with:
```go
	ref := m.resolveRef(call.ModelKey)
	// Eval-gated promotion (FR-142): an unvetted local classify/routine tier is
	// retargeted to its Claude fallback before any budget/routing decision. Pure
	// SQLite read — no Ollama call on the hot path.
	if m.cfg.EvalMinPass > 0 && !m.cfg.PromotionGateOff &&
		ref.Provider != config.ProviderClaude && isGatedTier(call.ModelKey) {
		if pct, ok := m.latestEvalPass(ctx, call.ModelKey, m.resolveModel(call.ModelKey)); !ok || pct < m.cfg.EvalMinPass {
			fbKey := m.fallbackClaudeKey(call.ModelKey)
			m.emit(events.LevelWarn, "token.unvetted_local", call.Operation,
				Authorization{Model: ref.Model, Provider: ref.Provider},
				map[string]any{"tier": call.ModelKey, "ref": m.resolveModel(call.ModelKey),
					"pass_pct": pct, "min_pass": m.cfg.EvalMinPass, "routed_to": fbKey})
			call.ModelKey = fbKey
			ref = m.resolveRef(call.ModelKey)
		}
	}
	model := ref.Model
```
Add helpers near `resolveModel`:
```go
// isGatedTier reports whether a model key is a promotable family alias subject
// to the eval-promotion gate. Concrete refs (deliberate overrides) and synthesis
// (always Claude) are never gated.
func isGatedTier(key string) bool { return key == "classify" || key == "routine" }

// latestEvalPass returns the most recent eval pass percent for (family, ref)
// via a raw query on the manager's DB (no internal/db import; mirrors the
// ledger SQL). ok is false when no row exists or the read fails (fail-closed:
// the caller treats the tier as unvetted).
func (m *manager) latestEvalPass(ctx context.Context, family, ref string) (int, bool) {
	if m.db == nil {
		return 0, false
	}
	var pct int
	if err := m.db.QueryRowContext(ctx,
		`SELECT pass_pct FROM eval_runs WHERE family = ? AND model_ref = ?
		 ORDER BY ran_at DESC, id DESC LIMIT 1;`, family, ref).Scan(&pct); err != nil {
		return 0, false
	}
	return pct, true
}
```
Confirm `events` and `config` are already imported in `manager.go` (they are). `AgentCall` is passed by value, so mutating the local `call` copy is safe.

- [ ] **Step 5: Map the field in managerConfig**

In `cmd/axon/status_cmd.go`, add to the `tokens.Config` literal in `managerConfig`:
```go
		EvalMinPass:    p.Models.EvalMinPass,
```

- [ ] **Step 6: Run tests to verify they pass**

Run: `env -u FORCE_COLOR go test ./internal/tokens/ -run 'TestGate|TestPromotionGateOff' -v && env -u FORCE_COLOR go test ./internal/tokens/`
Expected: PASS (new gate tests + no regression).

- [ ] **Step 7: Commit**

```bash
git add internal/tokens/manager.go internal/tokens/promotion_test.go cmd/axon/status_cmd.go
git commit -m "$(cat <<'EOF'
feat(tokens): eval-promotion admission gate in Authorize (FR-142)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 4: `axon eval` persists results + eval-mode bypass

**Files:**
- Modify: `internal/core/init.go` (add `OllamaDigest`)
- Modify: `cmd/axon/deps.go` (`evalManager` sets `PromotionGateOff`)
- Modify: `cmd/axon/eval_cmd.go` (persist + `--no-save`)
- Test: `cmd/axon/eval_cmd_test.go`

**Interfaces:**
- Consumes: `db.RecordEvalRun`/`db.EvalRun` (Task 1), `eval.Report`/`eval.FamilyReport` (R5.1), `config.ParseModelRef`, `ollamaModelPresent` pattern (existing `init.go`).
- Produces:
  ```go
  func OllamaDigest(ctx context.Context, host, model string) (string, bool) // internal/core
  func persistEvalRuns(ctx context.Context, ex db.Execer, rep eval.Report, digestOf func(ref string) string) error // cmd/axon
  ```

- [ ] **Step 1: Add the digest helper**

Add to `internal/core/init.go` (mirrors `ollamaModelPresent`, which GETs `/api/tags`):
```go
// OllamaDigest returns the content digest of a pulled model from /api/tags, and
// ok=false if Ollama is unreachable or the model is absent. Used out of the hot
// path (eval persistence, doctor drift, eval-drift automation) — never in the
// token gate.
func OllamaDigest(ctx context.Context, host, model string) (string, bool) {
	host = strings.TrimRight(host, "/")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, host+"/api/tags", nil)
	if err != nil {
		return "", false
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", false
	}
	defer resp.Body.Close()
	var body struct {
		Models []struct {
			Name   string `json:"name"`
			Digest string `json:"digest"`
		} `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", false
	}
	for _, mm := range body.Models {
		if mm.Name == model {
			return mm.Digest, true
		}
	}
	return "", false
}
```
Confirm `net/http`, `strings`, `encoding/json` are imported in `init.go` (they are, per `ollamaModelPresent`). No dedicated unit test (network); exercised via Task 4 Step 3 with a digest func that returns `""`.

- [ ] **Step 2: evalManager sets the bypass**

In `cmd/axon/deps.go` `evalManager`, force the gate off on the eval manager:
```go
	mc := managerConfig(d.name, p, d.cfg)
	mc.PromotionGateOff = true // eval measures the real local model (FR-142 guard)
	mgr := tokens.NewWithRouter(d.db, d.agentRouter(), d.buildSearcher(), bus, mc)
```
(replacing the current single `mgr := tokens.NewWithRouter(...)` line).

- [ ] **Step 3: Write the failing persistence test**

Add to `cmd/axon/eval_cmd_test.go` (add imports `context`, `github.com/jandro-es/axon/internal/db`):
```go
func TestPersistEvalRunsWritesRows(t *testing.T) {
	ctx := context.Background()
	sqlDB, err := db.Open(db.MemoryDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	if _, err := db.Migrate(sqlDB); err != nil {
		t.Fatal(err)
	}
	rep := eval.Report{Families: []eval.FamilyReport{
		{Family: eval.FamilyClassify, Model: "ollama:qwen", Total: 4, Passed: 3},
	}}
	if err := persistEvalRuns(ctx, sqlDB, rep, func(string) string { return "" }); err != nil {
		t.Fatal(err)
	}
	got, ok, err := db.LatestEvalRun(ctx, sqlDB, "classify", "ollama:qwen")
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if got.PassPct != 75 {
		t.Fatalf("pass_pct = %d, want 75", got.PassPct)
	}
}
```

- [ ] **Step 4: Run test to verify it fails**

Run: `env -u FORCE_COLOR go test ./cmd/axon/ -run TestPersistEvalRuns -v`
Expected: FAIL — `undefined: persistEvalRuns`.

- [ ] **Step 5: Implement persistence + wire `--no-save`**

In `cmd/axon/eval_cmd.go` add (imports: `time`, `github.com/jandro-es/axon/internal/core`, `github.com/jandro-es/axon/internal/db`, `github.com/jandro-es/axon/internal/config`):
```go
// persistEvalRuns writes one eval_runs row per family. digestOf resolves a
// family's model ref to its current digest ("" when unavailable / not ollama).
func persistEvalRuns(ctx context.Context, ex db.Execer, rep eval.Report, digestOf func(ref string) string) error {
	for _, f := range rep.Families {
		pct := 0
		if f.Total > 0 {
			pct = f.Passed * 100 / f.Total
		}
		if err := db.RecordEvalRun(ctx, ex, db.EvalRun{
			Family: string(f.Family), ModelRef: f.Model, Digest: digestOf(f.Model),
			Passed: f.Passed, Total: f.Total, PassPct: pct, RanAt: time.Now(),
		}); err != nil {
			return err
		}
	}
	return nil
}
```
Add `var noSave bool`, the flag `cmd.Flags().BoolVar(&noSave, "no-save", false, "do not persist results to eval_runs")`, and in `RunE` after `rep, err := eval.Run(...)` + error check, before output:
```go
			if !noSave {
				host := deps.profile.Models.OllamaHost
				digestOf := func(ref string) string {
					r := config.ParseModelRef(ref)
					if r.Provider != config.ProviderOllama {
						return ""
					}
					d, _ := core.OllamaDigest(cmd.Context(), host, r.Model)
					return d
				}
				if err := persistEvalRuns(cmd.Context(), deps.db, rep, digestOf); err != nil {
					return err
				}
			}
```

- [ ] **Step 6: Run tests to verify they pass**

Run: `env -u FORCE_COLOR go test ./cmd/axon/ -run 'TestPersistEvalRuns|TestRunEvalScorecard' -v && go build ./cmd/axon/`
Expected: PASS + clean build.

- [ ] **Step 7: Commit**

```bash
git add internal/core/init.go cmd/axon/deps.go cmd/axon/eval_cmd.go cmd/axon/eval_cmd_test.go
git commit -m "$(cat <<'EOF'
feat(eval): persist eval_runs on `axon eval` + eval-mode gate bypass (FR-142)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 5: Doctor vetting + drift status

**Files:**
- Modify: `internal/core/doctor.go`
- Test: `internal/core/doctor_eval_test.go`

**Interfaces:**
- Consumes: `db.EvalRun`/`db.LatestEvalRun` (Task 1), `OllamaDigest` (Task 4), existing `Check`/`StatusOK`/`StatusWarn`.
- Produces: pure `vettingCheck(name, tier, ref string, minPass int, row db.EvalRun, haveRow bool, curDigest string, digestKnown bool) Check` + a `localModelsVettingChecks(ctx, p, sqlDB)` assembler.

- [ ] **Step 1: Write the failing test**

Create `internal/core/doctor_eval_test.go` (use `strings.Contains`):
```go
package core

import (
	"strings"
	"testing"

	"github.com/jandro-es/axon/internal/db"
)

func TestVettingCheckStates(t *testing.T) {
	ref := "ollama:qwen"
	pass := db.EvalRun{Family: "classify", ModelRef: ref, Digest: "d1", PassPct: 90}
	cases := []struct {
		name     string
		minPass  int
		row      db.EvalRun
		haveRow  bool
		curDig   string
		digKnown bool
		want     Status
		substr   string
	}{
		{"ungated", 0, db.EvalRun{}, false, "d1", true, StatusWarn, "ungated"},
		{"not-vetted", 80, db.EvalRun{}, false, "d1", true, StatusWarn, "not vetted"},
		{"below", 80, db.EvalRun{PassPct: 60, Digest: "d1"}, true, "d1", true, StatusWarn, "below"},
		{"drift", 80, pass, true, "d2", true, StatusWarn, "changed"},
		{"vetted", 80, pass, true, "d1", true, StatusOK, "vetted"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := vettingCheck("local-model:classify", "classify", ref, c.minPass, c.row, c.haveRow, c.curDig, c.digKnown)
			if got.Status != c.want {
				t.Fatalf("status = %v, want %v (%s)", got.Status, c.want, got.Detail)
			}
			if !strings.Contains(got.Detail, c.substr) {
				t.Fatalf("detail %q missing %q", got.Detail, c.substr)
			}
		})
	}
}
```
Confirm `Check`'s field names (`Status`, `Detail`) against `internal/core/doctor.go`; adjust the assertions if the struct uses different names.

- [ ] **Step 2: Run test to verify it fails**

Run: `env -u FORCE_COLOR go test ./internal/core/ -run TestVettingCheckStates -v`
Expected: FAIL — `undefined: vettingCheck`.

- [ ] **Step 3: Implement `vettingCheck` + assembler**

Add to `internal/core/doctor.go`:
```go
// vettingCheck renders the eval-promotion status for one gated local tier
// (FR-143). Pure: the caller supplies the persisted row and live digest so it is
// unit-testable without Ollama.
func vettingCheck(name, tier, ref string, minPass int, row db.EvalRun, haveRow bool, curDigest string, digestKnown bool) Check {
	switch {
	case minPass == 0:
		return Check{name, StatusWarn,
			fmt.Sprintf("local tier %s is ungated — set models.eval_min_pass to require evals", ref)}
	case !haveRow:
		return Check{name, StatusWarn,
			fmt.Sprintf("%s not vetted — run `axon eval --family %s --model %s`", ref, tier, ref)}
	case row.PassPct < minPass:
		return Check{name, StatusWarn,
			fmt.Sprintf("%s scored %d%% below %d%% — routes to Claude until it passes", ref, row.PassPct, minPass)}
	case digestKnown && row.Digest != "" && curDigest != "" && row.Digest != curDigest:
		return Check{name, StatusWarn,
			fmt.Sprintf("%s changed since eval (%s → %s) — re-run `axon eval`", ref, short(row.Digest), short(curDigest))}
	default:
		return Check{name, StatusOK, fmt.Sprintf("%s vetted %d%%", ref, row.PassPct)}
	}
}

func short(d string) string {
	if len(d) > 12 {
		return d[:12]
	}
	return d
}

// localModelsVettingChecks emits one vettingCheck per gated local classify/
// routine tier. Called from the doctor assembler alongside localModelsCheck.
func localModelsVettingChecks(ctx context.Context, p config.Profile, sqlDB *sql.DB) []Check {
	var checks []Check
	m := p.Models
	host := m.OllamaHost
	for _, t := range []struct{ tier, ref string }{{"classify", m.Classify}, {"routine", m.Routine}} {
		r := config.ParseModelRef(t.ref)
		if r.Provider == config.ProviderClaude {
			continue // Claude tiers are never gated
		}
		name := "eval-vetting:" + t.tier
		if m.EvalMinPass == 0 {
			checks = append(checks, vettingCheck(name, t.tier, t.ref, 0, db.EvalRun{}, false, "", false))
			continue
		}
		row, have, _ := db.LatestEvalRun(ctx, sqlDB, t.tier, t.ref)
		var cur string
		var known bool
		if r.Provider == config.ProviderOllama {
			cur, known = OllamaDigest(ctx, host, r.Model)
		}
		checks = append(checks, vettingCheck(name, t.tier, t.ref, m.EvalMinPass, row, have, cur, known))
	}
	return checks
}
```
Then call `localModelsVettingChecks(ctx, p, sqlDB)` from the doctor assembler and append its results (find where `localModelsCheck(p)` is invoked and where the `*sql.DB` is available — doctor already opens the DB for other checks; thread it in). Confirm `context`, `database/sql`, `fmt` are imported in `doctor.go`.

- [ ] **Step 4: Run tests to verify they pass**

Run: `env -u FORCE_COLOR go test ./internal/core/ -run TestVettingCheckStates -v && env -u FORCE_COLOR go test ./internal/core/`
Expected: PASS + no regression.

- [ ] **Step 5: Commit**

```bash
git add internal/core/doctor.go internal/core/doctor_eval_test.go
git commit -m "$(cat <<'EOF'
feat(doctor): local-tier vetting + version-drift status (FR-143)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 6: `eval-drift` automation

**Files:**
- Create: `internal/automations/evaldrift.go`
- Modify: `internal/automations/registry.go`
- Test: `internal/automations/evaldrift_test.go`

**Interfaces:**
- Consumes: the `Automation` interface + `RunCtx`/`RunResult` (existing — **read `internal/automations/sessionmem.go` first** and copy its exact method set, RunCtx accessors, and RunResult "changed" field), `eval.LoadCases`/`eval.Run`/`eval.Options` (R5.1), `db.RecordEvalRun`/`db.LatestEvalRun`, `core.OllamaDigest`, `config.ParseModelRef`.
- Produces: an `Automation` named `eval-drift`, registered in `registry.go`, **default off** (only runs when the user adds it to `config.Automations`).

- [ ] **Step 1: Write the failing test**

Create `internal/automations/evaldrift_test.go`. Mirror `internal/automations/sessionmem_test.go` for the registry assertion and RunCtx wiring. Cover: (a) registered under `eval-drift`; (b) digest drift on a gated ollama tier → `eval.Run` invoked (fake chokepoint) + fresh `eval_runs` row; (c) stored digest == current → no eval, no new row; (d) `EvalMinPass == 0` → no work. Inject the digest via the struct's `digestFn` seam.
```go
func TestEvalDriftRegistered(t *testing.T) {
	p := config.Profile{Automations: map[string]config.Automation{"eval-drift": {Model: "routine"}}}
	reg := Registry(p) // mirror how sibling tests obtain the registry
	if _, ok := reg["eval-drift"]; !ok {
		t.Fatal("eval-drift must be registered")
	}
}
```
(Fill (b)–(d) mirroring `sessionmem_test.go`'s RunCtx construction; a fake `eval.Chokepoint` returns canned classify answers so `eval.Run` completes without network.)

- [ ] **Step 2: Run test to verify it fails**

Run: `env -u FORCE_COLOR go test ./internal/automations/ -run TestEvalDrift -v`
Expected: FAIL — `eval-drift` not registered.

- [ ] **Step 3: Implement the automation**

Create `internal/automations/evaldrift.go` (adapt method signatures to the real `Automation` interface after reading `sessionmem.go`):
```go
package automations

import (
	"context"
	"time"

	"github.com/jandro-es/axon/internal/config"
	"github.com/jandro-es/axon/internal/core"
	"github.com/jandro-es/axon/internal/db"
	"github.com/jandro-es/axon/internal/eval"
)

// evalDrift re-runs the eval harness for a gated local tier when its Ollama
// digest has changed since the last recorded eval (FR-143). Default off (S8);
// token-frugal (no digest change → no work). digestFn is a test seam.
type evalDrift struct {
	digestFn func(ctx context.Context, host, model string) (string, bool)
}

func newEvalDrift() *evalDrift { return &evalDrift{digestFn: core.OllamaDigest} }

func (a *evalDrift) Name() string { return "eval-drift" }

// Run: for each gated local classify/routine tier whose current digest differs
// from the latest eval_runs.digest (or has no row), run eval.Run through the
// chokepoint and record the fresh result. Adapt rc.* accessors to the real
// RunCtx (Manager, DB, Config).
func (a *evalDrift) Run(ctx context.Context, rc RunCtx) (RunResult, error) {
	m := rc.Config.Models
	if m.EvalMinPass == 0 {
		return RunResult{}, nil
	}
	var refreshed int
	for _, tier := range []struct{ family, ref string }{
		{"classify", m.Classify}, {"routine", m.Routine},
	} {
		pr := config.ParseModelRef(tier.ref)
		if pr.Provider != config.ProviderOllama {
			continue
		}
		cur, ok := a.digestFn(ctx, m.OllamaHost, pr.Model)
		if !ok {
			continue
		}
		row, have, err := db.LatestEvalRun(ctx, rc.DB, tier.family, tier.ref)
		if err != nil {
			return RunResult{}, err
		}
		if have && row.Digest == cur {
			continue
		}
		cases, err := eval.LoadCases(tier.family)
		if err != nil {
			return RunResult{}, err
		}
		rep, err := eval.Run(ctx, rc.Manager, cases, eval.Options{
			Model: tier.ref, Family: tier.family,
			ExpectModel: func(string) string { return pr.Model },
		})
		if err != nil {
			return RunResult{}, err
		}
		for _, f := range rep.Families {
			pct := 0
			if f.Total > 0 {
				pct = f.Passed * 100 / f.Total
			}
			if err := db.RecordEvalRun(ctx, rc.DB, db.EvalRun{
				Family: string(f.Family), ModelRef: tier.ref, Digest: cur,
				Passed: f.Passed, Total: f.Total, PassPct: pct, RanAt: time.Now(),
			}); err != nil {
				return RunResult{}, err
			}
		}
		refreshed++
	}
	return RunResult{Changed: refreshed > 0}, nil
}
```
Adjust: `rc.Manager` must satisfy `eval.Chokepoint` (it is `tokens.Manager` whose `Run(ctx, AgentCall) (AgentResult, error)` matches). Confirm `rc.DB`, `rc.Config`, and `RunResult`'s "did work" field names against `internal/automations/engine.go`.

- [ ] **Step 4: Register the automation**

In `internal/automations/registry.go`, add to the `reg` map:
```go
		"eval-drift": newEvalDrift(),
```
Match the map's value type (constructed automation vs factory) to its neighbors.

- [ ] **Step 5: Run tests to verify they pass**

Run: `env -u FORCE_COLOR go test ./internal/automations/ -run TestEvalDrift -v && env -u FORCE_COLOR go test ./internal/automations/`
Expected: PASS + no regression.

- [ ] **Step 6: Full-module verification**

Run:
```bash
go build ./... && go vet ./... && env -u FORCE_COLOR go test ./...
```
Expected: all green.

- [ ] **Step 7: Commit**

```bash
git add internal/automations/evaldrift.go internal/automations/registry.go internal/automations/evaldrift_test.go
git commit -m "$(cat <<'EOF'
feat(automations): eval-drift re-runs evals on model version change (FR-143)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Self-Review

**Spec coverage:** `eval_runs` table + repo (S9-exempt) — Task 1; `models.eval_min_pass` (0–100, default 0) — Task 2; runtime gate in `Authorize` (retarget unvetted local → Claude fallback, emit `token.unvetted_local`, pure DB read, both bypasses, only classify/routine gated) — Task 3; `axon eval` persistence + digest + `--no-save` + eval-mode bypass — Task 4; doctor five states — Task 5; `eval-drift` automation, default off, digest-gated — Task 6. Cardinal rules: gate only redirects (rule 1), no vault writes (rule 2), tokens uses raw SQL (no `internal/db` import) — Tasks 3–6.

**Type consistency:** `db.EvalRun`/`RecordEvalRun`/`LatestEvalRun` identical across Tasks 1/4/5/6. `tokens.Config.EvalMinPass`/`PromotionGateOff` set in Task 3 (managerConfig) + Task 4 (evalManager). `eval.Report`/`FamilyReport` fields (`Family`, `Model`, `Total`, `Passed`) match R5.1 in Tasks 4/6. `pass_pct = passed*100/total` computed identically in Tasks 4 and 6.

**Placeholder scan:** three seams are flagged "confirm against sibling file" rather than guessed — the `Queryer` single-row method (Task 1), the `Automation`/`RunCtx`/`RunResult` shape (Task 6, mirror `sessionmem.go`), and threading the DB into the doctor assembler (Task 5) — each names the concrete file to mirror and supplies complete logic. No TODO/TBD; no field used before it is defined.

**Scope check:** one plan, six independently-testable tasks, each ending in a commit. R5.3 (per-call verification cascade) explicitly excluded.

## Execution Handoff

Per Jandro's standing workflow (inline execution, spec-review gate passed), execute inline via superpowers:executing-plans with a checkpoint after each task's commit, on branch `feature/eval-gated-promotion`.
