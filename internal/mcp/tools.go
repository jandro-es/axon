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

	"github.com/jandro-es/axon/internal/automations"
	"github.com/jandro-es/axon/internal/config"
	"github.com/jandro-es/axon/internal/db"
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
	OK   bool   `json:"ok"`
	Path string `json:"path"`
}

// Write creates a new note (or overwrites with force). It refuses to clobber an
// existing note unless force is set, steering edits toward Patch/Move so human
// prose is never silently lost.
func (t *Tools) Write(ctx context.Context, in WriteIn) (WriteOut, error) {
	if t.deps.Vault.Exists(in.Path) && !in.Force {
		return WriteOut{}, fmt.Errorf("note %q exists; use vault.patch for managed-block edits, or pass force=true to overwrite", in.Path)
	}
	if err := t.deps.Vault.Write(ctx, in.Path, &vault.Note{Body: in.Body}); err != nil {
		return WriteOut{}, err
	}
	return WriteOut{OK: true, Path: in.Path}, nil
}

// --- patch ------------------------------------------------------------------

type PatchIn struct {
	Path    string `json:"path" jsonschema:"vault-relative note path"`
	Marker  string `json:"marker" jsonschema:"axon:<marker> managed block name"`
	Content string `json:"content" jsonschema:"new content for the managed block"`
}

type PatchOut struct {
	OK bool `json:"ok"`
}

// Patch edits only the content of an axon:<marker> managed block, never human
// prose outside it.
func (t *Tools) Patch(ctx context.Context, in PatchIn) (PatchOut, error) {
	if err := t.deps.Vault.Patch(ctx, in.Path, in.Marker, in.Content); err != nil {
		return PatchOut{}, err
	}
	return PatchOut{OK: true}, nil
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
	OK   bool   `json:"ok"`
	Path string `json:"path"`
}

// DailyAppend appends content to a daily note, creating it if absent.
func (t *Tools) DailyAppend(ctx context.Context, in DailyAppendIn, today string) (DailyAppendOut, error) {
	date := in.Date
	if date == "" {
		date = today
	}
	path := "Daily/" + date + ".md"
	if !t.deps.Vault.Exists(path) {
		if _, err := t.deps.Vault.Create(path, "---\ntitle: \""+date+"\"\ntype: daily\ntags: [daily]\n---\n\n## Log\n"); err != nil {
			return DailyAppendOut{}, err
		}
	}
	if err := t.deps.Vault.Append(path, strings.TrimRight(in.Content, "\n")+"\n"); err != nil {
		return DailyAppendOut{}, err
	}
	return DailyAppendOut{OK: true, Path: path}, nil
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
