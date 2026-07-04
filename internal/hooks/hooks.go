// Package hooks implements the deterministic in-session control logic invoked by
// Claude Code via thin `axon hook <event>` scripts (Component 08 §2). Hooks only
// TIGHTEN behaviour — SessionStart injects a cheap status block (no model call),
// PreToolUse authoritatively blocks unsafe vault operations (raw deletes and
// link-breaking renames), PostToolUse/Stop are advisory. Logic lives here so a
// Claude Code schema change is a one-file fix.
//
// The exact hook JSON schema is verified against the Claude Code hooks reference
// at build time; the field names below reflect the current contract.
package hooks

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/jandro-es/axon/internal/config"
	"github.com/jandro-es/axon/internal/db"
	"github.com/jandro-es/axon/internal/identity"
	"github.com/jandro-es/axon/internal/ingestion"
	"github.com/jandro-es/axon/internal/tokens"
	"github.com/jandro-es/axon/internal/vault"
)

// Event names (Claude Code hook events).
const (
	SessionStart = "SessionStart"
	PreToolUse   = "PreToolUse"
	PostToolUse  = "PostToolUse"
	Stop         = "Stop"
)

// Input is the JSON Claude Code passes on stdin to a hook command. Only the
// fields AXON uses are modelled; unknown fields are ignored.
type Input struct {
	HookEventName  string         `json:"hook_event_name"`
	SessionID      string         `json:"session_id"`
	CWD            string         `json:"cwd"`
	ToolName       string         `json:"tool_name"`
	ToolInput      map[string]any `json:"tool_input"`
	TranscriptPath string         `json:"transcript_path"`
}

// Deps are the services hooks read. Manager is used read-only (budget status);
// no hook makes a model call.
type Deps struct {
	Profile string
	DB      *sql.DB
	Vault   *vault.FS
	Manager tokens.Manager
	// Memory governs the SessionStart identity injection (Component 12, FR-72).
	Memory config.MemoryConfig
	// Redaction are the profile's redaction rules, applied to the injected
	// identity block before it can leave the machine (NFR-14).
	Redaction []string
}

// Result is what a hook returns: bytes to write to stdout and an exit code.
type Result struct {
	Stdout   []byte
	ExitCode int
}

// Handle dispatches a hook event. stdinJSON is the raw stdin; event is the
// subcommand argument (authoritative if HookEventName is absent).
func Handle(ctx context.Context, event string, stdinJSON []byte, deps Deps) (Result, error) {
	var in Input
	if len(stdinJSON) > 0 {
		_ = json.Unmarshal(stdinJSON, &in) // tolerate empty/garbage stdin
	}
	if event == "" {
		event = in.HookEventName
	}
	switch event {
	case SessionStart:
		return sessionStart(ctx, deps)
	case PreToolUse:
		return preToolUse(in, deps)
	case PostToolUse:
		return Result{ExitCode: 0}, nil // advisory; MCP-tool Claude usage is ledgered by the manager
	case Stop:
		return stop(ctx, in, deps), nil
	default:
		return Result{ExitCode: 0}, nil
	}
}

// sessionStart injects a compact status block (budget, inbox, review queue) with
// NO model call (FR-52).
func sessionStart(ctx context.Context, deps Deps) (Result, error) {
	var b strings.Builder
	b.WriteString("AXON vault status (profile: " + deps.Profile + ")\n")

	if deps.Manager != nil {
		if st, err := deps.Manager.Status(ctx, deps.Profile); err == nil {
			guard := ""
			if st.GuardPaused {
				guard = " — budget-guard ACTIVE"
			}
			fmt.Fprintf(&b, "- Budget: day %.0f%%, week %.0f%%%s\n", st.Day.Pct, st.Week.Pct, guard)
		}
	}
	if deps.Vault != nil {
		fmt.Fprintf(&b, "- Inbox: %d item(s) awaiting triage\n", inboxCount(ctx, deps.Vault))
		if pending := reviewQueueCount(deps.Vault); pending > 0 {
			fmt.Fprintf(&b, "- Review queue: %d pending suggestion(s) in .axon/review-queue.md\n", pending)
		}
		// Briefing pointer (FR-89): one deterministic line when today's
		// briefing exists; any error means no line, never a broken hook.
		if line := briefingPointer(deps.Vault); line != "" {
			b.WriteString(line)
		}
	}
	b.WriteString("- Conventions: never rename/move notes with raw file ops — use vault_move (wikilink-safe). Edit AXON output only inside axon:* managed blocks. See .claude/CLAUDE.md.\n")

	// Identity injection (FR-72): append a token-bounded snapshot of the personal
	// profile + persona + recent memory so the assistant knows the user. NO model
	// call — this reuses the hook AXON already owns and is deterministic and free.
	// Honours profile.memory.inject (a stricter work profile can disable it) and
	// applies redaction before the block can leave the machine (NFR-14).
	if deps.Vault != nil && deps.Memory.InjectEnabled() {
		if block := renderIdentity(ctx, deps); block != "" {
			b.WriteString("\n")
			b.WriteString(block)
		}
	}

	out := map[string]any{
		"hookSpecificOutput": map[string]any{
			"hookEventName":     SessionStart,
			"additionalContext": b.String(),
		},
	}
	data, _ := json.Marshal(out)
	return Result{Stdout: data, ExitCode: 0}, nil
}

