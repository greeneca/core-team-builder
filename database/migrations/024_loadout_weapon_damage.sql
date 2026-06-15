-- Migration 024: add encounter_loadouts.weapon_damage
--
-- Idempotent. Anthelmir's Construct reduces the target's Armor (a group-wide
-- penetration debuff) by an amount that scales off the wearer's higher Weapon or
-- Spell Damage. This per-slot value lets the client-side penetration calculator
-- compute that contribution. Defaults to 0 (no contribution until entered).

ALTER TABLE encounter_loadouts
    ADD COLUMN IF NOT EXISTS weapon_damage INTEGER NOT NULL DEFAULT 0;
