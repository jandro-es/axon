// Package mcp is the AXON MCP server: a stdio server exposing wikilink-safe
// vault tools, hybrid/knowledge search, token status and automation control to
// Claude Code (ADR-005). AXON owns this server so the core loop never depends on
// a fast-moving third-party one, and so every vault write is wikilink-safe and
// every Claude round-trip is ledgered. There is deliberately NO vault.delete.
//
// The tool logic lives on Tools (plain methods, directly testable); server.go
// registers thin SDK wrappers around them.
package mcp

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/jandro-es/axon/internal/ask"
	"github.com/jandro-es/axon/internal/automations"
	"github.com/jandro-es/axon/internal/config"
	"github.com/jandro-es/axon/internal/db"
	"github.com/jandro-es/axon/internal/identity"
	"github.com/jandro-es/axon/internal/ingestion"
	"github.com/jandro-es/axon/internal/search"
	"github.com/jandro-es/axon/internal/tokens"
	"github.com/jandro-es/axon/internal/vault"
)

// Deps are the services the MCP tools operate on, scoped to the active profile.
type Deps struct {
	Profile  string
	Config   config.Profile
	DB       *sql.DB
	Vault    *vault.FS
	Searcher *search.Searcher
	Manager  tokens.Manager
	Pipeline *ingestion.Pipeline
	Engine   *automations.Engine
	// ToolFilter, when non-empty, registers ONLY the named tools — the
	// server-side half of ADR-017's dual allowlisting. Empty = all tools.
	ToolFilter []string
	// DryRun puts the write tools in report-only mode (ADR-022 / FR-106):
	// each validates and computes its change, returns Applied=false with a
	// Would string, and performs no vault mutation. Read tools are unaffected.
	DryRun bool
}

// Tools holds the dependencies and implements each tool as a method.
type Tools struct {
	deps Deps
}

// NewTools constructs the tool set.
func NewTools(deps Deps) *Tools { return &Tools{deps: deps} }

// --- search -----------------------------------------------------------------

type SearchIn struct {
	Query string `json:"query" jsonschema:"the search query"`
	TopK  int    `json:"top_k,omitempty" jsonschema:"max results (default 8)"`
}

type Hit struct {
	Path    string  `json:"path"`
	Snippet string  `json:"snippet"`
	Score   float64 `json:"score"`
}

type SearchOut struct {
	Hits []Hit `json:"hits"`
}

// Search runs hybrid (lexical + semantic) search over the vault + knowledge.
func (t *Tools) Search(ctx context.Context, in SearchIn) (SearchOut, error) {
	topK := in.TopK
	if topK <= 0 {
		topK = 8
	}
	hits, err := t.deps.Searcher.Search(ctx, in.Query, topK)
	if err != nil {
		return SearchOut{}, err
	}
	out := SearchOut{Hits: make([]Hit, 0, len(hits))}
	for _, h := range hits {
		out.Hits = append(out.Hits, Hit{Path: h.Path, Snippet: h.Snippet, Score: h.Score})
	}
	return out, nil
}

// --- read -------------------------------------------------------------------

type ReadIn struct {
	Path string `json:"path" jsonschema:"vault-relative note path"`
}

type ReadOut struct {
	Path        string         `json:"path"`
	Frontmatter map[string]any `json:"frontmatter,omitempty"`
	Body        string         `json:"body"`
}

// Read returns a note's frontmatter and body.
func (t *Tools) Read(ctx context.Context, in ReadIn) (ReadOut, error) {
	n, err := t.deps.Vault.Read(ctx, in.Path)
	if err != nil {
		return ReadOut{}, err
	}
	return ReadOut{Path: n.Path, Frontmatter: n.Frontmatter, Body: n.Body}, nil
}

// --- write ------------------------------------------------------------------

type WriteIn struct {
	Path  string `json:"path" jsonschema:"vault-relative note path"`
	Body  string `json:"body" jsonschema:"note body (markdown)"`
	Force bool   `json:"force,omitempty" jsonschema:"overwrite an existing note (default false)"`
}

