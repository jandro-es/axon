# 04 — Data Model & Configuration

## 1. Vault layout

The scaffold AXON creates. `NN-` prefixes keep ordering stable. System folders are dot-prefixed so Obsidian and humans largely ignore them.

```
<vault>/
  00-Inbox/                 # frictionless capture; triaged into PARA by automation
  01-Projects/              # active, outcome-with-a-deadline work
  02-Areas/                 # ongoing responsibilities (no end date)
    Profile/                # personal identity layer (Component 12): USER.md, SOUL.md, MEMORY.md
  03-Resources/
    Knowledge/              # ingested sources (one note per source)
    …                       # topical reference
  04-Archive/               # completed/inactive
  Daily/                    # YYYY-MM-DD daily notes
  MOCs/                     # Maps of Content (topic indexes)
  Templates/                # note templates
  .axon/
    logs/                   # human-readable run logs (mirrors DB events)
    exports/                # context-export bundles
    snapshots/              # context snapshots for compaction
    dashboards/             # generated Dataview/Bases dashboard notes (symlinked/visible if desired)
    review-queue.md         # link suggestions & triage proposals awaiting human review
  .claude/
    CLAUDE.md               # vault schema + conventions (agent reads every session)
    .mcp.json               # AXON MCP server registration (profile-scoped)
    settings.json           # hooks
    plugins/axon/           # installed plugin: skills/ agents/ hooks/
  .obsidian/                # Obsidian's own config (left to Obsidian)
```

The **SQLite database lives outside the vault** in the profile data dir (`$AXON_HOME/profiles/<name>/db.sqlite`), so it never pollutes the vault, never syncs, and is trivially disposable.

### 1.1 Frontmatter conventions

Every managed note carries YAML frontmatter. Keep it small and stable; the agent must never reorder or strip unknown keys.

```yaml
---
title: "Human-readable title"
type: note            # note | daily | project | area | resource | source | moc
status: active        # active | someday | done | archived  (projects/areas)
created: 2026-06-28
updated: 2026-06-28
tags: [topic/x, topic/y]
aliases: []
# source-only fields:
source_url: "https://…"
source_author: ""
source_date: ""
ingested_by: axon
content_hash: "sha256:…"   # set by AXON for idempotency; do not edit by hand
axon_managed: true          # true if AXON may auto-edit body sections
---
```

**Rule:** AXON only auto-edits inside explicit markers in the body, never arbitrary prose:

```markdown
<!-- axon:summary:start -->
…agent-maintained summary…
<!-- axon:summary:end -->
```

This keeps human prose and agent output cleanly separable and makes edits diff-able and reversible.

## 2. Database schema (SQLite + sqlite-vec + FTS5)

One file per profile. Migrations are versioned and forward-only. Tables (illustrative DDL — the agent finalises types/indexes):

