package identity

import (
	"context"
	"strings"

	"github.com/jandro-es/axon/internal/vault"
)

// RenderOptions bound and shape the SessionStart injection.
type RenderOptions struct {
	// MaxTokens caps the rendered block (approximate; ~4 chars/token). The USER
	// and SOUL sections are kept whole; the MEMORY tail is trimmed first, then a
	// hard truncate guards the ceiling.
	MaxTokens int
	// RecentMemory is the maximum number of newest MEMORY entries to include.
	RecentMemory int
	// Redact, if set, is applied to the final block before it can leave the
	// machine (NFR-14). Callers wire the profile's redaction rules here.
	Redact func(string) string
}

// Render builds a compact, deterministic snapshot of the identity layer for the
// SessionStart hook. It makes NO model call (FR-72): it reads the three notes,
// assembles USER + SOUL + the most recent MEMORY entries, applies redaction and
// bounds the result to MaxTokens. An absent layer yields "" (no injection).
func Render(ctx context.Context, v *vault.FS, opts RenderOptions) (string, error) {
	if !Present(v) {
		return "", nil
	}
	user, err := readBody(ctx, v, UserPath)
	if err != nil {
		return "", err
	}
	soul, _ := readBody(ctx, v, SoulPath) // SOUL is optional steering
	entries, _ := RecentEntries(ctx, v, opts.RecentMemory)

	var b strings.Builder
	b.WriteString("AXON profile (who I am — injected from " + Dir + "/; edit in Obsidian):\n\n")
	if user != "" {
		b.WriteString("### User\n")
		b.WriteString(strings.TrimRight(stripQuotes(user), "\n"))
		b.WriteString("\n\n")
	}
	if soul != "" {
		b.WriteString("### Agent persona (SOUL)\n")
		b.WriteString(strings.TrimRight(stripQuotes(soul), "\n"))
		b.WriteString("\n\n")
	}

	// The fixed (USER+SOUL) prefix is essential; memory is the elastic tail.
	prefix := b.String()
	budget := opts.MaxTokens
	if budget <= 0 {
		budget = 1500
	}

	var mem strings.Builder
	if len(entries) > 0 {
		mem.WriteString("### Recent memory\n")
		for _, e := range entries {
			candidate := mem.String() + e + "\n" // mem already holds the header
			if approxTokens(prefix+candidate) > budget {
				mem.WriteString("- … (older memory omitted; search the vault for more)\n")
				break
			}
			mem.WriteString(e + "\n")
		}
	}

	out := prefix + mem.String()
	out = strings.TrimRight(out, "\n") + "\n"
	if opts.Redact != nil {
		out = opts.Redact(out)
	}
	// Final hard ceiling: if even USER+SOUL overrun, truncate by runes.
	if approxTokens(out) > budget {
		out = truncateToTokens(out, budget) + "\n… (profile truncated to fit the session budget)\n"
	}
	return out, nil
}

// RecentEntries returns up to n newest currently-valid entries (newest first)
// from the axon:memory managed block in MEMORY.md. Superseded/closed facts —
// struck lines and any with a valid_until — are excluded so SessionStart
// injection prefers currently-valid facts (FR-137). Legacy untyped lines (no
// kind, no interval) are open and included. Pure block parse, no DB dependency.
func RecentEntries(ctx context.Context, v *vault.FS, n int) ([]string, error) {
	if n <= 0 {
		n = 10
	}
	body, err := readBody(ctx, v, MemoryPath)
	if err != nil {
		return nil, err
	}
	all := parseEntries(extractBlock(body, MemoryBlock))
	open := make([]string, 0, len(all))
	for _, line := range all {
		f, ok := ParseFact(line)
		if !ok || f.Struck || f.ValidUntil != "" {
			continue
		}
		open = append(open, line)
	}
	if len(open) > n {
		open = open[:n]
	}
	return open, nil
}

// readBody returns a note's body (frontmatter stripped), or "" if absent.
func readBody(ctx context.Context, v *vault.FS, path string) (string, error) {
	if !v.Exists(path) {
		return "", nil
	}
	n, err := v.Read(ctx, path)
	if err != nil {
		return "", err
	}
	return n.Body, nil
}

// extractBlock returns the inner content of an axon:<name> managed block, or "".
func extractBlock(body, name string) string {
	start := "<!-- axon:" + name + ":start -->"
	end := "<!-- axon:" + name + ":end -->"
	i := strings.Index(body, start)
	if i < 0 {
		return ""
	}
	i += len(start)
	j := strings.Index(body[i:], end)
	if j < 0 {
		return ""
	}
	return strings.Trim(body[i:i+j], "\n")
}

// parseEntries splits a memory block into its "- …" bullet entries, preserving
// order (the block is maintained newest-first).
func parseEntries(block string) []string {
	var out []string
	for line := range strings.SplitSeq(block, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "- ") {
			out = append(out, strings.TrimSpace(line))
		}
	}
	return out
}

// stripQuotes removes leading Markdown blockquote markers (the explanatory ">"
// preamble in the layer files) so the injection stays compact.
func stripQuotes(body string) string {
	var keep []string
	for line := range strings.SplitSeq(body, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), ">") {
			continue
		}
		keep = append(keep, line)
	}
	return strings.TrimSpace(strings.Join(keep, "\n"))
}

// approxTokens mirrors the token manager's ~4-chars/token heuristic so the
// injection budget is consistent with the rest of AXON, without importing the
// heavier tokens package.
func approxTokens(s string) int {
	n := len([]rune(s))
	if n == 0 {
		return 0
	}
	return (n + 3) / 4
}

// truncateToTokens cuts s to at most budget tokens (by the rune heuristic).
func truncateToTokens(s string, budget int) string {
	r := []rune(s)
	max := budget * 4
	if len(r) <= max {
		return s
	}
	return string(r[:max])
}
