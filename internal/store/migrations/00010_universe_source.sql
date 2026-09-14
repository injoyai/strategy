-- M1S S2-03: the provenance of a universe saved from a screening run.
--
-- A pool saved from a run must carry the selection time, the data it was
-- selected against and the quality limits that exploration reported, or a
-- consumer cannot refuse it: SC-AC-10 requires a backtest to reject a pool
-- selected at t when its own decision time is earlier than t, and to keep the
-- exploration caveats visible. The evidence is one unit that is only ever read
-- whole, so it travels as a single JSON column ('' means "not saved from a
-- run"); it is deliberately outside definition_json because it does not change
-- which members were selected, only when and under what caveats.

-- +goose Up
ALTER TABLE universe_versions ADD COLUMN source_json TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE universe_versions DROP COLUMN source_json;
