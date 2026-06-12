-- Migration 014: extra penetration sources on encounter loadouts
--
-- Idempotent. Adds a free-form list of flat penetration sources that aren't
-- otherwise derivable from tracked data (e.g. the Sharpened weapon trait, a
-- Mace/Maul weapon type, generic set-piece bonuses, a Crusher enchant). Each
-- key's penetration value + bucket (self/group) lives in the frontend master
-- data; this column just stores the chosen keys per player per encounter.

ALTER TABLE encounter_loadouts
    ADD COLUMN IF NOT EXISTS pen_extra TEXT[] NOT NULL DEFAULT '{}';
