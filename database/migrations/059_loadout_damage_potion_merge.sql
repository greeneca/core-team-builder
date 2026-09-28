-- Migration 059: merge the two damage potions into a single loadout key
--
-- Idempotent. Update 51 hybridized alchemy: Essence of Spell Power and Essence
-- of Weapon Power now grant the same buffs (Major Brutality and Major
-- Savagery), so the picker collapsed `spell_power_potion` and
-- `weapon_power_potion` into one `damage_potion` entry. Rewrite the stored
-- keys to match.
--
-- Re-running is a no-op: after the first pass no row contains either old key.

-- Loadouts that don't already carry the merged key: drop the old keys and
-- append `damage_potion` once, so a loadout that listed both collapses to one.
UPDATE encounter_loadouts
SET potions = array_append(
        array_remove(array_remove(potions, 'spell_power_potion'), 'weapon_power_potion'),
        'damage_potion')
WHERE potions && ARRAY['spell_power_potion', 'weapon_power_potion']
  AND NOT ('damage_potion' = ANY (potions));

-- Loadouts that already carry the merged key: just drop the old keys.
UPDATE encounter_loadouts
SET potions = array_remove(array_remove(potions, 'spell_power_potion'), 'weapon_power_potion')
WHERE potions && ARRAY['spell_power_potion', 'weapon_power_potion'];
