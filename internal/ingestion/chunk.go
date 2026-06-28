package ingestion

import (
	"strings"

	"github.com/jandro-es/axon/internal/config"
)

// Chunking defaults (docs/05 §9): ~512-token chunks with ~64-token overlap.
const (
	defaultChunkTokens   = 512
	defaultOverlapTokens = 64
)

// Chunk is a piece of a source's cleaned text, with its content hash for
// per-chunk idempotency (only changed chunks are re-embedded, FR-24).
type Chunk struct {
	Ordinal     int
	Text        string
	TokenCount  int
	ContentHash string
}

// EstimateTokens approximates token count from character length (~4 chars/token).
// This is a local heuristic; the exact tokeniser is the token manager's concern
// in Phase 3. Good enough for sizing chunks and pre-flight bounds.
func EstimateTokens(s string) int {
	n := len([]rune(s))
	if n == 0 {
		return 0
	}
	return (n + 3) / 4
}

// ChunkText splits cleaned text into overlapping, paragraph-aligned chunks of
// roughly chunkTokens with overlapTokens of carry-over, so retrieval snippets
// keep context across boundaries. Zero/blank input yields no chunks.
func ChunkText(text string, chunkTokens, overlapTokens int) []Chunk {
	if chunkTokens <= 0 {
		chunkTokens = defaultChunkTokens
	}
	if overlapTokens < 0 || overlapTokens >= chunkTokens {
		overlapTokens = defaultOverlapTokens
	}
	paras := splitParagraphs(text)
	if len(paras) == 0 {
		return nil
	}

	var chunks []Chunk
	var cur []string
	curTokens := 0
	flush := func() {
		if len(cur) == 0 {
			return
		}
		body := strings.TrimSpace(strings.Join(cur, "\n\n"))
		if body == "" {
			return
		}
		chunks = append(chunks, Chunk{
			Ordinal:     len(chunks),
			Text:        body,
			TokenCount:  EstimateTokens(body),
			ContentHash: config.ContentHash(body),
		})
	}

	for _, p := range paras {
		pt := EstimateTokens(p)
		if curTokens > 0 && curTokens+pt > chunkTokens {
			flush()
			// Start the next chunk with an overlap tail of the previous one.
			cur, curTokens = overlapTail(cur, overlapTokens)
		}
		cur = append(cur, p)
		curTokens += pt
	}
	flush()
	return chunks
}

// DefaultChunks chunks text using the package defaults.
func DefaultChunks(text string) []Chunk {
	return ChunkText(text, defaultChunkTokens, defaultOverlapTokens)
}

// splitParagraphs breaks text on blank lines, dropping empties.
func splitParagraphs(text string) []string {
	rawParas := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n\n")
	var out []string
	for _, p := range rawParas {
		if s := strings.TrimSpace(p); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// overlapTail returns the trailing paragraphs of cur whose combined size is at
// most overlapTokens, to seed the next chunk for context continuity.
func overlapTail(cur []string, overlapTokens int) ([]string, int) {
	if overlapTokens == 0 {
		return nil, 0
	}
	var tail []string
	tokens := 0
	for i := len(cur) - 1; i >= 0; i-- {
		t := EstimateTokens(cur[i])
		if tokens+t > overlapTokens && len(tail) > 0 {
			break
		}
		tail = append([]string{cur[i]}, tail...)
		tokens += t
	}
	return tail, tokens
}
