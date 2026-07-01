package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jandro-es/axon/internal/claudeassets"
	"github.com/jandro-es/axon/internal/config"
	"github.com/jandro-es/axon/internal/db"
	"github.com/jandro-es/axon/internal/identity"
	"github.com/jandro-es/axon/internal/scaffold"
	"github.com/jandro-es/axon/internal/ui"
	"github.com/jandro-es/axon/internal/vault"
)

// StepStatus is the outcome of one init step, rendered with a distinct glyph.
type StepStatus string

const (
	StepDone    StepStatus = "done"    // ✓ created/performed
	StepAlready StepStatus = "already" // ↻ already present, nothing to do
	StepWarn    StepStatus = "warn"    // ⚠ non-fatal issue / soft check failed
	StepFailed  StepStatus = "failed"  // ✗ blocking failure
)

// StepResult is one line in the init report.
type StepResult struct {
	Name   string     `json:"name"`
	Status StepStatus `json:"status"`
	Detail string     `json:"detail"`
}

// InitReport is the full, machine-readable result of an init run.
type InitReport struct {
	Profile string        `json:"profile"`
	Steps   []StepResult  `json:"steps"`
	Reindex ReindexResult `json:"reindex"`
	Changed bool          `json:"changed"`
	OK      bool          `json:"ok"`
}

// InitOptions configures an init run.
type InitOptions struct {
	Config      *config.Config
	ProfileName string
	Profile     config.Profile
	Out         io.Writer
	// ConfigPath / BinaryPath are absolute paths baked into the generated
	// Claude Code wiring (.mcp.json / settings.json). If BinaryPath is empty the
	// running executable's path is used.
	ConfigPath string
	BinaryPath string
	// CheckEmbeddingModel optionally overrides the Ollama reachability probe so
	// tests can run hermetically. If nil, a real short-timeout probe is used.
	CheckEmbeddingModel func(ctx context.Context, e config.EmbeddingsConfig) StepResult
}

// Init converges the active profile's environment: it validates inputs, runs
// prerequisite checks, creates the data dir and database, verifies the
// embedding model, scaffolds the vault, wires Claude Code (.claude/), writes
// the in-vault dashboards and builds the first index — idempotently and
// verbosely (FR-01, FR-02; docs/10 steps 1–10).
//
// No step calls Claude: init spends no tokens (the cardinal token-chokepoint
// rule is satisfied by there being no model call at all in this phase).
func Init(ctx context.Context, opts InitOptions) (InitReport, error) {
	rep := InitReport{Profile: opts.ProfileName, OK: true}
	out := opts.Out
	if out == nil {
		out = io.Discard
	}
	st := ui.For(out)
	paths := opts.Profile.Paths()

	add := func(s StepResult) {
		rep.Steps = append(rep.Steps, s)
		fmt.Fprintln(out, renderStep(st, s))
		if s.Status == StepFailed {
			rep.OK = false
		}
	}

	fmt.Fprintf(out, "%s\n", st.Header(ui.IconWrench, fmt.Sprintf("axon init — profile %q", opts.ProfileName)))
	fmt.Fprintln(out, st.Divider(60))

	// Step 1 — Resolve profile & config (already loaded/validated by caller).
	add(StepResult{"config", StepDone, configSummary(opts.Profile, paths)})

	// Step 2 — Prerequisite checks (shared with `axon doctor`).
	for _, c := range Doctor(opts.Config, opts.ProfileName).Checks {
		add(StepResult{"check:" + c.Name, stepStatusFromCheck(c.Status), c.Detail})
	}

	// Step 3 — Data dir.
	add(dataDirStep(paths, opts.Profile))

	// Step 4 — Database (create/upgrade, run migrations).
	dbStep, sqlDB := databaseStep(paths)
	add(dbStep)
	if sqlDB == nil {
		rep.OK = false
		finish(out, st, rep)
		return rep, fmt.Errorf("init: database step failed")
	}
	defer sqlDB.Close()

	// Step 5 — Embedding model (soft check; real pull/dim-assert lands in Phase 2).
	check := opts.CheckEmbeddingModel
	if check == nil {
		check = probeEmbeddingModel
	}
	add(check(ctx, opts.Profile.Embeddings))

	// Step 6 — Vault scaffold (only where missing; never clobbers).
	scaffoldStep, vfs := vaultScaffoldStep(paths)
	add(scaffoldStep)
	if vfs == nil {
		rep.OK = false
		finish(out, st, rep)
		return rep, fmt.Errorf("init: vault scaffold failed")
	}

	// Step 7 — Claude Code wiring (.claude/: CLAUDE.md, .mcp.json, settings.json,
	// plugin skills + subagents). Profile-scoped; non-destructive.
	add(claudeWiringStep(opts, paths))

	// Step 8 — In-vault Dataview dashboards (.axon/dashboards/).
	add(dashboardsStep(vfs))

	// Step 9 — First index (build link graph from the vault; no Claude cost).
	idx, err := Reindex(ctx, vfs, sqlDB)
	if err != nil {
		add(StepResult{"index", StepFailed, err.Error()})
		rep.OK = false
		finish(out, st, rep)
		return rep, err
	}
	rep.Reindex = idx
	add(StepResult{"index", StepDone, fmt.Sprintf("%d notes, %d links (%d unresolved wikilinks)", idx.Notes, idx.Links, idx.BrokenWikilink)})

	// Personal identity layer (Component 12): created by `axon onboard`, not init,
	// so the layer reflects a real interview rather than placeholders. Surface the
	// hint when it's absent; never block (S8 — the system is useful without it).
	if !identity.Present(vfs) {
		add(StepResult{"profile", StepWarn, "no personal profile yet — run `axon onboard` to teach AXON who you are"})
	} else {
		add(StepResult{"profile", StepAlready, "personal identity layer present (" + identity.Dir + "/)"})
	}

	// Step 10 — Summary.
	rep.Changed = anyChanged(rep.Steps)
	finish(out, st, rep)
	return rep, nil
}