type WriteOut struct {
	OK      bool   `json:"ok"`
	Path    string `json:"path"`
	Applied bool   `json:"applied"`
	Would   string `json:"would,omitempty"`
}

// Write creates a new note (or overwrites with force). It refuses to clobber an
// existing note unless force is set, steering edits toward Patch/Move so human
// prose is never silently lost. Even with force, only AXON-authored notes
// (frontmatter `axon_managed: true`) may be overwritten: with no vault.delete,
// force-overwrite is the de-facto destructive op, and a prompt-injected agent
// must not be able to erase human prose with a tool argument (NFR-05).
func (t *Tools) Write(ctx context.Context, in WriteIn) (WriteOut, error) {
	if err := guardAgentPath(in.Path); err != nil {
		return WriteOut{}, err
	}
	if t.deps.Vault.Exists(in.Path) {
		if !in.Force {
			return WriteOut{}, fmt.Errorf("note %q exists; use vault.patch for managed-block edits, or pass force=true to overwrite", in.Path)
		}
		if !t.isAxonManaged(ctx, in.Path) {
			return WriteOut{}, fmt.Errorf("note %q is not AXON-managed; force-overwrite is only allowed on notes with `axon_managed: true` frontmatter — use vault_patch for managed-block edits, or vault_move to archive it", in.Path)
		}
	}
	if t.deps.DryRun {
		return WriteOut{OK: true, Path: in.Path, Applied: false,
			Would: fmt.Sprintf("create %s (%d bytes)", in.Path, len(in.Body))}, nil
	}
	if err := t.deps.Vault.Write(ctx, in.Path, &vault.Note{Body: in.Body}); err != nil {
		return WriteOut{}, err
	}
	return WriteOut{OK: true, Path: in.Path, Applied: true}, nil
}

// isAxonManaged reports whether a note's frontmatter marks it AXON-authored.
func (t *Tools) isAxonManaged(ctx context.Context, path string) bool {
	n, err := t.deps.Vault.Read(ctx, path)
	if err != nil {
		return false
	}
	switch v := n.Frontmatter["axon_managed"].(type) {
	case bool:
		return v
	case string:
		return strings.EqualFold(strings.TrimSpace(v), "true")
	}
	return false
}

// guardAgentPath refuses agent-supplied paths that target vault system
// directories (.claude, .obsidian, .axon, .git, .trash): writes there change
// AXON's or the agent's own configuration, not knowledge (NFR-05).
func guardAgentPath(paths ...string) error {
	for _, p := range paths {
		if vault.IsSystemPath(p) {
			return fmt.Errorf("path %q targets a vault system directory; AXON tools only operate on notes", p)
		}
	}
	return nil
}

// --- patch ------------------------------------------------------------------

type PatchIn struct {
	Path    string `json:"path" jsonschema:"vault-relative note path"`
	Marker  string `json:"marker" jsonschema:"axon:<marker> managed block name"`
	Content string `json:"content" jsonschema:"new content for the managed block"`
}

type PatchOut struct {
	OK      bool   `json:"ok"`
	Applied bool   `json:"applied"`
	Would   string `json:"would,omitempty"`
}

// Patch edits only the content of an axon:<marker> managed block, never human
// prose outside it.
func (t *Tools) Patch(ctx context.Context, in PatchIn) (PatchOut, error) {
	if err := guardAgentPath(in.Path); err != nil {
		return PatchOut{}, err
	}
	if t.deps.DryRun {
		return PatchOut{OK: true, Applied: false,
			Would: fmt.Sprintf("patch axon:%s in %s (%d chars)", in.Marker, in.Path, len(in.Content))}, nil
	}
	if err := t.deps.Vault.Patch(ctx, in.Path, in.Marker, in.Content); err != nil {
		return PatchOut{}, err
	}
	return PatchOut{OK: true, Applied: true}, nil
}

// --- move (wikilink-safe) ---------------------------------------------------

