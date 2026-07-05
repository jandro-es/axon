-- IVF-flat approximate vector index (ADR-025). Centroids and per-chunk
-- assignment are DERIVED from vec_chunks.embedding; `axon reindex` rebuilds
-- them (ADR-006 holds — this is doubly derived).
CREATE TABLE vec_centroids (
    id     INTEGER PRIMARY KEY,   -- centroid ordinal [0,k)
    dim    INTEGER NOT NULL,
    model  TEXT    NOT NULL,
    vector BLOB    NOT NULL        -- little-endian float32 (same codec as vec_chunks)
);

ALTER TABLE vec_chunks ADD COLUMN centroid INTEGER;   -- nullable; NULL = unassigned overflow
CREATE INDEX idx_vec_chunks_centroid ON vec_chunks(centroid);
