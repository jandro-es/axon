-- 0001_init — Phase 0 relational schema.
-- Mirrors docs/04 §2, EXCEPT the sqlite-vec (vec_chunks) and FTS5 (fts_chunks)
-- virtual tables, which require the sqlite-vec extension and land in Phase 2.
-- The vault is the source of truth (ADR-006); every table here is rebuildable
-- from Markdown via `axon reindex`.

-- Notes mirror (derived from the vault).
CREATE TABLE notes (
  id            INTEGER PRIMARY KEY,
  path          TEXT UNIQUE NOT NULL,   -- vault-relative
  title         TEXT,
  type          TEXT,
  status        TEXT,
  tags          TEXT,                   -- json array
  content_hash  TEXT,                   -- sha256 of normalised body
  word_count    INTEGER,
  created       TEXT,
  updated       TEXT,
  last_indexed  TEXT
);

-- Link graph (derived). Target may not yet exist.
CREATE TABLE links (
  src_note_id   INTEGER NOT NULL REFERENCES notes(id) ON DELETE CASCADE,
  dst_path      TEXT NOT NULL,
  dst_note_id   INTEGER,
  kind          TEXT,                   -- wikilink | embed | tag
  PRIMARY KEY (src_note_id, dst_path, kind)
);

-- Ingested sources.
CREATE TABLE sources (
  id            INTEGER PRIMARY KEY,
  note_id       INTEGER REFERENCES notes(id) ON DELETE SET NULL,
  url           TEXT,
  kind          TEXT,                   -- url | pdf | file
  fetched_at    TEXT,
  content_hash  TEXT,
  status        TEXT                    -- ok | failed | redacted
);

-- Chunks (vectors live in vec_chunks, added in Phase 2).
CREATE TABLE chunks (
  id            INTEGER PRIMARY KEY,
  note_id       INTEGER REFERENCES notes(id) ON DELETE CASCADE,
  source_id     INTEGER REFERENCES sources(id) ON DELETE CASCADE,
  ordinal       INTEGER,
  text          TEXT,
  token_count   INTEGER,
  content_hash  TEXT
);

-- Token ledger — every Claude call (the Component 07 chokepoint records here).
CREATE TABLE token_ledger (
  id              INTEGER PRIMARY KEY,
  ts              TEXT NOT NULL,
  profile         TEXT NOT NULL,
  operation       TEXT NOT NULL,
  model           TEXT NOT NULL,
  input_tokens    INTEGER,
  output_tokens   INTEGER,
  cache_read      INTEGER,
  cache_write     INTEGER,
  est_input       INTEGER,
  cost_usd        REAL,                 -- api_key mode only; null otherwise
  run_id          INTEGER REFERENCES runs(id)
);

-- Automation runs.
CREATE TABLE runs (
  id            INTEGER PRIMARY KEY,
  automation    TEXT NOT NULL,
  started_at    TEXT,
  finished_at   TEXT,
  status        TEXT,                   -- ok | skipped | failed | dry-run
  skip_reason   TEXT,
  changes       TEXT,                   -- json diff summary
  tokens        INTEGER,
  cost_usd      REAL,
  error         TEXT
);

-- Budget windows (derived/cached).
CREATE TABLE budget_state (
  profile       TEXT,
  window        TEXT,                   -- day | week
  period_start  TEXT,
  tokens_used   INTEGER,
  cost_used     REAL,
  PRIMARY KEY (profile, window)
);

-- Event bus persistence (drives dashboard + .axon/logs).
CREATE TABLE events (
  id   INTEGER PRIMARY KEY,
  ts   TEXT,
  level TEXT,
  kind TEXT,
  message TEXT,
  data TEXT                             -- json
);

CREATE INDEX idx_token_ledger_ts ON token_ledger(ts);
CREATE INDEX idx_runs_automation ON runs(automation);
CREATE INDEX idx_events_ts ON events(ts);
CREATE INDEX idx_links_dst ON links(dst_path);
