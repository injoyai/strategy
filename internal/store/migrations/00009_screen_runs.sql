-- M1S S2: screening runs and their published results.
--
-- A run row is created by the worker when it starts executing a screen.run
-- job and is published exactly once, atomically: the summary, the frozen
-- rows and the result hash land in one transaction, so a crash mid-run
-- leaves an unpublished run and never a partial result. Reading before
-- publish is answered with screenrun.result_not_ready rather than an empty
-- result.
--
-- Column notes:
--   * config_json is the frozen ScreenRunCreate payload and config_hash is
--     the checksum over it, so a published result can always be traced back
--     to the exact inputs it was computed from, even after the referenced
--     screener or universe gains new revisions.
--   * engine_version and scoring_policy_version are written on every run:
--     changing selection semantics changes published ranks, so a result must
--     name the pipeline that produced it.
--   * snapshot_hash pins the data the run actually read, independent of the
--     snapshot row that may later be re-listed.
--   * result_hash is the content checksum of the canonical result artifact;
--     the row paging cursor is bound to it, so a cursor cannot be replayed
--     against a different result.
--   * summary_json and columns_json are NULL until publish. columns_json
--     carries the display-column descriptors derived from the frozen rows so
--     every page of one listing reports the same schema.
--   * published_at is the publish marker; result rows are only written in
--     the same transaction that sets it.

-- +goose Up
CREATE TABLE screen_runs (
    id                     TEXT    PRIMARY KEY,
    workspace              TEXT    NOT NULL,
    job_id                 TEXT    NOT NULL,
    screener_id            TEXT    NOT NULL,
    source_run_id          TEXT    NOT NULL DEFAULT '',
    config_json            TEXT    NOT NULL,
    config_hash            TEXT    NOT NULL,
    engine_version         TEXT    NOT NULL,
    scoring_policy_version TEXT    NOT NULL,
    snapshot_hash          TEXT    NOT NULL,
    summary_json           TEXT,
    result_hash            TEXT    NOT NULL DEFAULT '',
    columns_json           TEXT    NOT NULL DEFAULT '[]',
    artifact_ids           TEXT    NOT NULL DEFAULT '[]',
    published_at           INTEGER,
    created_at             INTEGER NOT NULL
);
CREATE INDEX idx_screen_runs_workspace ON screen_runs (workspace, created_at);
CREATE INDEX idx_screen_runs_job       ON screen_runs (workspace, job_id);
CREATE INDEX idx_screen_runs_screener  ON screen_runs (workspace, screener_id, created_at);

-- One frozen result row. Where the run lists a row is decided by ordinal, the
-- engine's canonical order (selected and rankable rows by official rank, then
-- excluded rows by instrument id), so paging needs no re-sorting and stays
-- stable across pages. selected and stage exist as columns so the state filter
-- and a spot check never have to decode the frozen payload; row_json stays the
-- authoritative evidence.
CREATE TABLE screen_result_rows (
    run_id        TEXT    NOT NULL,
    workspace     TEXT    NOT NULL,
    ordinal       INTEGER NOT NULL,
    instrument_id TEXT    NOT NULL,
    stage         TEXT    NOT NULL,
    selected      INTEGER NOT NULL,
    row_json      TEXT    NOT NULL,
    PRIMARY KEY (run_id, instrument_id)
);
CREATE INDEX idx_screen_result_rows_order ON screen_result_rows (workspace, run_id, ordinal);

-- +goose Down
DROP TABLE IF EXISTS screen_result_rows;
DROP TABLE IF EXISTS screen_runs;
