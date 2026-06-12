-- Migration 012: per-player crit-damage inputs on encounter loadouts
--
-- Idempotent. Adds the per-encounter, per-player inputs the crit-damage
-- calculator needs that aren't otherwise tracked:
--   - cp_blue : slotted blue (Warfare) champion-point star keys
--   - weapons : equipped weapon-line keys (drives weapon-line crit passives)
--   - mundus  : the player's mundus stone key ('' = unset)
--   - armor_heavy / armor_medium / armor_light : count of each armor weight
--     (medium pieces drive the Dexterity crit passive). 0..7 each.

ALTER TABLE encounter_loadouts
    ADD COLUMN IF NOT EXISTS cp_blue      TEXT[]   NOT NULL DEFAULT '{}',
    ADD COLUMN IF NOT EXISTS weapons      TEXT[]   NOT NULL DEFAULT '{}',
    ADD COLUMN IF NOT EXISTS mundus       TEXT     NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS armor_heavy  SMALLINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS armor_medium SMALLINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS armor_light  SMALLINT NOT NULL DEFAULT 0;
