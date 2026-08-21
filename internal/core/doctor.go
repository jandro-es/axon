// Package core composes the leaf packages into the daemon. In Phase 0 it
// provides the doctor health checks; the scheduler, automations, ingestion and
// token manager are wired in here in later phases.
package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/jandro-es/axon/internal/clients"
	"github.com/jandro-es/axon/internal/config"
	"github.com/jandro-es/axon/internal/db"
	"github.com/jandro-es/axon/internal/embeddings"
	"github.com/jandro-es/axon/internal/identity"
	"github.com/jandro-es/axon/internal/service"
	"github.com/jandro-es/axon/internal/vault"
)

// CheckStatus is the outcome of a single doctor check.
type CheckStatus string

const (
	StatusOK   CheckStatus = "ok"
	StatusWarn CheckStatus = "warn"
	StatusFail CheckStatus = "fail"
)

// Check is one diagnostic line in the doctor report.
type Check struct {
	Name   string
	Status CheckStatus
	Detail string
	// Fix is the concrete thing to do about a warn/fail — usually a command to
	// run. Detail says what is wrong; Fix says what to do, so a GUI can render
	// it as a copyable command rather than making the user parse prose. Empty
	// when the check passes, or when there is nothing generic to suggest.
	Fix string
}

// DoctorReport is the full set of checks plus a derived overall verdict.
type DoctorReport struct {
	Checks []Check
}

// HasFailure reports whether any check failed (warnings do not count).
func (r DoctorReport) HasFailure() bool {
	for _, c := range r.Checks {
		if c.Status == StatusFail {
			return true
		}
	}
	return false
}

// lookPath is indirected so tests can stub external-binary discovery.
var lookPath = exec.LookPath

// lookupEnv is indirected so tests can control environment inspection.
var lookupEnv = os.LookupEnv

// Doctor runs the Phase 0 prerequisite checks. cfg may be nil (e.g. when the
// config failed to load); the relevant checks degrade to warnings/failures
// rather than panicking. activeProfile is the resolved profile name, used to
// pick the profile whose auth_mode governs the ANTHROPIC_API_KEY check.
//
// extras are caller-supplied checks appended after the built-ins, in order.
// They exist because two checks cannot live here: `update-available` needs the
// build version (a main-package linker variable), and `recipes` lives in
// internal/automations, which imports core — so core importing it back would
// be an import cycle. Routing them through this parameter keeps ONE assembly
// path, so the CLI and the daemon cannot disagree about the report (FR-206).
func Doctor(cfg *config.Config, activeProfile string, extras ...Check) DoctorReport {
	var checks []Check

	// 1. Config presence/validity.
	if cfg == nil {
		checks = append(checks, Check{
			Name:   "config",
			Status: StatusFail,
			Detail: "config not loaded or invalid (run `axon config validate`)",
		})
	} else {
		checks = append(checks, Check{
			Name:   "config",
			Status: StatusOK,
			Detail: fmt.Sprintf("valid; active profile %q", activeProfile),
		})
	}

	// 2. Stray ANTHROPIC_API_KEY for subscription/enterprise modes — the
	// explicit Phase 0 gate. Claude Code would prioritise the key and bill the
	// API account, diverting off the subscription.
	checks = append(checks, apiKeyCheck(cfg, activeProfile))

	// 3. claude CLI presence (informational — the default execution path).
	checks = append(checks, binaryCheck("claude-cli", "claude",
		"Claude Code CLI found", "claude CLI not found on PATH (needed for automations + interactive use)"))

	// 3b. If an OS service unit supervises the daemon, its PATH must resolve
	// claude too — launchd/systemd default to a minimal system PATH, so a
	// shell-visible claude can still be invisible to the supervised daemon.
	// The dashboard endpoint lets the check ask a running daemon directly
	// rather than trusting the unit file to describe the loaded job.
	var dashHost string
	var dashPort int
	if cfg != nil {
		if p, ok := cfg.Profiles[activeProfile]; ok {
			dashHost, dashPort = p.Dashboard.Host, p.Dashboard.Port
		}
	}
	checks = append(checks, serviceUnitCheck(activeProfile, dashHost, dashPort))

	// 4. Embeddings provider prerequisite (informational — local embeddings):
	// the ollama binary, or the compiled Apple helper, per the profile's config.
	// Without a resolvable profile, fall back to the generic ollama check.
	embChecked := false
	if cfg != nil {
		if p, ok := cfg.Profiles[activeProfile]; ok {
			checks = append(checks, embeddingsCheck(p))
			// 4b. Locally-routed model tiers (ADR-015), only when configured.
			checks = append(checks, localModelsCheck(p)...)
			// 4c. OCR provider prerequisite, only when ingestion.ocr is enabled.
			if p.Ingestion.OCRMode() != "off" {
				checks = append(checks, ocrCheck(p))
			}
			// 4c'. Local vision provider (ADR-035) + media caption tooling (advisory).
			checks = append(checks, visionCheck(p))
			checks = append(checks, mediaCheck(p))
			// 4c''. macOS 27 fm CLI posture (FR-191/FR-195, advisory; macOS only).
			if runtime.GOOS == "darwin" {
				checks = append(checks, fmCheck(p))
			}
			checks = append(checks, researchCheck(p))
			// 4d. Local reranker prerequisite, only when retrieval.rerank is set.
			if p.Retrieval.RerankMode() != "off" {
				checks = append(checks, rerankCheck(p))
			}
			// 4e. Verification cascade prerequisite, only when models.verify is set.
			if p.Models.VerifyMode() != "off" {
				checks = append(checks, verifyCheck(p))
			}
			// 4f. R9 resurfacer schedule + contradiction path (advisory).
			checks = append(checks, resurfaceCheck(p))
			// 4g. R7 near-duplicate merge-proposals sweep (advisory).
			checks = append(checks, mergeCheck(p))
			checks = append(checks, actionsReviewCheck(p))
			checks = append(checks, actionExtractCheck(p))
			embChecked = true
		}
	}
	if !embChecked {
		checks = append(checks, binaryCheck("ollama", "ollama",
			"Ollama found", "ollama not found on PATH (needed for local embeddings in Phase 2)"))
	}

	// 5–7. Profile-scoped prerequisites (FR-05): vault writable, dashboard port
	// free, and the data-residency posture.
	if cfg != nil {
		if p, ok := cfg.Profiles[activeProfile]; ok {
			paths := p.Paths()
			checks = append(checks, claudeAuthCheck(p, paths))
			checks = append(checks, vaultWritableCheck(paths.VaultPath))
			checks = append(checks, portFreeCheck(p.Dashboard.Host, p.Dashboard.Port))
			checks = append(checks, residencyCheck(p))
			checks = append(checks, annIndexCheck(p, paths))
			checks = append(checks, relatedCheck(p, paths))
			checks = append(checks, memoryFactsCheck(paths))
			checks = append(checks, actionsCheck(paths))
			checks = append(checks, localModelsVettingChecks(paths, p)...)
			// 8–9. Multi-client wiring (FR-75): is the AXON MCP server registered
			// with each Claude client, and is each client's guarantee honest.
			checks = append(checks, claudeCodeWiringCheck(paths.VaultPath))
			checks = append(checks, claudeDesktopCheck(activeProfile))
			checks = append(checks, interopCheck(p))
		}
	}

	checks = append(checks, extras...)
	return DoctorReport{Checks: checks}
}