// configSummary renders a secret-free one-line summary of the resolved profile.
func configSummary(p config.Profile, paths config.ResolvedPaths) string {
	tok := "none"
	if p.Claude.OAuthToken != "" {
		// Show only the reference, never a resolved secret value.
		tok = p.Claude.OAuthToken
	}
	return fmt.Sprintf("auth=%s vault=%s data=%s oauth=%s", p.Claude.AuthMode, paths.VaultPath, paths.DataDir, tok)
}

func dataDirStep(paths config.ResolvedPaths, p config.Profile) StepResult {
	wanted := []string{
		paths.DataDir,
		paths.LogsDir,
		filepath.Join(paths.DataDir, "exports"),
		filepath.Join(paths.DataDir, "snapshots"),
	}
	if paths.ConfigDir != "" {
		wanted = append(wanted, paths.ConfigDir)
	}
	createdAny := false
	for _, d := range wanted {
		if _, err := os.Stat(d); err == nil {
			continue
		}
		if err := os.MkdirAll(d, 0o755); err != nil {
			return StepResult{"data-dir", StepFailed, fmt.Sprintf("create %s: %v", d, err)}
		}
		createdAny = true
	}
	if createdAny {
		return StepResult{"data-dir", StepDone, "created " + paths.DataDir}
	}
	return StepResult{"data-dir", StepAlready, paths.DataDir}
}

func databaseStep(paths config.ResolvedPaths) (StepResult, *sql.DB) {
	existed := false
	if _, err := os.Stat(paths.DBPath); err == nil {
		existed = true
	}
	sqlDB, err := db.Open(paths.DBPath)
	if err != nil {
		return StepResult{"database", StepFailed, err.Error()}, nil
	}
	version, err := db.Migrate(sqlDB)
	if err != nil {
		_ = sqlDB.Close()
		return StepResult{"database", StepFailed, err.Error()}, nil
	}
	status := StepDone
	detail := fmt.Sprintf("created %s (schema v%d)", paths.DBPath, version)
	if existed {
		status = StepAlready
		detail = fmt.Sprintf("%s (schema v%d)", paths.DBPath, version)
	}
	return StepResult{"database", status, detail}, sqlDB
}

// dashboardsStep generates the in-vault Dataview dashboards (init step 8).
func dashboardsStep(vfs *vault.FS) StepResult {
	res, err := scaffold.Dashboards(vfs)
	if err != nil {
		return StepResult{"dashboards", StepFailed, err.Error()}
	}
	if len(res.CreatedFiles) > 0 {
		return StepResult{"dashboards", StepDone, fmt.Sprintf("wrote %d Dataview dashboard(s)", len(res.CreatedFiles))}
	}
	return StepResult{"dashboards", StepAlready, "in-vault dashboards present"}
}

// claudeWiringStep generates the .claude/ integration (init step 7).
func claudeWiringStep(opts InitOptions, paths config.ResolvedPaths) StepResult {
	binary := opts.BinaryPath
	if binary == "" {
		if exe, err := os.Executable(); err == nil {
			binary = exe
		} else {
			binary = "axon"
		}
	}
	res, err := claudeassets.Generate(vault.NewFS(paths.VaultPath), claudeassets.Params{
		Profile:    opts.ProfileName,
		Binary:     binary,
		ConfigPath: opts.ConfigPath,
		ConfigDir:  paths.ConfigDir,
		AxonHome:   config.AxonHome(),
	})
	if err != nil {
		return StepResult{"claude-wiring", StepFailed, err.Error()}
	}
	if res.Changed() {
		return StepResult{"claude-wiring", StepDone, fmt.Sprintf("wrote %d .claude file(s)", len(res.Created))}
	}
	return StepResult{"claude-wiring", StepAlready, ".claude integration present"}
}

