# Operations Overhaul Plan A — TUI Foundation, Command Migration, `axon configure`

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Adopt the Charm stack (bubbletea/huh/lipgloss), render every command as a live view on a TTY with untouched plain/JSON output elsewhere, and ship `axon configure` — including the one-flow Apple↔Ollama switch.

**Architecture:** A new `internal/tui` package owns TTY detection and four reusable surfaces (steps program, spinner runner, styled table, huh-based confirm/forms). Commands keep their existing core calls and plain renderers verbatim; on a TTY they route the same structured results through the tui surfaces. `internal/ui` is reimplemented on lipgloss with an unchanged exported API. `axon configure` composes `setConfigValue` + core probes + reembed into menus and scriptable subcommands.

**Tech Stack:** Go 1.26; charmbracelet/bubbletea v1.3.10, huh v1.0.0, lipgloss v1.1.0 (pin these); existing cobra CLI.

**Spec:** `docs/superpowers/specs/2026-07-02-operations-overhaul-design.md` (Components 1–3)

## Global Constraints

- Plain renderers are CANONICAL: non-TTY and `--json` output, exit codes, and greppable strings must not change — the existing CLI tests are the regression net and must keep passing untouched (except where a test asserts an interactive prompt we are deliberately replacing, i.e. onboard).
- Nothing may ever block on a TTY prompt when stdout is not a terminal (`tui.Interactive` is the single gate; it must return false for `CI=true`, non-file writers, and non-TTY files).
- All config edits go through `setConfigValue` (comment-preserving, re-validated).
- No new Claude paths; the token-manager cardinal rule is untouched.
- `gofmt`/`go vet`/`golangci-lint` clean; table-driven tests; errors wrapped with `%w`.

---

### Task 1: ADR-014 + Charm dependencies

**Files:**
- Modify: `go.mod` / `go.sum` (via `go get`)
- Modify: `docs/02-architecture.md` (append ADR-014 after ADR-013)
- Modify: `CLAUDE.md:~37` (repo structure: add `tui/` line under internal/)

**Interfaces:**
- Produces: modules `github.com/charmbracelet/bubbletea@v1.3.10`, `github.com/charmbracelet/huh@v1.0.0`, `github.com/charmbracelet/lipgloss@v1.1.0` available for import.

- [ ] **Step 1: Add dependencies**

```bash
go get github.com/charmbracelet/bubbletea@v1.3.10 github.com/charmbracelet/huh@v1.0.0 github.com/charmbracelet/lipgloss@v1.1.0
go mod tidy && go build ./...
```
Expected: build OK.

- [ ] **Step 2: Write ADR-014** — append to `docs/02-architecture.md` after ADR-013, matching the existing ADR format:

> ### ADR-014 — Charm TUI stack (bubbletea + huh + lipgloss) for the entire CLI surface
> **Decision:** Adopt the Charm family as the one terminal-UI dependency set: `bubbletea` runs live interactive views, `huh` provides forms/menus (onboard, configure, setup), `lipgloss` styles all output. Every command renders a live view on a TTY; the pre-existing plain renderers remain the canonical output for non-TTY, `--json`, and CI — enforced by a single `tui.Interactive` gate so headless paths can never block on a prompt (NFR-05 posture).
> **Why:** The ops surface (install, update, configure, provider switching, vault migration) needs real interactivity; hand-rolled ANSI + bufio prompts don't scale to menus/progress and were already duplicated across onboard and the installers. Charm is the de-facto standard, pure Go, and replaces bespoke code rather than adding to it (`internal/ui` shrinks to a lipgloss facade). User-directed adoption; recorded because it crosses the "no heavyweight framework without an ADR" guardrail.
> **Trade-offs:** three new dependencies and a second rendering path per command (live vs plain). Mitigations: plain renderers stay canonical and tested; live views are thin adapters over the same structured results; all Charm usage funnels through `internal/tui` so a future swap is one package.

- [ ] **Step 3: CLAUDE.md structure line** — in the repo-structure block, after the `ui/` line add:

```
  tui/         # Charm-based terminal UI: TTY gate, steps/spinner/table surfaces, forms (ADR-014)
```

- [ ] **Step 4: Commit**

```bash
git add go.mod go.sum docs/02-architecture.md CLAUDE.md
git commit -m "Adopt the Charm TUI stack (ADR-014)"
```

---

### Task 2: Reimplement `internal/ui` on lipgloss (API-frozen)

