# R7 — Near-duplicate merge proposals Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Sweep note embeddings for near-duplicate pairs, propose merges to the review queue, and on accept merge them wikilink-safely without ever deleting anything.

**Architecture:** A new zero-model automation (`merge-proposals`) reuses the resurfacer's vector primitives to write `merge [[a]] + [[b]]` lines to `.axon/review-queue.md`. A new review kind `merge` resolves through a new `vault.Merge` primitive (the ADR-032 destructive-op core): pick the more inbound-linked survivor, archive the loser to `.trash/merged/` intact, append the loser's body to the survivor's `axon:merged` managed block, retarget all inbound links to the survivor, then remove the original loser.

**Tech Stack:** Go 1.26; `modernc.org/sqlite`; existing `internal/vault`, `internal/review`, `internal/automations`, `internal/config`, `internal/db`, `internal/core` packages.

## Global Constraints

- Go 1.26+; `gofmt`/`goimports` clean; `go vet` + `golangci-lint` green (run `env -u FORCE_COLOR go test ./...` — the ambient shell exports `FORCE_COLOR=3`).
- **Cardinal rule 1:** no Claude call bypasses the token manager. R7 makes **zero** model calls anywhere — no chokepoint interaction at all.
- **Cardinal rule 2:** no vault mutation that isn't wikilink-safe. Merge retargets inbound links, appends only into a managed block, and **never deletes** (archive to `.trash/`). No raw `fs` writes outside the vault helpers.
- **S8:** all-off still runs and is useful — `merge-proposals` ships **disabled by default** in both profile templates.
- **S9:** the vault rebuilds the DB, never the reverse — no new DB table; detection reads derived vectors only.
- Nothing hardcoded that belongs in config: `merge.threshold` (default 0.92) and `merge.max_proposals` (default 5) come from config.
- **Provisional IDs:** FR-154 (detection), FR-155 (merge accept / `vault.Merge`), FR-156 (config + doctor + default-off); **ADR-032**. Current maxima before this slice: FR-153, ADR-031.
- Commit after each task with `Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>`. Do **not** use `git commit --amend` or `rm -rf` (the ambient GateGuard hook blocks both); fix forward.

## File structure

- **Create** `internal/vault/merge.go` — the `Merge` primitive + helpers (`chooseSurvivor`, `inboundCount`, `modTime`, `appendMerged`, `extractManagedBlock`, `neutralizeMarkers`). Test: `internal/vault/merge_test.go`.
- **Modify** `internal/review/review.go` — new `mergeRe`, `Load` case, `Accept` `case "merge"`, `Kind` doc. Test: `internal/review/merge_test.go`.
- **Modify** `internal/config/types.go` — `MergeConfig` struct + `Merge` field on `Profile` + `MergeThresholdOr()`/`MergeMaxProposalsOr()`. **Modify** `internal/config/starter.go` — add `merge-proposals` disabled automation line + a `merge:` block. Test: `internal/config/merge_test.go`.
- **Create** `internal/automations/dedup.go` — the `MergeProposals` automation. **Modify** `internal/automations/registry.go` (register) and `internal/automations/catalog.go` (description). Test: `internal/automations/dedup_test.go`.
- **Modify** `internal/core/doctor.go` — `mergeCheck` + wire into the checks list. Test: extend `internal/core/*doctor*_test.go` (new `merge_doctor_test.go`).
- **Modify** docs: `docs/03-requirements.md`, `docs/02-architecture.md` (ADR-032), `docs/06-component-automation-engine.md`, `docs/15-roadmap-1.2.md`, `README.md`.

---

## Task 1: `vault.Merge` primitive (ADR-032 destructive-op core)

**Files:**
- Create: `internal/vault/merge.go`
- Test: `internal/vault/merge_test.go`

**Interfaces:**
- Consumes (existing, same package): `(*FS).safeAbs`, `(*FS).Read`, `(*FS).Patch`, `(*FS).Exists`, `(*FS).writeRaw`, `(*FS).List`, `rewriteLinksForMove(body, from, to string) (string, int)`, `splitFrontmatter`, `reassemble`, `RelNoExt(rel string) string`.
- Produces: `func (v *FS) Merge(ctx context.Context, a, b string) (survivor string, err error)` — `a`/`b` are vault-relative note paths **with** `.md`; returns the survivor path **without** extension. Used by Task 2.

- [ ] **Step 1: Write the failing test**

Create `internal/vault/merge_test.go`:

