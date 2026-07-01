package automations

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/jandro-es/axon/internal/config"
	"github.com/jandro-es/axon/internal/db"
	"github.com/jandro-es/axon/internal/embeddings"
	"github.com/jandro-es/axon/internal/events"
	"github.com/jandro-es/axon/internal/ingestion"
	"github.com/jandro-es/axon/internal/search"
	"github.com/jandro-es/axon/internal/tokens"
	"github.com/jandro-es/axon/internal/vault"
)

// defaultRunTimeout bounds a single automation run; a hung run is cancelled and
// recorded as failed (FR-35).
const defaultRunTimeout = 5 * time.Minute

// EngineDeps are the shared services the engine threads into every RunCtx.
type EngineDeps struct {
	Profile  string
	Config   config.Profile
	DB       *sql.DB
	Vault    *vault.FS
	Manager  tokens.Manager
	Searcher *search.Searcher
	Embedder embeddings.Provider
	Pipeline *ingestion.Pipeline
	Bus      *events.Bus
	Log      *slog.Logger
	Now      func() time.Time
	Timeout  time.Duration
}

// Outcome is the engine's report of a single run.
type Outcome struct {
	Automation string   `json:"automation"`
	Status     string   `json:"status"` // ok | skipped | failed | dry-run
	SkipReason string   `json:"skip_reason,omitempty"`
	Summary    string   `json:"summary,omitempty"`
	Changes    []string `json:"changes,omitempty"`
	Tokens     int64    `json:"tokens"`
	Estimated  int      `json:"estimated_tokens,omitempty"`
	RunID      int64    `json:"run_id"`
	DryRun     bool     `json:"dry_run"`
	Err        string   `json:"error,omitempty"`
}

// Engine runs automations through the standard lifecycle.
type Engine struct {
	deps    EngineDeps
	timeout time.Duration
	now     func() time.Time
	locks   *lockSet
}

// NewEngine constructs an engine from shared deps.
func NewEngine(deps EngineDeps) *Engine {
	now := deps.Now
	if now == nil {
		now = time.Now
	}
	timeout := deps.Timeout
	if timeout == 0 {
		timeout = defaultRunTimeout
	}
	if deps.Log == nil {
		deps.Log = slog.New(slog.NewTextHandler(noopWriter{}, nil))
	}
	return &Engine{deps: deps, timeout: timeout, now: now, locks: newLockSet()}
}

// Run executes one automation through the full lifecycle: lock, open a runs row,
// change-gate (skip → no model call), budget pre-check (non-essential paused
// when guard active), run, accounting, and terminal status. A failure is
// recorded without leaving a half-edited note (vault writes are atomic).
func (e *Engine) Run(ctx context.Context, a Automation, dryRun bool) (Outcome, error) {
	name := a.Name()
	out := Outcome{Automation: name, DryRun: dryRun}

	// Advisory lock: never overlap two runs of the same automation (FR-35).
	unlock, ok := e.locks.tryLock(name)
	if !ok {
		out.Status = db.RunSkipped
		out.SkipReason = "already running (locked)"
		return out, nil
	}
	defer unlock()

	ctx, cancel := context.WithTimeout(ctx, e.timeout)
	defer cancel()

	started := e.now().UTC()
	runID, err := db.InsertRun(ctx, e.deps.DB, name, started.Format(time.RFC3339))
	if err != nil {
		return out, err
	}
	out.RunID = runID

	rc := e.runCtx(runID, dryRun)
	if cursor, cerr := db.GetCursor(ctx, e.deps.DB, name); cerr == nil {
		rc.LastCursor = cursor
	}

	// 1. Change-gate (cheap, no model). Nothing new → skip, no Claude call.
	change, err := a.DetectChange(ctx, rc)
	if err != nil {
		return e.finishFailed(ctx, out, runID, err)
	}
	if !change.Changed {
		return e.finishSkipped(ctx, out, runID, change.Reason)
	}

	// 2. Budget pre-check: non-essential automations pause when guard is active.
	// Surface the guard's own explanation (which window, how far over) so a
	// skipped run says *why*, not just "budget".
	if !a.Essential() {
		if st, serr := e.deps.Manager.Status(ctx, e.deps.Profile); serr == nil && st.GuardPaused {
			reason := st.GuardReason
			if reason == "" {
				reason = "budget guard active — non-essential automations paused"
			}
			return e.finishSkipped(ctx, out, runID, reason)
		}
	}

	// 3. Do the work.
	res, err := a.Run(ctx, rc)
	if err != nil {
		return e.finishFailed(ctx, out, runID, err)
	}

	// 4. Accounting + terminal status.
	tokensUsed, _ := db.SumRunTokens(ctx, e.deps.DB, runID)
	status := db.RunOK
	if dryRun {
		status = db.RunDryRun
	}
	out.Status = status
	out.Summary = res.Summary
	out.Changes = res.Changes
	out.Estimated = res.EstimatedTokens
	out.Tokens = tokensUsed

	if err := db.FinishRun(ctx, e.deps.DB, db.RunUpdate{
		ID: runID, Status: status, FinishedAt: e.now().UTC().Format(time.RFC3339),
		Changes: strings.Join(res.Changes, "\n"), Tokens: tokensUsed,
	}); err != nil {
		return out, err
	}

	// Persist the change-gate cursor only on a real (non-dry) successful run, so
	// a dry-run never "consumes" the change. A dropped cursor write would make
	// the next tick re-detect the same material as "changed" and re-spend tokens,
	// so surface the failure rather than swallowing it.
	if !dryRun && change.Cursor != "" {
		if err := db.SetCursor(ctx, e.deps.DB, name, change.Cursor, e.now().UTC().Format(time.RFC3339)); err != nil {
			e.deps.Log.Warn("failed to persist change-gate cursor; automation may re-run on unchanged material",
				"automation", name, "error", err)
		}
	}

	e.emit(events.LevelInfo, "automation.run", out)
	return out, nil
}

