-- Migration 008: enforce unique encounter names per team
--
-- Idempotent. Encounter names must be unique within a team (the backend also
-- validates this, plus a single-trial rule). This index is a database-level
-- backstop against duplicates from races or direct writes.
--
-- Note: if a team already has duplicate encounter names, this index creation
-- would fail; dedupe such rows first. (Fresh installs only ever start with a
-- single "Default" per team, so this is a non-issue there.)

-- Guarded: migration 048 re-keys encounters to roster_id, drops team_id, and
-- replaces this index with idx_encounters_roster_name. On a re-run over an
-- already-migrated DB the column is gone, so only (re)create the team-keyed
-- unique index while team_id still exists.
DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_name = 'encounters' AND column_name = 'team_id'
    ) THEN
        CREATE UNIQUE INDEX IF NOT EXISTS idx_encounters_team_name
            ON encounters(team_id, name);
    END IF;
END $$;