> **EXECUTION DEVIATION (recorded during implementation):** rewriting the Styler
> on lipgloss double-gates color decisions (lipgloss's renderer does its own
> TTY/profile detection, conflicting with ui's NO_COLOR/FORCE_COLOR contract and
> the `Styler{on: true}` test seam) while emitting byte-identical 4-bit ANSI for
> every color ui uses. The spec goal — one visual language — is met by shared
> glyphs + the same ANSI palette. Kept ui's internals; documented the ADR-014
> relationship in the package comment. lipgloss is used where it adds real value
> (tui tables and live views).

**Files:**
- Modify: `internal/ui/ui.go` (rewrite internals only)
- Test: `internal/ui/ui_test.go` (existing tests must pass unchanged; add one lipgloss-specific test)

**Interfaces:**
- Produces (unchanged API): `ui.For(w io.Writer) Styler`, `Styler.Enabled() bool`, `Bold/Dim/Red/Green/Yellow/Blue/Cyan/Gray(string) string`, `Divider(n int) string`, `Header(emoji, title string) string`, `ui.FprintError(w, err)`, all `Icon*` constants, `ui.Hint(err)`.

- [ ] **Step 1: Read `internal/ui/ui.go` and `ui_test.go` fully.** The existing tests assert exact ANSI sequences OR semantic behavior — if they assert raw `\x1b[1m` wrapping, port the assertions to "styled output differs from input and contains it" so the lipgloss renderer (which may emit different but equivalent sequences) passes honestly. Do NOT weaken the `Enabled()==false → output==input` assertions.

- [ ] **Step 2: Rewrite the Styler internals** — same file, same exports. Shape:

```go
type Styler struct {
	on bool
	r  *lipgloss.Renderer
}

func For(w io.Writer) Styler {
	r := lipgloss.NewRenderer(w)
	return Styler{on: colorEnabled(w), r: r}
}

func (s Styler) style(st lipgloss.Style, str string) string {
	if !s.on {
		return str
	}
	return st.Render(str)
}

func (s Styler) Bold(str string) string { return s.style(s.r.NewStyle().Bold(true), str) }
func (s Styler) Red(str string) string  { return s.style(s.r.NewStyle().Foreground(lipgloss.Color("1")), str) }
// ...Green("2"), Yellow("3"), Blue("4"), Cyan("6"), Gray("8"), Dim → Faint(true)
```

Keep `colorEnabled` (NO_COLOR / not-a-TTY logic) exactly as-is; `Divider`, `Header`, `FprintError` keep their current bodies (they compose the color methods).

- [ ] **Step 3: Run** `go test ./internal/ui/ ./cmd/axon/` — all PASS (cmd tests exercise styled output via non-TTY buffers, which must stay plain).

- [ ] **Step 4: Commit** — `git add internal/ui && git commit -m "Reimplement ui.Styler on lipgloss (API frozen)"`

---

### Task 3: `internal/tui` foundation

**Files:**
- Create: `internal/tui/tui.go` (Interactive gate)
- Create: `internal/tui/steps.go` (live step-list program)
- Create: `internal/tui/spin.go` (spinner runner)
- Create: `internal/tui/table.go` (styled table)
- Create: `internal/tui/ask.go` (huh wrappers: Confirm, TypedConfirm, Select, Input)
- Test: `internal/tui/tui_test.go`, `internal/tui/steps_test.go`, `internal/tui/table_test.go`

**Interfaces (everything later tasks consume):**

```go
package tui

// Interactive reports whether live TUI rendering is allowed on w:
// w must be an *os.File that is a terminal, CI must not be set.
func Interactive(w io.Writer) bool

// StepStatus mirrors core's step semantics plus a live "running" state.
type StepStatus string // "running", "done", "already", "warn", "failed"

// Steps is a live step-list. Zero-value unusable; construct with NewSteps.
// If out is not interactive, methods print plain lines via the fallback
// printer instead of running a tea program — callers never branch.
func NewSteps(out io.Writer, title string, plain func(name, detail string, st StepStatus)) *Steps
func (s *Steps) Start()                                        // begin rendering (no-op when plain)
func (s *Steps) Set(name, detail string, st StepStatus)        // upsert a row
func (s *Steps) Finish(summary string) error                   // stop, print summary
// Spin runs fn with a live spinner titled title; plain mode prints title,
// runs fn, prints the returned summary. fn's error is returned verbatim.
func Spin(out io.Writer, title string, fn func() (summary string, err error)) error

// Table renders a lipgloss-styled table (plain ASCII when not interactive).
func Table(out io.Writer, headers []string, rows [][]string)

// Confirm asks yes/no; returns defaultYes without prompting when out is not
// interactive. TypedConfirm requires typing phrase exactly; returns false
// (never blocks) when not interactive.
func Confirm(out io.Writer, in io.Reader, prompt string, defaultYes bool) bool
func TypedConfirm(out io.Writer, in io.Reader, prompt, phrase string) bool

// Select and Input are huh wrappers used by onboard/configure/setup.
func Select(out io.Writer, in io.Reader, title string, options []Option) (string, error)
func Input(out io.Writer, in io.Reader, title, placeholder, def string) (string, error)
type Option struct{ Label, Value, Hint string }
```

