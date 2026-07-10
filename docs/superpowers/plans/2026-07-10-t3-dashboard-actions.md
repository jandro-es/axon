# T3 — Dashboard Actions Tab + Completion Mutation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** The dashboard Actions tab (`GET /api/actions` list + counts + trend, an Actions SPA tab) and the one new mutation — `POST /api/actions/complete` → `vault.CompleteAction`, a byte-precise hash-addressed checkbox toggle (ADR-034).

**Architecture:** A pure `actions.Complete` + `actions.BucketFields` extend T1. `vault.CompleteAction` composes them with the existing atomic-write primitives (`splitFrontmatter`/`writeRaw`/`reassemble`) and returns a new `ErrActionNotFound` sentinel → 409. Two dashboard endpoints clone `handleRelated` (read) and `handleReviewAction` (write); an `ActionsTab` clones `ReviewTab`. Zero model calls.

**Tech Stack:** Go 1.26+, `modernc.org/sqlite`, net/http `ServeMux`, Vite+React+Recharts (`web/`).

**Spec:** `docs/superpowers/specs/2026-07-10-t3-dashboard-actions-design.md`

## Global Constraints

- **Cardinal rule 1:** No Claude call bypasses the token manager. *T3 makes zero model calls — no ledger entry.*
- **Cardinal rule 2:** No vault mutation outside wikilink-safe ops. *`CompleteAction` is the ONE amendment (ADR-034): a byte-precise, hash-addressed, open-only, refuse-on-stale toggle of a single line, user-initiated via the loopback dashboard only, never agent/model-driven, never a delete. All other writes stay managed-block.*
- **Trust boundary (ADR-023):** both endpoints behind `guardHost` (loopback+Host); the write requires `X-Axon-Actions: 1` + `application/json` + the `actions_enabled` kill-switch (404 when off).
- **S8:** `actions_enabled: false` removes both endpoints + the tab; the index/CLI/automation are unaffected.
- **S9:** `MarkActionDone` edits the *derived* table to what the next reindex would reproduce from the now-`[x]` source line — consistent, not authoritative. The vault is updated first.
- **NFR-06:** `CompleteAction` writes via `writeRaw` (temp+rename); a failed op leaves the note intact.
- Go: `gofmt`/`goimports` clean, `go vet` + `golangci-lint` green. Wrap errors with `%w`. Propagate `context.Context`.
- Run Go suites with `env -u FORCE_COLOR`. Build the SPA with `npm run build` in `web/` (re-touches `dist/.gitkeep`).
- Kill-switch defaults ON (`ActionsEnabled *bool`, pointer-default). `✅ date` = server local date `time.Now().Format("2006-01-02")`. `action.done` is the one new SSE kind (consolidation surfaces via existing `automation.run`).
- FR IDs: FR-162 (GET), FR-163 (POST + CompleteAction), FR-164 (config/health/SSE/SPA). New ADR: **ADR-034**.
- GateGuard fires a fact-force preamble on the first Write/Edit/Bash each turn and blocks `git commit --amend` / `rm -rf`; comply tersely, use follow-up commits, skip scratch cleanup. Never touch the user's `:7777` daemon (smoke on another port).

---

### Task 1: `actions.Complete` + `actions.BucketFields` (FR-162/163)

**Files:**
- Modify: `internal/actions/action.go` (add `Complete`, `BucketFields`; make `Bucket` delegate)
- Test: `internal/actions/complete_test.go` (create)

**Interfaces:**
- Consumes (existing in pkg): `checkboxRe` (`^\s*[-*+] \[(.)\] (.*)$`), `State`/`StateOpen`, `hasTag`.
- Produces (for Tasks 2/4): `func Complete(line, date string) (string, bool)`; `func BucketFields(state, due, scheduled, start string, tags []string, today time.Time) string`.

- [ ] **Step 1: Write the failing test**

Create `internal/actions/complete_test.go`:

