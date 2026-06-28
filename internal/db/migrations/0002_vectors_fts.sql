-- 0002_vectors_fts — Phase 2 lexical + semantic search tables.
-- Per ADR-010 (amending ADR-002): vectors are stored as float32 BLOBs and
-- searched by brute-force cosine KNN in Go; FTS5 provides lexical/bm25 search.
-- Both are rebuildable from the vault notes via `axon reindex` (ADR-006).

-- Semantic store: one row per chunk, embedding is a little-endian float32 BLOB.
CREATE TABLE vec_chunks (
  chunk_id   INTEGER PRIMARY KEY REFERENCES chunks(id) ON DELETE CASCADE,
  dim        INTEGER NOT NULL,
  model      TEXT,                       -- embedding model, so a model change is detectable
  embedding  BLOB NOT NULL               -- len = dim * 4 bytes
);

-- Lexical store: standalone FTS5 over chunk text, mapped back via chunk_id.
CREATE VIRTUAL TABLE fts_chunks USING fts5(
  text,
  chunk_id UNINDEXED
);
