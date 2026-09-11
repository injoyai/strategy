-- M0-04 minimal metadata model.
--
-- Conventions for every table:
--   * workspace: every business row carries a workspace boundary. M0 runs a
--     single "default" workspace, but authorization must never be built on
--     guessable ids alone, so the column exists from day one.
--   * timestamps: INTEGER unix nanoseconds, always UTC, so lexicographic
--     comparison pitfalls of TEXT timestamps cannot occur.
--   * foreign keys are intentionally deferred to M0-05, when the job state
--     machine fixes delete/cascade semantics; M0 relies on application-level
--     transactions for referential integrity.
-- schema_migrations itself is owned by the migration runner (store package)
-- and is therefore not created here.

-- +goose Up
CREATE TABLE idempotency_records (
    scope           TEXT    NOT NULL,
    key             TEXT    NOT NULL,
    request_hash    TEXT    NOT NULL,
    response_status INTEGER NOT NULL DEFAULT 0,
    body            BLOB,
    location        TEXT    NOT NULL DEFAULT '',
    resource_id     TEXT    NOT NULL DEFAULT '',
    created_at      INTEGER NOT NULL,
    expires_at      INTEGER NOT NULL,
    PRIMARY KEY (scope, key)
);

CREATE TABLE jobs (
    id             TEXT    NOT NULL PRIMARY KEY,
    workspace      TEXT    NOT NULL,
    run_id         TEXT    NOT NULL,
    parent_job_id  TEXT    NOT NULL DEFAULT '',
    kind           TEXT    NOT NULL,
    state          TEXT    NOT NULL,
    phase          TEXT    NOT NULL DEFAULT '',
    progress_total INTEGER,
    progress_done  INTEGER NOT NULL DEFAULT 0,
    config_hash    TEXT    NOT NULL,
    lease_owner    TEXT    NOT NULL DEFAULT '',
    lease_until    INTEGER,
    fencing_token  INTEGER NOT NULL DEFAULT 0,
    error          TEXT    NOT NULL DEFAULT '',
    created_at     INTEGER NOT NULL,
    updated_at     INTEGER NOT NULL
);
CREATE INDEX idx_jobs_state ON jobs (state);
CREATE INDEX idx_jobs_workspace ON jobs (workspace, created_at);

CREATE TABLE job_events (
    job_id         TEXT    NOT NULL,
    sequence       INTEGER NOT NULL,
    workspace      TEXT    NOT NULL,
    state          TEXT    NOT NULL,
    phase          TEXT    NOT NULL DEFAULT '',
    progress_total INTEGER,
    progress_done  INTEGER NOT NULL DEFAULT 0,
    payload        BLOB,
    created_at     INTEGER NOT NULL,
    PRIMARY KEY (job_id, sequence)
);

CREATE TABLE runs (
    id               TEXT    NOT NULL PRIMARY KEY,
    workspace        TEXT    NOT NULL,
    kind             TEXT    NOT NULL,
    immutable_config BLOB    NOT NULL,
    manifest_ref     TEXT    NOT NULL DEFAULT '',
    terminal_state   TEXT    NOT NULL DEFAULT '',
    source_run_id    TEXT    NOT NULL DEFAULT '',
    created_at       INTEGER NOT NULL,
    updated_at       INTEGER NOT NULL
);

-- Versions of provider connection settings. secret_ref points at the secret
-- store; plaintext secrets are never stored here.
CREATE TABLE provider_connection_versions (
    id          TEXT    NOT NULL PRIMARY KEY,
    workspace   TEXT    NOT NULL,
    parent_id   TEXT    NOT NULL,
    version     INTEGER NOT NULL,
    settings    BLOB    NOT NULL,
    secret_ref  TEXT    NOT NULL DEFAULT '',
    created_at  INTEGER NOT NULL,
    UNIQUE (workspace, parent_id, version)
);

CREATE TABLE batches (
    id                  TEXT    NOT NULL PRIMARY KEY,
    workspace           TEXT    NOT NULL,
    job_id              TEXT    NOT NULL,
    request_evidence    BLOB,
    raw_manifest        BLOB,
    normalized_manifest BLOB,
    quality_status      TEXT    NOT NULL DEFAULT '',
    checksums           BLOB,
    created_at          INTEGER NOT NULL
);
CREATE INDEX idx_batches_job ON batches (job_id);

CREATE TABLE snapshots (
    id             TEXT    NOT NULL PRIMARY KEY,
    workspace      TEXT    NOT NULL,
    manifest_hash  TEXT    NOT NULL,
    quality_status TEXT    NOT NULL DEFAULT '',
    policy_version TEXT    NOT NULL DEFAULT '',
    published_at   INTEGER,
    created_at     INTEGER NOT NULL
);

CREATE TABLE snapshot_batches (
    snapshot_id TEXT    NOT NULL,
    ordinal     INTEGER NOT NULL,
    batch_id    TEXT    NOT NULL,
    workspace   TEXT    NOT NULL,
    PRIMARY KEY (snapshot_id, ordinal)
);
CREATE INDEX idx_snapshot_batches_batch ON snapshot_batches (batch_id);

CREATE TABLE artifacts (
    id           TEXT    NOT NULL PRIMARY KEY,
    workspace    TEXT    NOT NULL,
    media_type   TEXT    NOT NULL,
    size         INTEGER NOT NULL,
    checksum     TEXT    NOT NULL,
    storage_key  TEXT    NOT NULL,
    published_at INTEGER,
    created_at   INTEGER NOT NULL,
    UNIQUE (storage_key)
);
CREATE INDEX idx_artifacts_workspace ON artifacts (workspace, created_at);

-- +goose Down
DROP TABLE IF EXISTS artifacts;
DROP TABLE IF EXISTS snapshot_batches;
DROP TABLE IF EXISTS snapshots;
DROP TABLE IF EXISTS batches;
DROP TABLE IF EXISTS provider_connection_versions;
DROP TABLE IF EXISTS runs;
DROP TABLE IF EXISTS job_events;
DROP TABLE IF EXISTS jobs;
DROP TABLE IF EXISTS idempotency_records;