type MoveIn struct {
	From string `json:"from" jsonschema:"current vault-relative path"`
	To   string `json:"to" jsonschema:"destination vault-relative path"`
}

type MoveOut struct {
	OK           bool     `json:"ok"`
	UpdatedLinks []string `json:"updated_links"`
}

// Move renames/moves a note and rewrites every inbound wikilink so none break.
// This is the only sanctioned rename path.
func (t *Tools) Move(ctx context.Context, in MoveIn) (MoveOut, error) {
	if err := guardAgentPath(in.From, in.To); err != nil {
		return MoveOut{}, err
	}
	// Capture inbound links before the move so we can report what was rewritten.
	var updated []string
	if id, err := db.GetNoteIDByPath(ctx, t.deps.DB, in.From); err == nil && id != nil {
		updated, _ = db.Backlinks(ctx, t.deps.DB, *id)
	}
	if err := t.deps.Vault.Move(ctx, in.From, in.To); err != nil {
		return MoveOut{}, err
	}
	return MoveOut{OK: true, UpdatedLinks: updated}, nil
}

// --- links ------------------------------------------------------------------

type LinksIn struct {
	Path string `json:"path" jsonschema:"vault-relative note path"`
}

type LinksOut struct {
	Outbound  []string `json:"outbound"`
	Backlinks []string `json:"backlinks"`
}

// Links returns a note's outbound links and backlinks from the link graph.
func (t *Tools) Links(ctx context.Context, in LinksIn) (LinksOut, error) {
	id, err := db.GetNoteIDByPath(ctx, t.deps.DB, in.Path)
	if err != nil {
		return LinksOut{}, err
	}
	if id == nil {
		return LinksOut{}, fmt.Errorf("note %q not indexed (run reindex)", in.Path)
	}
	outbound, err := db.OutboundLinks(ctx, t.deps.DB, *id)
	if err != nil {
		return LinksOut{}, err
	}
	back, err := db.Backlinks(ctx, t.deps.DB, *id)
	if err != nil {
		return LinksOut{}, err
	}
	return LinksOut{Outbound: outbound, Backlinks: back}, nil
}

// --- daily.append -----------------------------------------------------------

type DailyAppendIn struct {
	Content string `json:"content" jsonschema:"content to append"`
	Date    string `json:"date,omitempty" jsonschema:"YYYY-MM-DD (default today)"`
}

type DailyAppendOut struct {
	OK      bool   `json:"ok"`
	Path    string `json:"path"`
	Applied bool   `json:"applied"`
	Would   string `json:"would,omitempty"`
}

// DailyAppend appends content to a daily note, creating it if absent.
func (t *Tools) DailyAppend(ctx context.Context, in DailyAppendIn, today string) (DailyAppendOut, error) {
	date := in.Date
	if date == "" {
		date = today
	}
	path := "Daily/" + date + ".md"
	if t.deps.DryRun {
		return DailyAppendOut{OK: true, Path: path, Applied: false,
			Would: fmt.Sprintf("append %d byte(s) to %s", len(in.Content), path)}, nil
	}
	if !t.deps.Vault.Exists(path) {
		if _, err := t.deps.Vault.Create(path, "---\ntitle: \""+date+"\"\ntype: daily\ntags: [daily]\n---\n\n## Log\n"); err != nil {
			return DailyAppendOut{}, err
		}
	}
	if err := t.deps.Vault.Append(path, strings.TrimRight(in.Content, "\n")+"\n"); err != nil {
		return DailyAppendOut{}, err
	}
	return DailyAppendOut{OK: true, Path: path, Applied: true}, nil
}

// --- memory.remember --------------------------------------------------------

type RememberIn struct {
	Text   string `json:"text" jsonschema:"the durable fact, decision or learned preference to remember (one line)"`
	Kind   string `json:"kind,omitempty" jsonschema:"optional category: decision | lesson | preference"`
	Source string `json:"source,omitempty" jsonschema:"optional provenance, e.g. 'session' or an ADR id"`
}

