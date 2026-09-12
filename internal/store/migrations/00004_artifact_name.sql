-- +goose Up
ALTER TABLE artifacts ADD COLUMN name TEXT NOT NULL DEFAULT '';

-- +goose Down
-- modernc's bundled SQLite supports ALTER TABLE DROP COLUMN (3.35+).
ALTER TABLE artifacts DROP COLUMN name;