```go
package vault

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// write is a raw note-file helper for merge tests.
func write(t *testing.T, v *FS, rel, content string) {
	t.Helper()
	abs := filepath.Join(v.Root(), filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestMergeSurvivorByInboundLinks(t *testing.T) {
	v := NewFS(t.TempDir())
	// dup-b has more inbound links (referrer1 + referrer2) than dup-a (none),
	// so dup-b must survive.
	write(t, v, "notes/dup-a.md", "# Dup A\n\nUnique A prose.\n")
	write(t, v, "notes/dup-b.md", "# Dup B\n\nUnique B prose.\n")
	write(t, v, "refs/referrer1.md", "See [[dup-b]] for details.\n")
	write(t, v, "refs/referrer2.md", "Also [[dup-b]] here.\n")

	survivor, err := v.Merge(context.Background(), "notes/dup-a.md", "notes/dup-b.md")
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if survivor != "notes/dup-b" {
		t.Fatalf("survivor = %q, want notes/dup-b", survivor)
	}
	// Loser file gone from the live vault, present in the archive.
	if v.Exists("notes/dup-a.md") {
		t.Fatal("loser still in live vault")
	}
	if !v.Exists(".trash/merged/dup-a.md") {
		t.Fatal("loser not archived to .trash/merged/")
	}
	// Survivor gained the loser body in its axon:merged block.
	n, err := v.Read(context.Background(), "notes/dup-b.md")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(n.Body, "Merged from [[notes/dup-a]]") {
		t.Fatalf("survivor missing merged header:\n%s", n.Body)
	}
	if !strings.Contains(n.Body, "Unique A prose.") {
		t.Fatalf("survivor missing loser content:\n%s", n.Body)
	}
	// Inbound links to the loser were retargeted to the survivor — none dangle.
	if bodyOf(t, v, "refs/referrer1.md") != "See [[notes/dup-b]] for details.\n" {
		t.Fatalf("referrer1 not retargeted:\n%s", bodyOf(t, v, "refs/referrer1.md"))
	}
}

func bodyOf(t *testing.T, v *FS, rel string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(v.Root(), filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestMergeArchivesLoserBytesExactly(t *testing.T) {
	v := NewFS(t.TempDir())
	loserContent := "---\ntitle: A\n---\n\n# Dup A\n\nExact bytes.\n"
	write(t, v, "a.md", loserContent)
	write(t, v, "b.md", "# Dup B\n\nProse.\n")
	// No inbound links → tie broken by recency then path; a<b so "a" survives,
	// making "b" the loser. Assert b's bytes archived exactly.
	if _, err := v.Merge(context.Background(), "a.md", "b.md"); err != nil {
		t.Fatal(err)
	}
	got := bodyOf(t, v, ".trash/merged/b.md")
	if got != "# Dup B\n\nProse.\n" {
		t.Fatalf("archived bytes = %q", got)
	}
}

func TestMergeNeutralizesManagedMarkers(t *testing.T) {
	v := NewFS(t.TempDir())
	// The loser body carries its own axon:links block; merged into the survivor
	// it must not corrupt the survivor's axon:merged block.
	write(t, v, "a.md", "# A\n\n<!-- axon:links:start -->\n- [[x]]\n<!-- axon:links:end -->\n")
	write(t, v, "keep.md", "# Keep\n")
	write(t, v, "ref.md", "[[keep]]\n") // keep has an inbound link → keep survives
	survivor, err := v.Merge(context.Background(), "a.md", "keep.md")
	if err != nil {
		t.Fatal(err)
	}
	if survivor != "keep" {
		t.Fatalf("survivor = %q, want keep", survivor)
	}
	n, _ := v.Read(context.Background(), "keep.md")
	// Exactly one axon:merged:start and one axon:merged:end — markers from the
	// loser body were neutralized, so the block parser sees a single clean region.
	if strings.Count(n.Body, "<!-- axon:merged:start -->") != 1 ||
		strings.Count(n.Body, "<!-- axon:merged:end -->") != 1 {
		t.Fatalf("merged block corrupted:\n%s", n.Body)
	}
	if strings.Contains(n.Body, "<!-- axon:links:start -->") {
		t.Fatalf("loser markers not neutralized:\n%s", n.Body)
	}
}

func TestMergeRefusesBadInput(t *testing.T) {
	v := NewFS(t.TempDir())
	write(t, v, "a.md", "# A\n")
	cases := map[string][2]string{
		"same note":  {"a.md", "a.md"},
		"missing":    {"a.md", "gone.md"},
		"not md":     {"a.md", "b.txt"},
	}
	for name, pair := range cases {
		if _, err := v.Merge(context.Background(), pair[0], pair[1]); err == nil {
			t.Fatalf("%s: expected error, got nil", name)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `env -u FORCE_COLOR go test ./internal/vault/ -run TestMerge -v`
Expected: FAIL — `v.Merge undefined`.

- [ ] **Step 3: Write minimal implementation**

Create `internal/vault/merge.go`:

```go
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
	i := strings.Index(body, start)
	if i < 0 {
		return ""
	}
	rest := body[i+len(start):]
	j := strings.Index(rest, end)
	if j < 0 {
		return ""
	}
	return rest[:j]
}

