package automations

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/jandro-es/axon/internal/db"
	"github.com/jandro-es/axon/internal/ingestion"
)

const (
	// inboxDir is the capture funnel folder (scaffold convention).
	inboxDir = "00-Inbox"
	// captureFailureState is the automation_state key for capture's failure
	// memory — a second row beside the engine-managed "capture" cursor row.
	captureFailureState = "capture:failures"
)

// inboxEntry is one top-level item in the inbox listing.
type inboxEntry struct {
	Name string
	IsMD bool
}

// listInboxDir enumerates top-level inbox files, skipping README*, dotfiles
// and subdirectories. It reads the filesystem directly because vault.List is
// markdown-only and capture must see dropped PDFs/binaries. This listing is
// also the NFR-05 sandbox: ONLY files enumerated here are ever ingested as
// local files — paths written inside notes are never file targets.
func listInboxDir(root string) ([]inboxEntry, error) {
	entries, err := os.ReadDir(filepath.Join(root, inboxDir))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // fresh vault: nothing to capture
		}
		return nil, fmt.Errorf("list inbox: %w", err)
	}
	var out []inboxEntry
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		stem := strings.TrimSuffix(name, filepath.Ext(name))
		if strings.EqualFold(stem, "README") {
			continue
		}
		out = append(out, inboxEntry{Name: name, IsMD: strings.EqualFold(filepath.Ext(name), ".md")})
	}
	return out, nil
}

