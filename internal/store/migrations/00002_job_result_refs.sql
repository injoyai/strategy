-- M0-05: job result references.
--
-- The Job contract schema marks result_refs as required, but 00001 has no
-- column for it. Completed jobs publish the resources they produced
-- (snapshot, batch, artifact ids) as a JSON array of {kind,id} objects in
-- insertion order.
--
-- Foreign key decision (resolves the deferral note in 00001): M0 has no
-- delete path - jobs and runs are never removed, retries create new rows
-- instead of overwriting - so there are no cascade semantics to enforce.
-- Referential integrity rests on the single-transaction create path (a run
-- row is inserted in the same transaction as its job row). Add FKs only if
-- a delete API is ever introduced.

-- +goose Up
ALTER TABLE jobs ADD COLUMN result_refs TEXT NOT NULL DEFAULT '[]';

-- +goose Down
-- modernc's bundled SQLite supports ALTER TABLE DROP COLUMN (3.35+).
ALTER TABLE jobs DROP COLUMN result_refs;
