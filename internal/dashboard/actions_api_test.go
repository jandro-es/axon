package dashboard

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jandro-es/axon/internal/actions"
	"github.com/jandro-es/axon/internal/db"
	"github.com/jandro-es/axon/internal/events"
	"github.com/jandro-es/axon/internal/vault"
)

func actionsTestServer(t *testing.T, enabled bool) *httptest.Server {
	t.Helper()
	ctx := context.Background()
	d, err := db.Open(db.MemoryDSN)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	if _, err := db.Migrate(d); err != nil {
		t.Fatal(err)
	}
	_ = db.ReplaceActions(ctx, d, []db.Action{
		{Hash: "h1", SourcePath: "a.md", Text: "overdue one", State: "open", Checkbox: " ", Due: "2000-01-01", Updated: "u"},
	})
	srv := New(Config{DB: d, ActionsEnabled: enabled})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts
}

func TestActionsAPIReturnsList(t *testing.T) {
	ts := actionsTestServer(t, true)
	req, _ := http.NewRequest("GET", ts.URL+"/api/actions", nil)
	req.Header.Set("X-Axon-Actions", "1")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var out struct {
		Actions []map[string]any `json:"actions"`
		Counts  map[string]int   `json:"counts"`
		Trend   []map[string]any `json:"trend"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if len(out.Actions) != 1 || out.Actions[0]["bucket"] != "overdue" {
		t.Errorf("actions payload wrong: %+v", out.Actions)
	}
	// The SPA filters/renders on snake_case fields (a.state, a.source_path,
	// a.hash, a.line_no) — PascalCase Go field names silently break the list.
	a0 := out.Actions[0]
	for k, want := range map[string]any{"state": "open", "source_path": "a.md", "hash": "h1", "archived": false} {
		if a0[k] != want {
			t.Errorf("action[%q] = %v, want %v (full: %+v)", k, a0[k], want, a0)
		}
	}
	if out.Counts["overdue"] != 1 || out.Counts["open"] != 1 {
		t.Errorf("counts wrong: %+v", out.Counts)
	}
	if len(out.Trend) != 30 {
		t.Errorf("trend len = %d, want 30", len(out.Trend))
	}
}

func TestActionsAPIIncludesVaultName(t *testing.T) {
	ts, v, _ := actionsWriteServer(t)
	req, _ := http.NewRequest("GET", ts.URL+"/api/actions", nil)
	req.Header.Set("X-Axon-Actions", "1")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out struct {
		Vault string `json:"vault"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	// The SPA builds obsidian://open?vault=<this> deep links from the payload.
	if want := filepath.Base(v.Root()); out.Vault != want {
		t.Errorf("vault = %q, want %q", out.Vault, want)
	}
}

func TestActionsAPIGuards(t *testing.T) {
	// disabled → 404
	ts := actionsTestServer(t, false)
	req, _ := http.NewRequest("GET", ts.URL+"/api/actions", nil)
	req.Header.Set("X-Axon-Actions", "1")
	resp, _ := http.DefaultClient.Do(req)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("disabled status = %d, want 404", resp.StatusCode)
	}
	resp.Body.Close()

	// header-less → 403
	ts2 := actionsTestServer(t, true)
	req2, _ := http.NewRequest("GET", ts2.URL+"/api/actions", nil)
	resp2, _ := http.DefaultClient.Do(req2)
	if resp2.StatusCode != http.StatusForbidden {
		t.Errorf("header-less status = %d, want 403", resp2.StatusCode)
	}
	resp2.Body.Close()
}

func actionsWriteServer(t *testing.T) (*httptest.Server, *vault.FS, *sql.DB) {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	body := "- [ ] finish spec\n"
	if err := os.WriteFile(filepath.Join(dir, "p.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	v := vault.NewFS(dir)
	d, err := db.Open(db.MemoryDSN)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	if _, err := db.Migrate(d); err != nil {
		t.Fatal(err)
	}
	var hash string
	for _, a := range actions.Extract("p.md", body, false) {
		hash = a.Hash()
	}
	_ = db.ReplaceActions(ctx, d, []db.Action{{Hash: hash, SourcePath: "p.md", LineNo: 0, Text: "finish spec", Raw: "- [ ] finish spec", State: "open", Checkbox: " ", Updated: "u"}})
	srv := New(Config{Profile: "test", DB: d, Vault: v, Bus: events.NewBus(), ActionsEnabled: true})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts, v, d
}

func postComplete(t *testing.T, url, path, hash string, withHeader bool) *http.Response {
	t.Helper()
	req, _ := http.NewRequest("POST", url+"/api/actions/complete", strings.NewReader(`{"path":"`+path+`","hash":"`+hash+`"}`))
	req.Header.Set("Content-Type", "application/json")
	if withHeader {
		req.Header.Set("X-Axon-Actions", "1")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestActionCompleteFlipsAndGuards(t *testing.T) {
	ts, v, d := actionsWriteServer(t)
	ctx := context.Background()
	var hash string
	for _, a := range actions.Extract("p.md", "- [ ] finish spec\n", false) {
		hash = a.Hash()
	}

	// header-less → 403
	if r := postComplete(t, ts.URL, "p.md", hash, false); r.StatusCode != http.StatusForbidden {
		t.Errorf("header-less status = %d, want 403", r.StatusCode)
	}
	// stale hash → 409
	if r := postComplete(t, ts.URL, "p.md", "bogus", true); r.StatusCode != http.StatusConflict {
		t.Errorf("stale-hash status = %d, want 409", r.StatusCode)
	}
	// real completion → 200, file flipped, DB row done
	r := postComplete(t, ts.URL, "p.md", hash, true)
	if r.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", r.StatusCode)
	}
	n, _ := v.Read(ctx, "p.md")
	if !strings.Contains(n.Body, "- [x] finish spec ✅ ") {
		t.Errorf("source line not flipped:\n%s", n.Body)
	}
	got, _ := db.ListActions(ctx, d, db.ListActionsOpts{IncludeAll: true})
	if got[0].State != "done" {
		t.Errorf("DB row not marked done: %+v", got[0])
	}
}
