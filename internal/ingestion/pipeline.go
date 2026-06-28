package ingestion

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/jandro-es/axon/internal/config"
	"github.com/jandro-es/axon/internal/db"
	"github.com/jandro-es/axon/internal/embeddings"
	"github.com/jandro-es/axon/internal/events"
	"github.com/jandro-es/axon/internal/vault"
)

// KnowledgeDir is where ingested source notes are written (docs/04).
const KnowledgeDir = "03-Resources/Knowledge"

// reviewQueuePath is the human-approval queue for link suggestions (docs/05 §8).
const reviewQueuePath = ".axon/review-queue.md"

// Pipeline turns external sources into clean, linked, retrievable notes. It
// makes NO Claude call in Phase 2: enrichment is deterministic (Heuristic). The
// Embedder may be nil or unreachable — chunks are still written and lexically
// searchable, with vectors marked pending (docs/05 §6).
type Pipeline struct {
	Vault    *vault.FS
	DB       *sql.DB
	Embedder embeddings.Provider
	Enricher Enricher
	Fetcher  Fetcher
	Policy   config.PolicyConfig
	Bus      *events.Bus
	Profile  string
}

// IngestOptions tune a single run.
type IngestOptions struct {
	DryRun     bool // do everything except write/embed; report intended note
	ApplyLinks bool // if true, suggestions are applied (default: queued for review)
	// AllowLocalFiles permits reading local files (file://, plain paths). It is
	// true for the user-initiated CLI `ingest`, but FALSE for agent-driven paths
	// (the MCP knowledge_ingest tool), so a prompt-injected agent cannot read
	// arbitrary host files (e.g. ~/.ssh/id_rsa) into the vault and the model.
	AllowLocalFiles bool
}

// IngestResult summarises a run for the CLI/MCP and the event stream.
type IngestResult struct {
	Status        string   `json:"status"` // ok | skipped | redacted | dry-run | failed
	Input         string   `json:"input"`
	NotePath      string   `json:"note_path,omitempty"`
	Title         string   `json:"title,omitempty"`
	Chunks        int      `json:"chunks"`
	Embedded      int      `json:"embedded"`
	Redacted      bool     `json:"redacted"`
	SkippedReason string   `json:"skipped_reason,omitempty"`
	Suggestions   []string `json:"suggestions,omitempty"`
}