// serviceUnitCheck locates this profile's OS service unit (if the platform has
// one) and delegates to serviceUnitPathCheck.
func serviceUnitCheck(activeProfile, dashHost string, dashPort int) Check {
	home, err := os.UserHomeDir()
	if err != nil {
		return Check{Name: "service-path", Status: StatusOK, Detail: "cannot resolve home dir — service unit check skipped"}
	}
	unit, err := service.ForOS("", service.Params{Profile: activeProfile, HomeDir: home})
	if err != nil {
		return Check{Name: "service-path", Status: StatusOK, Detail: "no OS service units on this platform"}
	}
	return serviceUnitPathCheck(unit.Kind, unit.Path, unit.ReloadCmd, dashHost, dashPort)
}

// serviceUnitPathCheck warns when the daemon cannot resolve the claude CLI.
// launchd/systemd start daemons with a minimal system PATH, so a claude the
// user's login shell finds can still be invisible to the supervised daemon,
// which then fails every automation with `exec: "claude": executable file not
// found`. Advisory: absent unit = daemon not OS-supervised = nothing to check.
//
// The unit file on disk is the WEAKER signal, and on its own it produced a
// false green: both supervisors keep the definition they parsed at load time,
// so a unit corrected on disk can sit next to a loaded job still handing the
// daemon the old PATH. When the daemon is up we therefore ask the process
// itself — it is the only party that knows the PATH it actually got — and fall
// back to the file only when there is nothing running to ask.
func serviceUnitPathCheck(kind, unitPath, reloadCmd, dashHost string, dashPort int) Check {
	const name = "service-path"
	content, err := os.ReadFile(unitPath)
	if err != nil {
		if os.IsNotExist(err) {
			return Check{Name: name, Status: StatusOK, Detail: "no OS service unit installed — daemon runs manually with your shell's PATH"}
		}
		return Check{Name: name, Status: StatusWarn, Detail: fmt.Sprintf("cannot read service unit %s: %v", unitPath, err)}
	}
	pathEnv := service.UnitPathEnv(kind, string(content))

	// Ground truth, when it is available.
	if live, known := daemonClaudePath(dashHost, dashPort); known {
		if live != "" {
			return Check{Name: name, Status: StatusOK, Detail: "running daemon resolves claude (" + live + ")"}
		}
		detail := "the running daemon cannot resolve claude — every automation it runs will fail with `exec: \"claude\": executable file not found`"
		if pathEnv != "" && findExecutable("claude", pathEnv) != "" {
			detail += fmt.Sprintf("; %s already carries a PATH that would resolve it, so the loaded job is stale and needs a reload (not just a restart)", unitPath)
		}
		return Check{Name: name, Status: StatusWarn, Detail: detail, Fix: serviceReinstallFix(reloadCmd)}
	}

	if pathEnv == "" {
		return Check{Name: name, Status: StatusWarn,
			Detail: fmt.Sprintf("service unit %s sets no PATH — the supervised daemon cannot find claude outside system dirs", unitPath),
			Fix:    serviceReinstallFix(reloadCmd)}
	}
	if dir := findExecutable("claude", pathEnv); dir != "" {
		return Check{Name: name, Status: StatusOK, Detail: "service unit PATH resolves claude (" + dir + ")"}
	}
	return Check{Name: name, Status: StatusWarn,
		Detail: fmt.Sprintf("service unit PATH cannot resolve claude — automations run under it will fail (unit: %s)", unitPath),
		Fix:    serviceReinstallFix(reloadCmd)}
}

// serviceReinstallFix is the command that regenerates a service unit with a
// working PATH and reloads it — the fix for every service-path warning.
//
// The reload half comes from internal/service, which generates the unit and so
// is the one place that should know how to get it re-read. That matters more
// than it sounds: the fix must RELOAD, not merely restart, because both
// supervisors keep the job definition they parsed at load time. `launchctl
// kickstart -k` and a bare `systemctl --user restart` re-run the daemon under
// the OLD environment, so the previous version of this function emitted a
// command that rewrote a correct unit file and changed nothing.
func serviceReinstallFix(reloadCmd string) string {
	if reloadCmd == "" {
		return "axon service install"
	}
	return "axon service install && " + reloadCmd
}

// findExecutable reports the directory of pathEnv that holds an executable
// file named name, or "" — exec.LookPath against an explicit PATH string.
func findExecutable(name, pathEnv string) string {
	for _, dir := range filepath.SplitList(pathEnv) {
		if dir == "" {
			continue
		}
		st, err := os.Stat(filepath.Join(dir, name))
		if err == nil && st.Mode().IsRegular() && st.Mode()&0o111 != 0 {
			return dir
		}
	}
	return ""
}

