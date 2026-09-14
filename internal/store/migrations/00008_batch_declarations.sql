-- M0 dataset declarations.
--
-- Persists what an ingestion declared about its dataset: the field mapping's
-- target units (so fields have units at rest) and the availability policy the
-- operator chose (so a dataset's policy reference is recoverable).
--
-- The declaration has to be captured at ingestion time because it exists
-- nowhere else: observation rows carry values whose kind travels with the
-- value, but a field's unit is a normalization decision that leaves no trace
-- in the data. Without this column a field's unit could only be guessed, and
-- unit-mismatched comparisons would be undetectable.
--
-- The column lives on batches (not in a datasets table) so the declaration is
-- as immutable and as auditable as the batch it belongs to; the dataset
-- catalog is read as the newest declaration per dataset.
--
-- +goose Up
ALTER TABLE batches ADD COLUMN declaration_json TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE batches DROP COLUMN declaration_json;