// neutralizeMarkers makes any axon managed-block comment markers inert (inserts a
// zero-width space after "axon") so appended loser content can't corrupt the
// survivor's block structure.
func neutralizeMarkers(text string) string {
	return strings.ReplaceAll(text, "<!-- axon:", "<!-- axon​:")
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `env -u FORCE_COLOR go test ./internal/vault/ -run TestMerge -v`
Expected: PASS (all four tests).

- [ ] **Step 5: Commit**

```bash
git add internal/vault/merge.go internal/vault/merge_test.go
git commit -m "feat(R7): vault.Merge wikilink-safe near-duplicate merge (FR-155, ADR-032)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 2: review `merge` kind + Accept case

**Files:**
- Modify: `internal/review/review.go` (regex block ~line 46-56; `Kind` doc ~line 37; `Load` switch ~line 93-119; `Accept` switch ~line 138-155)
- Test: `internal/review/merge_test.go`

**Interfaces:**
- Consumes: `vault.(*FS).Merge(ctx, a, b) (string, error)` (Task 1); existing `Load`, `Accept`, `Dismiss`, `mark`.
- Produces: review items with `Kind:"merge"`, `Note:a`, `Target:b`; `Accept` resolves them via `vault.Merge`.

- [ ] **Step 1: Write the failing test**

Create `internal/review/merge_test.go`:

```go
package review

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jandro-es/axon/internal/vault"
)

