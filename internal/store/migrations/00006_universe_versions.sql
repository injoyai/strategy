-- M1-06: universe versions.
--
-- Persists immutable member-selection versions (static lists and historical
-- rule references) bound to the snapshot they resolve against. definition_json
-- is the canonical form (static members sorted and deduplicated) and
-- definition_hash is the checksum over that JSON excluding name and snapshot
-- binding, so the hash identifies the member-selection contract itself.
-- Creation is not idempotent: every save is a new version, mirroring
-- screening's save semantics.
--
-- Column notes:
--   * kind travels as its own column so listings and debugging can separate
--     static from historical_rule without decoding the JSON blob; the blob
--     stays the authoritative definition.
--   * snapshot_id is the only legal resolution context: the resolver opens a
--     DataView on this snapshot and never reads current tables.

-- +goose Up
CREATE TABLE universe_versions (
    id              TEXT    PRIMARY KEY,
    workspace       TEXT    NOT NULL,
    name            TEXT    NOT NULL,
    snapshot_id     TEXT    NOT NULL,
    kind            TEXT    NOT NULL,
    definition_json TEXT    NOT NULL,
    definition_hash TEXT    NOT NULL,
    created_at      INTEGER NOT NULL
);
CREATE INDEX idx_universe_workspace ON universe_versions (workspace, created_at);
CREATE INDEX idx_universe_snapshot  ON universe_versions (workspace, snapshot_id, created_at);

-- +goose Down
DROP TABLE IF EXISTS universe_versions;