func vaultScaffoldStep(paths config.ResolvedPaths) (StepResult, *vault.FS) {
	vfs := vault.NewFS(paths.VaultPath)
	res, err := scaffold.Apply(vfs)
	if err != nil {
		return StepResult{"scaffold", StepFailed, err.Error()}, nil
	}
	if res.Changed() {
		return StepResult{"scaffold", StepDone,
			fmt.Sprintf("created %d dir(s), %d file(s) in %s", len(res.CreatedDirs), len(res.CreatedFiles), paths.VaultPath)}, vfs
	}
	return StepResult{"scaffold", StepAlready, "vault layout present, nothing added"}, vfs
}

// probeEmbeddingModel does a short, non-fatal reachability check against Ollama.
// In Phase 1 embeddings are not yet used, so any failure is a warning: it never
// blocks init. The real pull + dimension assertion lands with the Ollama
// provider in Phase 2.
func probeEmbeddingModel(ctx context.Context, e config.EmbeddingsConfig) StepResult {
	if e.Provider != "ollama" {
		return StepResult{"embeddings", StepWarn, fmt.Sprintf("provider %q not checked in Phase 1", e.Provider)}
	}
	host := e.Host
	if host == "" {
		host = "http://localhost:11434"
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(host, "/")+"/api/tags", nil)
	if err != nil {
		return StepResult{"embeddings", StepWarn, err.Error()}
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return StepResult{"embeddings", StepWarn, fmt.Sprintf("Ollama not reachable at %s — start it with `ollama serve` (not required until Phase 2)", host)}
	}
	defer resp.Body.Close()
	var payload struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return StepResult{"embeddings", StepWarn, "could not read Ollama model list"}
	}
	for _, m := range payload.Models {
		if m.Name == e.Model || strings.HasPrefix(m.Name, e.Model+":") {
			return StepResult{"embeddings", StepDone, fmt.Sprintf("model %q present (dim assertion deferred to Phase 2)", e.Model)}
		}
	}
	return StepResult{"embeddings", StepWarn, fmt.Sprintf("model %q not pulled — run `ollama pull %s` (needed in Phase 2)", e.Model, e.Model)}
}

func anyChanged(steps []StepResult) bool {
	for _, s := range steps {
		switch s.Name {
		case "data-dir", "database", "scaffold", "claude-wiring", "dashboards":
			if s.Status == StepDone {
				return true
			}
		}
	}
	return false
}

func finish(out io.Writer, st ui.Styler, rep InitReport) {
	fmt.Fprintln(out, st.Divider(60))
	switch {
	case !rep.OK:
		fmt.Fprintln(out, st.Red(ui.IconError+" init failed — see the "+ui.IconError+" step(s) above"))
	case rep.Changed:
		fmt.Fprintln(out, st.Green(ui.IconSpark+" init complete — environment converged"))
	default:
		fmt.Fprintln(out, st.Green(ui.IconOK+" init complete — no changes, already converged"))
	}
	fmt.Fprintf(out, "%s %s\n", st.Cyan(ui.IconArrow),
		st.Dim("next: `axon start` to run the scheduler + dashboard; open Claude Code in the vault for interactive use"))
}

// renderStep formats one init step as a coloured, aligned line. The status glyph
// is coloured and the name is left-padded to a fixed width — the padding is
// applied before colouring so ANSI bytes never throw the columns off.
func renderStep(st ui.Styler, s StepResult) string {
	name := fmt.Sprintf("%-22s", s.Name)
	detail := s.Detail
	switch s.Status {
	case StepFailed:
		detail = st.Red(detail)
	case StepWarn:
		detail = st.Yellow(detail)
	}
	return fmt.Sprintf("  %s %s %s", glyphFor(st, s.Status), name, detail)
}

func glyphFor(st ui.Styler, s StepStatus) string {
	switch s {
	case StepDone:
		return st.Green(ui.IconOK)
	case StepAlready:
		return st.Cyan(ui.IconAlready)
	case StepWarn:
		return st.Yellow(ui.IconWarn)
	default:
		return st.Red(ui.IconError)
	}
}

func stepStatusFromCheck(c CheckStatus) StepStatus {
	switch c {
	case StatusOK:
		return StepAlready
	case StatusWarn:
		return StepWarn
	default:
		return StepFailed
	}
}
