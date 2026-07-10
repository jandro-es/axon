package dashboard

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jandro-es/axon/internal/db"
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
	if out.Counts["overdue"] != 1 || out.Counts["open"] != 1 {
		t.Errorf("counts wrong: %+v", out.Counts)
	}
	if len(out.Trend) != 30 {
		t.Errorf("trend len = %d, want 30", len(out.Trend))
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