// Ingest runs the full pipeline for one input. Errors are returned and also
// reflected as Status="failed"; a denied domain fails before any fetch.
func (p *Pipeline) Ingest(ctx context.Context, arg string, opts IngestOptions) (IngestResult, error) {
	in := ClassifyInput(arg)
	res := IngestResult{Input: arg, Status: "failed"}

	// Stage 1 — policy. URLs are gated by the egress allowlist; local files are
	// refused unless the caller explicitly allowed them (CLI only, never the
	// agent-driven MCP path) — an SSRF/local-file-read guard (NFR-05).
	switch in.Kind {
	case KindURL:
		if err := CheckIngestPolicy(p.Policy, in.Host); err != nil {
			return res, err
		}
	case KindFile, KindPDF:
		if !opts.AllowLocalFiles {
			return res, fmt.Errorf("local-file ingestion of %q is not permitted on this path (agent-driven ingestion is URL-only)", arg)
		}
	}

	// Stage 2 — fetch / read.
	doc, err := p.read(ctx, in)
	if err != nil {
		return res, err
	}

	// Stage 3+4 — extract main content and clean to Markdown.
	ex, err := p.extract(in, doc)
	if err != nil {
		return res, err
	}
	if strings.TrimSpace(ex.Markdown) == "" {
		return res, fmt.Errorf("ingest %q: empty extraction (nothing readable)", arg)
	}

	// Stage 5 — redact before anything is persisted or could reach a model.
	redactor, err := NewRedactor(p.Policy.RedactionRules)
	if err != nil {
		return res, err
	}
	cleaned, redacted := redactor.Redact(ex.Markdown)
	// Redact provenance fields too — a secret in an HTML <title>/byline would
	// otherwise reach the model (enrich prompt), the note frontmatter, the note
	// path slug, and the event stream unredacted.
	var rm bool
	ex.Title, rm = redactor.Redact(ex.Title)
	redacted = redacted || rm
	ex.Author, rm = redactor.Redact(ex.Author)
	redacted = redacted || rm
	ex.Date, rm = redactor.Redact(ex.Date)
	redacted = redacted || rm
	res.Redacted = redacted

	// Stage 6 — hash + idempotency.
	hash := config.ContentHash(cleaned)
	existing, err := db.GetSourceByURL(ctx, p.DB, in.URL)
	if err != nil {
		return res, err
	}
	if existing != nil && existing.ContentHash == hash {
		res.Status = "skipped"
		res.SkippedReason = "unchanged content (hash match)"
		p.emit(events.LevelInfo, "ingest.skip", fmt.Sprintf("skipped %s — unchanged", arg), res)
		return res, nil // NO enrich, NO embed, NO model call (FR-24/FR-31)
	}

	// Stage 7 — enrich (deterministic; grounded by a hybrid search of existing notes).
	related := p.relatedNotes(ctx, ex.Title+"\n"+cleaned)
	enr, err := p.Enricher.Enrich(ctx, EnrichInput{Title: ex.Title, Markdown: cleaned, Related: related})
	if err != nil {
		return res, err
	}
	if enr.Title == "" {
		enr.Title = "Untitled source"
	}
	res.Title = enr.Title
	res.Suggestions = enr.SuggestedLinks

	// Resolve the destination note path (stable across re-ingests).
	notePath, err := p.notePathFor(ctx, existing, enr.Title)
	if err != nil {
		return res, err
	}
	res.NotePath = notePath

	chunks := DefaultChunks(cleaned)
	res.Chunks = len(chunks)

	if opts.DryRun {
		res.Status = "dry-run"
		return res, nil
	}

	// Stage 8 — write the note (managed blocks; never clobbers human prose).
	if err := p.writeNote(ctx, notePath, enr, cleaned, ex, in, hash); err != nil {
		return res, err
	}

	// Stages 9–11 — persist note row, source, chunks, embed, index.
	embedded, status, err := p.persist(ctx, notePath, in, enr, cleaned, hash, chunks, redacted)
	if err != nil {
		return res, err
	}
	res.Embedded = embedded
	res.Status = status

	// Suggested links go to the review queue (human approves) unless applied.
	if len(enr.SuggestedLinks) > 0 && !opts.ApplyLinks {
		if err := p.appendReviewQueue(notePath, enr.SuggestedLinks); err != nil {
			p.emit(events.LevelWarn, "ingest.review_queue.fail",
				fmt.Sprintf("could not write link suggestions to the review queue: %v", err), nil)
		}
	}

	p.emit(events.LevelInfo, "ingest.done",
		fmt.Sprintf("ingested %s -> %s (%d chunks, %d embedded)", arg, notePath, res.Chunks, embedded), res)
	return res, nil
}

func (p *Pipeline) read(ctx context.Context, in Input) (*Document, error) {
	switch in.Kind {
	case KindURL:
		return p.Fetcher.Fetch(ctx, in.URL)
	case KindFile, KindPDF:
		// Both are local files; PDFs are parsed in the extract stage.
		return ReadFile(in.Path)
	default:
		return nil, fmt.Errorf("unsupported input kind %q", in.Kind)
	}
}

func (p *Pipeline) extract(in Input, doc *Document) (Extracted, error) {
	switch in.Kind {
	case KindURL:
		return ExtractHTML(doc.Body, in.URL)
	case KindPDF:
		return ExtractPDF(doc.Body, in.Path)
	default:
		return ExtractFile(doc.Body, in.Path), nil
	}
}

// relatedNotes runs a best-effort hybrid search to ground link suggestions in
// existing notes. Embedding the query is best-effort; if it fails, the search
// degrades to lexical-only.
func (p *Pipeline) relatedNotes(ctx context.Context, query string) []string {
	var qv []float32
	if p.Embedder != nil {
		probe := query
		if len(probe) > 2000 {
			probe = probe[:2000]
		}
		if vecs, err := p.Embedder.Embed(ctx, []string{probe}); err == nil && len(vecs) == 1 {
			qv = vecs[0]
		}
	}
	hits, err := db.HybridSearch(ctx, p.DB, db.SearchOpts{Query: query, QueryVector: qv, TopK: 5})
	if err != nil {
		return nil
	}
	seen := map[string]bool{}
	var paths []string
	for _, h := range hits {
		if h.Path == "" || seen[h.Path] {
			continue
		}
		seen[h.Path] = true
		paths = append(paths, h.Path)
	}
	return paths
}

