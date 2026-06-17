-- Migration 032: add encounter_loadouts.scribed_buffs
--
-- Idempotent. Scribing grimoires let a player attach a group buff to a slotted
-- scribed skill (e.g. Minor Berserk, Minor Courage). This per-slot list records
-- which group buffs the player's scribed skill(s) provide so the client-side
-- Group Buffs coverage card can count them. Empty for non-scribed builds.

ALTER TABLE encounter_loadouts
    ADD COLUMN IF NOT EXISTS scribed_buffs TEXT[] NOT NULL DEFAULT '{}';