func (e *Engine) runCtx(runID int64, dryRun bool) RunCtx {
	return RunCtx{
		Profile:  e.deps.Profile,
		Config:   e.deps.Config,
		DB:       e.deps.DB,
		Vault:    e.deps.Vault,
		Manager:  e.deps.Manager,
		Searcher: e.deps.Searcher,
		Embedder: e.deps.Embedder,
		Pipeline: e.deps.Pipeline,
		Log:      e.deps.Log,
		DryRun:   dryRun,
		RunID:    runID,
		Now:      e.now,
	}
}

func (e *Engine) finishSkipped(ctx context.Context, out Outcome, runID int64, reason string) (Outcome, error) {
	out.Status = db.RunSkipped
	out.SkipReason = reason
	if err := db.FinishRun(ctx, e.deps.DB, db.RunUpdate{
		ID: runID, Status: db.RunSkipped, FinishedAt: e.now().UTC().Format(time.RFC3339), SkipReason: reason,
	}); err != nil {
		e.deps.Log.Warn("failed to record skipped run", "automation", out.Automation, "error", err)
	}
	e.emit(events.LevelInfo, "automation.skip", out)
	return out, nil
}

func (e *Engine) finishFailed(ctx context.Context, out Outcome, runID int64, runErr error) (Outcome, error) {
	out.Status = db.RunFailed
	out.Err = runErr.Error()
	if err := db.FinishRun(ctx, e.deps.DB, db.RunUpdate{
		ID: runID, Status: db.RunFailed, FinishedAt: e.now().UTC().Format(time.RFC3339), Error: runErr.Error(),
	}); err != nil {
		e.deps.Log.Warn("failed to record failed run", "automation", out.Automation, "error", err)
	}
	e.emit(events.LevelError, "automation.fail", out)
	return out, runErr
}

func (e *Engine) emit(level events.Level, kind string, out Outcome) {
	if e.deps.Bus == nil {
		return
	}
	e.deps.Bus.Publish(events.Event{
		Level: level, Kind: kind,
		Message: fmt.Sprintf("%s: %s", out.Automation, out.Status),
		Data:    map[string]any{"profile": e.deps.Profile, "outcome": out},
	})
}

// lockSet is a per-name advisory lock manager (in-process; the daemon is a
// single process per profile).
type lockSet struct {
	mu   sync.Mutex
	held map[string]bool
}

func newLockSet() *lockSet { return &lockSet{held: make(map[string]bool)} }

func (l *lockSet) tryLock(name string) (func(), bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.held[name] {
		return nil, false
	}
	l.held[name] = true
	return func() {
		l.mu.Lock()
		delete(l.held, name)
		l.mu.Unlock()
	}, true
}

// ErrUnknownAutomation is returned by a registry lookup miss.
var ErrUnknownAutomation = errors.New("unknown automation")

type noopWriter struct{}

func (noopWriter) Write(p []byte) (int, error) { return len(p), nil }
