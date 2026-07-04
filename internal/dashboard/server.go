// Package dashboard is AXON's local operational observability surface (Component
// 09): a localhost-bound HTTP server that serves the embedded SPA, exposes a
// small REST API over the derived DB read-layer, and streams the live event bus
// over SSE. It holds no secrets and binds to loopback only (FR-63). It never
// calls Claude and never free-form writes; the only mutations are review-queue
// resolutions applied through the vault's wikilink-safe ops (ADR-020).
package dashboard

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/jandro-es/axon/internal/db"
	"github.com/jandro-es/axon/internal/events"
	"github.com/jandro-es/axon/internal/review"
	"github.com/jandro-es/axon/internal/tokens"
	"github.com/jandro-es/axon/internal/vault"
)

// Config configures the dashboard server.
type Config struct {
	Profile string
	Host    string // default 127.0.0.1
	Port    int    // default 7777
	DB      *sql.DB
	Manager tokens.Manager // for the usage view (matches `axon status`)
	Bus     *events.Bus    // live event stream
	Static  fs.FS          // embedded SPA assets (nil -> minimal built-in page)
	// Health optionally supplies extra health detail (e.g. Ollama reachability).
	Health func(ctx context.Context) map[string]any
	// Vault enables the Review tab's accept/dismiss actions (ADR-020). nil
	// disables the review endpoints (read-only deployments).
	Vault *vault.FS
}

// Server is the dashboard HTTP server.
type Server struct {
	cfg  Config
	addr string
}

// New constructs a dashboard server.
func New(cfg Config) *Server {
	if cfg.Host == "" {
		cfg.Host = "127.0.0.1"
	}
	if cfg.Port == 0 {
		cfg.Port = 7777
	}
	return &Server{cfg: cfg, addr: fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)}
}

// Addr returns the bound host:port.
func (s *Server) Addr() string { return s.addr }

// Handler builds the HTTP routes.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", s.handleHealth)
	mux.HandleFunc("GET /events", s.handleSSE)
	mux.HandleFunc("GET /api/tokens", s.jsonHandler(s.dataTokens))
	mux.HandleFunc("GET /api/usage", s.jsonHandler(s.dataUsage))
	mux.HandleFunc("GET /api/runs", s.jsonHandler(s.dataRuns))
	mux.HandleFunc("GET /api/ingestion", s.jsonHandler(s.dataIngestion))
	mux.HandleFunc("GET /api/vault", s.jsonHandler(s.dataVault))
	mux.HandleFunc("GET /api/graph", s.jsonHandler(s.dataGraph))
	mux.HandleFunc("GET /api/activity", s.jsonHandler(s.dataActivity))
	mux.HandleFunc("GET /api/review", s.jsonHandler(s.dataReview))
	mux.HandleFunc("POST /api/review/action", s.handleReviewAction)
	mux.HandleFunc("GET /api/export", s.handleExport)
	mux.Handle("/", s.staticHandler())
	return s.guardHost(mux)
}

// guardHost rejects requests whose Host header is not loopback. This defeats
// DNS-rebinding: a malicious web page the user visits cannot rebind a hostname
// to 127.0.0.1 and read the (localhost-only) dashboard API, because the browser
// still sends the attacker's Host header (FR-63 hardening).
func (s *Server) guardHost(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.Host
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		switch host {
		case "localhost", "127.0.0.1", "::1", "[::1]":
			next.ServeHTTP(w, r)
		default:
			http.Error(w, "forbidden host", http.StatusForbidden)
		}
	})
}

// ListenAndServe binds the configured (loopback) address and serves until ctx
// is cancelled. Every request's context derives from ctx (via BaseContext), so
// cancelling ctx promptly unblocks in-flight SSE handlers instead of waiting out
// the shutdown grace period.
func (s *Server) ListenAndServe(ctx context.Context) error {
	srv := &http.Server{
		Addr:              s.addr,
		Handler:           s.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		BaseContext:       func(net.Listener) context.Context { return ctx },
	}
	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutCtx)
	}()
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

// jsonHandler wraps a data function into an HTTP handler emitting JSON.
func (s *Server) jsonHandler(fn func(ctx context.Context, r *http.Request) (any, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		data, err := fn(r.Context(), r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, data)
	}
}

func writeJSON(w http.ResponseWriter, data any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(data)
}

// --- data endpoints ---------------------------------------------------------

func (s *Server) dataTokens(ctx context.Context, r *http.Request) (any, error) {
	days := queryInt(r, "days", 30)
	since := time.Now().UTC().AddDate(0, 0, -days).Format(time.RFC3339)
	return db.TokenSeries(ctx, s.cfg.DB, since)
}