```sql
-- schema_version: integer, single row

-- Notes mirror (derived from vault; rebuildable)
CREATE TABLE notes (
  id            INTEGER PRIMARY KEY,
  path          TEXT UNIQUE NOT NULL,      -- vault-relative
  title         TEXT,
  type          TEXT,
  status        TEXT,
  tags          TEXT,                       -- json array
  content_hash  TEXT,                       -- sha256 of normalised body
  word_count    INTEGER,
  created       TEXT,
  updated       TEXT,
  last_indexed  TEXT
);

-- Link graph (derived)
CREATE TABLE links (
  src_note_id   INTEGER NOT NULL REFERENCES notes(id) ON DELETE CASCADE,
  dst_path      TEXT NOT NULL,              -- target may not yet exist
  dst_note_id   INTEGER,                    -- resolved if it exists
  kind          TEXT,                       -- wikilink | embed | tag
  PRIMARY KEY (src_note_id, dst_path, kind)
);

-- Ingested sources
CREATE TABLE sources (
  id            INTEGER PRIMARY KEY,
  note_id       INTEGER REFERENCES notes(id) ON DELETE SET NULL,
  url           TEXT,
  kind          TEXT,                       -- url | pdf | file
  fetched_at    TEXT,
  content_hash  TEXT,
  status        TEXT                        -- ok | failed | redacted
);

-- Chunks + vectors (vector table via sqlite-vec vec0)
CREATE TABLE chunks (
  id            INTEGER PRIMARY KEY,
  note_id       INTEGER REFERENCES notes(id) ON DELETE CASCADE,
  source_id     INTEGER REFERENCES sources(id) ON DELETE CASCADE,
  ordinal       INTEGER,
  text          TEXT,
  token_count   INTEGER,
  content_hash  TEXT
);
CREATE VIRTUAL TABLE vec_chunks USING vec0(
  chunk_id INTEGER PRIMARY KEY,
  embedding float[768]          -- dim follows the embedding model; re-index on change
);
CREATE VIRTUAL TABLE fts_chunks USING fts5(text, content='chunks', content_rowid='id');

-- Optional IVF-flat ANN index (ADR-025, retrieval.index: ann). Both are DERIVED
-- from vec_chunks.embedding and rebuilt by `axon reindex`; `brute` mode leaves
-- them empty/NULL.
CREATE TABLE vec_centroids (           -- k≈√N spherical-k-means centroids
  id     INTEGER PRIMARY KEY,
  dim    INTEGER NOT NULL,
  model  TEXT    NOT NULL,
  vector BLOB    NOT NULL              -- little-endian float32, same codec as vec_chunks
);
-- vec_chunks gains: centroid INTEGER (nullable; NULL = unassigned overflow, always scanned)

-- Temporal memory facts (R1/ADR-028) — a DERIVED, disposable projection of the
-- axon:memory block in MEMORY.md; reindex delete-all+inserts these rows from
-- Markdown (the vault is the source of truth). Never authoritative.
CREATE TABLE memory_facts (
  id            INTEGER PRIMARY KEY,
  text          TEXT NOT NULL,
  kind          TEXT,
  source        TEXT,
  valid_from    TEXT NOT NULL,
  valid_until   TEXT,                    -- NULL = currently valid (open interval)
  superseded_by TEXT,
  struck        INTEGER NOT NULL DEFAULT 0,
  embedding     BLOB,                    -- little-endian float32, same codec as vec_chunks
  line_no       INTEGER,
  updated       TEXT NOT NULL
);
CREATE INDEX idx_memory_facts_open ON memory_facts(valid_until) WHERE valid_until IS NULL;

-- Actions / tasks (1.2.5 T1 / ADR-033) — a DERIVED, disposable projection of the
-- checkbox lines across the vault; reindex delete-all+inserts these rows from
-- Markdown (the vault is the source of truth). Never authoritative. The GTD
-- status "bucket" is NOT stored — it is date-relative and computed at read time.
CREATE TABLE actions (
  id           INTEGER PRIMARY KEY,
  hash         TEXT NOT NULL,          -- sha256(source_path + normalized checkbox-stripped line); state-independent identity
  source_path  TEXT NOT NULL,          -- vault-relative note the checkbox lives in
  line_no      INTEGER NOT NULL,       -- 0-based body line index (ordering/display)
  section      TEXT,                   -- nearest enclosing heading
  text         TEXT NOT NULL,          -- task text, date/priority markers stripped
  raw          TEXT NOT NULL,          -- full original line (byte-precise completion match, T3)
  state        TEXT NOT NULL,          -- open | done | cancelled (checkbox-derived, date-independent)
  checkbox     TEXT NOT NULL,          -- literal marker char
  priority     TEXT,                   -- highest|high|medium|low|lowest
  due          TEXT, scheduled TEXT, start TEXT, done_date TEXT,   -- YYYY-MM-DD | NULL (📅/⏳/🛫/✅)
  project      TEXT,                   -- explicit [[link]] target | NULL (fallback = source_path)
  contexts     TEXT, tags TEXT,        -- json arrays (@contexts / #tags)
  archived     INTEGER NOT NULL DEFAULT 0,   -- 1 = under 04-Archive/ (excluded from open views)
  updated      TEXT NOT NULL
);
CREATE INDEX idx_actions_open   ON actions(state) WHERE state = 'open';
CREATE INDEX idx_actions_source ON actions(source_path);

-- Token ledger (every Claude call)
CREATE TABLE token_ledger (
  id              INTEGER PRIMARY KEY,
  ts              TEXT NOT NULL,
  profile         TEXT NOT NULL,
  operation       TEXT NOT NULL,            -- ingest.enrich | automation.daily-log | mcp.search | session.tool | …
  model           TEXT NOT NULL,
  input_tokens    INTEGER,
  output_tokens   INTEGER,
  cache_read      INTEGER,
  cache_write     INTEGER,
  est_input       INTEGER,                  -- pre-flight estimate (local; exact count_tokens in api_key mode)
  cost_usd        REAL,                      -- populated only in auth_mode: api_key; null otherwise
  run_id          INTEGER REFERENCES runs(id)
);

-- Automation runs
CREATE TABLE runs (
  id            INTEGER PRIMARY KEY,
  automation    TEXT NOT NULL,
  started_at    TEXT, finished_at TEXT,
  status        TEXT,                        -- ok | skipped | failed | dry-run
  skip_reason   TEXT,                        -- e.g. "no new material" | "budget"
  changes       TEXT,                        -- json diff summary
  tokens        INTEGER, cost_usd REAL,
  error         TEXT
);

-- Budgets (current windows; derived/cached)
CREATE TABLE budget_state (
  profile       TEXT, window TEXT,           -- day | week
  period_start  TEXT,
  tokens_used   INTEGER, cost_used REAL,
  PRIMARY KEY (profile, window)
);

-- Event bus persistence (drives dashboard + .axon/logs)
CREATE TABLE events (
  id   INTEGER PRIMARY KEY,
  ts   TEXT, level TEXT, kind TEXT, message TEXT, data TEXT  -- data: json
);
```

