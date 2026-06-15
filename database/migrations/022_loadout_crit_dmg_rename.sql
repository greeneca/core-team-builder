-- Migration 022: rename encounter_loadouts.weapons -> crit_dmg
--
-- Idempotent. The `weapons` column was repurposed from "equipped weapon-line
-- keys" to "crit-damage source keys" (the two weapon-line passives that grant
-- Critical Damage). Rename the column to match its meaning and avoid confusion.
-- Existing data is preserved by the rename.

DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_name = 'encounter_loadouts' AND column_name = 'weapons'
    ) AND NOT EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_name = 'encounter_loadouts' AND column_name = 'crit_dmg'
    ) THEN
        ALTER TABLE encounter_loadouts RENAME COLUMN weapons TO crit_dmg;
    END IF;
END $$;

-- Safety net for any environment where neither column exists yet.
ALTER TABLE encounter_loadouts
    ADD COLUMN IF NOT EXISTS crit_dmg TEXT[] NOT NULL DEFAULT '{}';
