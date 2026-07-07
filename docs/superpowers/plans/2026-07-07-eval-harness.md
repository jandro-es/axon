# R5.1 — `axon eval` Harness Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship an eval harness + `axon eval` that runs in-repo golden sets (classify + routine task families) against any `(provider, model)` pair, grades them hybrid (deterministic for classify; `must_include` + Claude-judge for routine), routes every eval call through the token-manager chokepoint with fail-fast measurement, and prints a per-family scorecard.

**Architecture:** A new leaf package `internal/eval` (case model + embedded golden fixtures + grading + runner) and a thin `cmd/axon/eval_cmd.go`. The runner depends only on a consumer-defined `Chokepoint` interface satisfied by `tokens.Manager` — it never imports `internal/agent` (cardinal rule 1). Eval target calls run with `local_fallback: fail` so a broken local model surfaces as a **failed** case instead of silently falling forward to Claude and measuring Claude; the runner additionally compares `AgentResult.Model` against the intended concrete model and scores a mismatch **escalated**. The harness reads embedded fixtures and prints a scorecard — no vault mutation (cardinal rule 2).

**Tech Stack:** Go 1.26+, `github.com/goccy/go-yaml` (already the project's YAML lib), `//go:embed` for golden fixtures, `spf13/cobra` (CLI), existing `internal/tokens` + `internal/config`. No new third-party dependency.

## Global Constraints

Every task's requirements implicitly include this section. Copied from the spec (`docs/superpowers/specs/2026-07-07-eval-harness-design.md`) and CLAUDE.md:

- **Cardinal rule 1 — no Claude call bypasses the token manager.** Both the target call and the judge call go through `Chokepoint.Run` (satisfied by `tokens.Manager`, which ledgers every call). `internal/eval` must **not** import `internal/agent` and must **not** import `internal/automations`. Verify with a grep in Task 4.
- **Cardinal rule 2 — no vault mutation.** The harness reads embedded fixtures and writes only to the provided `io.Writer`. No `vault.*` import, no filesystem writes.
- **Measurement integrity.** The eval manager is built with `models.local_fallback: fail` (`internal/config` `ModelsConfig.LocalFallback`, values `"claude"|"fail"`). A target whose returned model differs from the intended concrete model is scored `Escalated`, never `Pass`.
- **Real chokepoint types (confirmed against `internal/tokens/manager.go`):** the call type is `tokens.AgentCall` (fields used: `Operation`, `ModelKey`, `System`, `Messages []tokens.Message{Role,Content}`, `ValidateOutput func(string) error`); the result type is `tokens.AgentResult` (fields used: `Text string`, `Model string`). `Manager.Run(ctx, AgentCall) (AgentResult, error)`. `ModelKey` accepts a family alias (`"classify"|"routine"|"synthesis"`) **or** a concrete ref (`"ollama:qwen2.5"`); the manager returns the **bare** model in `AgentResult.Model` (provider prefix stripped, e.g. `qwen2.5`).
- **Idiomatic Go.** Wrap errors with `%w`; `context.Context` is the first arg on every call that reaches the chokepoint; small interfaces defined at the consumer.
- **Tests run with `env -u FORCE_COLOR`** and use no network. The judge and target are exercised through a fake `Chokepoint`, never real Ollama/Claude.
- **Every commit message ends with:** `Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>`

---

## File Structure

- `internal/eval/case.go` — **new.** `Family`, `Grade`, `Case` types; embedded `golden/` FS; `LoadCases(family string) ([]Case, error)` with per-fixture validation.
- `internal/eval/golden/classify/capture-kind.yaml` — **new.** classify fixture, `expect_json`.
- `internal/eval/golden/classify/priority.yaml` — **new.** classify fixture, `expect_text`.
- `internal/eval/golden/routine/summarize-standup.yaml` — **new.** routine fixture, `must_include` + `rubric`.
- `internal/eval/case_test.go` — **new.** Embedded-fixture load/validate tests; a malformed test-local fixture fails loudly.
- `internal/eval/grade.go` — **new.** `Verdict`; `gradeClassify(c Case, got string) Verdict`; `mustInclude(anchors []string, got string) (bool, string)`; `parseJudge(raw string) (pass bool, reason string, err error)`; the judge system-prompt constant.
- `internal/eval/grade_test.go` — **new.** Table-driven grading tests (no I/O).
- `internal/eval/run.go` — **new.** `Chokepoint` interface; `Options`; `Report`/`FamilyReport`/`CaseResult`; `Run(ctx, cp, cases, opts) (Report, error)`.
- `internal/eval/run_test.go` — **new.** Runner scenarios with a fake `Chokepoint`.
- `cmd/axon/eval_cmd.go` — **new.** `newEvalCmd(gf)` — builds a fail-fast `*tokens.Manager` via the deps builder, resolves per-family expected models, delegates to `eval.Run`, prints scorecard / `--json`, sets exit code via `--min-pass`.
- `cmd/axon/eval_cmd_test.go` — **new.** CLI smoke against a fake-backed manager.
- `cmd/axon/root.go` — **modify.** Register `newEvalCmd(gf)`.
- `cmd/axon/deps.go` — **modify.** Add `evalManager` helper that clones the profile with `LocalFallback: "fail"` and returns a router-backed `tokens.Manager` + an `expectModel` resolver.

---

## Requirement → Task map

| FR | Requirement | Task(s) |
|----|-------------|---------|
| FR-140 | Eval harness + `axon eval` + in-repo golden sets, any `(provider,model)` pair, ledgered fail-fast calls with escalation visibility | 1, 3, 4 |
| FR-141 | Hybrid grading: deterministic (JSON/text) for classify; `must_include` + Claude judge for routine; CI grades against a fake | 2, 3, 4 |

---

### Task 1: Case model + embedded golden fixtures + `LoadCases`

**Files:**
- Create: `internal/eval/case.go`
- Create: `internal/eval/golden/classify/capture-kind.yaml`
- Create: `internal/eval/golden/classify/priority.yaml`
- Create: `internal/eval/golden/routine/summarize-standup.yaml`
- Test: `internal/eval/case_test.go`

**Interfaces:**
- Consumes: nothing (leaf).
- Produces:
  ```go
  type Family string // "classify" | "routine" | "synthesis"

  const (
      FamilyClassify  Family = "classify"
      FamilyRoutine   Family = "routine"
      FamilySynthesis Family = "synthesis"
  )

  type Grade struct {
      ExpectJSON  json.RawMessage `yaml:"expect_json"`
      ExpectText  string          `yaml:"expect_text"`
      MustInclude []string        `yaml:"must_include"`
      Rubric      string          `yaml:"rubric"`
  }

  type Case struct {
      Name   string `yaml:"name"`
      Family Family `yaml:"family"`
      System string `yaml:"system"`
      Prompt string `yaml:"prompt"`
      Grade  Grade  `yaml:"grade"`
  }

  // LoadCases parses every embedded golden/<family>/*.yaml. family=="" or "all"
  // loads all; otherwise only that family. Each case is validated at load.
  func LoadCases(family string) ([]Case, error)
  ```

- [ ] **Step 1: Write the failing test**

Create `internal/eval/case_test.go`:
```go
package eval

import "testing"

func TestLoadCasesAllFamilies(t *testing.T) {
	cases, err := LoadCases("all")
	if err != nil {
		t.Fatalf("LoadCases: %v", err)
	}
	if len(cases) < 3 {
		t.Fatalf("want >=3 embedded cases, got %d", len(cases))
	}
	var classify, routine int
	for _, c := range cases {
		if c.Prompt == "" {
			t.Errorf("case %q has empty prompt", c.Name)
		}
		switch c.Family {
		case FamilyClassify:
			classify++
			if len(c.Grade.ExpectJSON) == 0 && c.Grade.ExpectText == "" {
				t.Errorf("classify case %q has no deterministic expectation", c.Name)
			}
		case FamilyRoutine:
			routine++
			if len(c.Grade.MustInclude) == 0 {
				t.Errorf("routine case %q has no must_include gate", c.Name)
			}
		}
	}
	if classify == 0 || routine == 0 {
		t.Fatalf("want both families present, got classify=%d routine=%d", classify, routine)
	}
}

func TestLoadCasesFilter(t *testing.T) {
	cases, err := LoadCases("classify")
	if err != nil {
		t.Fatalf("LoadCases: %v", err)
	}
	for _, c := range cases {
		if c.Family != FamilyClassify {
			t.Errorf("filter classify returned %q family %q", c.Name, c.Family)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `env -u FORCE_COLOR go test ./internal/eval/ -run TestLoadCases -v`
Expected: FAIL — `undefined: LoadCases` / package has no fixtures.

- [ ] **Step 3: Write the fixtures**

Create `internal/eval/golden/classify/capture-kind.yaml`:
```yaml
name: capture-kind-url
family: classify
system: |
  You classify a captured item into exactly one kind. Respond with JSON only:
  {"kind": "<article|note|task|link>"}. No prose.
prompt: |
  Captured text:
  "https://go.dev/blog/error-handling-and-go — Error handling and Go, a blog post"
grade:
  expect_json: '{"kind":"article"}'
```

Create `internal/eval/golden/classify/priority.yaml`:
```yaml
name: priority-urgent
family: classify
system: |
  Classify the note's priority as exactly one lowercase word: high, medium, or low.
  Answer with the single word only.
prompt: |
  Note: "Production DB is down, customers cannot log in. Need a fix now."
grade:
  expect_text: high
```

Create `internal/eval/golden/routine/summarize-standup.yaml`:
```yaml
name: summarize-standup
family: routine
system: |
  Summarize the note in a single sentence. Preserve every person name and date.
prompt: |
  Note: On 2026-06-30, Alice shipped the search reranker; Bob is blocked on Ollama setup.
grade:
  must_include:
    - Alice
    - Bob
    - "2026-06-30"
  rubric: |
    PASS only if the summary is a single coherent sentence that says Alice shipped
    the reranker and Bob is blocked (on Ollama). Otherwise FAIL.
```

- [ ] **Step 4: Write `case.go`**

Create `internal/eval/case.go`:
```go
// Package eval is AXON's model-evaluation harness (R5.1, FR-140/141). It runs
// in-repo golden sets against any (provider, model) pair through the token
// chokepoint and grades them hybrid: deterministic for the classify family,
// must_include + a Claude judge for the routine family. It never imports
// internal/agent (cardinal rule 1) and never mutates the vault (cardinal rule 2).
package eval

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"

	"github.com/goccy/go-yaml"
)

