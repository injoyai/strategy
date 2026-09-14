-- M1S S2-03: the quality limits a published run was computed under.
--
-- A run's resolution reports non-blocking findings (an empty mother pool, a
-- binding the catalog cannot type). They do not stop the run, but a pool saved
-- from it must carry them forward — "探索质量限制不丢失" in the screening
-- design — so the run freezes their codes at publish time. Codes rather than
-- messages: the contract's ScreenUniverseSource.quality_limits is a list of
-- stable identifiers, and a message is not evidence a consumer can act on.

-- +goose Up
ALTER TABLE screen_runs ADD COLUMN quality_limits_json TEXT NOT NULL DEFAULT '[]';

-- +goose Down
ALTER TABLE screen_runs DROP COLUMN quality_limits_json;