// notePathFor returns the destination path: the existing note for a re-ingest,
// or a fresh unique slug under the Knowledge dir.
func (p *Pipeline) notePathFor(ctx context.Context, existing *db.SourceRow, title string) (string, error) {
	if existing != nil && existing.NoteID != nil {
		if path, err := db.GetNotePathByID(ctx, p.DB, *existing.NoteID); err == nil && path != "" {
			return path, nil
		}
	}
	base := slugify(title)
	if base == "" {
		base = "source"
	}
	candidate := KnowledgeDir + "/" + base + ".md"
	for i := 2; p.Vault.Exists(candidate); i++ {
		candidate = fmt.Sprintf("%s/%s-%d.md", KnowledgeDir, base, i)
	}
	return candidate, nil
}

// writeNote creates the source note, or updates only its managed blocks on
// re-ingest, preserving frontmatter and any human prose (cardinal rule 2).
func (p *Pipeline) writeNote(ctx context.Context, path string, enr Enrichment, cleaned string, ex Extracted, in Input, hash string) error {
	if p.Vault.Exists(path) {
		if err := p.Vault.Patch(ctx, path, "summary", enr.Summary); err != nil {
			return err
		}
		return p.Vault.Patch(ctx, path, "source", cleaned)
	}
	content := buildSourceNote(enr, cleaned, ex, in, hash)
	if _, err := p.Vault.Create(path, content); err != nil {
		return err
	}
	return nil
}

// persist writes the note row, source row, chunks (+FTS) and best-effort
// vectors, returning the number embedded and the source status.
func (p *Pipeline) persist(ctx context.Context, notePath string, in Input, enr Enrichment, cleaned, hash string, chunks []Chunk, redacted bool) (int, string, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	status := "ok"
	if redacted {
		status = "redacted"
	}

	tx, err := p.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, "failed", err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	noteID, err := db.UpsertNote(ctx, tx, db.NoteRow{
		Path: notePath, Title: enr.Title, Type: "source", Tags: enr.Tags,
		ContentHash: hash, WordCount: len(strings.Fields(cleaned)),
		Created: now, Updated: now, LastIndexed: now,
	})
	if err != nil {
		return 0, "failed", err
	}
	sourceID, err := db.UpsertSource(ctx, tx, db.SourceRow{
		NoteID: &noteID, URL: in.URL, Kind: string(in.Kind),
		FetchedAt: now, ContentHash: hash, Status: status,
	})
	if err != nil {
		return 0, "failed", err
	}
	if err := db.DeleteChunksForSource(ctx, tx, sourceID); err != nil {
		return 0, "failed", err
	}

	chunkIDs := make([]int64, len(chunks))
	for i, c := range chunks {
		id, err := db.InsertChunk(ctx, tx, db.ChunkRow{
			NoteID: &noteID, SourceID: &sourceID, Ordinal: c.Ordinal,
			Text: c.Text, TokenCount: c.TokenCount, ContentHash: c.ContentHash,
		})
		if err != nil {
			return 0, "failed", err
		}
		if err := db.InsertChunkFTS(ctx, tx, id, c.Text); err != nil {
			return 0, "failed", err
		}
		chunkIDs[i] = id
	}
	if err := tx.Commit(); err != nil {
		return 0, "failed", err
	}
	committed = true

	// Embedding is best-effort and happens outside the write transaction; a
	// failure leaves chunks lexically searchable with vectors pending.
	embedded := p.embedChunks(ctx, chunkIDs, chunks)
	return embedded, status, nil
}