// renderIdentity builds the bounded identity-layer injection, applying the
// profile's redaction rules. Any error (missing layer, bad rule) degrades to an
// empty string — a hook must never break the session.
func renderIdentity(ctx context.Context, deps Deps) string {
	var redact func(string) string
	if len(deps.Redaction) > 0 {
		if r, err := ingestion.NewRedactor(deps.Redaction); err == nil {
			redact = func(s string) string { out, _ := r.Redact(s); return out }
		}
	}
	block, err := identity.Render(ctx, deps.Vault, identity.RenderOptions{
		MaxTokens:    deps.Memory.SessionTokenBudget(),
		RecentMemory: deps.Memory.RecentMemoryEntries(),
		Redact:       redact,
	})
	if err != nil {
		return ""
	}
	return block
}

// Shell-command guardrails. IMPORTANT: this is best-effort defense-in-depth to
// steer the *interactive* agent away from raw file ops — it is NOT a sandbox. A
// regex denylist over free-form shell can always be evaded (interpreters, env
// indirection, novel tools). The real wikilink-safety guarantee is structural:
// AXON's own automation/MCP writes go exclusively through vault.move/patch. The
// patterns below cover the common destructive/rename footguns; residual evasion
// is accepted, not claimed solved.
var (
	// destructiveRe: commands that delete or destroy file contents.
	destructiveRe = regexp.MustCompile(`(?i)\b(rm|rmdir|unlink|trash|shred|truncate)\b`)
	// findDeleteRe: `find ... -delete` / `-exec rm`.
	findDeleteRe = regexp.MustCompile(`(?i)\bfind\b[\s\S]*(-delete\b|-exec\s+rm\b)`)
	// ddWriteRe: `dd ... of=` overwrites a file destructively.
	ddWriteRe = regexp.MustCompile(`(?i)\bdd\b[\s\S]*\bof=`)
	// renameRe: raw renames/moves (including `git mv`), which break inbound links.
	renameRe = regexp.MustCompile(`(?i)(\bgit\s+mv\b|\b(mv|rename)\b)`)
	// redirectMdRe: shell redirection that overwrites/truncates a .md note.
	redirectMdRe = regexp.MustCompile(`(?i)>>?\s*['"]?[^\s|&;'"]*\.md\b`)
)

// preToolUse blocks unsafe operations authoritatively (deny cannot be bypassed
// by permission mode). It tightens only; safe operations pass through.
func preToolUse(in Input, deps Deps) (Result, error) {
	if reason := denyReason(in, deps); reason != "" {
		out := map[string]any{
			"hookSpecificOutput": map[string]any{
				"hookEventName":            PreToolUse,
				"permissionDecision":       "deny",
				"permissionDecisionReason": reason,
			},
		}
		data, _ := json.Marshal(out)
		return Result{Stdout: data, ExitCode: 0}, nil
	}
	return Result{ExitCode: 0}, nil
}

// denyReason returns a non-empty reason if the tool call must be blocked.
func denyReason(in Input, deps Deps) string {
	switch in.ToolName {
	case "Bash":
		cmd, _ := in.ToolInput["command"].(string)
		if destructiveRe.MatchString(cmd) || findDeleteRe.MatchString(cmd) || ddWriteRe.MatchString(cmd) {
			return "Blocked: AXON never hard-deletes vault content. Archive via vault_move (to 04-Archive/), or delete out-of-band with confirmation."
		}
		if redirectMdRe.MatchString(cmd) {
			return "Blocked: overwriting a note via shell redirection bypasses AXON's managed blocks and link safety. Use vault_write/vault_patch."
		}
		if renameRe.MatchString(cmd) {
			return "Blocked: renaming/moving with a raw shell command breaks inbound wikilinks. Use the vault_move tool (wikilink-safe)."
		}
	case "Write", "Edit", "MultiEdit", "NotebookEdit":
		path, _ := in.ToolInput["file_path"].(string)
		if path == "" {
			path, _ = in.ToolInput["path"].(string)
		}
		if isProtectedPath(path) {
			return "Blocked: writes into .obsidian/ or .git/ are not permitted; AXON manages the vault via its tools."
		}
		// The native Write tool is a WHOLE-FILE overwrite — exactly the operation
		// vault_write refuses on existing notes. Deny it deterministically for
		// existing vault notes (Edit stays allowed: it is a surgical replace that
		// fails rather than clobbers when its anchor text is missing).
		if in.ToolName == "Write" && isExistingVaultNote(path, in.CWD, deps) {
			return "Blocked: Write would overwrite an existing vault note wholesale. Use vault_patch for managed-block edits, vault_write force=true for AXON-managed notes, or Edit for a surgical change."
		}
	}
	return ""
}

