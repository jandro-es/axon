# R5.3 Cascade-Verification Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a per-call verification cascade to the token-manager chokepoint: a successful local `routine` answer is scored by a cheap local judge and escalated to Claude when the score is below a floor — all ledgered, default off.

**Architecture:** Everything hangs off the existing `runLocal` success branch in `internal/tokens/manager.go`. Two pure helpers (`buildVerifyPrompt`/`parseVerifyScore`) build/parse the judge call; three manager methods (`verifyActive`/`runJudge`/`verifyAndMaybeEscalate`) run it and decide escalation via the existing `fallbackClaudeKey` + `Run` path. Config gains `models.verify` + `models.verify_min_score`; `doctor` gains `verifyCheck`. No new package, automation, or MCP tool.

**Tech Stack:** Go 1.26+, `modernc.org/sqlite`, existing `internal/agent` router + `internal/events` bus. Tests are table-driven with `agent.Fake` (its `RespondFn` scripts answer-then-judge by `req.Operation` suffix).

## Global Constraints

- **Cardinal rule 1:** the judge call and the Claude escalation both ledger through `tokens.Manager`; no new path reaches Claude. The judge is a local (budget-exempt) call ledgered as `<op>:verify`.
- **Cardinal rule 2:** no vault mutation anywhere in this slice.
- **Scope:** verify the `routine` tier **only** (synthesis is always Claude; classify is `ValidateOutput`-graded). Provider must be `ollama`.
- **Default off (S8):** `models.verify` empty/`"off"` → today's behaviour byte-for-byte.
- **Loop-safe / spend-safe:** the judge uses a concrete `ollama:<model>` ref (never re-gated/re-verified) and **never** falls forward to Claude; a broken/inconclusive judge keeps the local answer; a budget-blocked escalation degrades to the local answer.
- Run test suites with `env -u FORCE_COLOR` (the ambient shell exports `FORCE_COLOR=3`).
- No new automation or MCP tool → the registry/tool count-assertions must stay untouched.
- FR-144, FR-145; ADR-031. Spec: `docs/superpowers/specs/2026-07-07-cascade-verification-design.md`.

---

### Task 1: Config — `models.verify` + `models.verify_min_score`

**Files:**
- Modify: `internal/config/types.go` (add two fields to `ModelsConfig`, ~after line 308)
- Modify: `internal/config/models.go` (helpers + `validateLocalRouting` rule)
- Modify: `docs/04-data-model-and-config.md:218-220` (optional-keys comment)
- Test: `internal/config/verify_test.go` (create), `internal/config/models_test.go` (add cases)

**Interfaces:**
- Produces: `ModelsConfig.Verify string`, `ModelsConfig.VerifyMinScore int`; `(ModelsConfig).VerifyMode() string` (→ `"off"` when unset), `(ModelsConfig).VerifyMinScoreOr() int` (→ 6 when unset). Consumed by Tasks 3 and 4.

- [ ] **Step 1: Write the failing test**

Create `internal/config/verify_test.go`:

```go
package config

import "testing"

func TestVerifyModeDefaultsOff(t *testing.T) {
	if got := (ModelsConfig{}).VerifyMode(); got != "off" {
		t.Errorf("empty VerifyMode = %q, want off", got)
	}
	if got := (ModelsConfig{Verify: "ollama:judge"}).VerifyMode(); got != "ollama:judge" {
		t.Errorf("VerifyMode = %q", got)
	}
}

func TestVerifyMinScoreOr(t *testing.T) {
	if got := (ModelsConfig{}).VerifyMinScoreOr(); got != 6 {
		t.Errorf("default VerifyMinScoreOr = %d, want 6", got)
	}
	if got := (ModelsConfig{VerifyMinScore: 8}).VerifyMinScoreOr(); got != 8 {
		t.Errorf("VerifyMinScoreOr = %d, want 8", got)
	}
}
```

Add to `internal/config/models_test.go`'s `TestValidateLocalRouting` table (the same table `bad fallback rejected` lives in) these cases:

