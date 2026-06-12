-- Migration 011: per-player potions on encounter loadouts
--
-- Idempotent. Adds a third free-form loadout list (alongside gear and skills)
-- so each player's potions can be tracked per encounter. Potions are a buff
-- source, just like gear and skills.

ALTER TABLE encounter_loadouts
    ADD COLUMN IF NOT EXISTS potions TEXT[] NOT NULL DEFAULT '{}';
