package config

import (
	"strings"
	"testing"
)

func TestContentHashStableAndPrefixed(t *testing.T) {
	h := ContentHash("Hello world")
	if !strings.HasPrefix(h, "sha256:") {
		t.Errorf("hash %q missing sha256: prefix", h)
	}
	if h != ContentHash("Hello world") {
		t.Error("hash is not stable for identical input")
	}
	if ContentHash("Hello world") == ContentHash("Goodbye world") {
		t.Error("different content produced the same hash")
	}
}

func TestContentHashIgnoresFrontmatterAndManagedBlocks(t *testing.T) {
	withFM := "---\ntitle: X\nupdated: 2026-06-28\n---\nThe real prose.\n"
	withoutFM := "The real prose.\n"
	if ContentHash(withFM) != ContentHash(withoutFM) {
		t.Error("frontmatter should not affect the content hash")
	}

	clean := "Human prose here."
	withManaged := "Human prose here.\n<!-- axon:summary:start -->\nagent text\n<!-- axon:summary:end -->\n"
	if ContentHash(clean) != ContentHash(withManaged) {
		t.Error("axon:* managed blocks should not affect the content hash")
	}
}

func TestContentHashWhitespaceInsensitive(t *testing.T) {
	if ContentHash("a   b\n\nc") != ContentHash("a b c") {
		t.Error("whitespace normalisation failed")
	}
}