// Family is the task family a case exercises (== the tier it would promote).
type Family string

const (
	FamilyClassify  Family = "classify"
	FamilyRoutine   Family = "routine"
	FamilySynthesis Family = "synthesis"
)

// Grade carries the pass/fail criteria. classify uses ExpectJSON/ExpectText
// (deterministic); routine uses MustInclude (+ optional Rubric for the judge).
type Grade struct {
	ExpectJSON  json.RawMessage `yaml:"expect_json"`
	ExpectText  string          `yaml:"expect_text"`
	MustInclude []string        `yaml:"must_include"`
	Rubric      string          `yaml:"rubric"`
}

// Case is one self-contained golden example, loaded from an embedded YAML file.
type Case struct {
	Name   string `yaml:"name"`
	Family Family `yaml:"family"`
	System string `yaml:"system"`
	Prompt string `yaml:"prompt"`
	Grade  Grade  `yaml:"grade"`
}

//go:embed golden
var goldenFS embed.FS

// LoadCases parses every embedded golden/<family>/*.yaml. A family of "" or
// "all" loads every family; otherwise only that family's directory is read.
// Each case is validated so a malformed fixture fails loudly rather than
// silently scoring.
func LoadCases(family string) ([]Case, error) {
	return loadCasesFS(goldenFS, "golden", family)
}

