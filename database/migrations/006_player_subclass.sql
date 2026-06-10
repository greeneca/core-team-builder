-- Migration 006: player subclassing + build selections
--
-- Idempotent. Each player gains a `subclassed` flag plus the build selections:
--   subclassed = true  → up to 3 class skill lines (skill_line_1..3), each one
--                         of the 21 class skill lines (validated in backend).
--   subclassed = false → up to 2 class masteries (mastery_1..2) drawn from the
--                         5 masteries of the player's selected class.
-- The two sets are mutually exclusive; the backend clears the inactive set on
-- save. Empty string ('') means "unset".

ALTER TABLE players
    ADD COLUMN IF NOT EXISTS subclassed   BOOLEAN     NOT NULL DEFAULT false;

ALTER TABLE players
    ADD COLUMN IF NOT EXISTS skill_line_1 VARCHAR(40) NOT NULL DEFAULT '';
ALTER TABLE players
    ADD COLUMN IF NOT EXISTS skill_line_2 VARCHAR(40) NOT NULL DEFAULT '';
ALTER TABLE players
    ADD COLUMN IF NOT EXISTS skill_line_3 VARCHAR(40) NOT NULL DEFAULT '';

ALTER TABLE players
    ADD COLUMN IF NOT EXISTS mastery_1    VARCHAR(40) NOT NULL DEFAULT '';
ALTER TABLE players
    ADD COLUMN IF NOT EXISTS mastery_2    VARCHAR(40) NOT NULL DEFAULT '';
