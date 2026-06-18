-- Migration 037: pre-made simple-vs-specific signup mode
--
-- Idempotent. Adds a per-team toggle that controls how players sign up for a
-- pre-made run (only meaningful when pre_made is on):
--
--   * false (default) — "specific" signup: players claim an exact slot; the post
--     shows each slot's class/gear and offers a "get build details" dropdown.
--   * true            — "simple" signup: the post hides class/gear and the
--     details dropdown, players pick a ROLE, and claiming takes the first empty
--     slot matching that role.
--
-- Defaults to disabled so existing pre-made teams keep specific signups.

ALTER TABLE teams
    ADD COLUMN IF NOT EXISTS simple_signup BOOLEAN NOT NULL DEFAULT false;

ALTER TABLE teams
    ALTER COLUMN simple_signup SET DEFAULT false;
