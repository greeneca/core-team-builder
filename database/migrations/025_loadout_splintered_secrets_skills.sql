-- Migration 025: add encounter_loadouts.splintered_secrets_skills
--
-- Idempotent. The Arcanist Herald of the Tome passive "Splintered Secrets"
-- grants flat Offensive Penetration for each slotted Herald of the Tome ability
-- (1240 each, up to 5). This per-slot value lets the client-side penetration
-- calculator scale that contribution instead of assuming a flat 2 skills.
-- Defaults to 2 (the previous hard-coded behavior, 2480 Penetration).

ALTER TABLE encounter_loadouts
    ADD COLUMN IF NOT EXISTS splintered_secrets_skills INTEGER NOT NULL DEFAULT 2;
