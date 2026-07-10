package mcp

import (
	"context"
	"encoding/json"
	"sort"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestServerProtocolListsAndCallsTools drives the real MCP server over the SDK's
// in-memory transport: it lists tools and calls one, verifying the end-to-end
// protocol wiring (not just the tool methods).
func TestServerProtocolListsAndCallsTools(t *testing.T) {
	ctx := context.Background()
	tools, v, _ := newTestTools(t, map[string]string{"n.md": "---\ntitle: N\n---\nbody\n"})
	server := NewServer(tools.deps)

	clientT, serverT := mcp.NewInMemoryTransports()
	if _, err := server.Connect(ctx, serverT, nil); err != nil {
		t.Fatal(err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "1"}, nil)
	cs, err := client.Connect(ctx, clientT, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()

	// tools/list
	lt, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, 0, len(lt.Tools))
	for _, tool := range lt.Tools {
		got = append(got, tool.Name)
	}
	sort.Strings(got)
	want := []string{
		"action_complete", "actions_list",
		"automations_list", "automations_run", "daily_append", "knowledge_ingest",
		"knowledge_search", "memory_remember", "metrics_query", "tokens_status",
		"vault_ask", "vault_links", "vault_move", "vault_patch", "vault_read", "vault_related", "vault_search", "vault_write",
	}
	if len(got) != len(want) {
		t.Fatalf("tools/list returned %d tools (%v), want %d", len(got), got, len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("tool[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	// Crucially: no delete tool.
	for _, n := range got {
		if n == "vault_delete" {
			t.Error("a vault_delete tool exists; deletes must be out-of-band")
		}
	}

	// tools/call vault_read
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name:      "vault_read",
		Arguments: map[string]any{"path": "n.md"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("vault_read returned an error result: %+v", res.Content)
	}
	var out ReadOut
	raw, _ := json.Marshal(res.StructuredContent)
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("structured content not ReadOut: %v", err)
	}
	if out.Body == "" || out.Path != "n.md" {
		t.Errorf("vault_read via protocol returned %+v", out)
	}
	_ = v
}