```go
package actions

import (
	"strings"
	"testing"
)

func TestComplete(t *testing.T) {
	cases := []struct {
		line string
		ok   bool
		want string // when ok; "✅" checked separately
	}{
		{"- [ ] call bob", true, "- [x] call bob ✅ 2026-07-10"},
		{"- [/] in progress", true, "- [x] in progress ✅ 2026-07-10"},   // unknown-open marker
		{"  * [ ] indented star", true, "  * [x] indented star ✅ 2026-07-10"}, // preserves indent+bullet
		{"- [ ] has date 📅 2026-07-15", true, "- [x] has date 📅 2026-07-15 ✅ 2026-07-10"},
		{"- [x] already done", false, ""},
		{"- [X] already done", false, ""},
		{"- [-] cancelled", false, ""},
		{"not a task", false, ""},
	}
	for _, c := range cases {
		got, ok := Complete(c.line, "2026-07-10")
		if ok != c.ok {
			t.Fatalf("Complete(%q) ok=%v want %v", c.line, ok, c.ok)
		}
		if ok && got != c.want {
			t.Errorf("Complete(%q) = %q want %q", c.line, got, c.want)
		}
	}
}

func TestCompleteIdempotentTick(t *testing.T) {
	// A line that somehow already carries ✅ is not double-stamped.
	got, ok := Complete("- [ ] weird ✅ 2026-07-01", "2026-07-10")
	if !ok {
		t.Fatal("expected ok")
	}
	if strings.Count(got, "✅") != 1 {
		t.Errorf("must not double-stamp ✅: %q", got)
	}
	if !strings.HasPrefix(got, "- [x]") {
		t.Errorf("marker not flipped: %q", got)
	}
}

func TestBucketFieldsMatchesBucket(t *testing.T) {
	today := day("2026-07-10")
	a := Action{State: StateOpen, Due: "2026-07-09"}
	if BucketFields("open", "2026-07-09", "", "", nil, today) != "overdue" {
		t.Error("BucketFields overdue wrong")
	}
	if Bucket(a, today) != BucketFields(string(a.State), a.Due, a.Scheduled, a.Start, a.Tags, today) {
		t.Error("Bucket must delegate to BucketFields")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `env -u FORCE_COLOR go test ./internal/actions/ -run 'TestComplete|TestBucketFields' -v`
Expected: FAIL — `undefined: Complete` / `undefined: BucketFields`.

- [ ] **Step 3: Write minimal implementation**

In `internal/actions/action.go`, replace the `Bucket` function body to delegate, and add `BucketFields` + `Complete`:

```go
// Bucket resolves the single GTD bucket by precedence (delegates to BucketFields).
func Bucket(a Action, today time.Time) string {
	return BucketFields(string(a.State), a.Due, a.Scheduled, a.Start, a.Tags, today)
}

// BucketFields is Bucket over raw fields, so callers holding a db row (or any
// field set) need not construct an Action. Precedence:
// done > cancelled > someday > waiting > overdue > today > scheduled > next.
func BucketFields(state, due, scheduled, start string, tags []string, today time.Time) string {
	switch State(state) {
	case StateDone:
		return "done"
	case StateCancelled:
		return "cancelled"
	}
	t := today.Format("2006-01-02")
	switch {
	case hasTag(tags, "someday"):
		return "someday"
	case hasTag(tags, "waiting"):
		return "waiting"
	case due != "" && due < t:
		return "overdue"
	case due == t:
		return "today"
	case start > t || scheduled > t:
		return "scheduled"
	default:
		return "next"
	}
}

// Complete flips an OPEN checkbox line's marker to 'x' and appends " ✅ <date>"
// (unless a ✅ is already present), preserving indentation, bullet char, and the
// rest of the line byte-for-byte. ok=false if the line is not an open action.
func Complete(line, date string) (string, bool) {
	m := checkboxRe.FindStringSubmatch(line)
	if m == nil {
		return "", false
	}
	switch m[1] {
	case "x", "X", "-": // already done/cancelled
		return "", false
	}
	marker := "[" + m[1] + "]"
	idx := strings.Index(line, marker)
	if idx < 0 {
		return "", false
	}
	out := line[:idx] + "[x]" + line[idx+len(marker):]
	if !strings.Contains(out, "✅") {
		out = strings.TrimRight(out, " ") + " ✅ " + date
	}
	return out, true
}
```

> The existing `Bucket` had the `switch a.State {…}` body inline — replace it entirely with the delegating one-liner above so there is no duplicate logic. `day(...)` is the test helper already defined in `bucket_test.go` (same package).

- [ ] **Step 4: Run test to verify it passes**

Run: `env -u FORCE_COLOR go test ./internal/actions/ -v`
Expected: PASS (all, including the pre-existing `TestBucketPrecedence`).

- [ ] **Step 5: Commit**

```bash
git add internal/actions/action.go internal/actions/complete_test.go
git commit -m "feat(T3): actions.Complete + actions.BucketFields (Bucket delegates) (FR-162/163)

Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>"
```

---

### Task 2: `vault.CompleteAction` + `ErrActionNotFound` (FR-163, ADR-034)

**Files:**
- Create: `internal/vault/actions.go`
- Test: `internal/vault/actions_test.go`

**Interfaces:**
- Consumes: `(*FS).safeAbs` (fs.go:62), `splitFrontmatter` (note.go:29), `(*FS).writeRaw` (fs.go:334), `reassemble` (fs.go:399); `actions.Extract(path, body, archived) []actions.Action`, `actions.Action.Hash()`, `actions.StateOpen`, `actions.Complete` (Task 1).
- Produces (for Task 5): `var vault.ErrActionNotFound error`; `func (v *FS) CompleteAction(ctx context.Context, path, lineHash, date string) error`.

- [ ] **Step 1: Write the failing test**

Create `internal/vault/actions_test.go` (mirror `merge_test.go`'s `write`/`bodyOf` helpers — reuse them if present in the package's test files; otherwise use `newTempVault` + `os.ReadFile`):

```go
package vault

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jandro-es/axon/internal/actions"
)

