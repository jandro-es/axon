package vault

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Merge combines two near-duplicate notes into one, wikilink-safely and without
// deleting anything (ADR-032). The survivor — the more inbound-linked of the two
// (ties broken by recency, then path) — keeps its prose and gains the loser's body
// in its axon:merged managed block; every inbound wikilink to the loser is
// retargeted to the survivor; the loser is relocated intact to .trash/merged/
// (recoverable, out of the index). a and b are vault-relative note paths WITH the
// .md extension. Returns the survivor's vault-relative path WITHOUT extension.
//
// Ordering is archive-first so a crash mid-operation duplicates content at worst,
// never loses it: (1) copy loser to .trash, (2) append loser body to survivor,
// (3) retarget inbound links, (4) remove the original loser.
func (v *FS) Merge(ctx context.Context, a, b string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	aAbs, err := v.safeAbs(a)
	if err != nil {
		return "", err
	}
	bAbs, err := v.safeAbs(b)
	if err != nil {
		return "", err
	}
	if aAbs == bAbs {
		return "", fmt.Errorf("merge: %q and %q are the same note", a, b)
	}
	if !strings.EqualFold(filepath.Ext(a), ".md") || !strings.EqualFold(filepath.Ext(b), ".md") {
		return "", fmt.Errorf("merge: both paths must be .md notes")
	}
	if _, err := os.Stat(aAbs); err != nil {
		return "", fmt.Errorf("merge source %q: %w", a, err)
	}
	if _, err := os.Stat(bAbs); err != nil {
		return "", fmt.Errorf("merge source %q: %w", b, err)
	}

	survivor, loser := v.chooseSurvivor(ctx, a, b)
	loserAbs, err := v.safeAbs(loser)
	if err != nil {
		return "", err
	}

	loserRaw, err := os.ReadFile(loserAbs)
	if err != nil {
		return "", fmt.Errorf("merge read loser %q: %w", loser, err)
	}
	loserNote, err := v.Read(ctx, loser)
	if err != nil {
		return "", fmt.Errorf("merge parse loser %q: %w", loser, err)
	}

	// Stage inbound-link rewrites across every note EXCEPT the loser (survivor
	// included, so its own [[loser]] reference becomes a self-link, never a
	// dangling link into .trash/). Staged before any write: a read error aborts
	// with nothing changed.
	paths, err := v.List(ctx)
	if err != nil {
		return "", err
	}
	loserSlash := filepath.ToSlash(loser)
	type rewrite struct{ path, content string }
	var staged []rewrite
	for _, p := range paths {
		if p == loserSlash {
			continue
		}
		abs, err := v.safeAbs(p)
		if err != nil {
			return "", err
		}
		data, err := os.ReadFile(abs)
		if err != nil {
			return "", fmt.Errorf("merge scan %q: %w", p, err)
		}
		fm, body := splitFrontmatter(string(data))
		newBody, n := rewriteLinksForMove(body, loser, survivor)
		if n == 0 {
			continue
		}
		staged = append(staged, rewrite{p, reassemble(fm, newBody)})
	}

	now := time.Now().UTC()

	// 1. Archive the loser first (crash-safe).
	archiveRel := ".trash/merged/" + filepath.Base(loser)
	if v.Exists(archiveRel) {
		stem := strings.TrimSuffix(filepath.Base(loser), filepath.Ext(loser))
		archiveRel = fmt.Sprintf(".trash/merged/%s-%d.md", stem, now.UnixMilli())
	}
	if err := v.writeRaw(archiveRel, string(loserRaw)); err != nil {
		return "", fmt.Errorf("merge archive loser: %w", err)
	}

	// 2. Append the loser body into the survivor's axon:merged block (additive).
	if err := v.appendMerged(ctx, survivor, loser, loserNote.Body, now); err != nil {
		return "", fmt.Errorf("merge into survivor: %w", err)
	}

	// 3. Apply staged inbound-link rewrites (each atomic).
	for _, rw := range staged {
		if err := v.writeRaw(rw.path, rw.content); err != nil {
			return "", fmt.Errorf("merge rewrite links in %q (survivor updated; some links may need repair): %w", rw.path, err)
		}
	}

	// 4. Remove the original loser (content now in survivor block + archive).
	if err := os.Remove(loserAbs); err != nil {
		return "", fmt.Errorf("merge remove loser %q: %w", loser, err)
	}
	return RelNoExt(survivor), nil
}

// chooseSurvivor returns (survivor, loser): more inbound links wins; ties break to
// the more recently modified note, then the lexically-first path.
func (v *FS) chooseSurvivor(ctx context.Context, a, b string) (survivor, loser string) {
	ca, cb := v.inboundCount(ctx, a), v.inboundCount(ctx, b)
	if ca != cb {
		if ca > cb {
			return a, b
		}
		return b, a
	}
	ta, tb := v.modTime(a), v.modTime(b)
	if !ta.Equal(tb) {
		if ta.After(tb) {
			return a, b
		}
		return b, a
	}
	if a <= b {
		return a, b
	}
	return b, a
}

// inboundCount counts wikilinks/embeds across the vault (excluding the note
// itself) that resolve to target. It reuses rewriteLinksForMove's resolution
// logic by rewriting target→target (a no-op that still returns the match count).
func (v *FS) inboundCount(ctx context.Context, target string) int {
	paths, err := v.List(ctx)
	if err != nil {
		return 0
	}
	self := filepath.ToSlash(target)
	count := 0
	for _, p := range paths {
		if p == self {
			continue
		}
		n, err := v.Read(ctx, p)
		if err != nil {
			continue
		}
		if _, c := rewriteLinksForMove(n.Body, target, target); c > 0 {
			count += c
		}
	}
	return count
}

// modTime is a note's mtime (zero on any error, so a missing stat loses ties).
func (v *FS) modTime(rel string) time.Time {
	abs, err := v.safeAbs(rel)
	if err != nil {
		return time.Time{}
	}
	info, err := os.Stat(abs)
	if err != nil {
		return time.Time{}
	}
	return info.ModTime()
}

// appendMerged adds the loser's body to the survivor's axon:merged managed block,
// accumulating after any existing merged content. Managed-block markers in the
// loser body are neutralized so they cannot corrupt the survivor's block.
func (v *FS) appendMerged(ctx context.Context, survivor, loser, loserBody string, now time.Time) error {
	n, err := v.Read(ctx, survivor)
	if err != nil {
		return err
	}
	existing := strings.TrimSpace(extractManagedBlock(n.Body, "merged"))
	section := fmt.Sprintf("### Merged from [[%s]] (%s)\n\n%s",
		RelNoExt(loser), now.Format("2006-01-02"), neutralizeMarkers(strings.TrimRight(loserBody, "\n")))
	content := section
	if existing != "" {
		content = existing + "\n\n" + section
	}
	return v.Patch(ctx, survivor, "merged", content)
}

// extractManagedBlock returns the inner content of an axon:<name> managed block,
// or "" if absent.
func extractManagedBlock(body, name string) string {
	start := "<!-- axon:" + name + ":start -->"
	end := "<!-- axon:" + name + ":end -->"
	_, after, found := strings.Cut(body, start)
	if !found {
		return ""
	}
	inner, _, found := strings.Cut(after, end)
	if !found {
		return ""
	}
	return inner
}

// neutralizeMarkers makes any axon managed-block comment markers inert (inserts a
// zero-width space after "axon") so appended loser content can't corrupt the
// survivor's block structure.
func neutralizeMarkers(text string) string {
	return strings.ReplaceAll(text, "<!-- axon:", "<!-- axon​:")
}