**Cost** (`auth_mode: api_key` only) is computed from a small, config-overridable price table keyed by model so the ledger stays accurate as prices change (don't hardcode prices in logic). On subscription/enterprise installs `cost_usd` is null and the token windows are the budget axis.

## 3. Configuration reference (`config.yaml`)

The single declarative surface. It lives at `~/.axon/config.yaml` by default (`$AXON_HOME/config.yaml`, so it follows an `AXON_HOME` override) and is resolved independently of the working directory; pass `--config <path>` to use a different file. Validated with struct tags + a validator (`go-playground/validator`) in the `config` package; `axon config validate` checks it. Secrets are **not** here — they live in `.env`/keychain and are referenced by name. Example values shown; see `axon.config.example.yaml` (shipped in the repo) for a complete annotated file to copy to `~/.axon/config.yaml`.

```yaml
version: 1
project_name: axon
active_profile: personal      # one installation runs ONE active profile (override: AXON_PROFILE / --profile)

profiles:
  personal:                   # PERSONAL install — Claude Max
    vault_path: "~/Notes/Personal"
    data_dir: "~/.axon/profiles/personal"
    claude:
      auth_mode: subscription                 # subscription | enterprise | api_key
      config_dir: "~/.axon/profiles/personal/claude"   # CLAUDE_CONFIG_DIR — isolates the account
      oauth_token: env:CLAUDE_CODE_OAUTH_TOKEN_PERSONAL # from `claude setup-token`, for headless automations
      # NEVER set ANTHROPIC_API_KEY in this mode (Claude Code would bill the API account)
    dashboard: { host: "127.0.0.1", port: 7777 }   # ask_enabled (default true) gates the Ask panel / POST /api/ask (ADR-023); capture_enabled (default true) gates POST /api/capture + the served /capture page (ADR-024) — set false to forbid browser vault writes; related_enabled (default true) gates GET /api/related + the Related tab (R8/FR-150); actions_enabled (default true) gates GET /api/actions + POST /api/actions/complete + the Actions tab (T3/ADR-034) — set false to forbid the browser completion write
    embeddings: { provider: ollama, host: "http://localhost:11434", model: nomic-embed-text, dim: 768, batch_size: 32 }
    # provider: ollama | apple. `apple` uses Apple's on-device NLContextualEmbedding
    # (macOS 14+, no server; ADR-013): a Swift helper compiled by `axon init` (needs
    # Xcode CLT) at ~/.axon/bin/axon-apple-embed (override with `helper:`); `host` is
    # ignored, and model/dim must be apple-nlcontextual-v1 / 512. Simplest switch:
    # `axon configure embeddings apple --reindex` (or `... embeddings ollama
    # --model M --dim N --reindex`) — persists, converges and re-embeds in one
    # flow. `axon init --embeddings <p>` remains for provisioning-time choice.
    # models: per-tier model with provider-prefixed strings (ADR-015). A bare
    # string is a Claude model (passed to `claude -p --model`; plan tier governs
    # availability). "ollama:<model>" routes the tier to a local Ollama chat
    # model; "apple" routes classify to the Apple Foundation Models on-device
    # model (macOS 26+, Apple Silicon, Apple Intelligence; classify tier only).
    # synthesis is always Claude (validated). Local calls appear in token_ledger
    # with provider-prefixed model strings (ollama:qwen3:8b, apple-foundation-v1),
    # cost_usd null, and do NOT accrue to budget_windows (FR-78) — budgets keep
    # meaning Claude quota. Optional keys: ollama_host (default
    # http://localhost:11434, independent of embeddings.host), local_fallback
    # (claude|fail, default claude — FR-79), apple_helper (helper path override),
    # verify (off|ollama:<model>, default off — R5.3 per-call verification of
    # local routine answers, FR-144), verify_min_score (0–10, default 6).
    models:   { classify: claude-haiku-4-5, routine: claude-sonnet-5, synthesis: claude-opus-4-8 }
    # capture: the FR-26 capture funnel (ADR-016). Optional; defaults shown.
    # The capture automation itself is scheduled via automations.capture.
    # enrich: heuristic (default, zero tokens) | claude (chokepoint, routine
    # tier). archive_dir: vault-relative destination for ingested inbox
    # originals (moved wikilink-safely, never deleted).
    capture:  { enrich: heuristic, archive_dir: 04-Archive/Capture }
    limits:   { daily_tokens: 1_500_000, weekly_tokens: 8_000_000, guard_pause_at_pct: 80 }           # estimated tokens; no dollar cap here
    # index: brute (default, exact full scan) | ann (IVF-flat, ADR-025). In ann
    # mode the index auto-falls back to exact brute below ann.threshold vectors;
    # ann.nprobe clusters are probed per query (higher = more recall). Enable ann
    # then run `axon reindex` to build vec_centroids; `axon doctor` advises when.
    retrieval: { top_k: 8, max_context_tokens: 12_000, index: brute, ann: { threshold: 10_000, nprobe: 8 } }
    # resurfacing: the R9 resurfacer's spaced-repetition schedule + opt-in
    # contradiction check. intervals_weeks: the rung ladder in weeks (default
    # [1,2,4,8,16]; the last rung is the leech cap). contradiction_max_checks:
    # per-run routine-tier model calls for note-contradiction detection (default
    # 3; still gated on the resurfacer automation having budget_tokens > 0).
    resurfacing: { intervals_weeks: [1, 2, 4, 8, 16], contradiction_max_checks: 3 }
    # merge: the R7 near-duplicate merge-proposals sweep (ADR-032). threshold:
    # minimum mean-vector cosine to propose a pair (default 0.92). max_proposals:
    # per-run cap (default 5). The merge-proposals automation is zero-model and
    # off by default; accepting a proposal runs the wikilink-safe vault.Merge
    # (survivor keeps prose + gains the loser's body, inbound links retarget, the
    # loser is archived to .trash/merged/ — never deleted).
    merge: { threshold: 0.92, max_proposals: 5 }
    # actions: the 1.2.5 T5 stale-sweep threshold. stale_after_days: an open,
    # undated action whose source note hasn't been updated in this many days is
    # proposed to the review queue by the (off-by-default) actions-review
    # automation; accepting demotes it to Someday/Maybe (#someday). Default 30.
    actions: { stale_after_days: 30 }
    policy:
      data_residency: local-only
      egress_allowlist: ["localhost", "*"]
      ingest_domains_allow: ["*"]
      ingest_domains_deny: []
      redaction_rules: []
      allowed_automations: ["*"]
    automations:
      # heartbeat.model is opt-in: set → one budget-checked single-line synthesis when noteworthy (docs/06); unset → zero model work
      heartbeat:        { enabled: true, schedule: "0 9,13,17 * * *", model: classify,  budget_tokens: 50_000, catch_up: skip }
      daily-log:        { enabled: true, schedule: "30 21 * * *",     model: routine,   budget_tokens: 120_000 }
      inbox-triage:     { enabled: true, schedule: "*/30 * * * *",    model: routine,   budget_tokens: 80_000 }
      compaction:       { enabled: true, schedule: "0 3 * * 0",       model: synthesis, budget_tokens: 300_000 }
      context-export:   { enabled: true, schedule: "0 4 * * 0",       model: none,      budget_tokens: 0 }
      knowledge-reindex:{ enabled: true, schedule: "0 2 * * *",       model: none,      budget_tokens: 0 }
      knowledge-digest: { enabled: true, schedule: "0 8 * * 1",       model: synthesis, budget_tokens: 200_000 }
      link-suggester:   { enabled: true, schedule: "0 1 * * *",       model: classify,  budget_tokens: 60_000 }
      memory-distill:   { enabled: true, schedule: "0 5 * * *",       model: synthesis, budget_tokens: 120_000 }
      budget-guard:     { enabled: true, schedule: "*/15 * * * *",    model: none,      budget_tokens: 0 }
    memory:                     # personal identity/memory layer (Component 12); optional — these are the defaults
      inject: true              # inject USER/SOUL/recent MEMORY at SessionStart (no model call)
      session_tokens: 1500      # token ceiling for the injected block
      recent_entries: 10        # how many newest MEMORY entries to inject

  work:                       # WORK install (separate machine) — Claude Enterprise SSO, no API
    vault_path: "~/Notes/Work"
    data_dir: "~/.axon/profiles/work"
    claude:
      auth_mode: enterprise                   # SSO login; governed by org policy; no API key available
      config_dir: "~/.axon/profiles/work/claude"
      oauth_token: env:CLAUDE_CODE_OAUTH_TOKEN_WORK   # only if the org permits `claude setup-token`; else unset
    dashboard: { host: "127.0.0.1", port: 7777 }      # same port ok — never co-runs with personal
    embeddings: { provider: ollama, model: bge-m3, dim: 1024, batch_size: 16 }
    models:   { classify: claude-haiku-4-5, routine: claude-sonnet-5, synthesis: claude-sonnet-5 }
    limits:   { daily_tokens: 600_000, weekly_tokens: 3_000_000, guard_pause_at_pct: 70 }
    retrieval: { top_k: 6, max_context_tokens: 8_000 }
    policy:
      data_residency: local-only
      egress_allowlist: ["localhost"]
      ingest_domains_allow: ["docs.internal.example.com", "github.com"]
      ingest_domains_deny: ["*"]
      redaction_rules: ["(?i)client[-_ ]?name:\\s*\\S+", "AKIA[0-9A-Z]{16}"]
      allowed_automations: ["heartbeat", "daily-log", "inbox-triage", "knowledge-reindex", "budget-guard"]
    automations:
      compaction:       { enabled: false }
      knowledge-digest: { enabled: false }
      link-suggester:   { enabled: false }
      context-export:   { enabled: true, schedule: "0 18 * * 5" }
    memory: { inject: false }   # stricter env: keep the identity layer but never auto-inject it
```

### 3.1 Resolution & precedence
CLI flag → `AXON_*` env → `profiles.<active>` → top-level defaults → built-in defaults. `policy.allowed_automations` is an allow-list that overrides per-automation `enabled: true` (work can't accidentally run a disabled-by-policy automation).

### 3.2 `.env` (secrets only)
```
# subscription/enterprise: a Claude Code OAuth token from `claude setup-token` (for headless automations)
CLAUDE_CODE_OAUTH_TOKEN_PERSONAL=sk-ant-oat01-…
CLAUDE_CODE_OAUTH_TOKEN_WORK=sk-ant-oat01-…
# api_key mode ONLY (do NOT set on subscription/enterprise — Claude Code would bill the API account):
# ANTHROPIC_API_KEY=sk-ant-api03-…
# optional: OLLAMA_HOST overrides, etc.
```
Secrets are referenced from YAML as `env:NAME` (or `keychain:NAME`). The loader resolves them at runtime and they are **never** written to logs, events, exports or model prompts.

### 3.3 Pricing table (`auth_mode: api_key` only)
A separate `prices.yaml` (or a `prices:` block) maps model → input/output/cache unit prices, used only to compute `cost_usd` on the direct-API path. Kept out of logic so it can be updated without code changes; if a model is missing, cost is recorded as null and a warning is logged. Irrelevant on subscription/enterprise installs (no per-token billing).

## 4. Identifiers & hashing
- `content_hash` = sha256 of the note body normalised (strip frontmatter + the `axon:*` managed blocks + whitespace), so agent-maintained sections don't trigger false "changed" signals.
- Chunk hashes let re-embedding touch only changed chunks.
- These hashes are the backbone of the **change-gate** (FR-31) that prevents pointless Claude calls.
