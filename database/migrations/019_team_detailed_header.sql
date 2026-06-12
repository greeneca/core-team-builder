-- Migration 019: per-team detailed-post header
--
-- Idempotent. Adds a free-form text header the team can edit and that is
-- prepended to the top of the generated detailed Discord signup post.
-- Defaults to empty.

ALTER TABLE teams
    ADD COLUMN IF NOT EXISTS detailed_header TEXT NOT NULL DEFAULT '';