```go
		{"verify ollama ok", func(m *ModelsConfig) { m.Verify = "ollama:judge" }, false},
		{"verify off ok", func(m *ModelsConfig) { m.Verify = "off" }, false},
		{"verify claude rejected", func(m *ModelsConfig) { m.Verify = "claude-haiku-4-5" }, true},
		{"verify apple rejected", func(m *ModelsConfig) { m.Verify = "apple" }, true},
		{"verify empty-model rejected", func(m *ModelsConfig) { m.Verify = "ollama:" }, true},
		{"verify_min_score over range rejected", func(m *ModelsConfig) { m.VerifyMinScore = 11 }, true},
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `env -u FORCE_COLOR go test ./internal/config/ -run 'Verify|ValidateLocalRouting' -v`
Expected: FAIL — `VerifyMode`/`VerifyMinScoreOr` undefined; new validation cases don't error yet.

- [ ] **Step 3: Add the fields**

In `internal/config/types.go`, inside `ModelsConfig`, after the `EvalMinPass` field (line ~308):

```go
	// Verify, set to "ollama:<model>", enables per-call verification of local
	// routine answers (R5.3/FR-144): after a successful local routine response a
	// cheap local judge scores it 0–10; a score below VerifyMinScore escalates
	// the call to Claude. "" or "off" disables (default). Only the routine tier
	// is verified — synthesis is always Claude, classify is deterministically
	// validated.
	Verify string `yaml:"verify,omitempty"`
	// VerifyMinScore is the 0–10 confidence floor below which a verified local
	// routine answer escalates to Claude. 0 (unset) → default 6. Ignored when
	// verify is off.
	VerifyMinScore int `yaml:"verify_min_score,omitempty" validate:"omitempty,min=0,max=10"`
```

- [ ] **Step 4: Add helpers + validation**

In `internal/config/models.go`, add after `Fallback()`:

```go
// VerifyMode returns the configured verifier ref, or "off" when unset/"off".
func (m ModelsConfig) VerifyMode() string {
	if m.Verify == "" {
		return "off"
	}
	return m.Verify
}

// VerifyMinScoreOr returns the escalation floor, defaulting to 6.
func (m ModelsConfig) VerifyMinScoreOr() int {
	if m.VerifyMinScore <= 0 {
		return 6
	}
	return m.VerifyMinScore
}
```

In `validateLocalRouting`, before the final `return nil`:

```go
	if v := m.Verify; v != "" && v != "off" {
		if ref := ParseModelRef(v); ref.Provider != ProviderOllama || ref.Model == "" {
			return fmt.Errorf("models.verify must be off or a local ollama:<model> (got %q): the verifier is a cheap local judge, never Claude or apple", v)
		}
	}
	if m.VerifyMinScore < 0 || m.VerifyMinScore > 10 {
		return fmt.Errorf("models.verify_min_score must be 0..10 (got %d)", m.VerifyMinScore)
	}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `env -u FORCE_COLOR go test ./internal/config/ -run 'Verify|ValidateLocalRouting' -v`
Expected: PASS.

- [ ] **Step 6: Update the config reference comment**

In `docs/04-data-model-and-config.md`, extend the optional-keys sentence at lines 218-220 to end with:
`… apple_helper (helper path override), verify (off|ollama:<model>, default off — R5.3 per-call verification of local routine answers), verify_min_score (0–10, default 6).`

- [ ] **Step 7: Commit**

```bash
git add internal/config/types.go internal/config/models.go internal/config/verify_test.go internal/config/models_test.go docs/04-data-model-and-config.md
git commit -m "feat(config): models.verify + verify_min_score for R5.3 cascade (FR-144)"
```

---

### Task 2: Verify prompt & score parsing (pure helpers)

**Files:**
- Create: `internal/tokens/verify.go`
- Test: `internal/tokens/verify_test.go`

**Interfaces:**
- Consumes: `Message` (existing, `internal/tokens/manager.go`), `joinMessages` (existing).
- Produces: `buildVerifyPrompt(system string, msgs []Message, answer string) (sys, prompt string)`; `parseVerifyScore(text string) (score int, ok bool)`. Consumed by Task 3.

- [ ] **Step 1: Write the failing test**

Create `internal/tokens/verify_test.go`:

