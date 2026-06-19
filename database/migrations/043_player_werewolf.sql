-- Migration 043: per-player werewolf toggle
--
-- Idempotent. Adds a per-player werewolf flag. When set, the player runs a
-- werewolf build: the default werewolf skills are kept in that slot's skills
-- loadout across every encounter, and the Discord post/signup lines tag the slot
-- with "WW" before its gear. Clearing the flag removes the werewolf skills.
--
-- Existing players default to false (not a werewolf), so rosters and loadouts are
-- unchanged.

ALTER TABLE players
    ADD COLUMN IF NOT EXISTS werewolf BOOLEAN NOT NULL DEFAULT FALSE;
