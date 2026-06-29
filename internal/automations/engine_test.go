package automations

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jandro-es/axon/internal/agent"
	"github.com/jandro-es/axon/internal/config"
	"github.com/jandro-es/axon/internal/db"
	"github.com/jandro-es/axon/internal/embeddings"
	"github.com/jandro-es/axon/internal/search"
	"github.com/jandro-es/axon/internal/tokens"
	"github.com/jandro-es/axon/internal/vault"
)

// harnessClock is the fixed time every harness-built engine and token manager
// runs at, so tests are deterministic and never depend on the real date.
var harnessClock = time.Date(2026, 6, 28, 12, 0, 0, 0, time.UTC)

type harness struct {
	engine *Engine
	vault  *vault.FS
	db     interface {
		db.Queryer
		db.DBTX
	}
	agent *agent.Fake
}

func newHarness(t *testing.T, limits config.LimitsConfig) *harness {
	t.Helper()
	vdir := t.TempDir()
	d, err := db.Open(db.MemoryDSN)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	if _, err := db.Migrate(d); err != nil {
		t.Fatal(err)
	}
	fake := agent.NewFake()
	emb := embeddings.NewFake()
	searcher := search.New(d, emb)
	profile := config.Profile{
		Models: config.ModelsConfig{Classify: "haiku", Routine: "sonnet", Synthesis: "opus"},
		Limits: limits,
	}
	// The manager shares the harness clock so budget windows are keyed to the
	// same day the engine reports — otherwise budget tests depend on the real
	// wall-clock date and rot over time.
	mgr := tokens.New(d, fake, searcher, nil, tokens.Config{
		Profile: "test", AuthMode: "subscription", Models: profile.Models, Limits: limits,
		Now: func() time.Time { return harnessClock },
	})
	eng := NewEngine(EngineDeps{
		Profile: "test", Config: profile, DB: d, Vault: vault.NewFS(vdir),
		Manager: mgr, Searcher: searcher, Embedder: emb,
		Now: func() time.Time { return harnessClock },
	})
	return &harness{engine: eng, vault: vault.NewFS(vdir), db: d, agent: fake}
}

func genLimits() config.LimitsConfig {
	return config.LimitsConfig{DailyTokens: 1_000_000, WeeklyTokens: 5_000_000, GuardPauseAtPct: 80}
}

// --- test doubles -----------------------------------------------------------

type fakeAutomation struct {
	name      string
	essential bool
	changed   bool
	runFn     func(ctx context.Context, rc RunCtx) (RunResult, error)
}

func (f fakeAutomation) Name() string    { return f.name }
func (f fakeAutomation) Essential() bool { return f.essential }
func (f fakeAutomation) DetectChange(ctx context.Context, rc RunCtx) (Change, error) {
	return Change{Changed: f.changed, Reason: "test", Cursor: "c1"}, nil
}
func (f fakeAutomation) Run(ctx context.Context, rc RunCtx) (RunResult, error) {
	return f.runFn(ctx, rc)
}

// --- tests ------------------------------------------------------------------

func TestEngineSkipsWhenNothingChangedNoModelCall(t *testing.T) {
	h := newHarness(t, genLimits())
	a := fakeAutomation{name: "x", changed: false, runFn: func(ctx context.Context, rc RunCtx) (RunResult, error) {
		t.Fatal("Run must not be called when unchanged")
		return RunResult{}, nil
	}}
	out, err := h.engine.Run(context.Background(), a, false)
	if err != nil {
		t.Fatal(err)
	}
	if out.Status != db.RunSkipped {
		t.Errorf("status = %q, want skipped", out.Status)
	}
	if h.agent.CallCount() != 0 {
		t.Error("skipped run made a model call (S3 violation)")
	}
	// A skipped run is recorded.
	if n, _ := db.CountRuns(context.Background(), h.db); n != 1 {
		t.Errorf("runs = %d, want 1", n)
	}
}

func TestEngineDryRunWritesNothingButEstimates(t *testing.T) {
	h := newHarness(t, genLimits())
	ctx := context.Background()
	// A daily note so daily-log has material.
	day := "Daily/2026-06-28.md"
	if _, err := h.vault.Create(day, "---\ntitle: d\ntype: daily\n---\n## Log\n- did things today\n"); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(filepath.Join(h.vault.Root(), filepath.FromSlash(day)))

	out, err := h.engine.Run(ctx, DailyLog{}, true)
	if err != nil {
		t.Fatal(err)
	}
	if out.Status != db.RunDryRun {
		t.Errorf("status = %q, want dry-run", out.Status)
	}
	if out.Estimated == 0 {
		t.Error("dry-run should report a token estimate")
	}
	if h.agent.CallCount() != 0 {
		t.Error("dry-run executed a model call")
	}
	after, _ := os.ReadFile(filepath.Join(h.vault.Root(), filepath.FromSlash(day)))
	if string(before) != string(after) {
		t.Error("dry-run modified the note")
	}
	// Dry-run must not persist the change-gate cursor.
	if c, _ := db.GetCursor(ctx, h.db, "daily-log"); c != "" {
		t.Errorf("dry-run persisted cursor %q", c)
	}
}

