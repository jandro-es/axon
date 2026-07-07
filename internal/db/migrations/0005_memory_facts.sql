-- 0005_memory_facts — R1 temporal memory (ADR-028). A DERIVED, disposable
-- projection of the axon:memory block: reindex delete-all+inserts these rows
-- from Markdown (the vault is the source of truth, ADR-011). Never authoritative.

CREATE TABLE memory_facts (
  id            INTEGER PRIMARY KEY,
  text          TEXT NOT NULL,
  kind          TEXT,
  source        TEXT,
  valid_from    TEXT NOT NULL,
  valid_until   TEXT,
  superseded_by TEXT,
  struck        INTEGER NOT NULL DEFAULT 0,
  embedding     BLOB,
  line_no       INTEGER,
  updated       TEXT NOT NULL
);

CREATE INDEX idx_memory_facts_open ON memory_facts(valid_until) WHERE valid_until IS NULL;