- [ ] **Step 1: Write the failing tests** — `internal/tui/tui_test.go`:

```go
package tui

import (
	"bytes"
	"testing"
)

func TestInteractiveFalseForBuffers(t *testing.T) {
	if Interactive(&bytes.Buffer{}) {
		t.Error("a bytes.Buffer must never be interactive")
	}
}

func TestInteractiveFalseUnderCI(t *testing.T) {
	t.Setenv("CI", "true")
	// even a real tty (if the test has one) must be non-interactive under CI;
	// buffers double-check the cheap path.
	if Interactive(&bytes.Buffer{}) {
		t.Error("CI must force non-interactive")
	}
}
```

`internal/tui/steps_test.go` (plain-fallback behavior is the testable contract):

```go
func TestStepsPlainFallback(t *testing.T) {
	var out bytes.Buffer
	var got []string
	s := NewSteps(&out, "provisioning", func(name, detail string, st StepStatus) {
		got = append(got, name+"/"+string(st)+"/"+detail)
	})
	s.Start()
	s.Set("db", "creating", StatusRunning)
	s.Set("db", "created", StatusDone)
	if err := s.Finish("all good"); err != nil {
		t.Fatal(err)
	}
	// plain mode: every terminal-state update goes through the fallback printer
	want := []string{"db/done/created"}
	if len(got) != 1 || got[0] != want[0] {
		t.Errorf("plain fallback rows = %v, want %v", got, want)
	}
	if !strings.Contains(out.String(), "all good") {
		t.Errorf("summary missing: %q", out.String())
	}
}

func TestSpinPlainRunsFn(t *testing.T) {
	var out bytes.Buffer
	ran := false
	err := Spin(&out, "reindexing", func() (string, error) { ran = true; return "14 notes", nil })
	if err != nil || !ran {
		t.Fatalf("ran=%v err=%v", ran, err)
	}
	if !strings.Contains(out.String(), "14 notes") {
		t.Errorf("summary missing: %q", out.String())
	}
}
```

`internal/tui/table_test.go`:

```go
func TestTablePlain(t *testing.T) {
	var out bytes.Buffer
	Table(&out, []string{"NAME", "STATUS"}, [][]string{{"heartbeat", "ok"}, {"daily-log", "skipped"}})
	s := out.String()
	for _, want := range []string{"NAME", "heartbeat", "skipped"} {
		if !strings.Contains(s, want) {
			t.Errorf("table missing %q:\n%s", want, s)
		}
	}
}

func TestConfirmNonInteractiveReturnsDefault(t *testing.T) {
	var out bytes.Buffer
	if !Confirm(&out, strings.NewReader(""), "proceed?", true) {
		t.Error("non-interactive Confirm must return defaultYes=true")
	}
	if Confirm(&out, strings.NewReader(""), "proceed?", false) {
		t.Error("non-interactive Confirm must return defaultYes=false")
	}
	if TypedConfirm(&out, strings.NewReader(""), "delete all?", "delete") {
		t.Error("non-interactive TypedConfirm must refuse")
	}
}
```

- [ ] **Step 2: Run to verify RED** — `go test ./internal/tui/` → build failure (package empty).

- [ ] **Step 3: Implement.** `tui.go`:

```go
package tui

import (
	"io"
	"os"

	"golang.org/x/term"
)

// Interactive reports whether live TUI rendering is allowed on w. It is the
// single gate that keeps daemons, scripts and CI from ever blocking on a
// prompt or emitting control sequences into a pipe.
func Interactive(w io.Writer) bool {
	if os.Getenv("CI") != "" {
		return false
	}
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	return term.IsTerminal(int(f.Fd()))
}
```