```go
package tokens

import (
	"strings"
	"testing"
)

func TestParseVerifyScore(t *testing.T) {
	cases := []struct {
		in    string
		score int
		ok    bool
	}{
		{"8", 8, true},
		{"score: 3", 3, true},
		{"10", 10, true},
		{"12", 10, true}, // clamp
		{"abc", 0, false},
		{"", 0, false},
	}
	for _, c := range cases {
		got, ok := parseVerifyScore(c.in)
		if ok != c.ok || (ok && got != c.score) {
			t.Errorf("parseVerifyScore(%q) = (%d,%v), want (%d,%v)", c.in, got, ok, c.score, c.ok)
		}
	}
}

func TestBuildVerifyPromptIncludesTaskAndAnswer(t *testing.T) {
	sys, prompt := buildVerifyPrompt("be terse", []Message{{Role: "user", Content: "capital of France?"}}, "Paris")
	if sys == "" {
		t.Fatal("empty judge system prompt")
	}
	if !strings.Contains(prompt, "capital of France?") || !strings.Contains(prompt, "Paris") {
		t.Fatalf("prompt missing task or answer: %q", prompt)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `env -u FORCE_COLOR go test ./internal/tokens/ -run 'VerifyScore|VerifyPrompt' -v`
Expected: FAIL — `parseVerifyScore`/`buildVerifyPrompt` undefined.

- [ ] **Step 3: Write the helpers**

Create `internal/tokens/verify.go`:

```go
package tokens

import (
	"regexp"
	"strconv"
	"strings"
)

// verifyJudgeSystem pins the local judge to emit a single integer 0–10.
const verifyJudgeSystem = "You are a strict evaluator. Judge whether the ASSISTANT ANSWER correctly and faithfully completes the TASK. Reply with ONLY a single integer from 0 to 10, where 10 means fully correct and faithful and 0 means wrong or unfaithful. Output the number and nothing else."

// buildVerifyPrompt renders the judge's system + user prompt from the original
// task (system + messages) and the candidate answer.
func buildVerifyPrompt(system string, msgs []Message, answer string) (string, string) {
	var task strings.Builder
	if system != "" {
		task.WriteString(system)
		task.WriteString("\n\n")
	}
	task.WriteString(joinMessages(msgs))

	var b strings.Builder
	b.WriteString("TASK:\n")
	b.WriteString(task.String())
	b.WriteString("\n\nASSISTANT ANSWER:\n")
	b.WriteString(answer)
	b.WriteString("\n\nSCORE (0-10):")
	return verifyJudgeSystem, b.String()
}

// verifyScoreRe matches the first run of digits in the judge's reply. Mirrors
// rerank.parseScore's pragmatism: the judge is prompted for a bare number, so
// the first integer is the score; out-of-range values clamp.
var verifyScoreRe = regexp.MustCompile(`\d+`)

