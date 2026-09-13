-- M1-05: replay-time visibility bound.
--
-- Adds the denormalized ingested_at query column to observations. The JSON
-- provenance blob stays the single source of truth; the column exists so the
-- replay filter (ingested_at <= replay_time) never parses JSON. Rows written
-- before this migration backfill from provenance_json at second precision
-- (SQLite date functions carry no sub-second digits); rows appended after it
-- keep full nanosecond precision. Backfilled seconds are clamped at zero so
-- a missing or pre-epoch ingest time lands on the epoch instead of
-- overflowing the multiply.

-- +goose Up
ALTER TABLE observations ADD COLUMN ingested_at INTEGER NOT NULL DEFAULT 0;
UPDATE observations
SET ingested_at = MAX(COALESCE(CAST(strftime('%s', json_extract(provenance_json, '$.ingested_at')) AS INTEGER), 0), 0) * 1000000000;
CREATE INDEX idx_obs_ingested ON observations (workspace, dataset, ingested_at);

-- +goose Down
DROP INDEX IF EXISTS idx_obs_ingested;
ALTER TABLE observations DROP COLUMN ingested_at;
