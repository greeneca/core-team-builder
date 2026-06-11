-- Migration 008: enforce unique encounter names per team
--
-- Idempotent. Encounter names must be unique within a team (the backend also
-- validates this, plus a single-trial rule). This index is a database-level
-- backstop against duplicates from races or direct writes.
--
-- Note: if a team already has duplicate encounter names, this index creation
-- would fail; dedupe such rows first. (Fresh installs only ever start with a
-- single "Default" per team, so this is a non-issue there.)

CREATE UNIQUE INDEX IF NOT EXISTS idx_encounters_team_name
    ON encounters(team_id, name);
