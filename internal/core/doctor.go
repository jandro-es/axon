// Package core composes the leaf packages into the daemon. In Phase 0 it
// provides the doctor health checks; the scheduler, automations, ingestion and
// token manager are wired in here in later phases.
package core

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/jandro-es/axon/internal/config"
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

	// 4. ollama presence (informational — local embeddings).
	checks = append(checks, binaryCheck("ollama", "ollama",
		"Ollama found", "ollama not found on PATH (needed for local embeddings in Phase 2)"))

	// 5–7. Profile-scoped prerequisites (FR-05): vault writable, dashboard port
	// free, and the data-residency posture.
	if cfg != nil {
		if p, ok := cfg.Profiles[activeProfile]; ok {
			paths := p.Paths()
			checks = append(checks, vaultWritableCheck(paths.VaultPath))
			checks = append(checks, portFreeCheck(p.Dashboard.Host, p.Dashboard.Port))
			checks = append(checks, residencyCheck(p))
		}
	}

	return DoctorReport{Checks: checks}
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

// apiKeyCheck implements the cardinal-rule guard: warn if ANTHROPIC_API_KEY is
// set while the active profile uses subscription/enterprise auth.
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
