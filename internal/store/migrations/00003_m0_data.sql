-- M0-06: data plane.
--
-- Extends batches and snapshots to the OpenAPI wire contract (Batch and
-- Snapshot schemas require dataset_id/row_count/checksum/issues/ready and
-- name/strict_pit), and adds the observations table backing point-in-time
-- queries. Existing M0-04 rows predate the data plane, so every added column
-- gets an inert default and the ALTER set is purely additive.
--
-- Column notes:
--   * frequency travels with batches and observations because the same
--     dataset name can be served at different frequencies; the read path
--     filters on it, so it must be persisted, not inferred.
--   * observations carries denormalized query columns (available_at,
--     revision_id, published_at) extracted from provenance_json. The JSON
--     blob remains the single source of truth; the columns exist so PIT
--     filtering and the latest-revision rule never require parsing JSON.
--   * published_at NULL means the upstream never announced a publish time;
--     strict-PIT snapshots reject such rows at publish time.

-- +goose Up
ALTER TABLE batches ADD COLUMN dataset_id TEXT    NOT NULL DEFAULT '';
ALTER TABLE batches ADD COLUMN frequency  TEXT    NOT NULL DEFAULT '';
ALTER TABLE batches ADD COLUMN row_count  INTEGER NOT NULL DEFAULT 0;
ALTER TABLE batches ADD COLUMN checksum   TEXT    NOT NULL DEFAULT '';
ALTER TABLE batches ADD COLUMN issues     TEXT    NOT NULL DEFAULT '[]';
ALTER TABLE batches ADD COLUMN ready      INTEGER NOT NULL DEFAULT 1;
CREATE INDEX idx_batches_workspace ON batches (workspace, created_at);
CREATE INDEX idx_batches_dataset   ON batches (workspace, dataset_id, created_at);

ALTER TABLE snapshots ADD COLUMN name           TEXT    NOT NULL DEFAULT '';
ALTER TABLE snapshots ADD COLUMN strict_pit     INTEGER NOT NULL DEFAULT 1;
ALTER TABLE snapshots ADD COLUMN quality_issues TEXT    NOT NULL DEFAULT '[]';

CREATE TABLE observations (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    workspace       TEXT    NOT NULL,
    batch_id        TEXT    NOT NULL,
    dataset         TEXT    NOT NULL,
    frequency       TEXT    NOT NULL DEFAULT '',
    instrument_id   TEXT    NOT NULL DEFAULT '',
    entity_id       TEXT    NOT NULL DEFAULT '',
    event_time      INTEGER NOT NULL,
    period_end      TEXT    NOT NULL DEFAULT '',
    effective_from  INTEGER,
    effective_to    INTEGER,
    values_json     BLOB    NOT NULL,
    provenance_json BLOB    NOT NULL,
    available_at    INTEGER NOT NULL,
    revision_id     TEXT    NOT NULL DEFAULT '',
    supersedes      TEXT    NOT NULL DEFAULT '',
    published_at    INTEGER
);
CREATE INDEX idx_obs_lookup    ON observations (workspace, dataset, frequency, instrument_id, event_time);
CREATE INDEX idx_obs_batch     ON observations (batch_id);
CREATE INDEX idx_obs_available ON observations (workspace, dataset, available_at);

-- +goose Down
-- Index drops precede column drops: SQLite's DROP COLUMN refuses columns
-- referenced by an index.
DROP TABLE IF EXISTS observations;
DROP INDEX IF EXISTS idx_obs_available;
DROP INDEX IF EXISTS idx_obs_batch;
DROP INDEX IF EXISTS idx_obs_lookup;
DROP INDEX IF EXISTS idx_batches_dataset;
DROP INDEX IF EXISTS idx_batches_workspace;
ALTER TABLE batches DROP COLUMN dataset_id;
ALTER TABLE batches DROP COLUMN frequency;
ALTER TABLE batches DROP COLUMN row_count;
ALTER TABLE batches DROP COLUMN checksum;
ALTER TABLE batches DROP COLUMN issues;
ALTER TABLE batches DROP COLUMN ready;
ALTER TABLE snapshots DROP COLUMN name;
ALTER TABLE snapshots DROP COLUMN strict_pit;
ALTER TABLE snapshots DROP COLUMN quality_issues;
