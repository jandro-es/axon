-- 0003_automation_state — Phase 4 change-gate cursors.
-- Each automation persists an opaque cursor between runs so DetectChange can
-- decide whether there is new material worth a (possibly costly) run. The runs
-- table itself already exists from 0001.

CREATE TABLE automation_state (
  automation TEXT PRIMARY KEY,
  cursor     TEXT,
  updated    TEXT
);