type RememberOut struct {
	OK      bool   `json:"ok"`
	Entry   string `json:"entry"`
	Path    string `json:"path"`
	Applied bool   `json:"applied"`
	Would   string `json:"would,omitempty"`
}

// Remember appends a dated entry to the personal MEMORY note's axon:memory
// managed block, wikilink-safe and never touching human prose (cardinal rule 2).
// This is how durable memory grows during interactive work (FR-73). today is the
// UTC date the server stamps the entry with.
func (t *Tools) Remember(ctx context.Context, in RememberIn, today string) (RememberOut, error) {
	if t.deps.DryRun {
		return RememberOut{OK: true, Path: identity.MemoryPath, Applied: false,
			Would: fmt.Sprintf("remember %s: %s", in.Kind, in.Text)}, nil
	}
	line, err := identity.Remember(ctx, t.deps.Vault, identity.Entry{
		Text: in.Text, Kind: in.Kind, Source: in.Source, Date: today,
	})
	if err != nil {
		return RememberOut{}, err
	}
	return RememberOut{OK: true, Entry: line, Path: identity.MemoryPath, Applied: true}, nil
}

// --- knowledge.ingest -------------------------------------------------------

type IngestIn struct {
	Target string `json:"target" jsonschema:"a URL or local file path"`
	DryRun bool   `json:"dry_run,omitempty"`
}

type IngestOut struct {
	Status      string   `json:"status"`
	NotePath    string   `json:"note_path,omitempty"`
	Title       string   `json:"title,omitempty"`
	Suggestions []string `json:"suggested_links,omitempty"`
}

// Ingest runs the knowledge ingestion pipeline.
func (t *Tools) Ingest(ctx context.Context, in IngestIn) (IngestOut, error) {
	res, err := t.deps.Pipeline.Ingest(ctx, in.Target, ingestion.IngestOptions{DryRun: in.DryRun})
	if err != nil {
		return IngestOut{}, err
	}
	return IngestOut{Status: res.Status, NotePath: res.NotePath, Title: res.Title, Suggestions: res.Suggestions}, nil
}

// --- tokens.status ----------------------------------------------------------

type StatusOut struct {
	DayUsed     int64   `json:"day_used"`
	DayLimit    int64   `json:"day_limit"`
	DayPct      float64 `json:"day_pct"`
	WeekUsed    int64   `json:"week_used"`
	WeekLimit   int64   `json:"week_limit"`
	WeekPct     float64 `json:"week_pct"`
	GuardPaused bool    `json:"guard_paused"`
}

// Status returns the current token budget windows and guard state.
func (t *Tools) Status(ctx context.Context) (StatusOut, error) {
	st, err := t.deps.Manager.Status(ctx, t.deps.Profile)
	if err != nil {
		return StatusOut{}, err
	}
	return StatusOut{
		DayUsed: st.Day.Used, DayLimit: st.Day.Limit, DayPct: st.Day.Pct,
		WeekUsed: st.Week.Used, WeekLimit: st.Week.Limit, WeekPct: st.Week.Pct,
		GuardPaused: st.GuardPaused,
	}, nil
}

// --- metrics.query ----------------------------------------------------------

type MetricsIn struct {
	SinceDays int `json:"since_days,omitempty" jsonschema:"window in days to aggregate (default 7)"`
}

type MetricBucket struct {
	Day       string `json:"day"`
	Operation string `json:"operation"`
	Model     string `json:"model"`
	Input     int64  `json:"input"`
	Output    int64  `json:"output"`
}

type MetricsOut struct {
	SinceDays   int              `json:"since_days"`
	TotalInput  int64            `json:"total_input"`
	TotalOutput int64            `json:"total_output"`
	ByModel     map[string]int64 `json:"by_model"`
	ByOperation map[string]int64 `json:"by_operation"`
	Buckets     []MetricBucket   `json:"buckets"`
	DayUsed     int64            `json:"day_used"`
	DayLimit    int64            `json:"day_limit"`
	WeekUsed    int64            `json:"week_used"`
	WeekLimit   int64            `json:"week_limit"`
}

