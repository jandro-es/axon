package mcp

import (
	"context"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/jandro-es/axon/internal/automations"
)

// Version is reported in the MCP server implementation info.
const Version = "0.5.0"

// NoArgs is the input type for tools that take no arguments.
type NoArgs struct{}

// NewServer builds the AXON MCP server with all tools registered. Tool names use
// underscores (e.g. vault_search) so they map cleanly onto Claude Code's
// mcp__axon__<tool> identifiers.
func NewServer(deps Deps) *mcp.Server {
	t := NewTools(deps)
	s := mcp.NewServer(&mcp.Implementation{Name: "axon", Version: Version}, nil)

	mcp.AddTool(s, &mcp.Tool{Name: "vault_search", Description: "Hybrid lexical+semantic search across the vault and ingested knowledge."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in SearchIn) (*mcp.CallToolResult, SearchOut, error) {
			out, err := t.Search(ctx, in)
			return nil, out, err
		})

	mcp.AddTool(s, &mcp.Tool{Name: "knowledge_search", Description: "Search ingested knowledge sources (hybrid)."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in SearchIn) (*mcp.CallToolResult, SearchOut, error) {
			out, err := t.Search(ctx, in)
			return nil, out, err
		})

	mcp.AddTool(s, &mcp.Tool{Name: "vault_read", Description: "Read a note's frontmatter and body."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in ReadIn) (*mcp.CallToolResult, ReadOut, error) {
			out, err := t.Read(ctx, in)
			return nil, out, err
		})

	mcp.AddTool(s, &mcp.Tool{Name: "vault_write", Description: "Create a note (or overwrite with force). Refuses to clobber existing prose by default — use vault_patch."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in WriteIn) (*mcp.CallToolResult, WriteOut, error) {
			out, err := t.Write(ctx, in)
			return nil, out, err
		})

	mcp.AddTool(s, &mcp.Tool{Name: "vault_patch", Description: "Edit only the content of an axon:<marker> managed block; never touches human prose."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in PatchIn) (*mcp.CallToolResult, PatchOut, error) {
			out, err := t.Patch(ctx, in)
			return nil, out, err
		})

	mcp.AddTool(s, &mcp.Tool{Name: "vault_move", Description: "Rename/move a note, rewriting every inbound wikilink so none break. The ONLY safe rename path."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in MoveIn) (*mcp.CallToolResult, MoveOut, error) {
			out, err := t.Move(ctx, in)
			return nil, out, err
		})

	mcp.AddTool(s, &mcp.Tool{Name: "vault_links", Description: "Outbound links and backlinks for a note, from the link graph."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in LinksIn) (*mcp.CallToolResult, LinksOut, error) {
			out, err := t.Links(ctx, in)
			return nil, out, err
		})

	mcp.AddTool(s, &mcp.Tool{Name: "daily_append", Description: "Append content to today's (or a given) daily note, creating it if absent."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in DailyAppendIn) (*mcp.CallToolResult, DailyAppendOut, error) {
			out, err := t.DailyAppend(ctx, in, time.Now().UTC().Format("2006-01-02"))
			return nil, out, err
		})

	mcp.AddTool(s, &mcp.Tool{Name: "memory_remember", Description: "Append a durable memory entry (decision/lesson/preference) to the personal MEMORY note, wikilink-safe. Use for facts worth recalling across sessions."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in RememberIn) (*mcp.CallToolResult, RememberOut, error) {
			out, err := t.Remember(ctx, in, time.Now().UTC().Format("2006-01-02"))
			return nil, out, err
		})

	mcp.AddTool(s, &mcp.Tool{Name: "knowledge_ingest", Description: "Ingest a URL or local file into the knowledge base (policy-gated, idempotent)."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in IngestIn) (*mcp.CallToolResult, IngestOut, error) {
			out, err := t.Ingest(ctx, in)
			return nil, out, err
		})

	mcp.AddTool(s, &mcp.Tool{Name: "tokens_status", Description: "Current token budget (day/week) and budget-guard state."},
		func(ctx context.Context, _ *mcp.CallToolRequest, _ NoArgs) (*mcp.CallToolResult, StatusOut, error) {
			out, err := t.Status(ctx)
			return nil, out, err
		})

	mcp.AddTool(s, &mcp.Tool{Name: "automations_list", Description: "List automations with their essential flag, policy permission and last run."},
		func(ctx context.Context, _ *mcp.CallToolRequest, _ NoArgs) (*mcp.CallToolResult, ListOut, error) {
			out, err := t.ListAutomations(ctx)
			return nil, out, err
		})

	mcp.AddTool(s, &mcp.Tool{Name: "automations_run", Description: "Run an automation through the same engine path as the scheduler."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in RunIn) (*mcp.CallToolResult, automations.Outcome, error) {
			out, err := t.RunAutomation(ctx, in)
			return nil, out, err
		})

	return s
}

// Serve runs the MCP server over stdio until the context is cancelled or the
// peer disconnects.
func Serve(ctx context.Context, deps Deps) error {
	return NewServer(deps).Run(ctx, &mcp.StdioTransport{})
}
