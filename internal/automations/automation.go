// Package automations is AXON's portable, observable, budget-aware automation
// engine (Component 06). The engine enforces the run lifecycle — lock, run
// record, change-gate, budget pre-check, dry-run, accounting, and "never leave a
// half-edited note" — so individual automations only implement their detection
// and their work. Automations act on NEW MATERIAL, never on a clock for its own
// sake (FR-31): with nothing changed, the engine skips and makes no Claude call.
package automations

import (
	"context"
	"database/sql"
	"log/slog"
	"time"

	"github.com/jandro-es/axon/internal/config"
	"github.com/jandro-es/axon/internal/embeddings"
	"github.com/jandro-es/axon/internal/ingestion"
	"github.com/jandro-es/axon/internal/search"
	"github.com/jandro-es/axon/internal/tokens"
	"github.com/jandro-es/axon/internal/vault"
)

// Change is the verdict of a cheap, no-model change detection.
type Change struct {
	Changed bool
	Reason  string
	Cursor  string // opaque state persisted between runs (on a successful, non-dry run)
}

// RunResult is what an automation did (or, under dry-run, would do).
type RunResult struct {
	Summary         string   // one-line human summary
	Changes         []string // intended/applied edits, shown in dry-run and stored in runs.changes
	EstimatedTokens int      // pre-flight estimate for a model call (0 for no-model automations)
}

// RunCtx gives an automation everything it needs, all wikilink-safe and
// budget-mediated. Automations MUST honour DryRun (compute + report, write
// nothing) and route every Claude call through Manager (cardinal rule 1).
type RunCtx struct {
	Profile    string
	Config     config.Profile
	DB         *sql.DB
	Vault      *vault.FS
	Manager    tokens.Manager
	Searcher   *search.Searcher
	Embedder   embeddings.Provider
	Pipeline   *ingestion.Pipeline
	Log        *slog.Logger
	DryRun     bool
	RunID      int64
	LastCursor string // cursor persisted by the previous successful run
	Now        func() time.Time
}

// now returns the run's clock (injectable for tests).
func (rc RunCtx) now() time.Time {
	if rc.Now != nil {
		return rc.Now()
	}
	return time.Now()
}

// Automation is the contract every automation implements (docs/06 §2).
type Automation interface {
	// Name is the stable identifier (matches the config key).
	Name() string
	// Essential automations are never paused by budget-guard and are surfaced,
	// not silently blocked.
	Essential() bool
	// DetectChange is cheap and makes NO model call: it decides whether there is
	// new material worth processing and returns the cursor to persist.
	DetectChange(ctx context.Context, rc RunCtx) (Change, error)
	// Run does the work, honouring rc.DryRun and routing Claude calls via
	// rc.Manager.
	Run(ctx context.Context, rc RunCtx) (RunResult, error)
}