// embeddingsCheck verifies the configured embeddings provider's local
// prerequisite: the ollama binary, or the compiled Apple helper.
func embeddingsCheck(p config.Profile) Check {
	if p.Embeddings.Provider == "apple" {
		const name = "apple-embeddings"
		helper := p.Embeddings.Helper
		if helper == "" {
			helper = config.DefaultAppleHelperPath()
		}
		st, err := os.Stat(helper)
		if err != nil || st.Mode()&0o111 == 0 {
			return Check{Name: name, Status: StatusWarn, Detail: fmt.Sprintf("Apple embeddings helper not built at %s — run `axon init` (requires Xcode CLT)", helper), Fix: "axon init"}
		}
		return Check{Name: name, Status: StatusOK, Detail: "Apple embeddings helper present: " + helper}
	}
	return binaryCheck("ollama", "ollama",
		"Ollama found", "ollama not found on PATH (needed for local embeddings in Phase 2)")
}

// ocrCheck verifies the configured OCR provider's local prerequisite: the
// compiled Apple helper, or the pdftoppm+tesseract binaries. Read-only and
// tolerant — a missing prerequisite warns, never fails doctor.
func ocrCheck(p config.Profile) Check {
	const name = "ocr"
	switch p.Ingestion.OCRMode() {
	case "apple":
		helper := p.Ingestion.OCRHelper
		if helper == "" {
			helper = config.DefaultOCRHelperPath()
		}
		st, err := os.Stat(helper)
		if err != nil || st.Mode()&0o111 == 0 {
			return Check{Name: name, Status: StatusWarn, Detail: fmt.Sprintf("Apple OCR helper not built at %s — run `axon init` (requires Xcode CLT)", helper), Fix: "axon init"}
		}
		return Check{Name: name, Status: StatusOK, Detail: "Apple OCR helper present: " + helper}
	case "tesseract":
		var missing []string
		for _, bin := range []string{"pdftoppm", "tesseract"} {
			if _, err := exec.LookPath(bin); err != nil {
				missing = append(missing, bin)
			}
		}
		if len(missing) > 0 {
			return Check{Name: name, Status: StatusWarn, Detail: "OCR (tesseract) needs on PATH: " + strings.Join(missing, ", ") + " — install poppler + tesseract", Fix: installHint("tesseract")}
		}
		return Check{Name: name, Status: StatusOK, Detail: "tesseract OCR binaries present (pdftoppm, tesseract)"}
	default:
		return Check{Name: name, Status: StatusOK, Detail: "OCR off"}
	}
}