// Metrics returns token-ledger aggregates (by day/operation/model) over a recent
// window plus the current budget windows — the read-only counterpart to
// tokens_status for dashboards and agent introspection (FR-50).
func (t *Tools) Metrics(ctx context.Context, in MetricsIn, now time.Time) (MetricsOut, error) {
	days := in.SinceDays
	if days <= 0 {
		days = 7
	}
	since := now.UTC().AddDate(0, 0, -days).Format(time.RFC3339)
	rows, err := db.TokenSeries(ctx, t.deps.DB, since)
	if err != nil {
		return MetricsOut{}, err
	}
	out := MetricsOut{SinceDays: days, ByModel: map[string]int64{}, ByOperation: map[string]int64{}}
	for _, r := range rows {
		out.Buckets = append(out.Buckets, MetricBucket{Day: r.Day, Operation: r.Operation, Model: r.Model, Input: r.Input, Output: r.Output})
		out.TotalInput += r.Input
		out.TotalOutput += r.Output
		out.ByModel[r.Model] += r.Input + r.Output
		out.ByOperation[r.Operation] += r.Input + r.Output
	}
	if st, err := t.deps.Manager.Status(ctx, t.deps.Profile); err == nil {
		out.DayUsed, out.DayLimit = st.Day.Used, st.Day.Limit
		out.WeekUsed, out.WeekLimit = st.Week.Used, st.Week.Limit
	}
	return out, nil
}

// --- automations.list / run -------------------------------------------------

type AutomationInfo struct {
	Name      string `json:"name"`
	Essential bool   `json:"essential"`
	Allowed   bool   `json:"allowed"`
	LastRun   string `json:"last_run,omitempty"`
}

type ListOut struct {
	Automations []AutomationInfo `json:"automations"`
}

// ListAutomations reports the registered automations and their policy/last-run.
func (t *Tools) ListAutomations(ctx context.Context) (ListOut, error) {
	out := ListOut{}
	for _, name := range automations.Names(t.deps.Config) {
		a, _ := automations.Get(t.deps.Config, name)
		last, _ := db.LastRunStatus(ctx, t.deps.DB, name)
		out.Automations = append(out.Automations, AutomationInfo{
			Name: name, Essential: a.Essential(),
			Allowed: automations.AllowedByPolicy(t.deps.Config, name), LastRun: last,
		})
	}
	return out, nil
}

type RunIn struct {
	Name   string `json:"name" jsonschema:"automation name"`
	DryRun bool   `json:"dry_run,omitempty"`
}

// RunAutomation runs an automation through the same engine path as the scheduler.
func (t *Tools) RunAutomation(ctx context.Context, in RunIn) (automations.Outcome, error) {
	if !automations.AllowedByPolicy(t.deps.Config, in.Name) {
		return automations.Outcome{}, fmt.Errorf("automation %q not permitted by policy", in.Name)
	}
	a, err := automations.Get(t.deps.Config, in.Name)
	if err != nil {
		return automations.Outcome{}, err
	}
	return t.deps.Engine.Run(ctx, a, in.DryRun)
}

// --- ask (grounded RAG, FR-111 / ADR-023) -----------------------------------

type AskIn struct {
	Question string `json:"question" jsonschema:"the question to answer from the vault"`
	TopK     int    `json:"top_k,omitempty" jsonschema:"retrieval depth (default: retrieval.top_k)"`
}

// Ask answers a question grounded only in retrieved vault notes (internal/ask):
// grounded-or-silent, with [[wikilink]] citations, spending synthesis-tier
// tokens through the chokepoint. Read-only toward the vault.
func (t *Tools) Ask(ctx context.Context, in AskIn) (ask.Answer, error) {
	a, err := ask.Ask(ctx, ask.Deps{
		Searcher: t.deps.Searcher, Manager: t.deps.Manager, Config: t.deps.Config,
	}, in.Question, in.TopK)
	if err == nil && a.Conflicted {
		a.Text = "⚠ Sources conflict — " + a.Text
	}
	return a, err
}
