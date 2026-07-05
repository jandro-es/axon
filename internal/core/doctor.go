// Package core composes the leaf packages into the daemon. In Phase 0 it
// provides the doctor health checks; the scheduler, automations, ingestion and
// token manager are wired in here in later phases.
package core

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/jandro-es/axon/internal/clients"
	"github.com/jandro-es/axon/internal/config"
	"github.com/jandro-es/axon/internal/db"
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
func Doctor(cfg *config.Config, activeProfile string) DoctorReport {
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
			// 8–9. Multi-client wiring (FR-75): is the AXON MCP server registered
			// with each Claude client, and is each client's guarantee honest.
			checks = append(checks, claudeCodeWiringCheck(paths.VaultPath))
			checks = append(checks, claudeDesktopCheck(activeProfile))
			checks = append(checks, interopCheck(p))
		}
	}

	return DoctorReport{Checks: checks}
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
			return Check{name, StatusWarn, fmt.Sprintf("Apple embeddings helper not built at %s — run `axon init` (requires Xcode CLT)", helper)}
		}
		return Check{name, StatusOK, "Apple embeddings helper present: " + helper}
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
			return Check{name, StatusWarn, fmt.Sprintf("Apple OCR helper not built at %s — run `axon init` (requires Xcode CLT)", helper)}
		}
		return Check{name, StatusOK, "Apple OCR helper present: " + helper}
	case "tesseract":
		var missing []string
		for _, bin := range []string{"pdftoppm", "tesseract"} {
			if _, err := exec.LookPath(bin); err != nil {
				missing = append(missing, bin)
			}
		}
		if len(missing) > 0 {
			return Check{name, StatusWarn, "OCR (tesseract) needs on PATH: " + strings.Join(missing, ", ") + " — install poppler + tesseract"}
		}
		return Check{name, StatusOK, "tesseract OCR binaries present (pdftoppm, tesseract)"}
	default:
		return Check{name, StatusOK, "OCR off"}
	}
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
				checks = append(checks, Check{name, StatusWarn,
					fmt.Sprintf("Ollama not reachable at %s — %s calls will use models.local_fallback (%s)", host, t.tier, m.Fallback())})
				continue
			}
			if !ollamaModelPresent(ctx, host, ref.Model) {
				checks = append(checks, Check{name, StatusWarn,
					fmt.Sprintf("model %q not pulled — run `ollama pull %s` (until then %s calls use models.local_fallback: %s)", ref.Model, ref.Model, t.tier, m.Fallback())})
				continue
			}
			checks = append(checks, Check{name, StatusOK,
				fmt.Sprintf("ollama model %q available at %s", ref.Model, host)})
		case config.ProviderApple:
			if runtime.GOOS != "darwin" {
				checks = append(checks, Check{name, StatusWarn,
					fmt.Sprintf("tier configured as apple but this machine is not a mac — calls will use models.local_fallback (%s)", m.Fallback())})
				continue
			}
			helper := m.AppleHelper
			if helper == "" {
				helper = config.DefaultAppleLMHelperPath()
			}
			if st, err := os.Stat(helper); err != nil || st.Mode()&0o111 == 0 {
				checks = append(checks, Check{name, StatusWarn,
					fmt.Sprintf("Apple Foundation Models helper not built at %s — run `axon init` or `axon configure models classify apple` (requires Xcode CLT)", helper)})
				continue
			}
			checks = append(checks, Check{name, StatusOK, "Apple Foundation Models helper present: " + helper})
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
		return Check{name, StatusOK, "not configured (AXON's own server is the vault backend)"}
	}
	return Check{name, StatusOK, fmt.Sprintf("configured (%s) — registered alongside AXON by `axon mcp install`", obs.Command)}
}

// claudeCodeWiringCheck reports whether the project's Claude Code wiring exists
// (the .mcp.json that registers the AXON server). Claude Code is the
// full-featured client (hooks + skills + subagents + headless automations).
func claudeCodeWiringCheck(vaultPath string) Check {
	const name = "client:claude-code"
	if vaultPath == "" {
		return Check{name, StatusWarn, "no vault_path configured"}
	}
	if _, err := os.Stat(filepath.Join(vaultPath, ".claude", ".mcp.json")); err != nil {
		return Check{name, StatusWarn, "not wired — run `axon init` or `axon mcp install --client code`"}
	}
	return Check{name, StatusOK, "registered (full-featured: tools + hooks + skills + automations)"}
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
		return Check{name, StatusOK, "Claude Desktop not detected (optional)"}
	}
	st, err := clients.DetectDesktop(path)
	if err != nil {
		return Check{name, StatusOK, "Claude Desktop not detected (optional)"}
	}
	switch {
	case st.Registered:
		note := "registered (tools only — no hooks/skills/profile injection; keep vault edits in AXON tools)"
		if st.Profile != "" && st.Profile != activeProfile {
			note = fmt.Sprintf("registered for profile %q, not active %q — re-run `axon mcp install --client desktop`", st.Profile, activeProfile)
			return Check{name, StatusWarn, note}
		}
		return Check{name, StatusOK, note}
	case st.Present:
		return Check{name, StatusWarn, "Claude Desktop present but AXON not registered — run `axon mcp install --client desktop`"}
	default:
		return Check{name, StatusOK, "Claude Desktop not configured (optional; `axon mcp install --client desktop`)"}
	}
}