func (s *Server) dataUsage(ctx context.Context, _ *http.Request) (any, error) {
	if s.cfg.Manager == nil {
		return map[string]any{}, nil
	}
	st, err := s.cfg.Manager.Status(ctx, s.cfg.Profile)
	if err != nil {
		return nil, err
	}
	// Mirror exactly what `axon status` reports.
	return map[string]any{
		"profile":       st.Profile,
		"day_used":      st.Day.Used,
		"day_limit":     st.Day.Limit,
		"day_pct":       st.Day.Pct,
		"week_used":     st.Week.Used,
		"week_limit":    st.Week.Limit,
		"week_pct":      st.Week.Pct,
		"guard_pct":     st.GuardPct,
		"guard_paused":  st.GuardPaused,
		"guard_reason":  st.GuardReason,
		"day_cost_used": st.Day.CostUsed,
		"day_cost_cap":  st.Day.CostCap,
		"day_cost_pct":  st.Day.CostPct,
	}, nil
}

func (s *Server) dataRuns(ctx context.Context, r *http.Request) (any, error) {
	return db.RecentRuns(ctx, s.cfg.DB, queryInt(r, "limit", 100))
}

func (s *Server) dataIngestion(ctx context.Context, _ *http.Request) (any, error) {
	series, err := db.SourceSeries(ctx, s.cfg.DB)
	if err != nil {
		return nil, err
	}
	pending, err := db.PendingEmbeddings(ctx, s.cfg.DB)
	if err != nil {
		return nil, err
	}
	return map[string]any{"series": series, "embedding_queue": pending}, nil
}

func (s *Server) dataVault(ctx context.Context, _ *http.Request) (any, error) {
	stats, err := db.Stats(ctx, s.cfg.DB)
	if err != nil {
		return nil, err
	}
	growth, err := db.VaultGrowth(ctx, s.cfg.DB)
	if err != nil {
		return nil, err
	}
	return map[string]any{"stats": stats, "growth": growth}, nil
}

func (s *Server) dataGraph(ctx context.Context, r *http.Request) (any, error) {
	includeSimilar := r.URL.Query().Get("similar") == "1"
	return db.GraphData(ctx, s.cfg.DB, queryInt(r, "limit", 1000), includeSimilar)
}

func (s *Server) dataActivity(ctx context.Context, r *http.Request) (any, error) {
	return db.RecentEvents(ctx, s.cfg.DB, queryInt(r, "limit", 200))
}

func (s *Server) dataReview(ctx context.Context, _ *http.Request) (any, error) {
	if s.cfg.Vault == nil {
		return map[string]any{"items": []review.Item{}, "pending": 0}, nil
	}
	items, err := review.Load(ctx, s.cfg.Vault)
	if err != nil {
		return nil, err
	}
	pending := 0
	for _, it := range items {
		if !it.Checked {
			pending++
		}
	}
	if items == nil {
		items = []review.Item{}
	}
	return map[string]any{"items": items, "pending": pending}, nil
}

// handleReviewAction is the dashboard's only mutating endpoint (ADR-020).
// The JSON content type + custom header force a CORS preflight that no
// cross-origin page can pass (this server sends no CORS headers), on top of
// the loopback bind and Host guard.
func (s *Server) handleReviewAction(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Vault == nil {
		http.Error(w, "review actions unavailable (no vault wired)", http.StatusServiceUnavailable)
		return
	}
	if r.Header.Get("X-Axon-Review") != "1" ||
		!strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	var in struct {
		ID     string `json:"id"`
		Action string `json:"action"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	var item review.Item
	var err error
	switch in.Action {
	case "accept":
		item, err = review.Accept(r.Context(), s.cfg.Vault, in.ID)
	case "dismiss":
		item, err = review.Dismiss(r.Context(), s.cfg.Vault, in.ID)
	default:
		http.Error(w, "action must be accept or dismiss", http.StatusBadRequest)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if s.cfg.Bus != nil {
		s.cfg.Bus.Publish(events.Event{
			Level: events.LevelInfo, Kind: "review." + in.Action,
			Message: in.Action + ": " + item.Line,
			Data:    map[string]any{"profile": s.cfg.Profile, "id": item.ID, "kind": item.Kind},
		})
	}
	writeJSON(w, map[string]any{"item": item})
}

func queryInt(r *http.Request, key string, def int) int {
	v := r.URL.Query().Get(key)
	if v == "" {
		return def
	}
	var n int
	if _, err := fmt.Sscanf(v, "%d", &n); err != nil || n <= 0 {
		return def
	}
	return n
}