func TestCompleteActionFlipsLine(t *testing.T) {
	ctx := context.Background()
	body := "## Todo\n- [ ] call bob 📅 2026-07-15\n- [ ] other task\n"
	note := "---\ntitle: T\n---\n" + body
	v := newTempVault(t, map[string]string{"01-Projects/p.md": note})

	// Hash the target line the T1 way (Extract stamps SourcePath/LineNo).
	var target string
	for _, a := range actions.Extract("01-Projects/p.md", body, false) {
		if strings.Contains(a.Text, "call bob") {
			target = a.Hash()
		}
	}
	if target == "" {
		t.Fatal("could not hash target line")
	}

	if err := v.CompleteAction(ctx, "01-Projects/p.md", target, "2026-07-10"); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(v.Root(), "01-Projects", "p.md"))
	got := string(raw)
	if !strings.Contains(got, "- [x] call bob 📅 2026-07-15 ✅ 2026-07-10") {
		t.Errorf("target line not completed:\n%s", got)
	}
	if !strings.Contains(got, "- [ ] other task") {
		t.Error("other task must be untouched")
	}
	if !strings.HasPrefix(got, "---\ntitle: T\n---\n") {
		t.Error("frontmatter must be byte-preserved")
	}
}

func TestCompleteActionStaleHash(t *testing.T) {
	ctx := context.Background()
	v := newTempVault(t, map[string]string{"p.md": "- [ ] x\n"})
	err := v.CompleteAction(ctx, "p.md", "deadbeef-not-a-real-hash", "2026-07-10")
	if !errors.Is(err, ErrActionNotFound) {
		t.Fatalf("want ErrActionNotFound, got %v", err)
	}
	raw, _ := os.ReadFile(filepath.Join(v.Root(), "p.md"))
	if string(raw) != "- [ ] x\n" {
		t.Errorf("file must be unchanged on stale hash: %q", raw)
	}
}
```

> If `newTempVault` isn't the helper name in `internal/vault` test files, use whatever `fs_test.go`/`merge_test.go` expose (grep `func newTempVault` / `func write(`). The behavioral asserts are the contract.

- [ ] **Step 2: Run test to verify it fails**

Run: `env -u FORCE_COLOR go test ./internal/vault/ -run TestCompleteAction -v`
Expected: FAIL — `undefined: (*FS).CompleteAction` / `undefined: ErrActionNotFound`.

- [ ] **Step 3: Write minimal implementation**

Create `internal/vault/actions.go`:

```go
package vault

import (
	"context"
	"errors"
	"os"
	"strings"

	"github.com/jandro-es/axon/internal/actions"
)

// ErrActionNotFound is returned (nothing written) when no OPEN checkbox line in
// the note has the given identity hash — a stale/unknown hash. The dashboard
// maps it to 409.
var ErrActionNotFound = errors.New("no matching open action")

// CompleteAction toggles the single open checkbox line whose T1 identity hash
// equals lineHash: [ ]→[x] and appends " ✅ <date>". Byte-precise and atomic;
// human prose around the line is untouched. It is the ONE vault mutation that
// edits a human-authored line rather than a managed block (ADR-034) —
// user-initiated only, never model/agent-driven. Returns ErrActionNotFound
// (nothing written) when no open line matches.
func (v *FS) CompleteAction(ctx context.Context, path, lineHash, date string) error {
	abs, err := v.safeAbs(path)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return err
	}
	fm, body := splitFrontmatter(string(data))
	// Reuse T1's Extract so we match the EXACT line the index hashed (same
	// fenced-code / axon:actions-block skips, same body-relative LineNo).
	for _, a := range actions.Extract(path, body, false) {
		if a.State != actions.StateOpen || a.Hash() != lineHash {
			continue
		}
		lines := strings.Split(body, "\n")
		newLine, ok := actions.Complete(lines[a.LineNo], date)
		if !ok {
			return ErrActionNotFound
		}
		lines[a.LineNo] = newLine
		return v.writeRaw(path, reassemble(fm, strings.Join(lines, "\n")))
	}
	return ErrActionNotFound
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `env -u FORCE_COLOR go test ./internal/vault/ -run TestCompleteAction -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/vault/actions.go internal/vault/actions_test.go
git commit -m "feat(T3): vault.CompleteAction — hash-addressed checkbox toggle (ADR-034, FR-163)

Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>"
```

---

### Task 3: `db.MarkActionDone` (FR-163)

**Files:**
- Modify: `internal/db/actions.go` (add `MarkActionDone`)
- Test: `internal/db/actions_test.go` (add a test)

**Interfaces:**
- Consumes: `Execer`, the `actions` table (T1).
- Produces (for Task 5): `func MarkActionDone(ctx context.Context, q Execer, hash, doneDate string) (int64, error)`.

- [ ] **Step 1: Write the failing test**

Add to `internal/db/actions_test.go`:

```go
func TestMarkActionDone(t *testing.T) {
	ctx := context.Background()
	d := newMigratedDB(t)
	if err := ReplaceActions(ctx, d, []Action{
		{Hash: "h1", SourcePath: "a.md", LineNo: 1, Text: "x", Raw: "- [ ] x",
			State: "open", Checkbox: " ", Updated: "u"},
	}); err != nil {
		t.Fatal(err)
	}
	n, err := MarkActionDone(ctx, d, "h1", "2026-07-10")
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("rows affected = %d, want 1", n)
	}
	got, _ := ListActions(ctx, d, ListActionsOpts{IncludeAll: true})
	if got[0].State != "done" || got[0].DoneDate != "2026-07-10" {
		t.Errorf("row not marked done: %+v", got[0])
	}
	// Unknown hash → 0 rows.
	if n2, _ := MarkActionDone(ctx, d, "nope", "2026-07-10"); n2 != 0 {
		t.Errorf("unknown hash affected %d rows, want 0", n2)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `env -u FORCE_COLOR go test ./internal/db/ -run TestMarkActionDone -v`
Expected: FAIL — `undefined: MarkActionDone`.

- [ ] **Step 3: Write minimal implementation**

Add to `internal/db/actions.go`:

```go
// MarkActionDone flips one derived row to done in place (the vault is already
// updated; this keeps the disposable index in step until the next reindex, which
// reproduces the same row from the now-[x] source line — S9-consistent). Returns
// rows affected (0 = unknown/already-done hash).
func MarkActionDone(ctx context.Context, q Execer, hash, doneDate string) (int64, error) {
	res, err := q.ExecContext(ctx,
		`UPDATE actions SET state='done', checkbox='x', done_date=? WHERE hash=? AND state='open';`,
		doneDate, hash)
	if err != nil {
		return 0, fmt.Errorf("mark action done: %w", err)
	}
	return res.RowsAffected()
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `env -u FORCE_COLOR go test ./internal/db/ -run TestMarkActionDone -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/db/actions.go internal/db/actions_test.go
git commit -m "feat(T3): db.MarkActionDone — surgical done update (FR-163)

Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>"
```

---

### Task 4: `GET /api/actions` + config + health (FR-162/164)

**Files:**
- Create: `internal/dashboard/actions.go` (`handleActions`, `buildActionsPayload`)
- Modify: `internal/dashboard/server.go` (Config field + mux registration)
- Modify: `internal/config/types.go` (`ActionsEnabled` + `ActionsAllowed()`)
- Modify: `internal/dashboard/health.go` (`actions_enabled` flag)
- Modify: `cmd/axon/start_cmd.go` (wire `ActionsEnabled`)
- Test: `internal/dashboard/actions_api_test.go` (create); `internal/config/dashboard_test.go` (add helper test)

**Interfaces:**
- Consumes: `db.ListActions`, `db.Action`, `actions.BucketFields` (Task 1); `writeJSON`, `guardHost`, `Config` (dashboard); `DashboardConfig` (config).
- Produces (for Task 5): the `dashboard.Config.ActionsEnabled bool` field; `buildActionsPayload(rows []db.Action, today time.Time) map[string]any`.

- [ ] **Step 1: Write the failing tests**

Create `internal/dashboard/actions_api_test.go`:

```go
package dashboard

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jandro-es/axon/internal/db"
)

func actionsTestServer(t *testing.T, enabled bool) (*httptest.Server, *db.DB) {
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
	return ts, d
}

func TestActionsAPIReturnsList(t *testing.T) {
	ts, _ := actionsTestServer(t, true)
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
	ts, _ := actionsTestServer(t, false)
	req, _ := http.NewRequest("GET", ts.URL+"/api/actions", nil)
	req.Header.Set("X-Axon-Actions", "1")
	resp, _ := http.DefaultClient.Do(req)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("disabled status = %d, want 404", resp.StatusCode)
	}
	resp.Body.Close()

	// header-less → 403
	ts2, _ := actionsTestServer(t, true)
	req2, _ := http.NewRequest("GET", ts2.URL+"/api/actions", nil)
	resp2, _ := http.DefaultClient.Do(req2)
	if resp2.StatusCode != http.StatusForbidden {
		t.Errorf("header-less status = %d, want 403", resp2.StatusCode)
	}
	resp2.Body.Close()
}
```

Add to `internal/config/dashboard_test.go`:

```go
func TestActionsAllowedDefaultsOn(t *testing.T) {
	if !(config.DashboardConfig{}).ActionsAllowed() {
		t.Error("nil ActionsEnabled must default to allowed")
	}
	off := false
	if (config.DashboardConfig{ActionsEnabled: &off}).ActionsAllowed() {
		t.Error("*false must disallow")
	}
}
```

> Match `dashboard_test.go`'s existing import style (it may use `package config` internally or `config_test` externally — mirror `TestAskAllowedDefaultsOn` exactly, including whether it qualifies with `config.`).

- [ ] **Step 2: Run tests to verify they fail**

Run: `env -u FORCE_COLOR go test ./internal/dashboard/ ./internal/config/ -run 'TestActions' -v`
Expected: FAIL — `unknown field ActionsEnabled` / `undefined: ActionsAllowed`.

- [ ] **Step 3: Write minimal implementation**

In `internal/config/types.go`, add to `DashboardConfig` (after `RelatedEnabled`):

```go
	// ActionsEnabled gates the actions endpoints + tab (1.2.5 T3, ADR-034).
	// Pointer default-ON: unset = enabled; set false to forbid the browser
	// completion write and hide the tab.
	ActionsEnabled *bool `yaml:"actions_enabled,omitempty"`
```

And the helper (next to `RelatedAllowed`):

```go
func (d DashboardConfig) ActionsAllowed() bool { return d.ActionsEnabled == nil || *d.ActionsEnabled }
```

In `internal/dashboard/server.go`, add to `Config` (near `RelatedEnabled`):

```go
	// ActionsEnabled powers GET /api/actions + POST /api/actions/complete + the
	// Actions tab (1.2.5 T3). Default-ON via config; false disables (404).
	ActionsEnabled bool
```

Register both routes in `Handler()` (after the `/api/related` line):

```go
	mux.HandleFunc("GET /api/actions", s.handleActions)
	mux.HandleFunc("POST /api/actions/complete", s.handleActionComplete)
```

Create `internal/dashboard/actions.go`:

```go
package dashboard

import (
	"net/http"
	"time"

	"github.com/jandro-es/axon/internal/actions"
	"github.com/jandro-es/axon/internal/db"
)

func (s *Server) handleActions(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.ActionsEnabled || s.cfg.DB == nil {
		http.Error(w, "actions are disabled for this profile", http.StatusNotFound)
		return
	}
	if r.Header.Get("X-Axon-Actions") != "1" {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	rows, err := db.ListActions(r.Context(), s.cfg.DB, db.ListActionsOpts{IncludeAll: true})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, buildActionsPayload(rows, time.Now()))
}

// buildActionsPayload turns the derived rows into the tab's data: the non-archived
// actions (each tagged with its read-time bucket), a GTD counts summary, and a
// 30-day completion trend from done_date. Pure — unit-tested without HTTP.
func buildActionsPayload(rows []db.Action, today time.Time) map[string]any {
	weekAgo := today.AddDate(0, 0, -7).Format("2006-01-02")

	type item struct {
		db.Action
		Bucket string `json:"bucket"`
	}
	items := make([]item, 0, len(rows))
	counts := map[string]int{"open": 0, "overdue": 0, "today": 0, "waiting": 0, "someday": 0, "done7": 0}
	doneByDay := map[string]int{}
	for _, rrow := range rows {
		if rrow.Archived {
			continue
		}
		b := actions.BucketFields(rrow.State, rrow.Due, rrow.Scheduled, rrow.Start, rrow.Tags, today)
		items = append(items, item{rrow, b})
		switch b {
		case "done":
			if rrow.DoneDate != "" {
				doneByDay[rrow.DoneDate]++
			}
			if rrow.DoneDate >= weekAgo {
				counts["done7"]++
			}
		case "cancelled":
			// not counted
		default:
			counts["open"]++
			if _, ok := counts[b]; ok {
				counts[b]++
			}
		}
	}
	trend := make([]map[string]any, 0, 30)
	for i := 29; i >= 0; i-- {
		d := today.AddDate(0, 0, -i).Format("2006-01-02")
		trend = append(trend, map[string]any{"day": d, "done": doneByDay[d]})
	}
	return map[string]any{"actions": items, "counts": counts, "trend": trend}
}
```

In `internal/dashboard/health.go`, add after the `related_enabled` line:

```go
	out["actions_enabled"] = s.cfg.ActionsEnabled
```

In `cmd/axon/start_cmd.go`, add to the `dashboard.Config{...}` literal (near `RelatedEnabled`):

```go
					ActionsEnabled: deps.profile.Dashboard.ActionsAllowed(),
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `env -u FORCE_COLOR go test ./internal/dashboard/ ./internal/config/ -run 'TestActions' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/dashboard/actions.go internal/dashboard/server.go internal/dashboard/health.go internal/config/types.go internal/config/dashboard_test.go cmd/axon/start_cmd.go internal/dashboard/actions_api_test.go
git commit -m "feat(T3): GET /api/actions + actions_enabled kill-switch + health (FR-162/164)

Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>"
```

---

### Task 5: `POST /api/actions/complete` (FR-163/164)

**Files:**
- Modify: `internal/dashboard/actions.go` (add `handleActionComplete`)
- Test: `internal/dashboard/actions_api_test.go` (add write tests)

**Interfaces:**
- Consumes: `s.cfg.Vault (*vault.FS)`, `s.cfg.DB`, `s.cfg.Bus`, `s.cfg.Profile`, `s.cfg.ActionsEnabled`; `vault.CompleteAction`, `vault.ErrActionNotFound` (Task 2); `db.MarkActionDone` (Task 3); `events.Event`.
- Produces: the `POST /api/actions/complete` endpoint (registered in Task 4).

- [ ] **Step 1: Write the failing test**

Add to `internal/dashboard/actions_api_test.go` (this needs a vault-backed server; add a second builder mirroring `review_api_test.go`'s `reviewTestServer`):

```go
import (
	"os"                                        // add to the import block
	"path/filepath"                             // add
	"strings"                                   // add
	"github.com/jandro-es/axon/internal/actions" // add
	"github.com/jandro-es/axon/internal/events"  // add
	"github.com/jandro-es/axon/internal/vault"   // add
)

func actionsWriteServer(t *testing.T) (*httptest.Server, *vault.FS, *db.DB) {
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
	// Index it so MarkActionDone has a row + the test can read the hash.
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
```

> Confirm the event-bus constructor name (`events.NewBus()` here) against `review_api_test.go` — mirror how it builds `Config{... Bus: ...}`. If the write-server import set clashes with `related_api_test.go` (same package), keep all imports in one file's block.

- [ ] **Step 2: Run test to verify it fails**

Run: `env -u FORCE_COLOR go test ./internal/dashboard/ -run TestActionComplete -v`
Expected: FAIL — `s.handleActionComplete undefined` (route registered in Task 4 but handler missing) or 404 on the POST.

- [ ] **Step 3: Write minimal implementation**

Append to `internal/dashboard/actions.go` (add imports `encoding/json`, `errors`, `strings`, `github.com/jandro-es/axon/internal/events`, `github.com/jandro-es/axon/internal/vault`):

```go
func (s *Server) handleActionComplete(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.ActionsEnabled || s.cfg.Vault == nil {
		http.Error(w, "actions are disabled for this profile", http.StatusNotFound)
		return
	}
	if r.Header.Get("X-Axon-Actions") != "1" ||
		!strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	var in struct {
		Path string `json:"path"`
		Hash string `json:"hash"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if in.Path == "" || in.Hash == "" {
		http.Error(w, "path and hash required", http.StatusBadRequest)
		return
	}
	date := time.Now().Format("2006-01-02")
	err := s.cfg.Vault.CompleteAction(r.Context(), in.Path, in.Hash, date)
	if errors.Is(err, vault.ErrActionNotFound) {
		http.Error(w, "action not found (already done or changed) — refresh", http.StatusConflict)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if s.cfg.DB != nil {
		_, _ = db.MarkActionDone(r.Context(), s.cfg.DB, in.Hash, date)
	}
	if s.cfg.Bus != nil {
		s.cfg.Bus.Publish(events.Event{
			Level: events.LevelInfo, Kind: "action.done",
			Message: "completed action in " + in.Path,
			Data:    map[string]any{"profile": s.cfg.Profile, "path": in.Path, "date": date},
		})
	}
	writeJSON(w, map[string]any{"ok": true, "path": in.Path, "date": date})
}
```

> Confirm the bus publish signature against `handleReviewAction` (`s.cfg.Bus.Publish(events.Event{...})`) and `events.LevelInfo` — mirror exactly.

- [ ] **Step 4: Run test to verify it passes**

Run: `env -u FORCE_COLOR go test ./internal/dashboard/ -run TestActionComplete -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/dashboard/actions.go internal/dashboard/actions_api_test.go
git commit -m "feat(T3): POST /api/actions/complete — 409-on-stale + action.done (FR-163/164)

Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>"
```

---

### Task 6: SPA — `ActionsTab` (FR-164)

**Files:**
- Modify: `web/src/App.jsx` (TABS, filter, `SSE_KINDS`, `ActionsTab`, `Line`/`Area` import if needed)
- Modify: `web/src/App.css` (optional — reuse existing `tiles`/`related-list`/`review-*` classes)

**Interfaces:**
- Consumes: `GET /api/actions` (Task 4), `POST /api/actions/complete` (Task 5); existing `useFetch`, `Card`, `Tile`, `Empty`, `AreaChart`/`Area`/`ResponsiveContainer`/`CartesianGrid`/`XAxis`/`YAxis`/`Tooltip`, `SEMA`, `num`, `shortDay`, `CustomTooltip`.

- [ ] **Step 1: Add the tab registration + SSE kind**

In `web/src/App.jsx`:
- `TABS` (≈:794): add `['actions', 'Actions']` (place after `['review', 'Review']`).
- Nav filter (≈:837): extend the predicate with `&& (id !== 'actions' || health?.actions_enabled !== false)`.
- Body render (≈:885): add `{tab === 'actions' && <ActionsTab span="span-12" />}`.
- `SSE_KINDS` (≈:36): add `'action.done'` to the array.

- [ ] **Step 2: Add the fetch helpers + `ActionsTab` component**

Add near `getRelated`/`postReviewAction` (≈:668–726):

```jsx
function getActions(nonce) {
  return fetch('/api/actions?n=' + nonce, { headers: { 'X-Axon-Actions': '1' } })
    .then(async (r) => { if (!r.ok) throw new Error(await r.text()); return r.json() })
}
function postComplete(path, hash) {
  return fetch('/api/actions/complete', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', 'X-Axon-Actions': '1' },
    body: JSON.stringify({ path, hash }),
  }).then(async (r) => { if (!r.ok) throw new Error(await r.text()); return r.json() })
}

const BUCKET_ORDER = ['overdue', 'today', 'scheduled', 'next', 'waiting', 'someday']
const BUCKET_LABEL = { overdue: '🔴 Overdue', today: '📅 Today', scheduled: '⏳ Scheduled', next: '▶ Next', waiting: '🕓 Waiting', someday: '💭 Someday' }

function ActionsTab({ span }) {
  const [nonce, setNonce] = useState(0)
  const [data, setData] = useState(null)
  const [err, setErr] = useState(null)
  const [busy, setBusy] = useState(null)

  useEffect(() => {
    let live = true
    getActions(nonce).then((d) => { if (live) { setData(d); setErr(null) } }).catch((e) => { if (live) setErr(String(e.message || e)) })
    return () => { live = false }
  }, [nonce])

  const complete = (path, hash) => {
    setBusy(hash)
    postComplete(path, hash)
      .catch((e) => setErr(String(e.message || e)))
      .finally(() => { setBusy(null); setNonce((n) => n + 1) })
  }

  if (err) return <Card title="Actions" span={span}><Empty>{err}</Empty></Card>
  if (!data) return <Card title="Actions" span={span}><Empty>Loading…</Empty></Card>

  const c = data.counts || {}
  const open = (data.actions || []).filter((a) => a.state === 'open' && !a.archived)
  const byBucket = (b) => open.filter((a) => a.bucket === b)

  return (
    <>
      <Card title="Actions" meta={`${num(c.open || 0)} open`} span="span-8">
        <div className="tiles">
          <Tile label="Open" value={num(c.open || 0)} accent />
          <Tile label="Overdue" value={num(c.overdue || 0)} />
          <Tile label="Today" value={num(c.today || 0)} />
          <Tile label="Done (7d)" value={num(c.done7 || 0)} />
        </div>
      </Card>
      <Card title="Completions" meta="last 30 days" span="span-4">
        <ResponsiveContainer width="100%" height={140}>
          <AreaChart data={data.trend || []} margin={{ top: 6, right: 6, bottom: 0, left: -12 }}>
            <defs><linearGradient id="gDone" x1="0" y1="0" x2="0" y2="1"><stop offset="0%" stopColor={SEMA.ok} stopOpacity={0.5} /><stop offset="100%" stopColor={SEMA.ok} stopOpacity={0.02} /></linearGradient></defs>
            <CartesianGrid vertical={false} />
            <XAxis dataKey="day" tickFormatter={shortDay} fontSize={10} tickLine={false} axisLine={false} minTickGap={28} />
            <YAxis allowDecimals={false} fontSize={10} tickLine={false} axisLine={false} width={26} />
            <Tooltip content={<CustomTooltip />} labelFormatter={shortDay} />
            <Area type="monotone" dataKey="done" name="completed" stroke={SEMA.ok} strokeWidth={1.5} fill="url(#gDone)" />
          </AreaChart>
        </ResponsiveContainer>
      </Card>
      <Card title="Open actions" meta={`${open.length} shown`} span="span-12">
        {open.length === 0 && <Empty>Nothing open — inbox zero on actions. 🎉</Empty>}
        {BUCKET_ORDER.filter((b) => byBucket(b).length > 0).map((b) => (
          <div key={b} className="action-group">
            <div className="action-bucket">{BUCKET_LABEL[b]}</div>
            <ul className="related-list">
              {byBucket(b).map((a) => (
                <li key={a.hash + a.source_path + a.line_no}>
                  <span className="related-path">{a.text}{a.due ? ` · 📅 ${a.due}` : ''} <em>{a.source_path}</em></span>
                  <button disabled={busy === a.hash} onClick={() => complete(a.source_path, a.hash)}>{busy === a.hash ? '…' : 'done'}</button>
                </li>
              ))}
            </ul>
          </div>
        ))}
      </Card>
    </>
  )
}
```

> `useEffect`/`useState` are already imported (App.jsx uses hooks throughout); confirm `useEffect` is in the React import line and add it if missing. `AreaChart`/`Area`/etc. are already imported (App.jsx:2-6). `shortDay`, `num`, `CustomTooltip`, `SEMA`, `Card`, `Tile`, `Empty` all exist. The `.action-group`/`.action-bucket` classes are optional cosmetic — add minimal rules to `App.css` or reuse existing spacing.

- [ ] **Step 3: Build the SPA**

Run: `cd web && npm run build 2>&1 | tail -5 && cd ..`
Expected: Vite build succeeds; `web/dist/` repopulated (and `dist/.gitkeep` re-touched). If `node_modules` is absent, run `npm install` first.

- [ ] **Step 4: Verify Go build embeds the SPA**

Run: `env -u FORCE_COLOR go build ./cmd/axon && echo BUILT`
Expected: BUILT (the `//go:embed all:dist` picks up the new bundle).

- [ ] **Step 5: Commit**

```bash
git add web/src/App.jsx web/src/App.css web/dist
git commit -m "feat(T3): Actions SPA tab — tiles, completion trend, complete buttons (FR-164)

Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>"
```

---

### Task 7: Full green + ADR-034 + docs + live smoke

**Files:**
- Modify: `docs/02-architecture.md` (ADR-034), `docs/03-requirements.md` (FR-162/163/164), `docs/04-data-model-and-config.md` (`dashboard.actions_enabled`), `docs/09-component-dashboard-observability.md` (endpoints + Actions tab), `docs/16-roadmap-1.2.5.md` (T3 built + release criterion met), `CLAUDE.md` (FR→164, ADR→034)

**Interfaces:** none (verification + docs).

- [ ] **Step 1: Whole-suite green + lint**

Run: `env -u FORCE_COLOR go build ./... && env -u FORCE_COLOR go test ./... && golangci-lint run`
Expected: all pass. Fix any drift (`gofmt -w` touched files; the `buildActionsPayload` `_ = tStr` line — remove it if `tStr` ends up unused, or use it; watch De-Morgan/emoji-alignment nits).

- [ ] **Step 2: Write ADR-034 + docs**

- `docs/02-architecture.md`: add **ADR-034 — Task completion as a byte-precise, hash-addressed line edit** (status: built), following ADR-032/033 format. Context: the consolidated list needs a "mark done" that edits a human-authored checkbox line — the sole exception to cardinal rule 2's managed-block-only edits. Decision: `vault.CompleteAction(path, hash, date)` flips `[ ]`→`[x]` + appends `✅ date` on the single OPEN line whose T1 identity hash matches (via `actions.Extract`, so same skip rules), atomic temp+rename, `ErrActionNotFound` (nothing written) on stale/unknown hash → dashboard 409. User-initiated via the loopback dashboard only; **pinned out of every agent/MCP path** (T4's `action_complete` tool excluded from agentic allowlists); never a delete; fully reversible by hand. Consequences: introduces `errors.Is`/409 to the dashboard; the derived row is surgically marked done (`db.MarkActionDone`) then reindex-reproduced.
- `docs/03-requirements.md`: FR-162 (`GET /api/actions`), FR-163 (`POST /api/actions/complete` + `vault.CompleteAction`, ADR-034), FR-164 (kill-switch/health/SSE/Actions tab).
- `docs/04-data-model-and-config.md`: document `dashboard.actions_enabled` (pointer-default-ON) next to `related_enabled`.
- `docs/09-component-dashboard-observability.md`: add the two endpoints (guarded) + the **Actions** tab to the views table + the `/health` flag + `action.done` SSE kind.
- `docs/16-roadmap-1.2.5.md`: mark **T3** ✅ BUILT 2026-07-10 (FR-162/163/164, ADR-034) and note **the T1+T2+T3 release criterion is met**.
- `CLAUDE.md`: `docs/02` ADR range → ADR-034; `docs/03` FR range → FR-164; update the 1.2.5 doc-pack line (T1/T2/T3 shipped; remaining T4/T5/T6).

- [ ] **Step 3: Live smoke (real binary, isolated AXON_HOME — never :7777)**

```bash
go build -o /tmp/axon-t3 ./cmd/axon
SMOKE=$(mktemp -d)
# T1/T2 smoke config shape on port 7788; vault_path/data_dir under $SMOKE; automations.actions-consolidate enabled.
/tmp/axon-t3 init --config "$SMOKE/config.yaml"
mkdir -p "$SMOKE/vault/01-Projects"
printf '## Sprint\n- [ ] fix bug 📅 2000-01-01 ⏫\n- [ ] plan 📅 2999-01-01\n- [x] shipped ✅ %s\n' "$(date +%F)" > "$SMOKE/vault/01-Projects/work.md"
/tmp/axon-t3 reindex --config "$SMOKE/config.yaml"
/tmp/axon-t3 start --config "$SMOKE/config.yaml" &   # binds 7788
sleep 2
curl -s -H 'X-Axon-Actions: 1' http://127.0.0.1:7788/api/actions | head -c 600; echo
curl -s -o /dev/null -w '%{http_code}\n' http://127.0.0.1:7788/api/actions            # no header → 403
HASH=$(curl -s -H 'X-Axon-Actions: 1' http://127.0.0.1:7788/api/actions | python3 -c 'import sys,json; a=[x for x in json.load(sys.stdin)["actions"] if x["state"]=="open"][0]; print(a["hash"])')
curl -s -X POST -H 'Content-Type: application/json' -H 'X-Axon-Actions: 1' -d "{\"path\":\"01-Projects/work.md\",\"hash\":\"$HASH\"}" http://127.0.0.1:7788/api/actions/complete; echo
grep -n '\[x\] fix bug' "$SMOKE/vault/01-Projects/work.md"                            # source flipped + ✅ date
curl -s -X POST -H 'Content-Type: application/json' -H 'X-Axon-Actions: 1' -d "{\"path\":\"01-Projects/work.md\",\"hash\":\"$HASH\"}" -o /dev/null -w 'stale→%{http_code}\n' http://127.0.0.1:7788/api/actions/complete   # 409
curl -s http://127.0.0.1:7788/health | python3 -c 'import sys,json; print("actions_enabled:", json.load(sys.stdin).get("actions_enabled"))'
/tmp/axon-t3 stop --config "$SMOKE/config.yaml" 2>/dev/null || pkill -f "axon-t3 start"
```

Expected: `GET` returns `{actions,counts,trend}`; no-header→403; the completion flips `- [x] fix bug … ✅ <today>` in the source note; a second POST with the now-stale hash→409; `/health` shows `actions_enabled: true`. Optionally open `http://127.0.0.1:7788` and eyeball the Actions tab. Skip scratch cleanup (GateGuard). Kill the daemon you started; never touch `:7777`.

- [ ] **Step 4: Commit docs**

```bash
git add docs/ CLAUDE.md
git commit -m "docs(T3): ADR-034, FR-162/163/164, dashboard Actions endpoints, roadmap T3 built + release criterion met

Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>"
```

---

## Notes for the implementer

- **Read one neighbor first:** `internal/dashboard/related_api_test.go` + `review_api_test.go` (test-server builders, `events.NewBus`/`Config` shape), `internal/vault/merge_test.go` (`write`/`bodyOf`/`newTempVault` helpers), `web/src/App.jsx` `RelatedTab`/`ReviewTab`/`TokenTrend` (clone targets). The plan's assertions are the contract; match the harness the package already uses.
- **The vault is updated first, then the DB** — never mark the DB done before `CompleteAction` succeeds (S9: the vault is the source of truth).
- **Read-only everywhere except `CompleteAction`.** No `Move`/`Merge`/`fs` write; no managed-block change. If you're editing anything other than one checkbox line, you've drifted.
- **`web/dist` is committed** — commit the rebuilt bundle in Task 6 so a fresh `go build` embeds the new tab. Never delete `web/dist/.gitkeep`.
- **GateGuard:** first Write/Edit/Bash each turn triggers a fact-force preamble (comply tersely); `git commit --amend`/`rm -rf` blocked (follow-up commits; leave scratch dirs).
```
