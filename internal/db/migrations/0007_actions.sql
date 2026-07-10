-- 0007_actions — 1.2.5 T1 (ADR-033). A DERIVED, disposable projection of the
-- checkbox lines across the vault: reindex delete-all+inserts these rows from
-- Markdown (the vault is the source of truth, ADR-011). Never authoritative.
CREATE TABLE actions (
  id           INTEGER PRIMARY KEY,
  hash         TEXT NOT NULL,
  source_path  TEXT NOT NULL,
  line_no      INTEGER NOT NULL,
  section      TEXT,
  text         TEXT NOT NULL,
  raw          TEXT NOT NULL,
  state        TEXT NOT NULL,
  checkbox     TEXT NOT NULL,
  priority     TEXT,
  due          TEXT,
  scheduled    TEXT,
  start        TEXT,
  done_date    TEXT,
  project      TEXT,
  contexts     TEXT,
  tags         TEXT,
  archived     INTEGER NOT NULL DEFAULT 0,
  updated      TEXT NOT NULL
);
CREATE INDEX idx_actions_open   ON actions(state) WHERE state = 'open';
CREATE INDEX idx_actions_source ON actions(source_path);