// loadCasesFS is the fixture loader parameterised on the FS so tests can point
// it at a deliberately malformed set.
func loadCasesFS(fsys fs.FS, root, family string) ([]Case, error) {
	var out []Case
	err := fs.WalkDir(fsys, root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(p, ".yaml") {
			return nil
		}
		if family != "" && family != "all" && path.Base(path.Dir(p)) != family {
			return nil
		}
		raw, rerr := fs.ReadFile(fsys, p)
		if rerr != nil {
			return fmt.Errorf("read %s: %w", p, rerr)
		}
		var c Case
		if uerr := yaml.Unmarshal(raw, &c); uerr != nil {
			return fmt.Errorf("parse %s: %w", p, uerr)
		}
		if verr := c.validate(); verr != nil {
			return fmt.Errorf("invalid fixture %s: %w", p, verr)
		}
		out = append(out, c)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// validate enforces exactly one grading mode appropriate to the family so a
// malformed fixture cannot silently score.
func (c Case) validate() error {
	if c.Name == "" {
		return fmt.Errorf("missing name")
	}
	if strings.TrimSpace(c.Prompt) == "" {
		return fmt.Errorf("empty prompt")
	}
	switch c.Family {
	case FamilyClassify:
		if len(c.Grade.ExpectJSON) == 0 && c.Grade.ExpectText == "" {
			return fmt.Errorf("classify case needs expect_json or expect_text")
		}
		if len(c.Grade.ExpectJSON) > 0 && c.Grade.ExpectText != "" {
			return fmt.Errorf("classify case sets both expect_json and expect_text")
		}
	case FamilyRoutine:
		if len(c.Grade.MustInclude) == 0 {
			return fmt.Errorf("routine case needs at least one must_include anchor")
		}
	case FamilySynthesis:
		// Baseline-only (never promoted): must_include or a rubric is enough.
		if len(c.Grade.MustInclude) == 0 && c.Grade.Rubric == "" {
			return fmt.Errorf("synthesis case needs must_include or a rubric")
		}
	default:
		return fmt.Errorf("unknown family %q", c.Family)
	}
	return nil
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `env -u FORCE_COLOR go test ./internal/eval/ -run TestLoadCases -v`
Expected: PASS.

- [ ] **Step 6: Add the malformed-fixture guard test**

Append to `internal/eval/case_test.go` (add `"testing/fstest"` to the imports):
```go
func TestLoadCasesRejectsMalformed(t *testing.T) {
	bad := fstest.MapFS{
		"bad/classify/x.yaml": &fstest.MapFile{
			Data: []byte("name: x\nfamily: classify\nprompt: hi\ngrade: {}\n"),
		},
	}
	_, err := loadCasesFS(bad, "bad", "all")
	if err == nil {
		t.Fatal("a classify fixture with no expectation must fail to load")
	}
}
```

- [ ] **Step 7: Run + commit**

Run: `env -u FORCE_COLOR go test ./internal/eval/ -v`
Expected: PASS.
```bash
git add internal/eval/case.go internal/eval/golden internal/eval/case_test.go
git commit -m "$(cat <<'EOF'
feat(eval): Case model + embedded golden fixtures + validating LoadCases (FR-140)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 2: Deterministic grading — `gradeClassify`

**Files:**
- Create: `internal/eval/grade.go`
- Test: `internal/eval/grade_test.go`

**Interfaces:**
- Consumes: `Case`, `Grade` (Task 1).
- Produces:
  ```go
  type Verdict struct {
      Pass      bool
      Escalated bool
      Reason    string
  }

  func gradeClassify(c Case, got string) Verdict
  func normalizeText(s string) string // trim + collapse internal whitespace, lowercase
  ```

- [ ] **Step 1: Write the failing test**

Create `internal/eval/grade_test.go`:
```go
package eval

import (
	"encoding/json"
	"testing"
)

func TestGradeClassifyJSONSemantic(t *testing.T) {
	c := Case{Family: FamilyClassify, Grade: Grade{ExpectJSON: json.RawMessage(`{"kind":"article"}`)}}
	// Key order and whitespace differ but the JSON is semantically equal.
	if v := gradeClassify(c, "{ \"kind\" : \"article\" }"); !v.Pass {
		t.Fatalf("semantically-equal JSON should pass: %+v", v)
	}
	if v := gradeClassify(c, `{"kind":"note"}`); v.Pass {
		t.Fatal("different value must fail")
	}
	if v := gradeClassify(c, "not json"); v.Pass {
		t.Fatal("non-JSON candidate must fail, not panic")
	}
}

func TestGradeClassifyTextNormalized(t *testing.T) {
	c := Case{Family: FamilyClassify, Grade: Grade{ExpectText: "high"}}
	if v := gradeClassify(c, "  HIGH\n"); !v.Pass {
		t.Fatalf("normalized text should pass: %+v", v)
	}
	if v := gradeClassify(c, "low"); v.Pass {
		t.Fatal("wrong text must fail")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `env -u FORCE_COLOR go test ./internal/eval/ -run TestGradeClassify -v`
Expected: FAIL — `undefined: gradeClassify`.

- [ ] **Step 3: Write `grade.go` (deterministic half)**

Create `internal/eval/grade.go`:
```go
package eval

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// Verdict is the outcome of grading one case.
type Verdict struct {
	Pass      bool
	Escalated bool   // the answer came from a model other than the target (fall-forward)
	Reason    string // human-readable why
}

// gradeClassify grades a classify case deterministically: semantic JSON equality
// when ExpectJSON is set, else normalized text equality.
func gradeClassify(c Case, got string) Verdict {
	if len(c.Grade.ExpectJSON) > 0 {
		wantN, err := canonicalJSON(c.Grade.ExpectJSON)
		if err != nil {
			return Verdict{Reason: fmt.Sprintf("fixture expect_json invalid: %v", err)}
		}
		gotN, err := canonicalJSON([]byte(got))
		if err != nil {
			return Verdict{Reason: fmt.Sprintf("candidate is not valid JSON: %v", err)}
		}
		if bytes.Equal(wantN, gotN) {
			return Verdict{Pass: true}
		}
		return Verdict{Reason: fmt.Sprintf("json mismatch: want %s, got %s", wantN, gotN)}
	}
	if normalizeText(got) == normalizeText(c.Grade.ExpectText) {
		return Verdict{Pass: true}
	}
	return Verdict{Reason: fmt.Sprintf("text mismatch: want %q, got %q", c.Grade.ExpectText, got)}
}

// canonicalJSON unmarshals then re-marshals so key order and insignificant
// whitespace do not affect equality (Go sorts map keys on Marshal).
func canonicalJSON(raw []byte) ([]byte, error) {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, err
	}
	return json.Marshal(v)
}

// normalizeText trims, collapses internal whitespace runs to one space, and
// lowercases — so "  HIGH\n" == "high".
func normalizeText(s string) string {
	return strings.ToLower(strings.Join(strings.Fields(s), " "))
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `env -u FORCE_COLOR go test ./internal/eval/ -run TestGradeClassify -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/eval/grade.go internal/eval/grade_test.go
git commit -m "$(cat <<'EOF'
feat(eval): deterministic classify grading (semantic JSON + normalized text) (FR-141)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 3: Routine grading — `mustInclude` gate + judge parsing

**Files:**
- Modify: `internal/eval/grade.go`
- Test: `internal/eval/grade_test.go`

**Interfaces:**
- Consumes: `Grade` (Task 1), `Verdict` (Task 2).
- Produces:
  ```go
  const judgeSystem = `...` // pins the judge to {"pass":bool,"reason":string}

  func mustInclude(anchors []string, got string) (ok bool, missing string)
  func judgePrompt(rubric, candidate string) string
  func parseJudge(raw string) (pass bool, reason string, err error)
  func validateJudgeOutput(s string) error
  ```

- [ ] **Step 1: Write the failing test**

Append to `internal/eval/grade_test.go`:
```go
func TestMustIncludeGate(t *testing.T) {
	ok, missing := mustInclude([]string{"Alice", "Bob"}, "Alice shipped it; Bob is blocked")
	if !ok {
		t.Fatalf("all anchors present should pass, missing=%q", missing)
	}
	ok, missing = mustInclude([]string{"Alice", "Carol"}, "Alice shipped it")
	if ok || missing != "Carol" {
		t.Fatalf("want fail on missing Carol, got ok=%v missing=%q", ok, missing)
	}
}

func TestParseJudge(t *testing.T) {
	pass, reason, err := parseJudge(`{"pass":true,"reason":"looks good"}`)
	if err != nil || !pass || reason != "looks good" {
		t.Fatalf("parseJudge good: pass=%v reason=%q err=%v", pass, reason, err)
	}
	// Judges sometimes wrap JSON in prose/fences; extract the object.
	pass, _, err = parseJudge("Sure!\n```json\n{\"pass\":false,\"reason\":\"missing Bob\"}\n```")
	if err != nil || pass {
		t.Fatalf("parseJudge fenced: pass=%v err=%v", pass, err)
	}
	if _, _, err := parseJudge("no json here"); err == nil {
		t.Fatal("malformed judge output must return an error, not panic")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `env -u FORCE_COLOR go test ./internal/eval/ -run 'TestMustInclude|TestParseJudge' -v`
Expected: FAIL — `undefined: mustInclude`.

- [ ] **Step 3: Extend `grade.go`**

Append to `internal/eval/grade.go`:
```go
// judgeSystem pins the Claude judge to a strict JSON verdict. Because the judge
// is Claude, this schema is reliable (guarded further by ValidateOutput in the
// runner).
const judgeSystem = `You are a strict grader. Given a rubric and a candidate answer,
decide if the candidate satisfies the rubric. Respond with JSON only, no prose:
{"pass": <true|false>, "reason": "<one short sentence>"}`

// mustInclude reports whether every anchor substring appears in got. On failure
// it returns the first missing anchor for the verdict reason.
func mustInclude(anchors []string, got string) (bool, string) {
	for _, a := range anchors {
		if !strings.Contains(got, a) {
			return false, a
		}
	}
	return true, ""
}

// judgePrompt is the user turn handed to the judge: the rubric plus the
// candidate answer to grade.
func judgePrompt(rubric, candidate string) string {
	return fmt.Sprintf("Rubric:\n%s\n\nCandidate answer:\n%s", rubric, candidate)
}

// parseJudge extracts and parses the judge's {"pass","reason"} verdict, tolerant
// of surrounding prose or ```json fences. A malformed verdict is an error (the
// runner scores that case failed), never a panic.
func parseJudge(raw string) (bool, string, error) {
	start := strings.IndexByte(raw, '{')
	end := strings.LastIndexByte(raw, '}')
	if start < 0 || end < start {
		return false, "", fmt.Errorf("no JSON object in judge output")
	}
	var v struct {
		Pass   bool   `json:"pass"`
		Reason string `json:"reason"`
	}
	if err := json.Unmarshal([]byte(raw[start:end+1]), &v); err != nil {
		return false, "", fmt.Errorf("judge output not valid JSON: %w", err)
	}
	return v.Pass, v.Reason, nil
}

// validateJudgeOutput is the ValidateOutput guard for the judge call: it fails
// the call when the response is not a parseable verdict.
func validateJudgeOutput(s string) error {
	_, _, err := parseJudge(s)
	return err
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `env -u FORCE_COLOR go test ./internal/eval/ -run 'TestMustInclude|TestParseJudge' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/eval/grade.go internal/eval/grade_test.go
git commit -m "$(cat <<'EOF'
feat(eval): routine grading — must_include gate + tolerant judge-verdict parsing (FR-141)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 4: Runner — `Chokepoint` seam, `Run`, escalation-aware scorecard

**Files:**
- Create: `internal/eval/run.go`
- Test: `internal/eval/run_test.go`

**Interfaces:**
- Consumes: `Case`, `Family`, `Verdict`, `gradeClassify`, `mustInclude`, `judgeSystem`, `judgePrompt`, `parseJudge`, `validateJudgeOutput` (Tasks 1–3); `tokens.AgentCall`, `tokens.AgentResult`, `tokens.Message` (existing).
- Produces:
  ```go
  type Chokepoint interface {
      Run(ctx context.Context, call tokens.AgentCall) (tokens.AgentResult, error)
  }

  type Options struct {
      Model       string                       // override ref; "" ⇒ per-family alias
      Family      string                       // "classify"|"routine"|"synthesis"|"all"
      ExpectModel func(modelKey string) string // resolves the bare model a call should return
  }

  type CaseResult struct { Name string; Verdict Verdict }
  type FamilyReport struct {
      Family                           Family
      Model                            string
      Total, Passed, Escalated, Failed int
      Cases                            []CaseResult
  }
  type Report struct { Families []FamilyReport }

  func Run(ctx context.Context, cp Chokepoint, cases []Case, opts Options) (Report, error)
  func (r Report) MinPass(pct int) bool
  ```

- [ ] **Step 1: Write the failing test**

Create `internal/eval/run_test.go`:
```go
package eval

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/jandro-es/axon/internal/tokens"
)

// fakeCP is a scripted Chokepoint. It answers target calls (Operation
// "eval.target") from targetFn and judge calls ("eval.judge") from judgeFn.
type fakeCP struct {
	targetModel string
	targetFn    func(prompt string) (string, error)
	judgeFn     func() (string, error)
	calls       int
}

func (f *fakeCP) Run(_ context.Context, call tokens.AgentCall) (tokens.AgentResult, error) {
	f.calls++
	prompt := ""
	if len(call.Messages) > 0 {
		prompt = call.Messages[len(call.Messages)-1].Content
	}
	if call.Operation == "eval.judge" {
		txt, err := f.judgeFn()
		if err != nil {
			return tokens.AgentResult{}, err
		}
		return tokens.AgentResult{Text: txt, Model: "claude-judge"}, nil
	}
	txt, err := f.targetFn(prompt)
	if err != nil {
		return tokens.AgentResult{}, err
	}
	return tokens.AgentResult{Text: txt, Model: f.targetModel}, nil
}

func expectQwen(string) string { return "qwen" }

func TestRunClassifyPassAndFail(t *testing.T) {
	cases := []Case{
		{Name: "ok", Family: FamilyClassify, Grade: Grade{ExpectJSON: json.RawMessage(`{"kind":"article"}`)}, Prompt: "p"},
		{Name: "bad", Family: FamilyClassify, Grade: Grade{ExpectText: "high"}, Prompt: "p"},
	}
	cp := &fakeCP{targetModel: "qwen", targetFn: func(string) (string, error) { return `{"kind":"article"}`, nil }}
	rep, err := Run(context.Background(), cp, cases, Options{Model: "ollama:qwen", ExpectModel: expectQwen})
	if err != nil {
		t.Fatal(err)
	}
	fr := rep.Families[0]
	if fr.Total != 2 || fr.Passed != 1 || fr.Failed != 1 {
		t.Fatalf("totals: %+v", fr)
	}
}

func TestRunRoutineHybridJudge(t *testing.T) {
	cases := []Case{{
		Name: "sum", Family: FamilyRoutine, Prompt: "p",
		Grade: Grade{MustInclude: []string{"Alice"}, Rubric: "must mention Alice"},
	}}
	cp := &fakeCP{
		targetModel: "qwen",
		targetFn:    func(string) (string, error) { return "Alice shipped it", nil },
		judgeFn:     func() (string, error) { return `{"pass":true,"reason":"ok"}`, nil },
	}
	rep, err := Run(context.Background(), cp, cases, Options{Model: "ollama:qwen", ExpectModel: expectQwen})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Families[0].Passed != 1 {
		t.Fatalf("hybrid pass expected: %+v", rep.Families[0])
	}
	if cp.calls != 2 { // one target + one judge
		t.Fatalf("want 2 chokepoint calls, got %d", cp.calls)
	}
}

func TestRunRoutineMustIncludeFailsBeforeJudge(t *testing.T) {
	cases := []Case{{
		Name: "sum", Family: FamilyRoutine, Prompt: "p",
		Grade: Grade{MustInclude: []string{"Bob"}, Rubric: "must mention Bob"},
	}}
	judged := false
	cp := &fakeCP{
		targetModel: "qwen",
		targetFn:    func(string) (string, error) { return "Alice only", nil },
		judgeFn:     func() (string, error) { judged = true; return `{"pass":true}`, nil },
	}
	rep, _ := Run(context.Background(), cp, cases, Options{Model: "ollama:qwen", ExpectModel: expectQwen})
	if rep.Families[0].Failed != 1 {
		t.Fatalf("missing anchor must fail: %+v", rep.Families[0])
	}
	if judged {
		t.Fatal("judge must not run when must_include gate fails")
	}
}

func TestRunEscalationVisible(t *testing.T) {
	cases := []Case{{Name: "ok", Family: FamilyClassify, Prompt: "p",
		Grade: Grade{ExpectJSON: json.RawMessage(`{"kind":"article"}`)}}}
	// Target returns the RIGHT answer but the WRONG model (fell forward to Claude).
	cp := &fakeCP{targetModel: "claude-opus", targetFn: func(string) (string, error) { return `{"kind":"article"}`, nil }}
	rep, _ := Run(context.Background(), cp, cases, Options{Model: "ollama:qwen", ExpectModel: expectQwen})
	fr := rep.Families[0]
	if fr.Escalated != 1 || fr.Passed != 0 {
		t.Fatalf("escalated answer must not count as pass: %+v", fr)
	}
}

func TestRunTransportErrorFails(t *testing.T) {
	cases := []Case{{Name: "ok", Family: FamilyClassify, Prompt: "p",
		Grade: Grade{ExpectText: "high"}}}
	cp := &fakeCP{targetModel: "qwen", targetFn: func(string) (string, error) { return "", errors.New("connection refused") }}
	rep, _ := Run(context.Background(), cp, cases, Options{Model: "ollama:qwen", ExpectModel: expectQwen})
	fr := rep.Families[0]
	if fr.Failed != 1 || !strings.Contains(fr.Cases[0].Verdict.Reason, "connection refused") {
		t.Fatalf("transport error must be a failed case with the error reason: %+v", fr)
	}
}

func TestMinPass(t *testing.T) {
	rep := Report{Families: []FamilyReport{{Total: 4, Passed: 3}}} // 75%
	if !rep.MinPass(75) || rep.MinPass(76) {
		t.Fatal("MinPass boundary wrong")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `env -u FORCE_COLOR go test ./internal/eval/ -run TestRun -v`
Expected: FAIL — `undefined: Run`.

- [ ] **Step 3: Write `run.go`**

Create `internal/eval/run.go`:
```go
package eval

import (
	"context"
	"fmt"
	"sort"

	"github.com/jandro-es/axon/internal/tokens"
)

// Chokepoint is the minimal surface the runner needs — satisfied by
// *tokens.Manager. Defined at the consumer so the runner is unit-testable with a
// fake and never imports internal/agent (cardinal rule 1).
type Chokepoint interface {
	Run(ctx context.Context, call tokens.AgentCall) (tokens.AgentResult, error)
}

// Options parameterise a run.
type Options struct {
	// Model overrides the target for every case (e.g. "ollama:qwen2.5"); "" runs
	// each family against its configured tier alias (the family name).
	Model string
	// Family filters to one family; "" or "all" runs all present families.
	Family string
	// ExpectModel resolves the model key a call was sent with to the bare model
	// string the chokepoint should return. A mismatch is scored Escalated. The
	// CLI supplies it from config; a nil resolver disables the check.
	ExpectModel func(modelKey string) string
}

// CaseResult is one graded case.
type CaseResult struct {
	Name    string
	Verdict Verdict
}

// FamilyReport aggregates one family's results.
type FamilyReport struct {
	Family    Family
	Model     string // the target ref cases ran against (display)
	Total     int
	Passed    int
	Escalated int
	Failed    int
	Cases     []CaseResult
}

// Report is the full scorecard.
type Report struct {
	Families []FamilyReport
}

// MinPass reports whether every family's pass rate is >= pct percent.
func (r Report) MinPass(pct int) bool {
	for _, f := range r.Families {
		if f.Total == 0 {
			continue
		}
		if f.Passed*100 < pct*f.Total {
			return false
		}
	}
	return true
}

// Run evaluates cases through cp and returns a Report. For each case it issues
// one target call; records escalation by comparing AgentResult.Model against the
// intended bare model; grades; and — for routine cases with a Rubric that pass
// the must_include gate — issues one judge call. Never mutates the vault.
func Run(ctx context.Context, cp Chokepoint, cases []Case, opts Options) (Report, error) {
	byFamily := map[Family][]Case{}
	for _, c := range cases {
		byFamily[c.Family] = append(byFamily[c.Family], c)
	}
	fams := make([]Family, 0, len(byFamily))
	for f := range byFamily {
		fams = append(fams, f)
	}
	sort.Slice(fams, func(i, j int) bool { return fams[i] < fams[j] })

	var rep Report
	for _, fam := range fams {
		modelKey := opts.Model
		if modelKey == "" {
			modelKey = string(fam)
		}
		fr := FamilyReport{Family: fam, Model: modelKey}
		for _, c := range byFamily[fam] {
			v := runCase(ctx, cp, c, modelKey, opts.ExpectModel)
			fr.Cases = append(fr.Cases, CaseResult{Name: c.Name, Verdict: v})
			fr.Total++
			switch {
			case v.Escalated:
				fr.Escalated++
			case v.Pass:
				fr.Passed++
			default:
				fr.Failed++
			}
		}
		rep.Families = append(rep.Families, fr)
	}
	return rep, nil
}

// runCase issues the target call, checks escalation, then grades.
func runCase(ctx context.Context, cp Chokepoint, c Case, modelKey string, expect func(string) string) Verdict {
	res, err := cp.Run(ctx, tokens.AgentCall{
		Operation: "eval.target",
		ModelKey:  modelKey,
		System:    c.System,
		Messages:  []tokens.Message{{Role: "user", Content: c.Prompt}},
	})
	if err != nil {
		return Verdict{Reason: fmt.Sprintf("target call failed: %v", err)}
	}
	if expect != nil {
		if want := expect(modelKey); want != "" && res.Model != want {
			return Verdict{Escalated: true, Reason: fmt.Sprintf("answer came from %q, not target %q", res.Model, want)}
		}
	}
	if c.Family == FamilyClassify {
		return gradeClassify(c, res.Text)
	}
	return gradeRoutine(ctx, cp, c, res.Text)
}

// gradeRoutine applies the must_include gate then, if a Rubric is set, one judge
// call through the chokepoint.
func gradeRoutine(ctx context.Context, cp Chokepoint, c Case, got string) Verdict {
	if ok, missing := mustInclude(c.Grade.MustInclude, got); !ok {
		return Verdict{Reason: fmt.Sprintf("missing required anchor %q", missing)}
	}
	if c.Grade.Rubric == "" {
		return Verdict{Pass: true}
	}
	res, err := cp.Run(ctx, tokens.AgentCall{
		Operation:      "eval.judge",
		ModelKey:       "synthesis", // always Claude (ADR-015); ledgered like any call
		System:         judgeSystem,
		Messages:       []tokens.Message{{Role: "user", Content: judgePrompt(c.Grade.Rubric, got)}},
		ValidateOutput: validateJudgeOutput,
	})
	if err != nil {
		return Verdict{Reason: fmt.Sprintf("judge call failed: %v", err)}
	}
	pass, reason, err := parseJudge(res.Text)
	if err != nil {
		return Verdict{Reason: fmt.Sprintf("judge verdict unparseable: %v", err)}
	}
	if !pass {
		return Verdict{Reason: "judge: " + reason}
	}
	return Verdict{Pass: true}
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `env -u FORCE_COLOR go test ./internal/eval/ -v`
Expected: PASS (all runner + grading + load tests).

- [ ] **Step 5: Enforce cardinal-rule-1 import boundary**

Run: `go list -deps ./internal/eval/ | grep -E 'internal/(agent|automations|vault)$' && echo LEAK || echo clean`
Expected: `clean` — `internal/eval` must not depend on `agent`, `automations`, or `vault`.

- [ ] **Step 6: Commit**

```bash
git add internal/eval/run.go internal/eval/run_test.go
git commit -m "$(cat <<'EOF'
feat(eval): escalation-aware runner over the chokepoint seam (FR-140/141)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 5: CLI — `axon eval`, fail-fast manager wiring, scorecard

**Files:**
- Modify: `cmd/axon/deps.go`
- Create: `cmd/axon/eval_cmd.go`
- Create: `cmd/axon/eval_cmd_test.go`
- Modify: `cmd/axon/root.go`

**Interfaces:**
- Consumes: `eval.LoadCases`, `eval.Run`, `eval.Options`, `eval.Report`, `eval.FamilyReport` (Tasks 1–4); `loadProfileDeps`, `managerConfig`, `(*profileDeps).agentRouter`, `(*profileDeps).buildSearcher`, `tokens.NewWithRouter`, `config.ParseModelRef` (existing).
- Produces:
  ```go
  // deps.go
  func (d *profileDeps) evalManager(bus *events.Bus) (tokens.Manager, func(modelKey string) string)
  // eval_cmd.go
  func newEvalCmd(gf *globalFlags) *cobra.Command
  func writeScorecard(w io.Writer, rep eval.Report)
  func writeJSON(w io.Writer, rep eval.Report) error
  ```

- [ ] **Step 1: Add `evalManager` to `deps.go`**

The eval manager is router-backed (so local refs reach Ollama) built from a profile clone whose `Models.LocalFallback = "fail"` (measurement integrity). The returned resolver maps a model key (family alias or concrete ref) to the bare model the manager would return, so the runner can detect escalation.

Add to `cmd/axon/deps.go`:
```go
// evalManager builds a token manager for the eval harness: router-backed (local
// refs reach Ollama) but with local_fallback forced to "fail" so a broken local
// model surfaces as a failed case instead of silently measuring Claude
// (R5.1 measurement integrity). The returned resolver maps a model key — a
// family alias or a concrete ref — to the bare model string the manager returns,
// so the runner can flag escalation. Requires the database to be open.
func (d *profileDeps) evalManager(bus *events.Bus) (tokens.Manager, func(string) string) {
	p := d.profile
	p.Models.LocalFallback = "fail"
	mgr := tokens.NewWithRouter(d.db, d.agentRouter(), d.buildSearcher(), bus, managerConfig(d.name, p, d.cfg))
	resolve := func(key string) string {
		switch key {
		case "classify":
			key = p.Models.Classify
		case "routine":
			key = p.Models.Routine
		case "synthesis":
			key = p.Models.Synthesis
		}
		return config.ParseModelRef(key).Model
	}
	return mgr, resolve
}
```
(`config` and `events` are already imported in `deps.go`; if `agentRouter`/`buildSearcher` mutate nothing on `d`, the `p := d.profile` value-copy is a safe shallow clone for the `Models` override — confirm `config.Profile.Models` is a value field, which it is per `internal/config/types.go`.)

- [ ] **Step 2: Write the CLI smoke test (failing)**

Create `cmd/axon/eval_cmd_test.go`:
```go
package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/jandro-es/axon/internal/eval"
	"github.com/jandro-es/axon/internal/tokens"
)

// stubCP is a Chokepoint that answers target calls with a fixed classify answer
// and judge calls with pass:true — enough to exercise scorecard/JSON plumbing.
type stubCP struct{ text, model string }

func (s stubCP) Run(_ context.Context, call tokens.AgentCall) (tokens.AgentResult, error) {
	if call.Operation == "eval.judge" {
		return tokens.AgentResult{Text: `{"pass":true,"reason":"ok"}`, Model: "claude"}, nil
	}
	return tokens.AgentResult{Text: s.text, Model: s.model}, nil
}

func TestRunEvalScorecardAndJSON(t *testing.T) {
	cases, err := eval.LoadCases("classify")
	if err != nil {
		t.Fatal(err)
	}
	cp := stubCP{text: `{"kind":"article"}`, model: "qwen"}
	rep, err := eval.Run(context.Background(), cp, cases, eval.Options{
		Model: "ollama:qwen", ExpectModel: func(string) string { return "qwen" },
	})
	if err != nil {
		t.Fatal(err)
	}

	var text bytes.Buffer
	writeScorecard(&text, rep)
	if !strings.Contains(text.String(), "classify") {
		t.Fatalf("scorecard missing family header:\n%s", text.String())
	}

	var jsonOut bytes.Buffer
	if err := writeJSON(&jsonOut, rep); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(jsonOut.String(), `"Family"`) {
		t.Fatalf("json missing Family field:\n%s", jsonOut.String())
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `env -u FORCE_COLOR go test ./cmd/axon/ -run TestRunEvalScorecard -v`
Expected: FAIL — `undefined: writeScorecard`.

- [ ] **Step 4: Write `eval_cmd.go`**

First confirm the `internal/ui` styler API used elsewhere: `grep -n "func For\|func (s Styler)\|Green\|Yellow\|Red\|Bold\|Dim" internal/ui/*.go` and mirror the exact type/method names (see `cmd/axon/ask_cmd.go`, which uses `sty := ui.For(out)` then `sty.Yellow(...)`, `sty.Bold(...)`, `sty.Dim(...)`, `sty.Cyan(...)`). If the exported styler type is not named `Styler`, adjust `pctLabel`'s parameter type to match; if a colour method is missing, drop that colour and print plain.

Create `cmd/axon/eval_cmd.go`:
```go
package main

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/jandro-es/axon/internal/eval"
	"github.com/jandro-es/axon/internal/ui"
)

func newEvalCmd(gf *globalFlags) *cobra.Command {
	var family, model string
	var asJSON bool
	var minPass int
	cmd := &cobra.Command{
		Use:   "eval",
		Short: "Evaluate a local (or any) model against AXON's golden task sets",
		Long: "Runs in-repo golden sets (classify + routine families) through the token\n" +
			"chokepoint and grades them hybrid: deterministic for classify, must_include +\n" +
			"a Claude judge for routine. Eval calls run fail-fast (local_fallback: fail) so a\n" +
			"broken local model is scored failed/escalated, never silently answered by Claude\n" +
			"(FR-140/141). With no --model each family runs against its configured tier.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cases, err := eval.LoadCases(family)
			if err != nil {
				return err
			}
			if len(cases) == 0 {
				return fmt.Errorf("no golden cases for family %q", family)
			}
			deps, err := loadProfileDeps(gf, true)
			if err != nil {
				return err
			}
			defer deps.close()
			mgr, expect := deps.evalManager(nil)

			rep, err := eval.Run(cmd.Context(), mgr, cases, eval.Options{
				Model: model, Family: family, ExpectModel: expect,
			})
			if err != nil {
				return err
			}

			out := cmd.OutOrStdout()
			if asJSON {
				if err := writeJSON(out, rep); err != nil {
					return err
				}
			} else {
				writeScorecard(out, rep)
			}
			if minPass > 0 && !rep.MinPass(minPass) {
				return fmt.Errorf("eval below --min-pass %d%%", minPass)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&family, "family", "all", "task family: classify|routine|synthesis|all")
	cmd.Flags().StringVar(&model, "model", "", "model ref to evaluate (e.g. ollama:qwen2.5); default: configured tier")
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit the report as JSON")
	cmd.Flags().IntVar(&minPass, "min-pass", 0, "exit non-zero if any family's pass rate is below this percent")
	return cmd
}

// writeJSON emits the machine-readable report.
func writeJSON(w io.Writer, rep eval.Report) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(rep)
}

// writeScorecard prints a per-family scorecard plus per-case pass/fail lines.
func writeScorecard(w io.Writer, rep eval.Report) {
	sty := ui.For(w)
	for _, f := range rep.Families {
		fmt.Fprintf(w, "\n%s  %s %s\n", sty.Bold(string(f.Family)), sty.Dim("model:"), f.Model)
		fmt.Fprintf(w, "  %s %d/%d passed", pctLabel(sty, f), f.Passed, f.Total)
		if f.Escalated > 0 {
			fmt.Fprintf(w, ", %s escalated", sty.Yellow(fmt.Sprintf("%d", f.Escalated)))
		}
		if f.Failed > 0 {
			fmt.Fprintf(w, ", %s failed", sty.Red(fmt.Sprintf("%d", f.Failed)))
		}
		fmt.Fprintln(w)
		for _, c := range f.Cases {
			mark, detail := sty.Green("✓"), ""
			switch {
			case c.Verdict.Escalated:
				mark, detail = sty.Yellow("↑"), "  "+sty.Dim(c.Verdict.Reason)
			case !c.Verdict.Pass:
				mark, detail = sty.Red("✗"), "  "+sty.Dim(c.Verdict.Reason)
			}
			fmt.Fprintf(w, "    %s %s%s\n", mark, c.Name, detail)
		}
	}
}

// pctLabel colours the pass rate green at 100%, else yellow.
func pctLabel(sty ui.Styler, f eval.FamilyReport) string {
	pct := 0
	if f.Total > 0 {
		pct = f.Passed * 100 / f.Total
	}
	s := fmt.Sprintf("%d%%", pct)
	if pct == 100 {
		return sty.Green(s)
	}
	return sty.Yellow(s)
}
```

- [ ] **Step 5: Register the command**

In `cmd/axon/root.go`, change:
```go
	root.AddCommand(newAutomationsCmd(gf), newHealthCmd(gf))
```
to:
```go
	root.AddCommand(newAutomationsCmd(gf), newHealthCmd(gf), newEvalCmd(gf))
```

- [ ] **Step 6: Run tests to verify they pass**

Run: `env -u FORCE_COLOR go test ./cmd/axon/ -run TestRunEvalScorecard -v`
Expected: PASS.

- [ ] **Step 7: Full build + vet + package tests**

Run:
```bash
go build ./cmd/axon/ && go vet ./internal/eval/ ./cmd/axon/ && env -u FORCE_COLOR go test ./internal/eval/ ./cmd/axon/
```
Expected: build clean, vet clean, tests PASS.

- [ ] **Step 8: Smoke the command help**

Run: `go run ./cmd/axon eval --help`
Expected: usage text lists `--family`, `--model`, `--json`, `--min-pass`.

- [ ] **Step 9: Commit**

```bash
git add cmd/axon/eval_cmd.go cmd/axon/eval_cmd_test.go cmd/axon/deps.go cmd/axon/root.go
git commit -m "$(cat <<'EOF'
feat(eval): axon eval command — fail-fast manager wiring + scorecard (FR-140/141)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Self-Review

**Spec coverage:**
- Architecture (`internal/eval` leaf + thin `cmd/axon/eval_cmd.go`, no `agent`/`automations` import) — Tasks 1–5; boundary asserted in Task 4 Step 5.
- Data model (`Family`, `Grade`, `Case`, `LoadCases` with validation) — Task 1.
- Grading — classify deterministic (Task 2), routine `must_include` + judge (Tasks 3–4).
- Runner + chokepoint seam + escalation via `AgentResult.Model` + fail-fast — Task 4 (+ manager built fail-fast in Task 5).
- CLI (`--model`/`--family`/`--json`/`--min-pass`, per-family default tier, exit code) — Task 5.
- Measurement integrity (fail-fast manager, escalation scored not-pass) — Task 5 `evalManager` + Task 4 escalation test.
- CI grades against a fake — the runner/CLI tests use fake Chokepoints (`fakeCP`, `stubCP`); real Ollama/Claude never invoked.
- Out of scope (no DB persistence, no doctor check, no promotion-gating, no cascade) — none added. ✓

**Type consistency:** `tokens.AgentCall`/`tokens.AgentResult`/`tokens.Message` used verbatim from `internal/tokens/manager.go` (corrects the spec's provisional `RunResult`). `Verdict{Pass,Escalated,Reason}`, `Report`/`FamilyReport`/`CaseResult`, `Options{Model,Family,ExpectModel}` consistent across Tasks 4–5. `Family` constants consistent across Tasks 1–5. `ExpectModel` returns the **bare** model, matching `AgentResult.Model`.

**Placeholder scan:** none — every code step is complete; the only deferred confirmations are the `internal/ui` styler method names (Task 5 Step 4) and the `config.Profile.Models` value-field assumption (Task 5 Step 1), each flagged with the concrete file to check.

## Execution Handoff

Per Jandro's standing workflow (inline execution, spec-review gate already passed), this plan is executed **inline** via superpowers:executing-plans with a checkpoint after each task's commit.