// parseVerifyScore extracts the first integer from text, clamped to [0,10]; ok
// is false when none is found (→ inconclusive → the caller keeps the local
// answer).
func parseVerifyScore(text string) (int, bool) {
	m := verifyScoreRe.FindString(text)
	if m == "" {
		return 0, false
	}
	n, err := strconv.Atoi(m)
	if err != nil {
		return 0, false
	}
	if n < 0 {
		n = 0
	}
	if n > 10 {
		n = 10
	}
	return n, true
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `env -u FORCE_COLOR go test ./internal/tokens/ -run 'VerifyScore|VerifyPrompt' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/tokens/verify.go internal/tokens/verify_test.go
git commit -m "feat(tokens): verify judge prompt + score parsing (FR-144)"
```

---

### Task 3: The cascade in `runLocal`

**Files:**
- Modify: `internal/tokens/manager.go` (add `verifyActive`/`runJudge`/`verifyAndMaybeEscalate`; wire into `runLocal`'s success branch ~line 642-647)
- Test: `internal/tokens/verify_cascade_test.go` (create)

**Interfaces:**
- Consumes: `buildVerifyPrompt`/`parseVerifyScore` (Task 2); `ModelsConfig.VerifyMode`/`VerifyMinScoreOr` (Task 1); existing `resolveModel`, `fallbackClaudeKey`, `record`, `recordFailure`, `emit`, `applyRedaction`, `router.Resolve`, `estimator`, `HeuristicEstimator`.
- Produces: cascade behaviour on the existing `Run`/`runLocal` path. No signature changes.

- [ ] **Step 1: Write the failing tests**

Create `internal/tokens/verify_cascade_test.go`:

```go
package tokens

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jandro-es/axon/internal/agent"
)

// verifyConfig routes routine locally with verification on (gate off so the
// local tier serves without an eval row).
func verifyConfig() Config {
	c := localTestConfig()
	c.Models.Routine = "ollama:qwen"
	c.Models.Verify = "ollama:judge"
	return c
}

// localAnswerThenScore scripts the ollama fake: the ":verify" call returns
// score, every other call returns answer.
func localAnswerThenScore(answer, score string) *agent.Fake {
	f := agent.NewFake()
	f.RespondFn = func(r agent.Request) (*agent.Response, error) {
		if strings.HasSuffix(r.Operation, ":verify") {
			return &agent.Response{Text: score, Model: r.Model}, nil
		}
		return &agent.Response{Text: answer, Model: r.Model}, nil
	}
	return f
}

func hasLedgerOp(rows [][2]string, op string) bool {
	for _, r := range rows {
		if r[0] == op {
			return true
		}
	}
	return false
}

func TestVerifyKeepsLocalWhenJudgePasses(t *testing.T) {
	ctx := context.Background()
	claude := agent.NewFake()
	claude.Reply = "from-claude"
	local := localAnswerThenScore("local-answer", "9")
	m := testManagerRouter(t, verifyConfig(), agent.Router{Claude: claude, Ollama: local})

	res, err := m.Run(ctx, AgentCall{Operation: "op", ModelKey: "routine",
		Messages: []Message{{Role: "user", Content: "q"}}})
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != "local-answer" {
		t.Fatalf("passing judge should keep local, got %q", res.Text)
	}
	if claude.CallCount() != 0 {
		t.Fatalf("claude must not be called on a pass, got %d", claude.CallCount())
	}
	rows := ledgerRows(t, m.db)
	if !hasLedgerOp(rows, "op") || !hasLedgerOp(rows, "op:verify") {
		t.Fatalf("ledger missing op/op:verify rows: %v", rows)
	}
}

func TestVerifyEscalatesWhenJudgeFails(t *testing.T) {
	ctx := context.Background()
	claude := agent.NewFake()
	claude.Reply = "from-claude"
	local := localAnswerThenScore("local-answer", "2")
	m := testManagerRouter(t, verifyConfig(), agent.Router{Claude: claude, Ollama: local})

	res, err := m.Run(ctx, AgentCall{Operation: "op", ModelKey: "routine",
		Messages: []Message{{Role: "user", Content: "q"}}})
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != "from-claude" {
		t.Fatalf("failing judge should escalate to Claude, got %q", res.Text)
	}
	if claude.CallCount() != 1 {
		t.Fatalf("claude should be called once, got %d", claude.CallCount())
	}
	rows := ledgerRows(t, m.db)
	if !hasLedgerOp(rows, "op:verify") {
		t.Fatalf("ledger missing op:verify row: %v", rows)
	}
}

func TestVerifyInconclusiveKeepsLocal(t *testing.T) {
	ctx := context.Background()
	claude := agent.NewFake()
	claude.Reply = "from-claude"
	local := agent.NewFake()
	local.RespondFn = func(r agent.Request) (*agent.Response, error) {
		if strings.HasSuffix(r.Operation, ":verify") {
			return nil, errors.New("judge down")
		}
		return &agent.Response{Text: "local-answer", Model: r.Model}, nil
	}
	m := testManagerRouter(t, verifyConfig(), agent.Router{Claude: claude, Ollama: local})

	res, _ := m.Run(ctx, AgentCall{Operation: "op", ModelKey: "routine",
		Messages: []Message{{Role: "user", Content: "q"}}})
	if res.Text != "local-answer" {
		t.Fatalf("inconclusive judge should keep local, got %q", res.Text)
	}
	if claude.CallCount() != 0 {
		t.Fatalf("a broken judge must not spend Claude, got %d", claude.CallCount())
	}
}

func TestVerifyEscalationDegradesWhenClaudeErrors(t *testing.T) {
	ctx := context.Background()
	claude := agent.NewFake()
	claude.Err = errors.New("boom")
	local := localAnswerThenScore("local-answer", "1")
	m := testManagerRouter(t, verifyConfig(), agent.Router{Claude: claude, Ollama: local})

	res, err := m.Run(ctx, AgentCall{Operation: "op", ModelKey: "routine",
		Messages: []Message{{Role: "user", Content: "q"}}})
	if err != nil {
		t.Fatalf("degrade should not error, got %v", err)
	}
	if res.Text != "local-answer" {
		t.Fatalf("failed escalation should degrade to local, got %q", res.Text)
	}
	if claude.CallCount() != 1 {
		t.Fatalf("claude should have been attempted once, got %d", claude.CallCount())
	}
}

func TestVerifyOffTakesNoJudgeCall(t *testing.T) {
	ctx := context.Background()
	claude := agent.NewFake()
	local := agent.NewFake()
	local.Reply = "local-answer"
	c := localTestConfig()
	c.Models.Routine = "ollama:qwen" // verify unset → off
	m := testManagerRouter(t, c, agent.Router{Claude: claude, Ollama: local})

	res, _ := m.Run(ctx, AgentCall{Operation: "op", ModelKey: "routine",
		Messages: []Message{{Role: "user", Content: "q"}}})
	if res.Text != "local-answer" {
		t.Fatalf("verify off should return local, got %q", res.Text)
	}
	if local.CallCount() != 1 {
		t.Fatalf("verify off should make exactly one local call, got %d", local.CallCount())
	}
}

func TestVerifyDoesNotTouchClassify(t *testing.T) {
	ctx := context.Background()
	claude := agent.NewFake()
	local := agent.NewFake()
	local.Reply = "local-answer"
	c := verifyConfig() // classify defaults to ollama:qwen3:8b, verify on
	m := testManagerRouter(t, c, agent.Router{Claude: claude, Ollama: local})

	res, _ := m.Run(ctx, AgentCall{Operation: "op", ModelKey: "classify",
		Messages: []Message{{Role: "user", Content: "q"}}})
	if res.Text != "local-answer" {
		t.Fatalf("classify should return local, got %q", res.Text)
	}
	if local.CallCount() != 1 {
		t.Fatalf("classify must not be verified (scope), got %d local calls", local.CallCount())
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `env -u FORCE_COLOR go test ./internal/tokens/ -run 'TestVerify(Keeps|Escalates|Inconclusive|Escalation|Off|DoesNot)' -v`
Expected: FAIL — `verifyActive` undefined; cascade not wired (routine local answer returns without judging).

- [ ] **Step 3: Add the three methods**

In `internal/tokens/manager.go`, add after `runLocal` (i.e. after line ~650):

```go
// verifyActive reports whether a successful local answer should be judged before
// it is trusted (R5.3/FR-144): the routine family alias, an ollama provider, and
// verify configured. The judge itself uses a concrete ref, so it never re-enters
// this predicate.
func (m *manager) verifyActive(call AgentCall, auth Authorization) bool {
	return call.ModelKey == "routine" &&
		auth.Provider == config.ProviderOllama &&
		m.cfg.Models.VerifyMode() != "off"
}

// runJudge issues one local judge call scoring answer 0–10 for the task in call,
// using the configured verify model (a concrete ollama ref). Ledgered as
// "<op>:verify" (budget-exempt). Best-effort: any transport/parse failure returns
// ok=false (the caller keeps the local answer). It never falls forward to Claude
// — a broken judge must not spend the Claude quota. (ADR-031.)
func (m *manager) runJudge(ctx context.Context, call AgentCall, answer string) (int, bool) {
	ref := config.ParseModelRef(m.cfg.Models.VerifyMode())
	ag, err := m.router.Resolve(ref.Provider)
	if err != nil {
		return 0, false
	}
	sys, prompt := buildVerifyPrompt(call.System, call.Messages, answer)
	judge := AgentCall{Operation: call.Operation + ":verify", RunID: call.RunID}
	vAuth := Authorization{
		Decision: DecisionProceed, Model: ref.Model, Provider: config.ProviderOllama,
		EstInput: m.estimator.Estimate(sys + "\n" + prompt),
	}
	resp, err := ag.Run(ctx, agent.Request{
		Operation: judge.Operation,
		Model:     ref.Model,
		System:    m.applyRedaction(sys),
		Prompt:    m.applyRedaction(prompt),
	})
	if err != nil {
		m.recordFailure(ctx, judge, vAuth, AgentResult{Auth: vAuth, Model: ref.Model})
		return 0, false
	}
	vres := AgentResult{Auth: vAuth, Model: ref.Model, Text: resp.Text, Usage: resp.Usage}
	if vres.Usage.InputTokens+vres.Usage.OutputTokens == 0 {
		vres.Usage.InputTokens = vAuth.EstInput
		vres.Usage.OutputTokens = HeuristicEstimator{}.Estimate(resp.Text)
	}
	if _, lerr := m.record(ctx, judge, vAuth, vres); lerr != nil {
		m.emit(events.LevelError, "token.error", judge.Operation, vAuth,
			map[string]any{"error": "ledger write after verify judge: " + lerr.Error()})
	}
	return parseVerifyScore(resp.Text)
}

// verifyAndMaybeEscalate judges a successful local routine answer and, below the
// floor, escalates the call to Claude through the normal budget path. The local
// answer (already ledgered by the caller) and the judge call are both ledgered
// before any escalation. Degrades to the local answer if the judge is
// inconclusive OR Claude is unavailable (budget deny/defer or adapter error) — S8.
func (m *manager) verifyAndMaybeEscalate(ctx context.Context, call AgentCall, auth Authorization, local AgentResult) (AgentResult, error) {
	score, ok := m.runJudge(ctx, call, local.Text)
	floor := m.cfg.Models.VerifyMinScoreOr()
	if !ok || score >= floor {
		m.emit(events.LevelInfo, "token.verify_pass", call.Operation, auth,
			map[string]any{"score": score, "scored": ok, "floor": floor})
		return local, nil
	}
	esc := call
	esc.ModelKey = m.fallbackClaudeKey("routine")
	m.emit(events.LevelWarn, "token.verify_escalate", call.Operation, auth,
		map[string]any{"score": score, "floor": floor, "escalate_to": esc.ModelKey})
	res, err := m.Run(ctx, esc)
	if err != nil {
		m.emit(events.LevelWarn, "token.verify_escalate_failed", call.Operation, auth,
			map[string]any{"score": score, "error": err.Error()})
		return local, nil
	}
	return res, nil
}
```

- [ ] **Step 4: Wire into `runLocal`'s success branch**

In `internal/tokens/manager.go`, in `runLocal`, replace the tail of the success branch:

```go
		res.LedgerID = ledgerID
		return res, nil
```

with:

```go
		res.LedgerID = ledgerID
		if m.verifyActive(call, auth) {
			return m.verifyAndMaybeEscalate(ctx, call, auth, res)
		}
		return res, nil
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `env -u FORCE_COLOR go test ./internal/tokens/ -run 'TestVerify' -v`
Expected: PASS (all six cascade tests + Task 2's prompt/parse tests).

- [ ] **Step 6: Run the full tokens package (guard against regressions)**

Run: `env -u FORCE_COLOR go test ./internal/tokens/`
Expected: PASS — the existing promotion/local/manager tests are unaffected (verify is off in `localTestConfig`; routine there is Claude).

- [ ] **Step 7: Commit**

```bash
git add internal/tokens/manager.go internal/tokens/verify_cascade_test.go
git commit -m "feat(tokens): per-call verification cascade for local routine tier (FR-144, ADR-031)"
```

---

### Task 4: Doctor — `verifyCheck`

**Files:**
- Modify: `internal/core/doctor.go` (add `verifyCheck`; wire it after the rerank block ~line 110)
- Test: `internal/core/verify_doctor_test.go` (create)

**Interfaces:**
- Consumes: `ModelsConfig.VerifyMode`/`VerifyMinScoreOr` (Task 1); existing `ollamaReachable`, `ollamaModelPresent`, `Check`, `StatusOK`/`StatusWarn`.
- Produces: `verifyCheck(p config.Profile) Check`.

- [ ] **Step 1: Write the failing test**

Create `internal/core/verify_doctor_test.go`:

```go
package core

import (
	"strings"
	"testing"

	"github.com/jandro-es/axon/internal/config"
)

func TestVerifyCheckMalformed(t *testing.T) {
	p := config.Profile{Models: config.ModelsConfig{Verify: "cohere:x", Routine: "ollama:qwen"}}
	c := verifyCheck(p)
	if c.Status != StatusWarn || !strings.Contains(strings.ToLower(c.Detail), "off or ollama") {
		t.Fatalf("malformed verify check = %+v", c)
	}
}

func TestVerifyCheckRoutineNotLocalWarns(t *testing.T) {
	p := config.Profile{Models: config.ModelsConfig{Verify: "ollama:judge", Routine: "claude-sonnet-5"}}
	c := verifyCheck(p)
	if c.Status != StatusWarn || !strings.Contains(c.Detail, "never triggers") {
		t.Fatalf("routine-not-local verify check = %+v", c)
	}
}

func TestVerifyCheckUnreachableWarns(t *testing.T) {
	p := config.Profile{Models: config.ModelsConfig{
		Verify: "ollama:judge", Routine: "ollama:qwen", OllamaHost: "http://127.0.0.1:1"}}
	if c := verifyCheck(p); c.Status != StatusWarn {
		t.Fatalf("unreachable verify should warn, got %v", c.Status)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `env -u FORCE_COLOR go test ./internal/core/ -run TestVerifyCheck -v`
Expected: FAIL — `verifyCheck` undefined.

- [ ] **Step 3: Add `verifyCheck`**

In `internal/core/doctor.go`, add after `rerankCheck` (~line 217):

```go
// verifyCheck reports the R5.3 verification cascade's prerequisites (FR-145):
// verify must name a local ollama model, the routine tier must itself be local
// (else verification never triggers), and the judge model must be pulled.
// Warn-only, mirroring rerankCheck — a broken verifier just keeps local answers.
func verifyCheck(p config.Profile) Check {
	const name = "verify"
	mode := p.Models.VerifyMode()
	if !strings.HasPrefix(mode, "ollama:") {
		return Check{name, StatusWarn, fmt.Sprintf("models.verify %q not recognised — use off or ollama:<model>", mode)}
	}
	if config.ParseModelRef(p.Models.Routine).Provider != config.ProviderOllama {
		return Check{name, StatusWarn, "models.verify is set but the routine tier is not local — verification never triggers"}
	}
	model := strings.TrimPrefix(mode, "ollama:")
	host := p.Models.OllamaHost
	if host == "" {
		host = "http://localhost:11434"
	}
	host = strings.TrimRight(host, "/")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if !ollamaReachable(ctx, host) {
		return Check{name, StatusWarn, fmt.Sprintf("verify Ollama not reachable at %s — start `ollama serve` (routine answers stay local, unverified)", host)}
	}
	if !ollamaModelPresent(ctx, host, model) {
		return Check{name, StatusWarn, fmt.Sprintf("verify model %q not pulled — run `ollama pull %s`", model, model)}
	}
	return Check{name, StatusOK, fmt.Sprintf("verify ready: %s, floor %d/10", mode, p.Models.VerifyMinScoreOr())}
}
```

- [ ] **Step 4: Wire it in**

In `internal/core/doctor.go`, after the rerank block (line ~110), add:

```go
			// 4e. Verification cascade prerequisite, only when models.verify is set.
			if p.Models.VerifyMode() != "off" {
				checks = append(checks, verifyCheck(p))
			}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `env -u FORCE_COLOR go test ./internal/core/ -run TestVerifyCheck -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/core/doctor.go internal/core/verify_doctor_test.go
git commit -m "feat(doctor): verifyCheck reports R5.3 cascade prerequisites (FR-145)"
```

---

### Task 5: Docs sweep + full-suite verification

**Files:**
- Modify: `docs/07-component-context-token-manager.md` (add a cascade bullet near the FR-79 fallback bullet at line ~67)
- Modify: `docs/15-roadmap-1.2.md` (mark R5.3 built; note R5 complete → 1.2 release criterion R1+R5 met)
- Modify: `README.md` (if it enumerates chokepoint behaviours/config keys, add verify; otherwise skip)

- [ ] **Step 1: Add the chokepoint doc bullet**

In `docs/07-component-context-token-manager.md`, after the FR-79 "Local fallback ladder" bullet (line ~67), add:

```markdown
- **Per-call verification cascade (FR-144, ADR-031, default off):** when `models.verify: ollama:<model>` is set, a *successful* local `routine` answer is scored 0–10 by that cheap local judge (ledgered `<op>:verify`, budget-exempt); below `models.verify_min_score` (default 6) the call escalates to Claude via `fallbackClaudeKey` through the normal budget path (`token.verify_pass` / `verify_escalate` / `verify_escalate_failed` events). The judge uses a concrete ref (never re-verified) and never falls forward to Claude; an inconclusive judge or a budget-blocked escalation degrades to the retained local answer. Scope is `routine` only.
```

- [ ] **Step 2: Mark the roadmap slice built**

In `docs/15-roadmap-1.2.md`, in the R5 section and the build-order table, append to the R5 line a note that R5.3 (cascade-with-verification, FR-144/145, ADR-031) is **built**, completing R5; and that with R1 already shipped, the **R1 + R5 release criterion is met**.

- [ ] **Step 3: README check**

Run: `grep -n "eval_min_pass\|local_fallback\|reranker\|18 automations" README.md`
If a config-key or chokepoint list surfaces, add `models.verify` alongside `eval_min_pass`. If nothing relevant, skip (no code count changed — no automation/tool added).

- [ ] **Step 4: Commit docs**

```bash
git add docs/07-component-context-token-manager.md docs/15-roadmap-1.2.md README.md
git commit -m "docs(R5.3): chokepoint cascade bullet + roadmap R5 complete (FR-144/145)"
```

- [ ] **Step 5: Full-suite verification**

Run: `env -u FORCE_COLOR go build ./... && env -u FORCE_COLOR go test ./... && env -u FORCE_COLOR golangci-lint run`
Expected: build clean, all tests PASS, lint green. In particular confirm the MCP tool-count and automation registry assertions are **unchanged** (no new tool/automation this slice). If lint flags `gofmt` drift, run `gofmt -w` on the touched files and commit as a follow-up (never `git commit --amend` — GateGuard blocks it).

- [ ] **Step 6: Commit any lint/fmt fixes** (only if Step 5 required them)

```bash
git add -A
git commit -m "chore(R5.3): gofmt/lint fixes"
```

---

## Self-Review

**Spec coverage:**
- FR-144 (cascade: config, judge, escalation, ledgering, degrade, scope) → Tasks 1+2+3. ✓
- FR-145 (doctor status: off/misconfigured/model-missing/ready) → Task 4. ✓
- ADR-031 methodology (ledgered judge, routine-only, default off) → Task 3 wiring + Task 1 default. ✓
- Spec "Testing" bullets → config (T1), prompt/parse (T2), cascade six-case table (T3), doctor table (T4). ✓
- "No new automation/MCP tool → no count-assertion bumps" → verified in T5 Step 5. ✓

**Placeholder scan:** none — every step carries exact code/commands.

**Type consistency:** `verifyActive(call, auth)`, `runJudge(ctx, call, answer) (int, bool)`, `verifyAndMaybeEscalate(ctx, call, auth, local) (AgentResult, error)`, `buildVerifyPrompt(system, msgs, answer) (string, string)`, `parseVerifyScore(text) (int, bool)`, `VerifyMode() string`, `VerifyMinScoreOr() int`, `verifyCheck(p) Check` — names/signatures identical across Tasks 1→4 and the spec. `agent.Request`/`agent.Response`/`agent.Fake.RespondFn` match `internal/agent/fake.go`. Ledger label `<op>:verify` consistent between T3 code and T3/T5 assertions.
