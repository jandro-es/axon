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
	HookEventName string         `json:"hook_event_name"`
	SessionID     string         `json:"session_id"`
	CWD           string         `json:"cwd"`
	ToolName      string         `json:"tool_name"`
	ToolInput     map[string]any `json:"tool_input"`
}

// Deps are the services hooks read. Manager is used read-only (budget status);
// no hook makes a model call.
type Deps struct {
	Profile string
	DB      *sql.DB
	Vault   *vault.FS
	Manager tokens.Manager
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
		return stop(), nil
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
	}
	b.WriteString("- Conventions: never rename/move notes with raw file ops — use vault_move (wikilink-safe). Edit AXON output only inside axon:* managed blocks. See .claude/CLAUDE.md.\n")

	out := map[string]any{
		"hookSpecificOutput": map[string]any{
			"hookEventName":     SessionStart,
			"additionalContext": b.String(),
		},
	}
	data, _ := json.Marshal(out)
	return Result{Stdout: data, ExitCode: 0}, nil
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
	if reason := denyReason(in); reason != "" {
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
func denyReason(in Input) string {
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
	}
	return ""
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
func stop() Result {
	return Result{
		Stdout:   []byte("Reminder: persist anything durable into the vault (vault_write/vault_patch) and consider /compact if context is large.\n"),
		ExitCode: 0,
	}
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
