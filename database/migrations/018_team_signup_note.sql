-- Migration 018: per-team Discord signup note
--
-- Idempotent. Adds a free-form text footer the team can edit and that is
-- appended to the bottom of the generated condensed Discord signup list.
-- Defaults to empty.

ALTER TABLE teams
    ADD COLUMN IF NOT EXISTS signup_note TEXT NOT NULL DEFAULT '';
