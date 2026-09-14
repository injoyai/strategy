-- M1S S2: screener versions.
--
-- Persists immutable screening rule sets (input bindings, condition tree,
-- ranking/scoring, selection and display columns). definition_json is the
-- canonical wire form of the definition and definition_hash is the checksum
-- over that JSON alone, excluding name, description and parent_id, so the
-- hash identifies the rule contract itself rather than the label it was
-- saved under.
--
-- Creation is not idempotent: every save is a new immutable revision, so a
-- re-run of the same request mints another version. (id, version) addresses
-- exactly one frozen rule set: id is the screener identity and survives
-- across revisions, version is the revision label.
--
-- Column notes:
--   * rule_schema_version pins the schema a definition was written against,
--     so a future schema change can never silently reinterpret an old row.
--   * parent_id carries the lineage when a save derives from an earlier
--     version; it is deliberately outside the hashed payload.

-- +goose Up
CREATE TABLE screener_versions (
    id                  TEXT    NOT NULL,
    workspace           TEXT    NOT NULL,
    version             TEXT    NOT NULL,
    name                TEXT    NOT NULL,
    description         TEXT    NOT NULL,
    parent_id           TEXT    NOT NULL,
    rule_schema_version TEXT    NOT NULL,
    definition_json     TEXT    NOT NULL,
    definition_hash     TEXT    NOT NULL,
    created_at          INTEGER NOT NULL,
    PRIMARY KEY (workspace, id, version)
);
CREATE INDEX idx_screener_workspace ON screener_versions (workspace, created_at);
CREATE INDEX idx_screener_name      ON screener_versions (workspace, name);

-- +goose Down
DROP TABLE IF EXISTS screener_versions;