// embedChunks embeds chunk texts and stores vectors; returns how many were
// embedded. Any failure is non-fatal (vectors remain pending for re-embed).
func (p *Pipeline) embedChunks(ctx context.Context, ids []int64, chunks []Chunk) int {
	if p.Embedder == nil || len(chunks) == 0 {
		if p.Embedder == nil {
			p.emit(events.LevelWarn, "ingest.embed.skip", "no embedder configured; vectors pending", nil)
		}
		return 0
	}
	texts := make([]string, len(chunks))
	for i, c := range chunks {
		texts[i] = c.Text
	}
	vecs, err := p.Embedder.Embed(ctx, texts)
	if err != nil {
		p.emit(events.LevelWarn, "ingest.embed.fail",
			fmt.Sprintf("embedding failed (%v); vectors pending, lexical search still works", err), nil)
		return 0
	}
	model := p.Embedder.Model()
	embedded := 0
	for i, v := range vecs {
		if i >= len(ids) {
			break
		}
		if err := db.UpsertChunkVector(ctx, p.DB, ids[i], model, v); err != nil {
			p.emit(events.LevelWarn, "ingest.embed.fail", fmt.Sprintf("store vector: %v", err), nil)
			continue
		}
		embedded++
	}
	return embedded
}

func (p *Pipeline) appendReviewQueue(notePath string, suggestions []string) error {
	var b strings.Builder
	stamp := time.Now().UTC().Format("2006-01-02 15:04")
	fmt.Fprintf(&b, "\n## Link suggestions for [[%s]] (%s)\n", vault.RelNoExt(notePath), stamp)
	for _, s := range suggestions {
		fmt.Fprintf(&b, "- [ ] link to [[%s]]\n", vault.RelNoExt(s))
	}
	return p.Vault.Append(reviewQueuePath, b.String())
}

func (p *Pipeline) emit(level events.Level, kind, msg string, res any) {
	if p.Bus == nil {
		return
	}
	data := map[string]any{"profile": p.Profile}
	if res != nil {
		data["result"] = res
	}
	p.Bus.Publish(events.Event{Level: level, Kind: kind, Message: msg, Data: data})
}

// buildSourceNote renders a fresh source note: frontmatter + an axon:summary
// managed block + a human Notes area + an axon:source managed block holding the
// cleaned full text. Frontmatter order is fixed and deterministic.
func buildSourceNote(enr Enrichment, cleaned string, ex Extracted, in Input, hash string) string {
	now := time.Now().UTC().Format("2006-01-02")
	var b strings.Builder
	b.WriteString("---\n")
	fmt.Fprintf(&b, "title: %s\n", yamlString(enr.Title))
	b.WriteString("type: source\n")
	fmt.Fprintf(&b, "created: %s\n", now)
	fmt.Fprintf(&b, "updated: %s\n", now)
	b.WriteString("tags: " + yamlList(enr.Tags) + "\n")
	b.WriteString("aliases: []\n")
	fmt.Fprintf(&b, "source_url: %s\n", yamlString(sourceURL(in)))
	fmt.Fprintf(&b, "source_author: %s\n", yamlString(ex.Author))
	fmt.Fprintf(&b, "source_date: %s\n", yamlString(ex.Date))
	b.WriteString("ingested_by: axon\n")
	fmt.Fprintf(&b, "content_hash: %s\n", yamlString(hash))
	b.WriteString("axon_managed: true\n")
	b.WriteString("---\n")
	b.WriteString("<!-- axon:summary:start -->\n")
	if enr.Summary != "" {
		b.WriteString(enr.Summary + "\n")
	}
	b.WriteString("<!-- axon:summary:end -->\n\n")
	b.WriteString("## Notes\n\n")
	b.WriteString("<!-- axon:source:start -->\n")
	b.WriteString(cleaned)
	if !strings.HasSuffix(cleaned, "\n") {
		b.WriteString("\n")
	}
	b.WriteString("<!-- axon:source:end -->\n")
	return b.String()
}

// sourceURL returns the human-facing source reference for the frontmatter.
func sourceURL(in Input) string {
	if in.Kind == KindURL {
		return in.URL
	}
	return in.Raw
}

var slugRe = regexp.MustCompile(`[^a-z0-9]+`)

// slugify produces a filesystem- and link-friendly slug from a title.
func slugify(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = slugRe.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if len(s) > 80 {
		s = strings.Trim(s[:80], "-")
	}
	return s
}

// yamlString quotes a scalar for safe YAML frontmatter.
func yamlString(s string) string {
	if s == "" {
		return `""`
	}
	return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
}

// yamlList renders a string slice as a YAML flow sequence.
func yamlList(items []string) string {
	if len(items) == 0 {
		return "[]"
	}
	quoted := make([]string, len(items))
	for i, it := range items {
		quoted[i] = yamlString(it)
	}
	return "[" + strings.Join(quoted, ", ") + "]"
}
