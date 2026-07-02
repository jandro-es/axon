package ui

import (
	"errors"
	"os"
	"strings"
)

// Hint returns a short, actionable suggestion for a known class of error, or the
// empty string when nothing specific applies. It is best-effort presentation
// sugar layered on top of the real error — never a substitute for wrapping
// errors with context at their source. Matching prefers robust signals
// (errors.Is) and falls back to substring checks for errors that only carry a
// message.
func Hint(err error) string {
	if err == nil {
		return ""
	}
	msg := strings.ToLower(err.Error())

	switch {
	// Missing config file — by far the most common first-run stumble.
	case errors.Is(err, os.ErrNotExist) && strings.Contains(msg, "config"):
		return "No config file found. Run `axon init` to create one, or pass --config <path>."

	// Config parsed but failed validation.
	case strings.Contains(msg, "config validation failed"):
		return "Fix the reported field(s) in config.yaml, then re-run `axon config validate`."

	// active_profile / --profile points at a profile that isn't defined.
	case strings.Contains(msg, "is not defined in profiles"):
		return "That profile is not defined. List available profiles with `axon profiles`, or check `active_profile` in your config."

	// YAML syntax error.
	case strings.Contains(msg, "parse config"):
		return "The config file is not valid YAML. Check indentation and syntax, then run `axon config validate`."

	// Apple embeddings helper missing/failed (must precede the generic
	// embedding-provider matches).
	case strings.Contains(msg, "apple embed helper"), strings.Contains(msg, "axon-apple-embed"):
		return "The Apple embeddings helper isn't built or failed. Re-run `axon init` (needs Xcode Command Line Tools), then verify with `axon doctor`."

	// Ollama not reachable (embeddings / ingestion).
	case strings.Contains(msg, "connection refused") && strings.Contains(msg, "11434"),
		strings.Contains(msg, "ollama"):
		return "Ollama is not reachable. Start it with `ollama serve`, then verify with `axon doctor`."

	// A stray ANTHROPIC_API_KEY diverts Claude Code onto API billing.
	case strings.Contains(msg, "anthropic_api_key"):
		return "Unset ANTHROPIC_API_KEY for subscription/enterprise auth — it diverts Claude Code onto API billing. See `axon doctor`."

	// Budget guard tripped.
	case strings.Contains(msg, "budget") && (strings.Contains(msg, "exceeded") || strings.Contains(msg, "paused")):
		return "The token budget guard is active. Check remaining budget with `axon status`, or raise limits in your config."

	// Generic permission problems.
	case errors.Is(err, os.ErrPermission):
		return "Permission denied. Check the path's ownership and permissions."

	// Daemon already running / stale pidfile.
	case strings.Contains(msg, "already running"):
		return "AXON already appears to be running. Use `axon stop` first, or `axon status` to inspect it."
	}
	return ""
}