`steps.go` — a tea.Model holding ordered rows `{name, detail string; st StepStatus}` with a spinner frame on `running`; `*Steps` wraps `tea.NewProgram(model, tea.WithOutput(out))` started in a goroutine, `Set` delivered via `program.Send(stepMsg{...})`, `Finish` sends a done message and waits for the program to exit, then prints the summary line via `ui.For(out)`. In plain mode (`!Interactive(out)`) `Set` calls the fallback printer ONLY for terminal states (done/already/warn/failed — never "running", to keep plain output identical to today), and `Finish` just prints the summary. Glyph mapping reuses `ui` icons: done ✓ / already ↻ / warn ⚠ / failed ✗.

`spin.go` — interactive: bubbletea spinner model, fn runs in a `tea.Cmd`, result message stops the program, summary line printed after; plain: print `title…`, run fn synchronously, print summary or return err.

`table.go` — build with `lipgloss/table` when interactive, plain `text/tabwriter` otherwise (both paths include headers + all cells so tests pass on either).

`ask.go` — each wrapper: if `!Interactive(out)` return the documented non-interactive result (Confirm→defaultYes, TypedConfirm→false, Select→error "requires a terminal", Input→def); else run the corresponding `huh` field group with `huh.WithOutput(out)`/`WithInput(in)`.

- [ ] **Step 4: GREEN** — `go test ./internal/tui/ -v` all PASS; `go vet ./...` clean.

- [ ] **Step 5: Commit** — `git add internal/tui && git commit -m "Add internal/tui: TTY gate, steps/spinner/table surfaces, huh forms"`

---

### Task 4: Live views for init + doctor

**Files:**
- Modify: `internal/core/init.go:52-66` (InitOptions gains OnStep), `:85-91` (add() calls it)
- Modify: `cmd/axon/init_cmd.go` (route through tui.Steps on TTY)
- Modify: `cmd/axon/doctor_cmd.go` (render checks through tui.Steps)
- Test: `internal/core/init_test.go` (OnStep receives every step), existing cmd tests stay green (buffers → plain path)

**Interfaces:**
- Consumes: `tui.NewSteps`, `tui.Interactive`.
- Produces: `core.InitOptions.OnStep func(StepResult)` (nil-safe, called for every step in order).

- [ ] **Step 1: Failing test** (`internal/core/init_test.go`):

```go
func TestInitOnStepReceivesEveryStep(t *testing.T) {
	cfg, profile := initProfile(t)
	var seen []string
	_, err := Init(context.Background(), InitOptions{
		Config: cfg, ProfileName: "personal", Profile: profile,
		CheckEmbeddingModel: stubEmbedCheck,
		OnStep:              func(s StepResult) { seen = append(seen, s.Name) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(seen) < 5 { // config, checks…, data dir, db, embeddings, scaffold…
		t.Errorf("OnStep saw only %v", seen)
	}
}
```

- [ ] **Step 2: RED** — unknown field OnStep.

- [ ] **Step 3: Implement.** InitOptions gains `OnStep func(StepResult)`; in `Init`'s `add` closure, after appending/printing: `if opts.OnStep != nil { opts.OnStep(s) }`. In `init_cmd.go` RunE, after building opts:

```go
out := cmd.OutOrStdout()
if !asJSON && tui.Interactive(out) {
	steps := tui.NewSteps(out, fmt.Sprintf("axon init — profile %q", name), nil)
	steps.Start()
	opts.Out = io.Discard // live view replaces streamed text
	opts.OnStep = func(s core.StepResult) {
		steps.Set(s.Name, s.Detail, tui.StepStatus(s.Status))
	}
	rep, runErr := core.Init(cmd.Context(), opts)
	summary := "environment converged"
	if !rep.Changed {
		summary = "no changes, already converged"
	}
	_ = steps.Finish(summary)
	if runErr != nil { return runErr }
	if !rep.OK { return fmt.Errorf("init completed with blocking failures") }
	return nil
}
// existing plain/JSON path unchanged below
```

`doctor_cmd.go`: on TTY, feed each `Check` into a `tui.Steps` (status mapping ok→done, warn→warn, fail→failed) and `Finish` with the overall status line; plain/JSON path untouched.