// visionCheck verifies the configured local vision provider (ADR-035). Advisory
// and tolerant — a missing prerequisite warns (images fall back to OCR-only),
// never fails doctor. Mirrors rerankCheck.
func visionCheck(p config.Profile) Check {
	const name = "vision"
	mode := p.Ingestion.VisionMode()
	switch {
	case mode == "off":
		return Check{Name: name, Status: StatusOK, Detail: "vision off"}
	case mode == "apple" || mode == "apple:pcc":
		return visionCheckApple(mode, detectFM(context.Background()))
	case strings.HasPrefix(mode, "ollama:"):
		model := strings.TrimPrefix(mode, "ollama:")
		host := p.Embeddings.Host
		if host == "" {
			host = embeddings.DefaultOllamaHost
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if !ollamaReachable(ctx, host) {
			return Check{Name: name, Status: StatusWarn, Detail: fmt.Sprintf("vision Ollama not reachable at %s — start `ollama serve` (images fall back to OCR-only)", host), Fix: "ollama serve"}
		}
		if !ollamaModelPresent(ctx, host, model) {
			return Check{Name: name, Status: StatusWarn, Detail: fmt.Sprintf("vision model %q not pulled — run `ollama pull %s`", model, model), Fix: fmt.Sprintf("ollama pull %s", model)}
		}
		return Check{Name: name, Status: StatusOK, Detail: "vision ready: " + mode}
	default:
		return Check{Name: name, Status: StatusWarn, Detail: fmt.Sprintf("ingestion.vision %q not recognised — use off, ollama:<model>, or apple", mode)}
	}
}

// mediaCheck reports whether yt-dlp is available for media caption ingestion.
// Advisory — absent yt-dlp means media URLs are captured and flagged, not
// ingested; it never fails doctor.
func mediaCheck(_ config.Profile) Check {
	const name = "media"
	if _, err := lookPath("yt-dlp"); err != nil {
		return Check{Name: name, Status: StatusWarn, Detail: "yt-dlp not found on PATH — media URLs will be captured and flagged (install yt-dlp for transcript ingestion)", Fix: installHint("yt-dlp")}
	}
	return Check{Name: name, Status: StatusOK, Detail: "media caption ingestion ready (yt-dlp present)"}
}

// rerankCheck verifies the configured local reranker's prerequisite: a
// reachable Ollama server with the model pulled. Read-only and tolerant — a
// missing prerequisite or malformed value warns (rerank silently falls back to
// the fused order), never fails doctor.
func rerankCheck(p config.Profile) Check {
	const name = "rerank"
	mode := p.Retrieval.RerankMode()
	if !strings.HasPrefix(mode, "ollama:") {
		return Check{Name: name, Status: StatusWarn, Detail: fmt.Sprintf("retrieval.rerank %q not recognised — use off or ollama:<model>", mode)}
	}
	model := strings.TrimPrefix(mode, "ollama:")
	host := p.Embeddings.Host
	if host == "" {
		host = embeddings.DefaultOllamaHost
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if !ollamaReachable(ctx, host) {
		return Check{Name: name, Status: StatusWarn, Detail: fmt.Sprintf("reranker Ollama not reachable at %s — start `ollama serve` (rerank falls back to fused order)", host), Fix: "ollama serve"}
	}
	if !ollamaModelPresent(ctx, host, model) {
		return Check{Name: name, Status: StatusWarn, Detail: fmt.Sprintf("reranker model %q not pulled — run `ollama pull %s`", model, model), Fix: fmt.Sprintf("ollama pull %s", model)}
	}
	return Check{Name: name, Status: StatusOK, Detail: "reranker ready: " + mode}
}

// verifyCheck reports the R5.3 verification cascade's prerequisites (FR-145):
// verify must name a local ollama model, the routine tier must itself be local
// (else verification never triggers), and the judge model must be pulled.
// Warn-only, mirroring rerankCheck — a broken verifier just keeps local answers.
func verifyCheck(p config.Profile) Check {
	const name = "verify"
	mode := p.Models.VerifyMode()
	if !strings.HasPrefix(mode, "ollama:") {
		return Check{Name: name, Status: StatusWarn, Detail: fmt.Sprintf("models.verify %q not recognised — use off or ollama:<model>", mode)}
	}
	if config.ParseModelRef(p.Models.Routine).Provider != config.ProviderOllama {
		return Check{Name: name, Status: StatusWarn, Detail: "models.verify is set but the routine tier is not local — verification never triggers"}
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
		return Check{Name: name, Status: StatusWarn, Detail: fmt.Sprintf("verify Ollama not reachable at %s — start `ollama serve` (routine answers stay local, unverified)", host), Fix: "ollama serve"}
	}
	if !ollamaModelPresent(ctx, host, model) {
		return Check{Name: name, Status: StatusWarn, Detail: fmt.Sprintf("verify model %q not pulled — run `ollama pull %s`", model, model), Fix: fmt.Sprintf("ollama pull %s", model)}
	}
	return Check{Name: name, Status: StatusOK, Detail: fmt.Sprintf("verify ready: %s, floor %d/10", mode, p.Models.VerifyMinScoreOr())}
}

// resurfaceCheck reports the R9 resurfacer's spaced-repetition + contradiction
// configuration. Advisory (always StatusOK) — mirrors rerankCheck's tone; the
// resurfacer works zero-model by default and the contradiction path is opt-in.
func resurfaceCheck(p config.Profile) Check {
	const name = "resurface"
	weeks := p.Resurfacing.IntervalsWeeksOr()
	auto, ok := p.Automations["resurfacer"]
	active := ok && auto.BudgetTokens > 0 && p.Resurfacing.ContradictionMaxChecksOr() > 0
	state := "contradiction path off (zero-model resurfacing; set resurfacer.budget_tokens to enable)"
	if active {
		state = fmt.Sprintf("contradiction path active (routine tier, ≤%d checks/run)", p.Resurfacing.ContradictionMaxChecksOr())
	}
	return Check{Name: name, Status: StatusOK, Detail: fmt.Sprintf("resurfacer ladder %v weeks; %s", weeks, state)}
}

// researchCheck reports the deep-research automation posture (1.3 H2). Advisory
// and tolerant — deep research is off by default and personal-first; fetches
// obey the existing ingest allow-list. Never fails doctor.
func researchCheck(p config.Profile) Check {
	const name = "research"
	if !p.Research.Enabled {
		return Check{Name: name, Status: StatusOK, Detail: "deep research off (set research.enabled on the personal profile to opt in)"}
	}
	return Check{Name: name, Status: StatusOK, Detail: fmt.Sprintf("deep research on — %d fetch(es) / %d token(s) per run; fetches obey the ingest allow-list",
		p.Research.MaxFetchesOr(), p.Research.BudgetTokensOr())}
}

// actionExtractCheck reports the T6 implicit action extractor. Advisory (always
// StatusOK): routine-tier, off by default, chokepoint-gated, proposes to the
// review queue only.
func actionExtractCheck(p config.Profile) Check {
	const name = "action-extract"
	auto, ok := p.Automations["action-extract"]
	if !ok || !auto.Enabled {
		return Check{Name: name, Status: StatusOK, Detail: "action-extract off (opt-in model extraction of implicit action items)"}
	}
	return Check{Name: name, Status: StatusOK, Detail: "action-extract active (routine tier, local-routable; extracts commitments → review queue → axon:tasks)"}
}

// actionsReviewCheck reports the T5 stale-action sweep. Advisory (always
// StatusOK): zero-model, off by default; accept demotes to #someday (never deletes).
func actionsReviewCheck(p config.Profile) Check {
	const name = "actions-review"
	auto, ok := p.Automations["actions-review"]
	if !ok || !auto.Enabled {
		return Check{Name: name, Status: StatusOK, Detail: "actions-review off (stale-action sweep; enable to nudge forgotten tasks)"}
	}
	return Check{Name: name, Status: StatusOK, Detail: fmt.Sprintf("actions-review active (open undated actions in notes untouched > %dd → review queue; accept → #someday)", p.Actions.StaleAfterDaysOr())}
}

// mergeCheck reports the R7 near-duplicate merge-proposals sweep. Advisory
// (always StatusOK): the sweep is zero-model and disabled by default; accept is
// wikilink-safe and never deletes.
func mergeCheck(p config.Profile) Check {
	const name = "merge"
	auto, ok := p.Automations["merge-proposals"]
	if !ok || !auto.Enabled {
		return Check{Name: name, Status: StatusOK, Detail: "merge-proposals off (near-duplicate sweep; enable in automations to propose merges)"}
	}
	return Check{Name: name, Status: StatusOK, Detail: fmt.Sprintf("merge-proposals active (cosine ≥ %.2f, ≤%d proposals/run; accept archives to .trash, never deletes)",
		p.Merge.ThresholdOr(), p.Merge.MaxProposalsOr())}
}

// vettingCheck renders the eval-promotion status for one gated local tier
// (FR-143). Pure: the caller supplies the persisted row and live digest so it is
// unit-testable without Ollama.
func vettingCheck(name, tier, ref string, minPass int, row db.EvalRun, haveRow bool, curDigest string, digestKnown bool) Check {
	switch {
	case minPass == 0:
		return Check{Name: name, Status: StatusWarn, Detail: fmt.Sprintf("local tier %s is ungated — set models.eval_min_pass to require evals", ref)}
	case !haveRow:
		return Check{Name: name, Status: StatusWarn, Detail: fmt.Sprintf("%s not vetted — run `axon eval --family %s --model %s`", ref, tier, ref)}
	case row.PassPct < minPass:
		return Check{Name: name, Status: StatusWarn, Detail: fmt.Sprintf("%s scored %d%% below %d%% — routes to Claude until it passes", ref, row.PassPct, minPass)}
	case digestKnown && row.Digest != "" && curDigest != "" && row.Digest != curDigest:
		return Check{Name: name, Status: StatusWarn, Detail: fmt.Sprintf("%s changed since eval (%s → %s) — re-run `axon eval`", ref, short(row.Digest), short(curDigest))}
	default:
		return Check{Name: name, Status: StatusOK, Detail: fmt.Sprintf("%s vetted %d%%", ref, row.PassPct)}
	}
}

func short(d string) string {
	if len(d) > 12 {
		return d[:12]
	}
	return d
}

// localModelsVettingChecks emits one vettingCheck per gated local classify/
// routine tier (FR-143). It opens the profile DB read-only (like
// memoryFactsCheck) and fetches the live Ollama digest for drift detection —
// out of the token hot path.
func localModelsVettingChecks(paths config.ResolvedPaths, p config.Profile) []Check {
	m := p.Models
	ctx := context.Background()
	var d *sql.DB
	if _, err := os.Stat(paths.DBPath); err == nil {
		if dd, oerr := sql.Open("sqlite", paths.DBPath); oerr == nil {
			d = dd
			defer func() { _ = d.Close() }()
		}
	}
	var out []Check
	for _, t := range []struct{ tier, ref string }{{"classify", m.Classify}, {"routine", m.Routine}} {
		r := config.ParseModelRef(t.ref)
		if r.Provider == config.ProviderClaude {
			continue // Claude tiers are never gated
		}
		name := "eval-vetting:" + t.tier
		if m.EvalMinPass == 0 {
			out = append(out, vettingCheck(name, t.tier, t.ref, 0, db.EvalRun{}, false, "", false))
			continue
		}
		var (
			row  db.EvalRun
			have bool
		)
		if d != nil {
			row, have, _ = db.LatestEvalRun(ctx, d, t.tier, t.ref)
		}
		var (
			cur   string
			known bool
		)
		if r.Provider == config.ProviderOllama {
			cur, known = OllamaDigest(ctx, m.OllamaHost, r.Model)
		}
		out = append(out, vettingCheck(name, t.tier, t.ref, m.EvalMinPass, row, have, cur, known))
	}
	return out
}

// localModelsCheck reports the state of any locally-routed model tier
// (ADR-015): the Ollama chat host/model for "ollama:" tiers, the compiled
// Foundation Models helper for "apple". Informational (warnings only): a
// broken local provider degrades to models.local_fallback at runtime.
// Checks are stat/HTTP-based — core never imports agent (dependency rule:
// tokens is the only importer).
func localModelsCheck(p config.Profile) []Check {
	var checks []Check
	m := p.Models
	tiers := []struct{ tier, value string }{
		{"classify", m.Classify},
		{"routine", m.Routine},
	}
	for _, t := range tiers {
		ref := config.ParseModelRef(t.value)
		name := "local-model:" + t.tier
		switch ref.Provider {
		case config.ProviderOllama:
			host := m.OllamaHost
			if host == "" {
				host = "http://localhost:11434"
			}
			host = strings.TrimRight(host, "/")
			ctx := context.Background()
			if !ollamaReachable(ctx, host) {
				checks = append(checks, Check{Name: name, Status: StatusWarn, Detail: fmt.Sprintf("Ollama not reachable at %s — %s calls will use models.local_fallback (%s)", host, t.tier, m.Fallback())})
				continue
			}
			if !ollamaModelPresent(ctx, host, ref.Model) {
				checks = append(checks, Check{Name: name, Status: StatusWarn, Detail: fmt.Sprintf("model %q not pulled — run `ollama pull %s` (until then %s calls use models.local_fallback: %s)", ref.Model, ref.Model, t.tier, m.Fallback())})
				continue
			}
			checks = append(checks, Check{Name: name, Status: StatusOK, Detail: fmt.Sprintf("ollama model %q available at %s", ref.Model, host)})
		case config.ProviderApple:
			if runtime.GOOS != "darwin" {
				checks = append(checks, Check{Name: name, Status: StatusWarn, Detail: fmt.Sprintf("tier configured as apple but this machine is not a mac — calls will use models.local_fallback (%s)", m.Fallback())})
				continue
			}
			helper := m.AppleHelper
			if helper == "" {
				helper = config.DefaultAppleLMHelperPath()
			}
			if st, err := os.Stat(helper); err != nil || st.Mode()&0o111 == 0 {
				checks = append(checks, Check{Name: name, Status: StatusWarn, Detail: fmt.Sprintf("Apple Foundation Models helper not built at %s — run `axon init` or `axon configure models classify apple` (requires Xcode CLT)", helper)})
				continue
			}
			checks = append(checks, Check{Name: name, Status: StatusOK, Detail: "Apple Foundation Models helper present: " + helper})
		case config.ProviderAppleFM:
			st := DetectFM(context.Background())
			switch st.State {
			case FMStateReady:
				checks = append(checks, Check{Name: name, Status: StatusOK, Detail: fmt.Sprintf("apple:%s served by fm (%s)", ref.Model, st.Detail)})
			case FMStateLicensePending:
				checks = append(checks, Check{Name: name, Status: StatusWarn, Detail: fmt.Sprintf("tier apple:%s configured but the fm licence is not agreed — calls will use models.local_fallback (%s)", ref.Model, m.Fallback()), Fix: "sudo fm license"})
			default:
				checks = append(checks, Check{Name: name, Status: StatusWarn, Detail: fmt.Sprintf("tier apple:%s configured but fm is unavailable (%s) — calls will use models.local_fallback (%s)", ref.Model, st.Detail, m.Fallback())})
			}
		}
	}
	return checks
}

// interopCheck reports the optional external-MCP backend posture (FR-54). It is
// informational: AXON's own server is always the default vault contract.
func interopCheck(p config.Profile) Check {
	const name = "interop:obsidian-mcp"
	obs := p.Interop.ObsidianMCP
	if !obs.Configured() {
		return Check{Name: name, Status: StatusOK, Detail: "not configured (AXON's own server is the vault backend)"}
	}
	return Check{Name: name, Status: StatusOK, Detail: fmt.Sprintf("configured (%s) — registered alongside AXON by `axon mcp install`", obs.Command)}
}

// claudeCodeWiringCheck reports whether the project's Claude Code wiring exists
// (the .mcp.json that registers the AXON server). Claude Code is the
// full-featured client (hooks + skills + subagents + headless automations).
func claudeCodeWiringCheck(vaultPath string) Check {
	const name = "client:claude-code"
	if vaultPath == "" {
		return Check{Name: name, Status: StatusWarn, Detail: "no vault_path configured"}
	}
	if _, err := os.Stat(filepath.Join(vaultPath, ".claude", ".mcp.json")); err != nil {
		return Check{Name: name, Status: StatusWarn, Detail: "not wired — run `axon init` or `axon mcp install --client code`"}
	}
	return Check{Name: name, Status: StatusOK, Detail: "registered (full-featured: tools + hooks + skills + automations)"}
}

// desktopConfigPath is indirected so tests can point the Desktop check at a temp
// file. It honours AXON_DESKTOP_CONFIG, then the OS default.
var desktopConfigPath = func() (string, error) {
	if v := os.Getenv("AXON_DESKTOP_CONFIG"); v != "" {
		return v, nil
	}
	return clients.DesktopConfigPath()
}

// claudeDesktopCheck reports the AXON registration state in Claude Desktop and is
// honest about Desktop's reduced guarantees (FR-75): tools only, no hooks/skills/
// profile injection. Any resolution/read error degrades to an informational OK —
// a missing Desktop is normal, not a failure.
func claudeDesktopCheck(activeProfile string) Check {
	const name = "client:claude-desktop"
	path, err := desktopConfigPath()
	if err != nil {
		return Check{Name: name, Status: StatusOK, Detail: "Claude Desktop not detected (optional)"}
	}
	st, err := clients.DetectDesktop(path)
	if err != nil {
		return Check{Name: name, Status: StatusOK, Detail: "Claude Desktop not detected (optional)"}
	}
	switch {
	case st.Registered:
		note := "registered (tools only — no hooks/skills/profile injection; keep vault edits in AXON tools)"
		if st.Profile != "" && st.Profile != activeProfile {
			note = fmt.Sprintf("registered for profile %q, not active %q — re-run `axon mcp install --client desktop`", st.Profile, activeProfile)
			return Check{Name: name, Status: StatusWarn, Detail: note}
		}
		return Check{Name: name, Status: StatusOK, Detail: note}
	case st.Present:
		return Check{Name: name, Status: StatusWarn, Detail: "Claude Desktop present but AXON not registered — run `axon mcp install --client desktop`"}
	default:
		return Check{Name: name, Status: StatusOK, Detail: "Claude Desktop not configured (optional; `axon mcp install --client desktop`)"}
	}
}

// vaultWritableCheck confirms the vault path is writable (or createable).
func vaultWritableCheck(vaultPath string) Check {
	const name = "vault-writable"
	if vaultPath == "" {
		return Check{Name: name, Status: StatusWarn, Detail: "no vault_path configured"}
	}
	target := vaultPath
	// Walk up to the nearest existing ancestor and test writability there.
	for {
		if info, err := os.Stat(target); err == nil {
			if !info.IsDir() {
				return Check{Name: name, Status: StatusFail, Detail: fmt.Sprintf("%s exists but is not a directory", target)}
			}
			break
		}
		parent := filepath.Dir(target)
		if parent == target {
			break
		}
		target = parent
	}
	f, err := os.CreateTemp(target, ".axon-doctor-*")
	if err != nil {
		return Check{Name: name, Status: StatusFail, Detail: fmt.Sprintf("%s not writable: %v", target, err)}
	}
	_ = f.Close()
	_ = os.Remove(f.Name())
	return Check{Name: name, Status: StatusOK, Detail: "vault path writable: " + vaultPath}
}

// portFreeCheck reports whether the daemon can serve on its configured port.
//
// A busy port is only a problem when something OTHER than AXON holds it. On a
// machine where the daemon is already running — the normal, healthy state —
// warning about the port is warning about the thing the user wants, and offers
// nothing to act on. So when the port is busy, ask what is listening: an AXON
// daemon answers /health with its own profile, and that is a pass.
func portFreeCheck(host string, port int) Check {
	const name = "dashboard-port"
	if port == 0 {
		return Check{Name: name, Status: StatusWarn, Detail: "no dashboard port configured"}
	}
	if host == "" {
		host = "127.0.0.1"
	}
	addr := net.JoinHostPort(host, strconv.Itoa(port))

	ln, err := net.Listen("tcp", addr)
	if err == nil {
		_ = ln.Close()
		return Check{Name: name, Status: StatusOK, Detail: "dashboard port free: " + addr}
	}

	if profile, ok := axonServing(addr); ok {
		detail := "AXON is already serving " + addr
		if profile != "" {
			detail = fmt.Sprintf("AXON is already serving %s (profile %q)", addr, profile)
		}
		return Check{Name: name, Status: StatusOK, Detail: detail}
	}

	return Check{
		Name:   name,
		Status: StatusWarn,
		Detail: fmt.Sprintf("%s is held by something that is not AXON: %v", addr, err),
		Fix:    fmt.Sprintf("lsof -nP -iTCP:%d -sTCP:LISTEN   # then stop it, or set dashboard.port", port),
	}
}

// daemonHealth is the subset of GET /health doctor reads. ClaudePath is a
// pointer so an absent field (a daemon older than the field) stays
// distinguishable from a present-but-empty one (a daemon that genuinely cannot
// resolve claude) — the two call for opposite verdicts.
type daemonHealth struct {
	Status     string  `json:"status"`
	Profile    string  `json:"profile"`
	ClaudePath *string `json:"claude_path"`
}

// axonServing asks whatever holds addr whether it is an AXON daemon, returning
// its profile name.
func axonServing(addr string) (string, bool) {
	h, ok := fetchDaemonHealth(addr)
	if !ok {
		return "", false
	}
	return h.Profile, true
}

// daemonClaudePath reports the claude binary the daemon serving this profile's
// dashboard resolved on its OWN PATH, and whether that answer is known at all.
// Not-known covers every case where the file on disk remains the best evidence:
// no port configured, daemon down, something else on the port, or a daemon too
// old to report the field.
func daemonClaudePath(host string, port int) (string, bool) {
	if port == 0 {
		return "", false
	}
	if host == "" {
		host = "127.0.0.1"
	}
	h, ok := fetchDaemonHealth(net.JoinHostPort(host, strconv.Itoa(port)))
	if !ok || h.ClaudePath == nil {
		return "", false
	}
	return *h.ClaudePath, true
}

// fetchDaemonHealth reads GET /health from addr and reports whether an AXON
// daemon answered. Loopback-only and short-timeout: doctor must stay fast and
// must never hang on a socket that accepts but never speaks.
func fetchDaemonHealth(addr string) (daemonHealth, bool) {
	var health daemonHealth
	client := &http.Client{Timeout: 1500 * time.Millisecond}
	resp, err := client.Get("http://" + addr + "/health")
	if err != nil {
		return health, false
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return health, false
	}

	// Cap the read: an unknown listener may answer 200 with anything at all.
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return health, false
	}
	if err := json.Unmarshal(body, &health); err != nil {
		return health, false
	}
	// `status` is the field only an AXON /health carries; a JSON 200 from
	// something else will not have it.
	if health.Status == "" {
		return daemonHealth{}, false
	}
	return health, true
}

// residencyCheck reports the data-residency posture (NFR-01: local-first).
func residencyCheck(p config.Profile) Check {
	const name = "data-residency"
	res := p.Policy.DataResidency
	if res == "" {
		res = "local-only"
	}
	return Check{Name: name, Status: StatusOK, Detail: fmt.Sprintf("%s (all state on local disk; only Claude + Ollama + allowed ingest domains egress)", res)}
}

// annIndexCheck advises on the vector-search backend (ADR-025, FR-115): suggest
// enabling ann once the corpus is large, and warn when ann is enabled but the
// index has not been built. Read-only and tolerant — a missing/unreadable DB is
// reported as ok and never fails doctor.
// actionsCheck reports the derived action-index size. Advisory (always
// StatusOK): the index is read-only and rebuilt from Markdown by reindex (ADR-033).
func actionsCheck(paths config.ResolvedPaths) Check {
	const name = "actions"
	ctx := context.Background()
	if _, err := os.Stat(paths.DBPath); err != nil {
		return Check{Name: name, Status: StatusOK, Detail: "no database yet"}
	}
	d, err := sql.Open("sqlite", paths.DBPath)
	if err != nil {
		return Check{Name: name, Status: StatusOK, Detail: "database not readable; skipped"}
	}
	defer func() { _ = d.Close() }()

	total, open, done, cancelled, _, err := db.ActionStateCounts(ctx, d)
	if err != nil {
		return Check{Name: name, Status: StatusOK, Detail: "actions not counted; skipped"}
	}
	return Check{Name: name, Status: StatusOK, Detail: fmt.Sprintf("%d actions indexed (%d open / %d done / %d cancelled)", total, open, done, cancelled)}
}

// memoryFactsCheck reports the derived memory_facts index size (open/superseded)
// and flags any axon:memory block line that fails ParseFact — a parse anomaly
// means someone hand-edited a fact into an unparseable shape. Advisory: a
// missing/unreadable DB or absent layer is reported ok and never fails doctor.
func memoryFactsCheck(paths config.ResolvedPaths) Check {
	const name = "memory-facts"
	ctx := context.Background()

	// Flag unparseable block lines directly from the vault (no DB needed).
	v := vault.NewFS(paths.VaultPath)
	if lines, err := identity.BlockLines(ctx, v); err == nil {
		for _, line := range lines {
			if _, ok := identity.ParseFact(line); !ok {
				return Check{Name: name, Status: StatusWarn, Detail: fmt.Sprintf("MEMORY.md has an unparseable memory line: %q — fix it in Obsidian", line)}
			}
		}
	}

	if _, err := os.Stat(paths.DBPath); err != nil {
		return Check{Name: name, Status: StatusOK, Detail: "no database yet"}
	}
	d, err := sql.Open("sqlite", paths.DBPath)
	if err != nil {
		return Check{Name: name, Status: StatusOK, Detail: "database not readable; skipped"}
	}
	defer func() { _ = d.Close() }()

	total, open, superseded, err := db.MemoryFactCounts(ctx, d)
	if err != nil {
		return Check{Name: name, Status: StatusOK, Detail: "memory facts not counted; skipped"}
	}
	return Check{Name: name, Status: StatusOK, Detail: fmt.Sprintf("%d facts (%d open / %d superseded)", total, open, superseded)}
}

func annIndexCheck(p config.Profile, paths config.ResolvedPaths) Check {
	const name = "ann-index"
	if _, err := os.Stat(paths.DBPath); err != nil {
		return Check{Name: name, Status: StatusOK, Detail: "no database yet"}
	}
	d, err := sql.Open("sqlite", paths.DBPath)
	if err != nil {
		return Check{Name: name, Status: StatusOK, Detail: "database not readable; skipped"}
	}
	defer func() { _ = d.Close() }()
	ctx := context.Background()

	vectors, err := db.CountVectors(ctx, d)
	if err != nil {
		return Check{Name: name, Status: StatusOK, Detail: "vectors not counted; skipped"}
	}
	centroids, _ := db.CountCentroids(ctx, d)
	threshold := p.Retrieval.ANN.ThresholdOr()

	if p.Retrieval.IndexMode() == "ann" {
		if centroids == 0 && vectors > 0 {
			return Check{Name: name, Status: StatusWarn, Detail: "retrieval.index: ann is set but the index is not built — run `axon reindex`"}
		}
		return Check{Name: name, Status: StatusOK, Detail: fmt.Sprintf("ann index active (%d centroids over %d vectors)", centroids, vectors)}
	}
	if vectors > threshold {
		return Check{Name: name, Status: StatusWarn, Detail: fmt.Sprintf("%d vectors indexed — set `retrieval.index: ann` and run `axon reindex` for faster search", vectors)}
	}
	return Check{Name: name, Status: StatusOK, Detail: fmt.Sprintf("brute-force search (%d vectors, threshold %d)", vectors, threshold)}
}

// relatedCheck reports the related-notes surface (R8): whether the endpoint is
// enabled and how many vectors it has to work with. Advisory — never fails.
// The ANN seam's own health is covered by annIndexCheck; this does not duplicate it.
func relatedCheck(p config.Profile, paths config.ResolvedPaths) Check {
	const name = "related"
	if !p.Dashboard.RelatedAllowed() {
		return Check{Name: name, Status: StatusOK, Detail: "related-notes endpoint disabled (dashboard.related_enabled: false)"}
	}
	if _, err := os.Stat(paths.DBPath); err != nil {
		return Check{Name: name, Status: StatusOK, Detail: "related-notes enabled; no database yet"}
	}
	d, err := sql.Open("sqlite", paths.DBPath)
	if err != nil {
		return Check{Name: name, Status: StatusOK, Detail: "related-notes enabled; database not readable, skipped"}
	}
	defer func() { _ = d.Close() }()
	vectors, err := db.CountVectors(context.Background(), d)
	if err != nil {
		return Check{Name: name, Status: StatusOK, Detail: "related-notes enabled; vectors not counted"}
	}
	return Check{Name: name, Status: StatusOK, Detail: fmt.Sprintf("related-notes enabled (%d vectors indexed)", vectors)}
}

// apiKeyCheck implements the cardinal-rule guard: warn if ANTHROPIC_API_KEY is
// set while the active profile uses subscription/enterprise auth.
// claudeAuthCheck verifies the profile can actually reach Claude in its
// auth_mode (FR-05) — deterministically and without spending tokens. api_key
// needs a resolvable key; subscription/enterprise needs a resolvable
// CLAUDE_CODE_OAUTH_TOKEN for headless automations and/or a `claude login`
// session in the profile's CLAUDE_CONFIG_DIR for interactive use.
func claudeAuthCheck(p config.Profile, paths config.ResolvedPaths) Check {
	const name = "claude-auth"
	if p.Claude.AuthMode == "api_key" {
		if _, set := lookupEnv("ANTHROPIC_API_KEY"); set {
			return Check{Name: name, Status: StatusOK, Detail: "auth_mode api_key: ANTHROPIC_API_KEY set"}
		}
		if key, err := config.ResolveSecret(p.Claude.OAuthToken); err == nil && key != "" {
			return Check{Name: name, Status: StatusOK, Detail: "auth_mode api_key: key resolvable from the configured secret ref"}
		}
		return Check{Name: name, Status: StatusFail, Detail: "auth_mode api_key but no ANTHROPIC_API_KEY and no resolvable secret ref — the agent adapter cannot authenticate"}
	}

	token, terr := config.ResolveSecret(p.Claude.OAuthToken)
	hasToken := terr == nil && token != ""
	hasSession := false
	if paths.ConfigDir != "" {
		if _, err := os.Stat(filepath.Join(paths.ConfigDir, ".credentials.json")); err == nil {
			hasSession = true
		}
	}
	switch {
	case hasToken && hasSession:
		return Check{Name: name, Status: StatusOK, Detail: "OAuth token resolvable (headless) and login session present (interactive)"}
	case hasToken:
		return Check{Name: name, Status: StatusOK, Detail: "OAuth token resolvable — headless automations ready; run `claude login` in the vault for interactive sessions"}
	case hasSession:
		return Check{Name: name, Status: StatusWarn, Detail: "login session found but no CLAUDE_CODE_OAUTH_TOKEN resolvable — scheduled headless automations will fail; run `claude setup-token`"}
	default:
		return Check{Name: name, Status: StatusWarn, Detail: fmt.Sprintf("no OAuth token resolvable and no session file in %s (macOS may hold the session in the Keychain) — run `claude login` and `claude setup-token`", paths.ConfigDir)}
	}
}

func apiKeyCheck(cfg *config.Config, activeProfile string) Check {
	const name = "anthropic-api-key"
	_, keySet := lookupEnv("ANTHROPIC_API_KEY")

	authMode := ""
	if cfg != nil {
		if p, ok := cfg.Profiles[activeProfile]; ok {
			authMode = p.Claude.AuthMode
		}
	}

	switch {
	case keySet && (authMode == "subscription" || authMode == "enterprise"):
		return Check{
			Name:   name,
			Status: StatusWarn,
			Detail: fmt.Sprintf("ANTHROPIC_API_KEY is set but auth_mode is %q; Claude Code would bill the API account. Unset it.", authMode),
		}
	case keySet && authMode == "api_key":
		return Check{Name: name, Status: StatusOK, Detail: "ANTHROPIC_API_KEY set (auth_mode: api_key)"}
	case keySet:
		// Key set but auth mode unknown (no config) — flag conservatively.
		return Check{Name: name, Status: StatusWarn, Detail: "ANTHROPIC_API_KEY is set; ensure this is intended (api_key mode only)"}
	default:
		return Check{Name: name, Status: StatusOK, Detail: "no stray ANTHROPIC_API_KEY"}
	}
}

func binaryCheck(name, bin, okDetail, missingDetail string) Check {
	if _, err := lookPath(bin); err != nil {
		return Check{Name: name, Status: StatusWarn, Detail: missingDetail, Fix: installHint(bin)}
	}
	return Check{Name: name, Status: StatusOK, Detail: okDetail}
}

// installHint is how you get a missing external tool on this machine. Kept
// beside the checks rather than in a client so every consumer — the CLI, the
// menu bar app, anything later — gives the same answer.
func installHint(bin string) string {
	switch bin {
	case "ollama":
		if runtime.GOOS == "darwin" {
			return "brew install ollama && ollama serve && ollama pull nomic-embed-text"
		}
		return "curl -fsSL https://ollama.com/install.sh | sh && ollama pull nomic-embed-text"
	case "claude":
		return "npm install -g @anthropic-ai/claude-code && claude login"
	case "yt-dlp":
		if runtime.GOOS == "darwin" {
			return "brew install yt-dlp"
		}
		return "pipx install yt-dlp"
	case "tesseract", "pdftoppm":
		if runtime.GOOS == "darwin" {
			return "brew install tesseract poppler"
		}
		return "sudo apt-get install -y tesseract-ocr poppler-utils"
	}
	return ""
}