// vaultWritableCheck confirms the vault path is writable (or createable).
func vaultWritableCheck(vaultPath string) Check {
	const name = "vault-writable"
	if vaultPath == "" {
		return Check{name, StatusWarn, "no vault_path configured"}
	}
	target := vaultPath
	// Walk up to the nearest existing ancestor and test writability there.
	for {
		if info, err := os.Stat(target); err == nil {
			if !info.IsDir() {
				return Check{name, StatusFail, fmt.Sprintf("%s exists but is not a directory", target)}
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
		return Check{name, StatusFail, fmt.Sprintf("%s not writable: %v", target, err)}
	}
	_ = f.Close()
	_ = os.Remove(f.Name())
	return Check{name, StatusOK, "vault path writable: " + vaultPath}
}

// portFreeCheck confirms the dashboard port is bindable on the loopback host.
func portFreeCheck(host string, port int) Check {
	const name = "dashboard-port"
	if port == 0 {
		return Check{name, StatusWarn, "no dashboard port configured"}
	}
	if host == "" {
		host = "127.0.0.1"
	}
	addr := net.JoinHostPort(host, fmt.Sprintf("%d", port))
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return Check{name, StatusWarn, fmt.Sprintf("%s is in use (a daemon may already be running): %v", addr, err)}
	}
	_ = ln.Close()
	return Check{name, StatusOK, "dashboard port free: " + addr}
}

// residencyCheck reports the data-residency posture (NFR-01: local-first).
func residencyCheck(p config.Profile) Check {
	const name = "data-residency"
	res := p.Policy.DataResidency
	if res == "" {
		res = "local-only"
	}
	return Check{name, StatusOK, fmt.Sprintf("%s (all state on local disk; only Claude + Ollama + allowed ingest domains egress)", res)}
}

// annIndexCheck advises on the vector-search backend (ADR-025, FR-115): suggest
// enabling ann once the corpus is large, and warn when ann is enabled but the
// index has not been built. Read-only and tolerant — a missing/unreadable DB is
// reported as ok and never fails doctor.
func annIndexCheck(p config.Profile, paths config.ResolvedPaths) Check {
	const name = "ann-index"
	if _, err := os.Stat(paths.DBPath); err != nil {
		return Check{name, StatusOK, "no database yet"}
	}
	d, err := sql.Open("sqlite", paths.DBPath)
	if err != nil {
		return Check{name, StatusOK, "database not readable; skipped"}
	}
	defer func() { _ = d.Close() }()
	ctx := context.Background()

	vectors, err := db.CountVectors(ctx, d)
	if err != nil {
		return Check{name, StatusOK, "vectors not counted; skipped"}
	}
	centroids, _ := db.CountCentroids(ctx, d)
	threshold := p.Retrieval.ANN.ThresholdOr()

	if p.Retrieval.IndexMode() == "ann" {
		if centroids == 0 && vectors > 0 {
			return Check{name, StatusWarn, "retrieval.index: ann is set but the index is not built — run `axon reindex`"}
		}
		return Check{name, StatusOK, fmt.Sprintf("ann index active (%d centroids over %d vectors)", centroids, vectors)}
	}
	if vectors > threshold {
		return Check{name, StatusWarn, fmt.Sprintf("%d vectors indexed — set `retrieval.index: ann` and run `axon reindex` for faster search", vectors)}
	}
	return Check{name, StatusOK, fmt.Sprintf("brute-force search (%d vectors, threshold %d)", vectors, threshold)}
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
			return Check{name, StatusOK, "auth_mode api_key: ANTHROPIC_API_KEY set"}
		}
		if key, err := config.ResolveSecret(p.Claude.OAuthToken); err == nil && key != "" {
			return Check{name, StatusOK, "auth_mode api_key: key resolvable from the configured secret ref"}
		}
		return Check{name, StatusFail, "auth_mode api_key but no ANTHROPIC_API_KEY and no resolvable secret ref — the agent adapter cannot authenticate"}
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
		return Check{name, StatusOK, "OAuth token resolvable (headless) and login session present (interactive)"}
	case hasToken:
		return Check{name, StatusOK, "OAuth token resolvable — headless automations ready; run `claude login` in the vault for interactive sessions"}
	case hasSession:
		return Check{name, StatusWarn, "login session found but no CLAUDE_CODE_OAUTH_TOKEN resolvable — scheduled headless automations will fail; run `claude setup-token`"}
	default:
		return Check{name, StatusWarn, fmt.Sprintf("no OAuth token resolvable and no session file in %s (macOS may hold the session in the Keychain) — run `claude login` and `claude setup-token`", paths.ConfigDir)}
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
		return Check{Name: name, Status: StatusWarn, Detail: missingDetail}
	}
	return Check{Name: name, Status: StatusOK, Detail: okDetail}
}
