package config

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strings"
)

// frontmatterRe matches a leading YAML frontmatter block (--- ... ---).
var frontmatterRe = regexp.MustCompile(`(?s)\A---\n.*?\n---\n?`)

// managedBlockRe matches AXON-managed body blocks so agent-maintained sections
// don't trigger false "changed" signals in the change-gate (docs/04 §4).
var managedBlockRe = regexp.MustCompile(`(?s)<!--\s*axon:[\w-]+:start\s*-->.*?<!--\s*axon:[\w-]+:end\s*-->`)

// wsRe collapses runs of whitespace to a single space for normalisation.
var wsRe = regexp.MustCompile(`\s+`)

// NormalizeBody strips frontmatter and axon:* managed blocks, then collapses
// whitespace. This is the canonical form hashed for change detection.
func NormalizeBody(body string) string {
	b := frontmatterRe.ReplaceAllString(body, "")
	b = managedBlockRe.ReplaceAllString(b, "")
	b = wsRe.ReplaceAllString(b, " ")
	return strings.TrimSpace(b)
}

// ContentHash returns a stable "sha256:<hex>" digest of a note body, normalised
// per NormalizeBody so that human prose drives the hash and agent-maintained
// sections do not. This is the backbone of the change-gate (FR-31).
func ContentHash(body string) string {
	sum := sha256.Sum256([]byte(NormalizeBody(body)))
	return "sha256:" + hex.EncodeToString(sum[:])
}
