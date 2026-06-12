-- Migration 013: player race
--
-- Idempotent. Adds the ESO race for each roster slot. Race is a character-level
-- attribute (not per-encounter), so it lives on players. It feeds the crit
-- calculator (e.g. the Khajiit "Feline Ambush" passive). '' = unset.

ALTER TABLE players
    ADD COLUMN IF NOT EXISTS race TEXT NOT NULL DEFAULT '';