func writeNote(t *testing.T, v *vault.FS, rel, content string) {
	t.Helper()
	abs := filepath.Join(v.Root(), filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestMergeItemLoadAndAccept(t *testing.T) {
	v := vault.NewFS(t.TempDir())
	writeNote(t, v, "a.md", "# A\n\nA prose.\n")
	writeNote(t, v, "b.md", "# B\n\nB prose.\n")
	writeNote(t, v, "ref.md", "[[b]]\n") // b has an inbound link → b survives
	if err := v.Append(".axon/review-queue.md",
		"## Near-duplicate merges\n- [ ] merge [[a]] + [[b]] (sim 0.94)\n"); err != nil {
		t.Fatal(err)
	}

	items, err := Load(context.Background(), v)
	if err != nil {
		t.Fatal(err)
	}
	var it Item
	for _, i := range items {
		if i.Kind == "merge" {
			it = i
		}
	}
	if it.Kind != "merge" || it.Note != "a" || it.Target != "b" {
		t.Fatalf("parsed item = %+v, want merge a/b", it)
	}

	got, err := Accept(context.Background(), v, it.ID)
	if err != nil {
		t.Fatalf("Accept: %v", err)
	}
	if !strings.Contains(got.Line, "✓ merged into [[b]]") {
		t.Fatalf("resolution line = %q", got.Line)
	}
	if v.Exists("a.md") {
		t.Fatal("loser a.md still present after merge accept")
	}
	if !v.Exists(".trash/merged/a.md") {
		t.Fatal("loser not archived")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `env -u FORCE_COLOR go test ./internal/review/ -run TestMergeItem -v`
Expected: FAIL — the line parses as `info`, so no `merge` item is found (`it.Kind` empty).

- [ ] **Step 3: Write minimal implementation**

In `internal/review/review.go`:

Add to the `var (...)` regex block (after `contradictsRe`):

```go
	mergeRe       = regexp.MustCompile(`^merge \[\[([^\]]+)\]\] \+ \[\[([^\]]+)\]\]`)
```

Update the `Kind` field doc comment (line ~37) to include `merge`:

```go
	Kind    string   `json:"kind"` // link | pair | triage | resurface | contradicts | merge | reconcile | info
```

Add a case to the `Load` switch (after the `contradictsRe` case, before `reconcileRe`):

```go
		case mergeRe.MatchString(body):
			mm := mergeRe.FindStringSubmatch(body)
			it.Kind, it.Note, it.Target = "merge", mm[1], mm[2]
```

Add a case to the `Accept` switch (after the `reconcile` case, before `default`):

```go
	case "merge":
		survivor, merr := v.Merge(ctx, it.Note+".md", it.Target+".md")
		if merr != nil {
			return Item{}, merr
		}
		suffix = "✓ merged into [[" + survivor + "]]"
```

- [ ] **Step 4: Run test to verify it passes**

Run: `env -u FORCE_COLOR go test ./internal/review/ -v`
Expected: PASS (new test + existing review tests unaffected).

- [ ] **Step 5: Commit**

```bash
git add internal/review/review.go internal/review/merge_test.go
git commit -m "feat(R7): review merge kind resolves via vault.Merge (FR-155)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 3: config `merge` block

**Files:**
- Modify: `internal/config/types.go` (add `Merge` field on `Profile` near line 48-50; add `MergeConfig` struct + accessors near the `ResurfacingConfig` block ~line 384-410)
- Modify: `internal/config/starter.go` (automations map ~line 93-94; add a `merge:` block near `memory:` ~line 96)
- Test: `internal/config/merge_test.go`

**Interfaces:**
- Produces: `config.Profile.Merge config.MergeConfig`; `(MergeConfig).ThresholdOr() float64` (default 0.92); `(MergeConfig).MaxProposalsOr() int` (default 5). Used by Task 4 (`rc.Config.Merge...`) and Task 5 (doctor).

- [ ] **Step 1: Write the failing test**

Create `internal/config/merge_test.go`:

```go
package config

import "testing"

func TestMergeConfigDefaults(t *testing.T) {
	var m MergeConfig // zero value
	if got := m.ThresholdOr(); got != 0.92 {
		t.Fatalf("ThresholdOr default = %v, want 0.92", got)
	}
	if got := m.MaxProposalsOr(); got != 5 {
		t.Fatalf("MaxProposalsOr default = %v, want 5", got)
	}
	m = MergeConfig{Threshold: 0.8, MaxProposals: 3}
	if m.ThresholdOr() != 0.8 || m.MaxProposalsOr() != 3 {
		t.Fatalf("explicit values not returned: %+v", m)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `env -u FORCE_COLOR go test ./internal/config/ -run TestMergeConfig -v`
Expected: FAIL — `MergeConfig undefined`.

- [ ] **Step 3: Write minimal implementation**

In `internal/config/types.go`, add the field to `Profile` (beside `Resurfacing` at line ~50):

```go
	// Merge tunes the R7 near-duplicate merge-proposals sweep (FR-154…156).
	Merge MergeConfig `yaml:"merge"`
```

Add the struct + accessors after the `ResurfacingConfig` accessors (after line ~409):

```go
// MergeConfig tunes the near-duplicate merge-proposals sweep (R7). Zero values
// take the documented defaults. Detection is zero-model; accept never deletes.
type MergeConfig struct {
	// Threshold is the minimum mean-vector cosine for a pair to be proposed as a
	// near-duplicate. 0 → default 0.92 (a far higher bar than resurfacing's 0.75).
	Threshold float64 `yaml:"threshold,omitempty" validate:"omitempty,gt=0,lte=1"`
	// MaxProposals caps merge proposals emitted per run. 0 → default 5.
	MaxProposals int `yaml:"max_proposals,omitempty" validate:"omitempty,gte=0"`
}

// ThresholdOr returns the configured near-duplicate cosine floor, default 0.92.
func (m MergeConfig) ThresholdOr() float64 {
	if m.Threshold <= 0 {
		return 0.92
	}
	return m.Threshold
}

// MaxProposalsOr returns the per-run proposal cap, default 5.
func (m MergeConfig) MaxProposalsOr() int {
	if m.MaxProposals <= 0 {
		return 5
	}
	return m.MaxProposals
}
```

In `internal/config/starter.go`, add to the automations map (after the `project-pulse:` line ~94):

```go
      merge-proposals:   { enabled: false, schedule: "0 11 * * 1",      model: none,      budget_tokens: 0 }
```

And add a `merge:` block after the `memory:` block (or beside `resurfacing` if present in the template; if not present, add near `memory:` ~line 96):

```go
    merge:
      threshold: 0.92
      max_proposals: 5
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `env -u FORCE_COLOR go test ./internal/config/ -v`
Expected: PASS (new test + the starter config still parses/validates — the `Parse`/validate tests exercise `Starter` output).

- [ ] **Step 5: Commit**

```bash
git add internal/config/types.go internal/config/starter.go internal/config/merge_test.go
git commit -m "feat(R7): merge config block, disabled by default (FR-156)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 4: detection automation `merge-proposals`

**Files:**
- Create: `internal/automations/dedup.go`
- Modify: `internal/automations/registry.go` (add to the `reg` map ~line 18-36)
- Modify: `internal/automations/catalog.go` (add a description entry)
- Test: `internal/automations/dedup_test.go`

**Interfaces:**
- Consumes: `db.CountVectors`, `db.NotesUpdatedSince(ctx, DB, "0001-01-01", N)`, `db.NoteMeanVectors(ctx, DB, present)`, `db.Cosine`, `db.NoteStamp{ID,Path,Updated}`; `scannableNote(path) bool`; `pairKey(a,b) string`; `stripExt`; `loadProposalMemory`/`saveProposalMemory`; `review.Load`; the `Automation` interface + `RunCtx`.
- Produces: `MergeProposals` automation, `Name()=="merge-proposals"`.

- [ ] **Step 1: Write the failing test**

Create `internal/automations/dedup_test.go`. Follow the resurfacer test setup (real in-memory SQLite migrated via the db test helper + seeded chunk vectors + a real temp vault). If a shared seeding helper exists in `proactive_test.go` (e.g. seeding notes with vectors), reuse it; otherwise this test seeds directly:

```go
package automations

import (
	"context"
	"strings"
	"testing"
)

func TestMergeProposalsEmitsNearDuplicatePair(t *testing.T) {
	rc := newVectorTestCtx(t) // helper: migrated DB + vault + logger (see below)
	// Seed two near-identical notes (cosine ~1.0) and one unrelated note.
	seedNoteWithVector(t, rc, "notes/alpha.md", "# Alpha\n\nShared body.\n", []float32{1, 0, 0})
	seedNoteWithVector(t, rc, "notes/alpha-copy.md", "# Alpha copy\n\nShared body.\n", []float32{1, 0, 0})
	seedNoteWithVector(t, rc, "notes/other.md", "# Other\n\nDifferent.\n", []float32{0, 1, 0})

	res, err := MergeProposals{}.Run(context.Background(), rc)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(res.Changes) != 1 {
		t.Fatalf("changes = %v, want 1 pair", res.Changes)
	}
	if !strings.HasPrefix(res.Changes[0], "merge [[") ||
		!strings.Contains(res.Changes[0], "alpha") {
		t.Fatalf("unexpected proposal line: %q", res.Changes[0])
	}
	// Second run: the pair is now pending in the queue → not re-proposed.
	res2, err := MergeProposals{}.Run(context.Background(), rc)
	if err != nil {
		t.Fatal(err)
	}
	if len(res2.Changes) != 0 {
		t.Fatalf("second run re-proposed a pending pair: %v", res2.Changes)
	}
}

func TestMergeProposalsDryRunWritesNothing(t *testing.T) {
	rc := newVectorTestCtx(t)
	rc.DryRun = true
	seedNoteWithVector(t, rc, "a.md", "# A\n", []float32{1, 0, 0})
	seedNoteWithVector(t, rc, "b.md", "# B\n", []float32{1, 0, 0})
	res, err := MergeProposals{}.Run(context.Background(), rc)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Changes) != 1 {
		t.Fatalf("dry-run should still compute 1 pair, got %v", res.Changes)
	}
	if rc.Vault.Exists(".axon/review-queue.md") {
		t.Fatal("dry-run wrote the queue")
	}
}
```

> **Note for the implementer:** `newVectorTestCtx` and `seedNoteWithVector` are test helpers. Check `proactive_test.go` / `standard_test.go` for an existing equivalent (the resurfacer tests already seed notes with mean vectors) and reuse it — do NOT duplicate. If none is directly reusable, add a small local helper in `dedup_test.go` that: opens an in-memory DB via the package's existing migrated-DB helper, inserts a note row + one chunk row with an encoded vector (`db.EncodeVector`), and returns a `RunCtx` with `DB`, `Vault`, `Config`, `Log`, and a fixed `Now`. Mirror the exact helper names used by `proactive_test.go`.

- [ ] **Step 2: Run test to verify it fails**

Run: `env -u FORCE_COLOR go test ./internal/automations/ -run TestMergeProposals -v`
Expected: FAIL — `MergeProposals undefined`.

- [ ] **Step 3: Write minimal implementation**

Create `internal/automations/dedup.go`:

```go
package automations

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/jandro-es/axon/internal/db"
	"github.com/jandro-es/axon/internal/review"
)

const mergeProposalState = "merge-proposals/proposed"

// MergeProposals sweeps note mean-vectors for near-duplicate pairs and proposes
// merges to the review queue (R7, FR-154). Zero model calls: the vectors already
// exist and the cosine IS the rationale. Accepting a proposal runs the wikilink-
// safe vault.Merge (never a delete); this automation only surfaces candidates.
type MergeProposals struct{}

func (MergeProposals) Name() string    { return "merge-proposals" }
func (MergeProposals) Essential() bool { return false }

func (MergeProposals) DetectChange(ctx context.Context, rc RunCtx) (Change, error) {
	n, err := db.CountVectors(ctx, rc.DB)
	if err != nil {
		return Change{}, err
	}
	if n == 0 {
		return Change{Changed: false, Reason: "no embeddings yet"}, nil
	}
	year, week := rc.now().UTC().ISOWeek()
	cursor := fmt.Sprintf("merge:%d:%d-%d", n, year, week)
	if cursor == rc.LastCursor {
		return Change{Changed: false, Reason: "no new embeddings this week"}, nil
	}
	return Change{Changed: true, Reason: "embeddings or week changed", Cursor: cursor}, nil
}

func (MergeProposals) Run(ctx context.Context, rc RunCtx) (RunResult, error) {
	threshold := rc.Config.Merge.ThresholdOr()
	maxProps := rc.Config.Merge.MaxProposalsOr()

	// All scannable notes (the all-notes idiom: since 0001-01-01).
	all, err := db.NotesUpdatedSince(ctx, rc.DB, "0001-01-01", 5000)
	if err != nil {
		return RunResult{}, err
	}
	var notes []db.NoteStamp
	present := map[int64]bool{}
	for _, n := range all {
		if !scannableNote(n.Path) {
			continue
		}
		notes = append(notes, n)
		present[n.ID] = true
	}
	if len(notes) < 2 {
		return RunResult{Summary: "merge-proposals: fewer than 2 scannable notes"}, nil
	}

	means, err := db.NoteMeanVectors(ctx, rc.DB, present)
	if err != nil {
		return RunResult{}, err
	}

	// Pending pairs already in the queue — never duplicate.
	pending := map[string]bool{}
	if items, lerr := review.Load(ctx, rc.Vault); lerr == nil {
		for _, it := range items {
			if it.Checked || it.Kind != "merge" {
				continue
			}
			pending[pairKey(it.Note, it.Target)] = true
		}
	}
	// Dismissed pairs — proposal memory suppresses re-nagging.
	proposed := loadProposalMemory(ctx, rc, mergeProposalState)

	type cand struct {
		a, b string
		key  string
		sim  float64
	}
	var cands []cand
	for i := 0; i < len(notes); i++ {
		vi, ok := means[notes[i].ID]
		if !ok {
			continue
		}
		for j := i + 1; j < len(notes); j++ {
			vj, ok := means[notes[j].ID]
			if !ok {
				continue
			}
			sim := db.Cosine(vi, vj)
			if sim < threshold {
				continue
			}
			a, b := stripExt(notes[i].Path), stripExt(notes[j].Path)
			key := pairKey(a, b)
			if pending[key] || proposed[key] {
				continue
			}
			cands = append(cands, cand{a: a, b: b, key: key, sim: sim})
		}
	}
	sort.Slice(cands, func(i, j int) bool { return cands[i].sim > cands[j].sim })
	if len(cands) > maxProps {
		cands = cands[:maxProps]
	}

	var changes, queue []string
	for _, c := range cands {
		a, b := c.a, c.b
		if b < a {
			a, b = b, a // stable lexical rendering
		}
		line := fmt.Sprintf("merge [[%s]] + [[%s]] (sim %.2f)", a, b, c.sim)
		changes = append(changes, line)
		queue = append(queue, "- [ ] "+line)
	}

	if rc.DryRun {
		return RunResult{Summary: fmt.Sprintf("would propose %d merge(s)", len(changes)), Changes: changes}, nil
	}
	if len(queue) > 0 {
		header := fmt.Sprintf("\n## Near-duplicate merges (%s)\n", rc.now().UTC().Format("2006-01-02 15:04"))
		if aerr := rc.Vault.Append(".axon/review-queue.md", header+strings.Join(queue, "\n")+"\n"); aerr != nil {
			return RunResult{}, aerr
		}
		for _, c := range cands {
			proposed[c.key] = true
		}
		saveProposalMemory(ctx, rc, mergeProposalState, proposed)
	}
	return RunResult{Summary: fmt.Sprintf("proposed %d merge(s)", len(changes)), Changes: changes}, nil
}
```

In `internal/automations/registry.go`, add to the `reg` map:

```go
		MergeProposals{}.Name():   MergeProposals{},
```

In `internal/automations/catalog.go`, add the description (keyed `"merge-proposals"`):

```go
	"merge-proposals":    "Weekly near-duplicate sweep (R7): proposes note merges to the review queue by mean-vector cosine (zero-model). Accepting merges wikilink-safely — survivor keeps prose + gains the loser's content, inbound links retarget, the loser is archived to .trash/, never deleted.",
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `env -u FORCE_COLOR go test ./internal/automations/ -v`
Expected: PASS. **Adjust the registry count assertion** in `registry_test.go` (want-list +`merge-proposals`) — one new automation. The MCP tool-count asserts are untouched (no new MCP tool).

- [ ] **Step 5: Commit**

```bash
git add internal/automations/dedup.go internal/automations/registry.go internal/automations/catalog.go internal/automations/dedup_test.go internal/automations/registry_test.go
git commit -m "feat(R7): merge-proposals near-duplicate sweep automation (FR-154)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 5: doctor `mergeCheck`

**Files:**
- Modify: `internal/core/doctor.go` (add `mergeCheck`; wire into the checks list near the `resurfaceCheck` append ~line 116)
- Test: `internal/core/merge_doctor_test.go`

**Interfaces:**
- Consumes: `config.Profile.Merge`, `config.Profile.Automations`, `MergeConfig.ThresholdOr/MaxProposalsOr` (Task 3); `Check{Name,Status,Detail}` + `StatusOK`.
- Produces: `mergeCheck(p config.Profile) Check`.

- [ ] **Step 1: Write the failing test**

Create `internal/core/merge_doctor_test.go`:

```go
package core

import (
	"strings"
	"testing"

	"github.com/jandro-es/axon/internal/config"
)

func TestMergeCheck(t *testing.T) {
	off := mergeCheck(config.Profile{})
	if off.Status != StatusOK || !strings.Contains(off.Detail, "off") {
		t.Fatalf("disabled: %+v", off)
	}
	on := mergeCheck(config.Profile{
		Automations: map[string]config.Automation{"merge-proposals": {Enabled: true}},
	})
	if on.Status != StatusOK || !strings.Contains(on.Detail, "0.92") {
		t.Fatalf("enabled: %+v", on)
	}
}
```

> **Note:** confirm the `config.Automation` field name for enablement (`Enabled bool`) and the `Check` struct/`StatusOK` spelling from `doctor.go` before running — mirror exactly what `resurfaceCheck` uses.

- [ ] **Step 2: Run test to verify it fails**

Run: `env -u FORCE_COLOR go test ./internal/core/ -run TestMergeCheck -v`
Expected: FAIL — `mergeCheck undefined`.

- [ ] **Step 3: Write minimal implementation**

In `internal/core/doctor.go`, add after `resurfaceCheck`:

```go
// mergeCheck reports the R7 near-duplicate merge-proposals sweep. Advisory
// (always StatusOK): the sweep is zero-model and disabled by default; accept is
// wikilink-safe and never deletes.
func mergeCheck(p config.Profile) Check {
	const name = "merge"
	auto, ok := p.Automations["merge-proposals"]
	if !ok || !auto.Enabled {
		return Check{name, StatusOK, "merge-proposals off (near-duplicate sweep; enable in automations to propose merges)"}
	}
	return Check{name, StatusOK, fmt.Sprintf("merge-proposals active (cosine ≥ %.2f, ≤%d proposals/run; accept archives to .trash, never deletes)",
		p.Merge.ThresholdOr(), p.Merge.MaxProposalsOr())}
}
```

Wire it into the checks list (beside the `resurfaceCheck` append ~line 116, in the same embedding-checked block):

```go
			checks = append(checks, mergeCheck(p))
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `env -u FORCE_COLOR go test ./internal/core/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/core/doctor.go internal/core/merge_doctor_test.go
git commit -m "feat(R7): doctor merge check (FR-156)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 6: docs — FR rows, ADR-032, roadmap, component, README

**Files:**
- Modify: `docs/03-requirements.md` (FR-154/155/156 rows)
- Modify: `docs/02-architecture.md` (ADR-032)
- Modify: `docs/06-component-automation-engine.md` (`merge-proposals` entry)
- Modify: `docs/15-roadmap-1.2.md` (R7 marked built; net-new slate complete)
- Modify: `README.md` (automation count bump)

- [ ] **Step 1: Add FR rows to `docs/03-requirements.md`**

Match the existing row format (find the FR-151..153 rows for the template). Add:

- **FR-154** — merge-proposals sweep: a weekly zero-model automation proposes near-duplicate note pairs (mean-vector cosine ≥ `merge.threshold`, default 0.92) to the review queue, deduped against pending items and dismissed-pair proposal memory, capped at `merge.max_proposals`. Disabled by default.
- **FR-155** — merge accept: a `merge` review item resolves through `vault.Merge` — survivor by inbound-link centrality keeps its prose and gains the loser's body in its `axon:merged` block; inbound links retarget to the survivor; the loser is archived intact to `.trash/merged/` and never deleted (zero broken links, both originals recoverable).
- **FR-156** — merge config + doctor: `merge{threshold,max_proposals}` config, validated; advisory `doctor` merge check; default-off (S8).

- [ ] **Step 2: Add ADR-032 to `docs/02-architecture.md`**

Follow the ADR format used by ADR-031. Record: **the destructive-op design pass.** Context — near-duplicate merge is the closest thing to a destructive op AXON has. Decision — merge = retarget inbound links + preserve loser content in the survivor's managed block + archive the loser to `.trash/` (out of index, recoverable); survivor chosen by inbound-link centrality (ties → recency → path); zero model calls; user-approved through the review queue only (no MCP tool, no agent-driven merge). Consequences — nothing is ever deleted; the vault still rebuilds the DB (S9); disabled by default (S8).

- [ ] **Step 3: Add the automation entry to `docs/06-component-automation-engine.md`**

Add a `merge-proposals` entry beside the resurfacer's, describing the weekly zero-model sweep, the `merge [[a]] + [[b]]` queue line, and the wikilink-safe accept via `vault.Merge`.

- [ ] **Step 4: Mark R7 built in `docs/15-roadmap-1.2.md`**

Update the R7 section (lines ~141-149) and the build-order table row 6 with a **✅ BUILT 2026-07-10** marker + shipped summary (FR-154/155/156, ADR-032), and note the 1.2 net-new slate (R1,R2,R5,R7,R8,R9) is complete.

- [ ] **Step 5: Bump the README automation count**

Find the automation count in `README.md` (currently 18 after 1.1; +1 → 19) and update it.

- [ ] **Step 6: Full green gate + commit**

Run: `env -u FORCE_COLOR go test ./... && gofmt -l internal/ cmd/ && go vet ./... && golangci-lint run`
Expected: tests PASS, `gofmt -l` prints nothing, vet + lint clean.

```bash
git add docs/ README.md
git commit -m "docs(R7): FR-154/155/156, ADR-032, roadmap R7 built, automation entry

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Self-review notes

- **Spec coverage:** detection (Task 4 → FR-154), `vault.Merge` accept (Tasks 1+2 → FR-155), config+doctor+default-off (Tasks 3+5 → FR-156), ADR-032 + docs (Task 6). All spec sections mapped.
- **Zero-model guarantee:** no task touches `tokens.Manager`/`runModel` — cardinal rule 1 trivially satisfied.
- **Type consistency:** `vault.Merge(ctx, a, b) (string, error)` is defined in Task 1 and consumed with the same signature in Task 2; `MergeConfig.ThresholdOr()/MaxProposalsOr()` defined in Task 3, consumed in Tasks 4+5; `MergeProposals` name/type consistent across Tasks 4+5.
- **Live smoke (post-merge, before final push):** on a scratch vault with two near-identical seeded+embedded notes, run `axon run merge-proposals` (real Ollama needed for embeddings; if absent, the change-gate skips — cover the model-free path via the unit tests as prior slices did), then accept the queued item and confirm: loser in `.trash/merged/`, survivor `axon:merged` block populated, inbound links retargeted, `axon doctor` merge check both states. **Never touch the user's real :7777 daemon — use an isolated `AXON_HOME` + a spare port.**