- [ ] **Step 4: GREEN + regression** — `go test ./internal/core/ ./cmd/axon/` all PASS (cmd tests use buffers → plain path exactly as before).

- [ ] **Step 5: Commit** — `git add internal/core cmd/axon && git commit -m "Render init and doctor as live step views on a TTY"`

---

### Task 5: Live views for reindex/run/ingest + tables for status/automations/health/search

**Files:**
- Modify: `cmd/axon/reindex_cmd.go`, `cmd/axon/run_cmd.go`, `cmd/axon/ingest_cmd.go` (tui.Spin)
- Modify: `cmd/axon/status_cmd.go`, `cmd/axon/automations_cmd.go`, `cmd/axon/health_cmd.go`, `cmd/axon/search_cmd.go` (tui.Table / styled list)
- Test: existing cmd tests (buffers → plain, all green); no new unit surface beyond what Task 3 covered

Each command follows one mechanical recipe — shown once, applied to each file:

```go
out := cmd.OutOrStdout()
if !asJSON && tui.Interactive(out) {
	return tui.Spin(out, "reindexing vault…", func() (string, error) {
		res, err := core.Reindex(...)            // the SAME core call the plain path makes
		if err != nil { return "", err }
		return fmt.Sprintf("%d notes, %d links…", res.Notes, res.Links), nil
	})
}
// existing plain path unchanged
```

- Table commands: build the same rows the plain printer prints, pass to `tui.Table(out, headers, rows)` when interactive, else fall through to the existing printer.
- `run_cmd.go`: `tui.Spin(out, "running "+name+"…", …)` returning `printOutcome`'s summary string; on failure return the run error so exit codes are identical.

- [ ] **Step 1: Apply the recipe to all seven files** (read each first; keep every flag and JSON branch bit-identical).
- [ ] **Step 2: `go test ./cmd/axon/`** — all PASS.
- [ ] **Step 3: Manual spot-check** — `go run ./cmd/axon --config <temp> status` inside a real terminal shows the styled table; piped through `| cat` shows today's plain output.
- [ ] **Step 4: Commit** — `git add cmd/axon && git commit -m "Live spinners and styled tables for data commands"`

---

### Task 6: onboard on huh

**Files:**
- Modify: `cmd/axon/onboard_cmd.go` (`ask`/`askList` become huh-backed on TTY)
- Test: `cmd/axon/onboard_cmd_test.go` (existing non-interactive tests stay green)

- [ ] **Step 1: Read `onboard_cmd.go` fully.** The wizard already supports `--non-interactive` and `--from <file>`; those paths and their tests are the contract.
- [ ] **Step 2: Swap the interactive internals**: `gatherOnboardValues`'s TTY path replaces bufio `ask`/`askList` with `tui.Input` and a huh multi-field form (name, role, tone, focus areas, boundaries — the exact fields it collects today), keeping identical `identity.Values` semantics. Non-TTY without `--non-interactive`: keep today's behavior (bufio prompts still work over pipes for tests/scripts — do NOT route through huh, which requires a terminal).
- [ ] **Step 3: `go test ./cmd/axon/ -run TestOnboard`** — PASS.
- [ ] **Step 4: Commit** — `git add cmd/axon && git commit -m "Onboard wizard on huh forms (non-interactive contract unchanged)"`

---

### Task 7: `axon configure`

**Files:**
- Create: `cmd/axon/configure_cmd.go`
- Create: `cmd/axon/configure_embeddings.go` (the switch chain, shared by menu + subcommand)
- Modify: `cmd/axon/root.go` (register)
- Modify: `internal/core/init.go` (export `ProbeEmbeddings`)
- Test: `cmd/axon/configure_cmd_test.go`

**Interfaces:**
- Consumes: `setConfigValue` (config_cmd.go), `core.Reembed` (reembed.go), `tui.Select/Input/Confirm/Spin`, `config.AppleEmbeddingModel/AppleEmbeddingDim`.
- Produces: `core.ProbeEmbeddings(ctx, e config.EmbeddingsConfig) StepResult` (exported alias of probeEmbeddingModel); commands `axon configure` (menu), `axon configure embeddings <ollama|apple> [--model M --dim N] [--reindex]`, `axon configure models <classify|routine|synthesis> <model>`, `axon configure automations <name> <on|off>`, `axon configure limits <daily|weekly> <tokens>`.

- [ ] **Step 1: Failing tests** (`configure_cmd_test.go`; non-interactive paths, temp config via `writeTempConfig`):