func TestEngineModelRunLedgersAgainstRun(t *testing.T) {
	h := newHarness(t, genLimits())
	ctx := context.Background()
	h.agent.RespondFn = func(r agent.Request) (*agent.Response, error) {
		return &agent.Response{Text: "- summary point", Model: r.Model, Usage: agent.Usage{InputTokens: 80, OutputTokens: 20}}, nil
	}
	day := "Daily/2026-06-28.md"
	if _, err := h.vault.Create(day, "---\ntitle: d\ntype: daily\n---\n## Log\n- shipped the engine\n"); err != nil {
		t.Fatal(err)
	}

	out, err := h.engine.Run(ctx, DailyLog{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if out.Status != db.RunOK {
		t.Fatalf("status = %q, want ok", out.Status)
	}
	if h.agent.CallCount() != 1 {
		t.Errorf("model calls = %d, want 1", h.agent.CallCount())
	}
	if out.Tokens != 100 {
		t.Errorf("run tokens = %d, want 100 (linked by run_id)", out.Tokens)
	}
	n, _ := h.vault.Read(ctx, day)
	if !strings.Contains(n.Body, "axon:summary:start") || !strings.Contains(n.Body, "summary point") {
		t.Errorf("summary not written: %q", n.Body)
	}
	// Cursor persisted on a real successful run.
	if c, _ := db.GetCursor(ctx, h.db, "daily-log"); c == "" {
		t.Error("expected change-gate cursor to be persisted")
	}
}

func TestEngineBudgetPausePausesNonEssential(t *testing.T) {
	// Tiny limits + pre-spend so guard is active.
	limits := config.LimitsConfig{DailyTokens: 100, WeeklyTokens: 1000, GuardPauseAtPct: 80}
	h := newHarness(t, limits)
	ctx := context.Background()
	_ = db.AddBudgetUsage(ctx, h.db, "test", "day", harnessClock.UTC().Format("2006-01-02"), 90, 0)

	nonEssential := fakeAutomation{name: "ne", essential: false, changed: true,
		runFn: func(ctx context.Context, rc RunCtx) (RunResult, error) { return RunResult{Summary: "ran"}, nil }}
	out, err := h.engine.Run(ctx, nonEssential, false)
	if err != nil {
		t.Fatal(err)
	}
	if out.Status != db.RunSkipped || out.SkipReason != "budget" {
		t.Errorf("non-essential should skip for budget; got %+v", out)
	}

	// Essential automations are never paused.
	essential := fakeAutomation{name: "ess", essential: true, changed: true,
		runFn: func(ctx context.Context, rc RunCtx) (RunResult, error) { return RunResult{Summary: "ran"}, nil }}
	out, err = h.engine.Run(ctx, essential, false)
	if err != nil {
		t.Fatal(err)
	}
	if out.Status != db.RunOK {
		t.Errorf("essential should run despite guard; got %+v", out)
	}
}

func TestEngineFailedRunRecordedNoHalfEdit(t *testing.T) {
	h := newHarness(t, genLimits())
	ctx := context.Background()
	target := "01-Projects/p.md"
	if _, err := h.vault.Create(target, "original content\n"); err != nil {
		t.Fatal(err)
	}

	boom := fakeAutomation{name: "boom", changed: true, runFn: func(ctx context.Context, rc RunCtx) (RunResult, error) {
		// Error before writing: the note must be untouched.
		return RunResult{}, errors.New("kaboom")
	}}
	out, err := h.engine.Run(ctx, boom, false)
	if err == nil {
		t.Fatal("expected the run error to propagate")
	}
	if out.Status != db.RunFailed || out.Err == "" {
		t.Errorf("expected failed status with error; got %+v", out)
	}
	n, _ := h.vault.Read(ctx, target)
	if n.Body != "original content\n" {
		t.Errorf("note was modified by a failed run: %q", n.Body)
	}
	if s, _ := db.LastRunStatus(ctx, h.db, "boom"); s != db.RunFailed {
		t.Errorf("last run status = %q, want failed", s)
	}
}

func TestEngineLockPreventsOverlap(t *testing.T) {
	ls := newLockSet()
	unlock, ok := ls.tryLock("a")
	if !ok {
		t.Fatal("first lock should succeed")
	}
	if _, ok := ls.tryLock("a"); ok {
		t.Error("second lock on same name should fail")
	}
	unlock()
	if _, ok := ls.tryLock("a"); !ok {
		t.Error("lock should be reacquirable after unlock")
	}
}
