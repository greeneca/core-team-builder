-- Migration 017: per-team encounters toggle
--
-- Idempotent. Adds a flag controlling whether a team uses multiple encounters.
-- When disabled, the UI hides the encounters section and shows only the first
-- encounter. The team still keeps at least one encounter in the database; the
-- flag only affects how the encounters are surfaced. Defaults to disabled so new
-- teams start in single-encounter mode; an editor can opt in per team.

ALTER TABLE teams
    ADD COLUMN IF NOT EXISTS encounters_enabled BOOLEAN NOT NULL DEFAULT false;

-- Ensure the default is disabled even if the column already exists from an
-- earlier run of this migration (it was previously created defaulting to true).
ALTER TABLE teams
    ALTER COLUMN encounters_enabled SET DEFAULT false;