```go
func TestConfigureModelsSubcommand(t *testing.T) {
	dir := t.TempDir()
	cfgPath := writeTempConfig(t, dir)
	if _, err := run(t, "configure", "models", "synthesis", "claude-opus-4-8", "--config", cfgPath); err != nil {
		t.Fatal(err)
	}
	cfg, _ := config.Load(cfgPath)
	_, p, _ := cfg.ResolveProfile("")
	if p.Models.Synthesis != "claude-opus-4-8" {
		t.Errorf("synthesis = %q", p.Models.Synthesis)
	}
}

func TestConfigureEmbeddingsSwitchPersistsAndReportsPendingReindex(t *testing.T) {
	dir := t.TempDir()
	cfgPath := writeTempConfig(t, dir)
	out, err := run(t, "configure", "embeddings", "apple", "--config", cfgPath) // no --reindex
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	cfg, _ := config.Load(cfgPath)
	_, p, _ := cfg.ResolveProfile("")
	if p.Embeddings.Provider != "apple" || p.Embeddings.Dim != config.AppleEmbeddingDim {
		t.Errorf("not persisted: %+v", p.Embeddings)
	}
	if !strings.Contains(out, "reindex") {
		t.Errorf("must announce the pending re-embed:\n%s", out)
	}
}

func TestConfigureEmbeddingsToOllamaRequiresModelAndDim(t *testing.T) {
	dir := t.TempDir()
	cfgPath := writeTempConfig(t, dir) // starts as ollama
	// switch to apple first, then back without --model/--dim → error (cannot guess)
	if _, err := run(t, "configure", "embeddings", "apple", "--config", cfgPath); err != nil {
		t.Fatal(err)
	}
	if _, err := run(t, "configure", "embeddings", "ollama", "--config", cfgPath); err == nil {
		t.Error("switching to ollama non-interactively must require --model and --dim")
	}
	if _, err := run(t, "configure", "embeddings", "ollama", "--model", "nomic-embed-text", "--dim", "768", "--config", cfgPath); err != nil {
		t.Error(err)
	}
}

func TestConfigureAutomationsToggle(t *testing.T) {
	dir := t.TempDir()
	cfgPath := writeTempConfig(t, dir)
	if _, err := run(t, "configure", "automations", "heartbeat", "off", "--config", cfgPath); err != nil {
		t.Fatal(err)
	}
	// writeTempConfig has `automations: {}` — toggling a missing key must fail
	// loudly (setConfigValue only updates existing keys), so seed it first in
	// the fixture: add `automations: { heartbeat: { enabled: true } }`.
}
```

(Adjust `writeTempConfig` to include `automations: { heartbeat: { enabled: true } }` so the toggle has a key to edit; update the fixture once, in this task.)

- [ ] **Step 2: RED** — unknown command "configure".

- [ ] **Step 3: Implement.** `configure_cmd.go`: parent cobra command; RunE with no args + TTY → menu loop:

```go
choice, err := tui.Select(out, cmd.InOrStdin(), "What do you want to change?", []tui.Option{
	{Label: "Embeddings provider", Value: "embeddings", Hint: current.Embeddings.Provider},
	{Label: "Models per operation class", Value: "models"},
	{Label: "Token budgets", Value: "limits"},
	{Label: "Automations on/off", Value: "automations"},
	{Label: "Dashboard port", Value: "dashboard"},
	{Label: "Done", Value: "done"},
})
```

Each branch collects values via `tui.Select`/`tui.Input` and calls the same helpers the subcommands use. No args + no TTY → print help (never hang). Subcommands: thin cobra wrappers over helpers that call `setConfigValue` with the right dotted keys (`models.synthesis`, `limits.daily_tokens`, `automations.<name>.enabled`, `dashboard.port`).

`configure_embeddings.go` — the chain used by both menu and subcommand:

```go
// switchEmbeddings persists the provider (+model/dim), converges it, and
// (interactively or with --reindex) runs the mandatory re-embed.
func switchEmbeddings(cmd *cobra.Command, gf *globalFlags, provider, model string, dim int, doReindex bool) error {
	out := cmd.OutOrStdout()
	if provider == "apple" {
		if model == "" { model = config.AppleEmbeddingModel }
		if dim == 0 { dim = config.AppleEmbeddingDim }
	}
	if provider == "ollama" && (model == "" || dim == 0) {
		if !tui.Interactive(out) {
			return fmt.Errorf("switching to ollama needs --model and --dim (e.g. --model nomic-embed-text --dim 768)")
		}
		model, _ = tui.Input(out, cmd.InOrStdin(), "Ollama embedding model", "nomic-embed-text", "nomic-embed-text")
		d, _ := tui.Input(out, cmd.InOrStdin(), "Vector dimension", "768", "768")
		dim, _ = strconv.Atoi(d)
	}
	for k, v := range map[string]string{
		"embeddings.provider": provider,
		"embeddings.model":    model,
		"embeddings.dim":      strconv.Itoa(dim),
	} {
		if err := setConfigValue(gf.configPath, gf.profile, k, v); err != nil {
			return fmt.Errorf("persist %s: %w", k, err)
		}
	}
	deps, err := loadProfileDeps(gf, true) // reload with the NEW config
	if err != nil { return err }
	defer deps.close()

	if err := tui.Spin(out, "converging "+provider+" embeddings…", func() (string, error) {
		res := core.ProbeEmbeddings(cmd.Context(), deps.profile.Embeddings)
		if res.Status == core.StepFailed { return "", fmt.Errorf("%s", res.Detail) }
		return res.Detail, nil
	}); err != nil { return err }

	if !doReindex && tui.Interactive(out) {
		doReindex = tui.Confirm(out, cmd.InOrStdin(),
			"Switching providers changes vector dimensions — re-embed the index now?", true)
	}
	if doReindex {
		return tui.Spin(out, "re-embedding index…", func() (string, error) {
			n, err := core.Reembed(cmd.Context(), deps.db, deps.embedder, ...) // exact signature per reembed.go
			if err != nil { return "", err }
			return fmt.Sprintf("re-embedded %d chunks via %s", n, model), nil
		})
	}
	fmt.Fprintln(out, ui.For(out).Yellow(ui.IconArrow)+" pending: run `axon reindex --embeddings` to re-vectorise the index")
	return nil
}
```

In `internal/core/init.go` add: `// ProbeEmbeddings converges + verifies the configured embeddings provider (exported for configure).` `func ProbeEmbeddings(ctx context.Context, e config.EmbeddingsConfig) StepResult { return probeEmbeddingModel(ctx, e) }` — read `core/reembed.go` first and use its real exported function/signature for the re-embed call.

- [ ] **Step 4: GREEN** — `go test ./cmd/axon/ ./internal/core/` all PASS.
- [ ] **Step 5: Commit** — `git add cmd/axon internal/core && git commit -m "Add axon configure: interactive menu + scriptable subcommands, one-flow provider switch"`

---

### Task 8: Plan-A gates + docs touch

- [ ] **Step 1:** `gofmt -l cmd internal | wc -l` → 0; `go vet ./...`; `go test ./...`; `golangci-lint run` → 0 issues.
- [ ] **Step 2:** GUIDE.md: replace any "edit config.yaml then run three commands" switching instructions with `axon configure` (section "Changing settings"); docs/04 embeddings comment gains "or simply `axon configure embeddings apple|ollama`".
- [ ] **Step 3:** Live spot-check in a real terminal: `axon init` (step view), `axon doctor` (step view), `axon status` (table), `axon configure` (menu opens, Esc exits cleanly), `axon configure embeddings apple --reindex` on a temp profile end-to-end.
- [ ] **Step 4:** Commit — `git add -A && git commit -m "Plan A gates + docs: configure is the switching surface"`

---

## Self-Review (done at plan time)

- **Spec coverage (Components 1–3):** foundation+gate (T3), ui-on-lipgloss (T2), ADR+deps (T1), every command migrated (T4 init/doctor, T5 the rest, T6 onboard), configure menu + subcommands + full switch chain (T7), docs (T8). Plain/JSON canonical rule enforced in every task's tests.
- **Placeholder scan:** T5/T6 give one exact recipe applied to named files (each step says "read the file first; keep flags and JSON branches bit-identical") — mechanical, not deferred. T7's reembed call says to read `core/reembed.go` for the real signature, with the intended call shape shown.
- **Type consistency:** `tui.StepStatus` string-kinded to cast from `core.StepStatus`; `NewSteps(out, title, plain)`, `Set(name, detail, st)`, `Finish(summary)`, `Spin(out, title, fn)`, `Confirm(out, in, prompt, defaultYes)` used identically in T3/T4/T5/T7.
