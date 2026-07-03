package automations

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
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