// isExistingVaultNote reports whether path (absolute, or relative to cwd)
// resolves to an existing .md file inside the profile's vault.
func isExistingVaultNote(path, cwd string, deps Deps) bool {
	if deps.Vault == nil || path == "" || !strings.EqualFold(filepath.Ext(path), ".md") {
		return false
	}
	abs := path
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(cwd, abs)
	}
	abs = filepath.Clean(abs)
	root := deps.Vault.Root()
	if rootReal, err := filepath.EvalSymlinks(root); err == nil {
		root = rootReal
	}
	if absReal, err := filepath.EvalSymlinks(abs); err == nil {
		abs = absReal // existing file: compare resolved paths
	}
	if abs != root && !strings.HasPrefix(abs, root+string(filepath.Separator)) {
		return false
	}
	info, err := os.Stat(abs)
	return err == nil && !info.IsDir()
}

// protectedDirs are system directories AXON never lets the agent write into.
var protectedDirs = map[string]bool{".obsidian": true, ".git": true}

// isProtectedPath reports whether a path targets a protected system directory.
// It normalises the path (collapsing "." / ".." and redundant separators) and
// compares each segment case-insensitively, so ".OBSIDIAN/app.json",
// "vault/.git/config" and "a/../.obsidian/x" are all caught regardless of the
// host filesystem's case sensitivity.
func isProtectedPath(path string) bool {
	clean := filepath.ToSlash(filepath.Clean(path))
	for _, seg := range strings.Split(clean, "/") {
		if protectedDirs[strings.ToLower(seg)] {
			return true
		}
	}
	return false
}

// stop surfaces a non-blocking reminder to persist durable knowledge.
// stop reminds the agent to persist durable work AND records the session for
// memory distillation (ADR-021, FR-97): a deterministic upsert of
// {session_id → transcript_path, last_stop} — paths only, never content —
// gated by memory.capture_sessions. Every failure is silent: a hook must
// never break the session.
func stop(ctx context.Context, in Input, deps Deps) Result {
	recordSession(ctx, in, deps)
	return Result{
		Stdout:   []byte("Reminder: persist anything durable into the vault (vault_write/vault_patch) and consider /compact if context is large.\n"),
		ExitCode: 0,
	}
}

// sessionPendingCap bounds the recorder's map (newest LastStop wins).
const sessionPendingCap = 50

func recordSession(ctx context.Context, in Input, deps Deps) {
	if deps.DB == nil || !deps.Memory.SessionCaptureEnabled() ||
		in.SessionID == "" || in.TranscriptPath == "" {
		return
	}
	pending, err := db.LoadPendingSessions(ctx, deps.DB)
	if err != nil {
		return
	}
	now := time.Now().UTC().Format(time.RFC3339)
	pending[in.SessionID] = db.PendingSession{TranscriptPath: in.TranscriptPath, LastStop: now}
	for len(pending) > sessionPendingCap {
		oldestID, oldest := "", ""
		for id, p := range pending {
			if oldest == "" || p.LastStop < oldest {
				oldestID, oldest = id, p.LastStop
			}
		}
		delete(pending, oldestID)
	}
	_ = db.SavePendingSessions(ctx, deps.DB, pending, now)
}

func inboxCount(ctx context.Context, v *vault.FS) int {
	paths, err := v.List(ctx)
	if err != nil {
		return 0
	}
	n := 0
	for _, p := range paths {
		if strings.HasPrefix(p, "00-Inbox/") && !strings.EqualFold(filepath.Base(p), "README.md") {
			n++
		}
	}
	return n
}

func reviewQueueCount(v *vault.FS) int {
	data, err := os.ReadFile(filepath.Join(v.Root(), ".axon", "review-queue.md"))
	if err != nil {
		return 0
	}
	return strings.Count(string(data), "- [ ]")
}

// briefingPointer returns the one-line pointer to today's axon:briefing
// block, or "" when the daily note or block is absent (FR-89).
func briefingPointer(v *vault.FS) string {
	date := time.Now().UTC().Format("2006-01-02")
	data, err := os.ReadFile(filepath.Join(v.Root(), "Daily", date+".md"))
	if err != nil {
		return ""
	}
	if !strings.Contains(string(data), "<!-- axon:briefing:start -->") {
		return ""
	}
	return "- Briefing: Daily/" + date + ".md (axon:briefing)\n"
}
