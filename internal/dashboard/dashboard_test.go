package dashboard

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jandro-es/axon/internal/agent"
	"github.com/jandro-es/axon/internal/config"
	"github.com/jandro-es/axon/internal/db"
	"github.com/jandro-es/axon/internal/events"
	"github.com/jandro-es/axon/internal/search"
	"github.com/jandro-es/axon/internal/tokens"
)

func newTestServer(t *testing.T) (*Server, *sql.DB, *events.Bus, tokens.Manager) {
	t.Helper()
	d, err := db.Open(db.MemoryDSN)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	if _, err := db.Migrate(d); err != nil {
		t.Fatal(err)
	}
	bus := events.NewBus()
	t.Cleanup(bus.Close)
	mgr := tokens.New(d, agent.NewFake(), search.New(d, nil), bus, tokens.Config{
		Profile: "test", AuthMode: "subscription",
		Limits: config.LimitsConfig{DailyTokens: 1000, WeeklyTokens: 5000, GuardPauseAtPct: 80},
	})
	srv := New(Config{Profile: "test", DB: d, Manager: mgr, Bus: bus})
	return srv, d, bus, mgr
}

func TestHealthEndpoint(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/health", nil))
	if rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out["status"] != "ok" || out["db"] != true {
		t.Errorf("unexpected health: %v", out)
	}
}

func TestUsageMatchesManagerStatus(t *testing.T) {
	srv, dbtx, _, mgr := newTestServer(t)
	ctx := context.Background()
	// Spend some tokens via the ledger/budget so usage is non-zero.
	_ = db.AddBudgetUsage(ctx, dbtx, "test", "day", time.Now().UTC().Format("2006-01-02"), 250, 0)

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/api/usage", nil))
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)

	st, _ := mgr.Status(ctx, "test")
	if int64(out["day_used"].(float64)) != st.Day.Used {
		t.Errorf("usage day_used=%v, status=%d (must match axon status)", out["day_used"], st.Day.Used)
	}
	if int64(out["day_limit"].(float64)) != 1000 {
		t.Errorf("day_limit=%v, want 1000", out["day_limit"])
	}
}

func TestTokensSplitByAutomationAndModel(t *testing.T) {
	srv, dbtx, _, _ := newTestServer(t)
	ctx := context.Background()
	ts := time.Now().UTC().Format(time.RFC3339)
	_, _ = db.InsertLedger(ctx, dbtx, db.LedgerRow{TS: ts, Profile: "test", Operation: "automation.daily-log", Model: "sonnet", InputTokens: 100, OutputTokens: 20})
	_, _ = db.InsertLedger(ctx, dbtx, db.LedgerRow{TS: ts, Profile: "test", Operation: "ingest.enrich", Model: "haiku", InputTokens: 50, OutputTokens: 10})

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/api/tokens?days=7", nil))
	var buckets []db.TokenBucket
	if err := json.Unmarshal(rec.Body.Bytes(), &buckets); err != nil {
		t.Fatal(err)
	}
	ops := map[string]bool{}
	models := map[string]bool{}
	for _, b := range buckets {
		ops[b.Operation] = true
		models[b.Model] = true
	}
	if !ops["automation.daily-log"] || !ops["ingest.enrich"] {
		t.Errorf("token series not split by operation: %+v", buckets)
	}
	if !models["sonnet"] || !models["haiku"] {
		t.Errorf("token series not split by model: %+v", buckets)
	}
}

func TestGraphEndpoint(t *testing.T) {
	srv, dbtx, _, _ := newTestServer(t)
	ctx := context.Background()
	a, _ := db.UpsertNote(ctx, dbtx, db.NoteRow{Path: "01-Projects/a.md", Title: "A", Type: "project", Tags: []string{"x"}})
	b, _ := db.UpsertNote(ctx, dbtx, db.NoteRow{Path: "02-Areas/b.md", Title: "B", Type: "area"})
	_ = db.InsertLink(ctx, dbtx, db.LinkRow{SrcNoteID: a, DstPath: "b", DstNoteID: &b, Kind: "wikilink"})

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/api/graph", nil))
	var g db.Graph
	if err := json.Unmarshal(rec.Body.Bytes(), &g); err != nil {
		t.Fatal(err)
	}
	if len(g.Nodes) != 2 || len(g.Edges) != 1 {
		t.Errorf("graph = %d nodes, %d edges; want 2,1", len(g.Nodes), len(g.Edges))
	}
	// Nodes carry folder/tag info for client-side filtering.
	if g.Nodes[0].Type == "" {
		t.Error("graph nodes should carry type for filtering")
	}
}

// TestSSEDeliversLiveEvents is the S4 latency gate: an event published to the bus
// reaches an SSE client within well under 5 seconds.
func TestSSEDeliversLiveEvents(t *testing.T) {
	srv, _, bus, _ := newTestServer(t)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	req, _ := http.NewRequest("GET", ts.URL+"/events", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	got := make(chan string, 1)
	go func() {
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			line := sc.Text()
			if strings.HasPrefix(line, "data: ") {
				got <- strings.TrimPrefix(line, "data: ")
				return
			}
		}
	}()

	// Give the subscription a moment, then publish.
	time.Sleep(50 * time.Millisecond)
	bus.Publish(events.Event{Level: events.LevelInfo, Kind: "ingest.done", Message: "hello dashboard"})

	select {
	case payload := <-got:
		if !strings.Contains(payload, "hello dashboard") {
			t.Errorf("unexpected SSE payload: %s", payload)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("event did not reach SSE client within 3s (S4 latency gate)")
	}
}

func TestEventPersistence(t *testing.T) {
	_, dbtx, bus, _ := newTestServer(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go PersistEvents(ctx, bus, dbtx)

	time.Sleep(20 * time.Millisecond)
	bus.Publish(events.Event{Level: events.LevelInfo, Kind: "run.end", Message: "persisted"})

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if n, _ := db.CountLedger(ctx, dbtx); n >= 0 {
			evs, _ := db.RecentEvents(ctx, dbtx, 10)
			if len(evs) > 0 && evs[0].Message == "persisted" {
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("event was not persisted to the events table")
}

func TestLocalhostBindAndNoSecrets(t *testing.T) {
	srv := New(Config{Profile: "p", Host: "127.0.0.1", Port: 7777})
	if !strings.HasPrefix(srv.Addr(), "127.0.0.1:") {
		t.Errorf("dashboard must bind loopback; got %q", srv.Addr())
	}
	// The server config carries no secret fields — only DB/manager/bus/static.
	// A response can therefore never include an OAuth token or API key.
	srv2, dbtx, _, _ := newTestServer(t)
	for _, path := range []string{"/health", "/api/usage", "/api/vault"} {
		rec := httptest.NewRecorder()
		srv2.Handler().ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		body := rec.Body.String()
		for _, secret := range []string{"sk-ant", "oauth", "OAUTH", "ANTHROPIC_API_KEY", "token_"} {
			if strings.Contains(body, secret) {
				t.Errorf("%s leaked a secret-like string %q: %s", path, secret, body)
			}
		}
	}
	_ = dbtx
}