// inboxFingerprint hashes the inbox listing (name + size + mtime) — the
// capture change gate. Deliberately does not read content: a tick over an
// unchanged inbox must be near-free.
func inboxFingerprint(root string) (string, error) {
	entries, err := listInboxDir(root)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	for _, e := range entries {
		st, err := os.Stat(filepath.Join(root, inboxDir, e.Name))
		if err != nil {
			continue // raced away between ReadDir and Stat; next tick catches it
		}
		fmt.Fprintf(h, "%s\x00%d\x00%d\n", e.Name, st.Size(), st.ModTime().UnixNano())
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// Capture is the FR-26 capture funnel (ADR-016): own-line URLs in inbox notes
// and files dropped into 00-Inbox are ingested through the pipeline on each
// tick; originals are archived wikilink-safely; failures are remembered.
// No model call of its own (enrichment goes through the chokepoint when
// capture.enrich is "claude").
type Capture struct{}

func (Capture) Name() string    { return "capture" }
func (Capture) Essential() bool { return false }

func (Capture) DetectChange(ctx context.Context, rc RunCtx) (Change, error) {
	fp, err := inboxFingerprint(rc.Vault.Root())
	if err != nil {
		return Change{}, err
	}
	if fp == rc.LastCursor {
		return Change{Changed: false, Reason: "inbox unchanged since last capture"}, nil
	}
	// A run that archives files changes the listing after this cursor was
	// computed, so the next tick runs once more and finds nothing new — DB
	// lookups only, no network. Accepted cost of the engine's cursor timing.
	return Change{Changed: true, Reason: "inbox changed", Cursor: fp}, nil
}

func (Capture) Run(ctx context.Context, rc RunCtx) (RunResult, error) {
	root := rc.Vault.Root()
	entries, err := listInboxDir(root)
	if err != nil {
		return RunResult{}, err
	}
	failures := loadCaptureFailures(ctx, rc)
	pl := capturePipeline(rc)

	var (
		changes                   []string
		queue                     []string
		captured, skipped, failed int
	)

	for _, e := range entries {
		if e.IsMD {
			note, rerr := rc.Vault.Read(ctx, inboxDir+"/"+e.Name)
			if rerr != nil {
				continue
			}
			for _, u := range extractCaptureURLs(note.Body) {
				if _, known := failures[u]; known {
					skipped++
					continue
				}
				if src, _ := db.GetSourceByURL(ctx, rc.DB, u); src != nil {
					skipped++ // already ingested: DB-only skip, no network
					continue
				}
				if rc.DryRun {
					changes = append(changes, "would ingest "+u)
					continue
				}
				res, ierr := pl.Ingest(ctx, u, ingestion.IngestOptions{})
				if ierr != nil {
					failed++
					failures[u] = captureErr(ierr, res.SkippedReason)
					queue = append(queue, fmt.Sprintf("- [ ] capture FAILED: %s — %s", u, failures[u]))
					continue
				}
				captured++
				changes = append(changes, fmt.Sprintf("%s → %s", u, res.NotePath))
				queue = append(queue, fmt.Sprintf("- [x] captured %s → [[%s]]", u, stripExt(res.NotePath)))
			}
			continue
		}

		// Dropped file. The NFR-05 sandbox: e.Name came from the physical
		// inbox listing; note contents never reach this path.
		abs := filepath.Join(root, inboxDir, e.Name)
		key, kerr := fileCaptureKey(abs)
		if kerr != nil {
			continue // raced away; next tick
		}
		if _, known := failures[key]; known {
			skipped++
			continue
		}
		if rc.DryRun {
			changes = append(changes, fmt.Sprintf("would ingest %s and archive the original", e.Name))
			continue
		}
		res, ierr := pl.Ingest(ctx, abs, ingestion.IngestOptions{AllowLocalFiles: true})
		if ierr != nil {
			failed++
			failures[key] = captureErr(ierr, res.SkippedReason)
			queue = append(queue, fmt.Sprintf("- [ ] capture FAILED: %s — %s", e.Name, failures[key]))
			continue
		}
		// ok / skipped(hash match) / redacted all mean the content is in the
		// knowledge base: archive the original either way so the inbox stays
		// an inbox.
		dest, merr := archiveInboxFile(ctx, rc, e.Name)
		if merr != nil {
			failed++
			failures[key] = "ingested but archive move failed: " + merr.Error()
			queue = append(queue, fmt.Sprintf("- [ ] capture FAILED: %s — %s", e.Name, failures[key]))
			continue
		}
		captured++
		changes = append(changes, fmt.Sprintf("%s → %s (original: %s)", e.Name, res.NotePath, dest))
		if res.NotePath != "" {
			queue = append(queue, fmt.Sprintf("- [x] captured %s → [[%s]] (original: %s)", e.Name, stripExt(res.NotePath), dest))
		} else {
			queue = append(queue, fmt.Sprintf("- [x] archived %s → %s (%s)", e.Name, dest, res.SkippedReason))
		}
	}

	if !rc.DryRun {
		if len(queue) > 0 {
			header := fmt.Sprintf("\n## Capture (%s)\n", rc.now().UTC().Format("2006-01-02 15:04"))
			if aerr := rc.Vault.Append(".axon/review-queue.md", header+strings.Join(queue, "\n")+"\n"); aerr != nil {
				rc.Log.Warn("capture: review queue append failed", "err", aerr)
			}
		}
		saveCaptureFailures(ctx, rc, failures)
	}

	return RunResult{
		Summary: fmt.Sprintf("captured %d, skipped %d, failed %d", captured, skipped, failed),
		Changes: changes,
	}, nil
}

// capturePipeline returns the pipeline to ingest with: a shallow copy of the
// shared one (never mutated), with the enricher per capture.enrich. "claude"
// goes through the token-manager chokepoint on the routine tier (cardinal
// rule 1); the default stays the pipeline's zero-token heuristic.
func capturePipeline(rc RunCtx) *ingestion.Pipeline {
	pl := *rc.Pipeline
	if rc.Config.Capture.EnrichMode() == "claude" {
		pl.Enricher = ingestion.ClaudeEnricher{Manager: rc.Manager, ModelKey: "routine"}
	}
	return &pl
}

// fileCaptureKey identifies a dropped file for failure memory: path + content
// hash, so an edited/replaced file is retried but an unchanged one is not.
func fileCaptureKey(abs string) (string, error) {
	data, err := os.ReadFile(abs)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return abs + "\x00" + hex.EncodeToString(sum[:])[:16], nil
}

// archiveInboxFile moves an ingested original to <archive>/<YYYY-MM>/<name>
// via the wikilink-safe vault move, suffixing -2, -3… on collision. Returns
// the vault-relative destination.
func archiveInboxFile(ctx context.Context, rc RunCtx, name string) (string, error) {
	month := rc.now().UTC().Format("2006-01")
	ext := filepath.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	for i := 1; i <= 100; i++ {
		candidate := stem
		if i > 1 {
			candidate = fmt.Sprintf("%s-%d", stem, i)
		}
		dest := fmt.Sprintf("%s/%s/%s%s", rc.Config.Capture.Archive(), month, candidate, ext)
		if rc.Vault.Exists(dest) {
			continue
		}
		if err := rc.Vault.Move(ctx, inboxDir+"/"+name, dest); err != nil {
			return "", err
		}
		return dest, nil
	}
	return "", fmt.Errorf("no free archive name for %q", name)
}

// loadCaptureFailures reads the failure memory (empty map on any problem —
// worst case a failed item is retried once).
func loadCaptureFailures(ctx context.Context, rc RunCtx) map[string]string {
	out := map[string]string{}
	raw, err := db.GetCursor(ctx, rc.DB, captureFailureState)
	if err != nil || raw == "" {
		return out
	}
	_ = json.Unmarshal([]byte(raw), &out)
	return out
}

// saveCaptureFailures persists the failure memory beside the engine cursor.
func saveCaptureFailures(ctx context.Context, rc RunCtx, failures map[string]string) {
	raw, err := json.Marshal(failures)
	if err != nil {
		return
	}
	if err := db.SetCursor(ctx, rc.DB, captureFailureState, string(raw), rc.now().UTC().Format(time.RFC3339)); err != nil {
		rc.Log.Warn("capture: persist failure memory", "err", err)
	}
}

// captureErr picks the most informative failure text.
func captureErr(err error, reason string) string {
	if err != nil {
		return err.Error()
	}
	if reason != "" {
		return reason
	}
	return "ingest failed"
}

// captureURLLine matches a URL standing alone on a (trimmed) line: either a
// bare http(s) URL or a single markdown link. Mid-sentence URLs are NOT
// capture requests (FR-26: deliberate paste, predictable trigger).
var captureURLLine = regexp.MustCompile(`^(?:(https?://\S+)|\[[^\]]*\]\((https?://[^)\s]+)\))$`)

// extractCaptureURLs returns the own-line URLs in a note body, deduplicated,
// in order of first appearance.
func extractCaptureURLs(body string) []string {
	var urls []string
	seen := map[string]bool{}
	for _, line := range strings.Split(body, "\n") {
		m := captureURLLine.FindStringSubmatch(strings.TrimSpace(line))
		if m == nil {
			continue
		}
		u := m[1]
		if u == "" {
			u = m[2]
		}
		if !seen[u] {
			seen[u] = true
			urls = append(urls, u)
		}
	}
	return urls
}
