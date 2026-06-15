-- Migration 023: add encounter_loadouts.catalyst_elements
--
-- Idempotent. Elemental Catalyst grants +5% Critical Damage taken per distinct
-- elemental damage type (Flame/Frost/Shock) the wearer applies, up to 3 (15%).
-- This per-slot count lets a build that only deals 1 or 2 damage types model the
-- reduced bonus. Defaults to 3 so existing loadouts keep the previous full value.

ALTER TABLE encounter_loadouts
    ADD COLUMN IF NOT EXISTS catalyst_elements SMALLINT NOT NULL DEFAULT 3;
