-- eval_runs: machine-local record of each `axon eval` outcome, keyed by task
-- family + model ref. Derived/operational (like automation_state), DB-only and
-- S9-exempt: there is no vault source to rebuild it from, so reindex ignores it.
CREATE TABLE eval_runs (
    id        INTEGER PRIMARY KEY AUTOINCREMENT,
    family    TEXT    NOT NULL,
    model_ref TEXT    NOT NULL,
    digest    TEXT    NOT NULL DEFAULT '',
    passed    INTEGER NOT NULL,
    total     INTEGER NOT NULL,
    pass_pct  INTEGER NOT NULL,
    ran_at    TEXT    NOT NULL
);
CREATE INDEX idx_eval_runs_lookup ON eval_runs (family, model_ref, ran_at DESC);
