-- Migration 045: rewrite stored "perfected_*" gear sets to their base set
--
-- Idempotent. The Perfected variants of every gear set were removed from the
-- app's master data (frontend/js/gear-skills.js). Existing per-slot loadouts may
-- still reference a "perfected_*" key in encounter_loadouts.gear; this migration
-- maps each such key to its non-perfected base set so old loadouts keep their
-- set instead of showing an unknown value.
--
-- gear is an ordered TEXT[] of canonical set keys, so we unnest WITH ORDINALITY,
-- remap each element via the mapping below, then re-aggregate in original order.
--
-- perfected_stone_talkers_oath has no non-perfected counterpart in the data, so
-- it maps to '' and is dropped from the array.
--
-- Re-running is a no-op: only rows whose gear still contains a perfected_* key
-- are touched, and after the rewrite none remain.

WITH map(perfected, base) AS (
    VALUES
        ('perfected_aegis_of_galenwe', 'aegis_of_galenwe'),
        ('perfected_ansuuls_torment', 'ansuuls_torment'),
        ('perfected_arms_of_relequen', 'arms_of_relequen'),
        ('perfected_bahseis_mania', 'bahseis_mania'),
        ('perfected_caustic_arrow', 'caustic_arrow'),
        ('perfected_chaotic_whirlwind', 'chaotic_whirlwind'),
        ('perfected_claw_of_yolnahkriin', 'claw_of_yolnahkriin'),
        ('perfected_concentrated_force', 'concentrated_force'),
        ('perfected_coral_riptide', 'coral_riptide'),
        ('perfected_cruel_flurry', 'cruel_flurry'),
        ('perfected_crushing_wall', 'crushing_wall'),
        ('perfected_defensive_position', 'defensive_position'),
        ('perfected_destructive_impact', 'destructive_impact'),
        ('perfected_disciplined_slash', 'disciplined_slash'),
        ('perfected_dolorous_arena', 'dolorous_arena'),
        ('perfected_executioners_blade', 'executioners_blade'),
        ('perfected_eye_of_nahviintaas', 'eye_of_nahviintaas'),
        ('perfected_false_gods_devotion', 'false_gods_devotion'),
        ('perfected_force_overflow', 'force_overflow'),
        ('perfected_frenzied_momentum', 'frenzied_momentum'),
        ('perfected_gallant_charge', 'gallant_charge'),
        ('perfected_grand_rejuvenation', 'grand_rejuvenation'),
        ('perfected_harmony_in_chaos', 'harmony_in_chaos'),
        ('perfected_kazpians_cruel_signet', 'kazpians_cruel_signet'),
        ('perfected_kynes_wind', 'kynes_wind'),
        ('perfected_lucent_echoes', 'lucent_echoes'),
        ('perfected_mantle_of_siroria', 'mantle_of_siroria'),
        ('perfected_menders_ward', 'menders_ward'),
        ('perfected_merciless_charge', 'merciless_charge'),
        ('perfected_mora_scribes_thesis', 'mora_scribes_thesis'),
        ('perfected_olorime', 'vestment_of_olorime'),
        ('perfected_peace_and_serenity', 'peace_and_serenity'),
        ('perfected_pearlescent_ward', 'pearlescent_ward'),
        ('perfected_piercing_spray', 'piercing_spray'),
        ('perfected_pillagers_profit', 'pillagers_profit'),
        ('perfected_point_blank_snipe', 'point_blank_snipe'),
        ('perfected_precise_regeneration', 'precise_regeneration'),
        ('perfected_puncturing_remedy', 'puncturing_remedy'),
        ('perfected_radial_uppercut', 'radial_uppercut'),
        ('perfected_rampaging_slash', 'rampaging_slash'),
        ('perfected_recovery_convergence', 'recovery_convergence'),
        ('perfected_roaring_opportunist', 'roaring_opportunist'),
        ('perfected_saxhleel_champion', 'saxhleel_champion'),
        ('perfected_slivers_of_the_null_arca', 'slivers_of_the_null_arca'),
        ('perfected_spectral_cloak', 'spectral_cloak'),
        ('perfected_stinging_slashes', 'stinging_slashes'),
        ('perfected_stone_talkers_oath', ''),
        ('perfected_sul_xans_torment', 'sul_xans_torment'),
        ('perfected_test_of_resolve', 'test_of_resolve'),
        ('perfected_thunderous_volley', 'thunderous_volley'),
        ('perfected_timeless_blessing', 'timeless_blessing'),
        ('perfected_titanic_cleave', 'titanic_cleave'),
        ('perfected_tooth_of_lokkestiiz', 'tooth_of_lokkestiiz'),
        ('perfected_transformative_hope', 'transformative_hope'),
        ('perfected_virulent_shot', 'virulent_shot'),
        ('perfected_void_bash', 'void_bash'),
        ('perfected_vrols_command', 'vrols_command'),
        ('perfected_whorl_of_the_depths', 'whorl_of_the_depths'),
        ('perfected_wild_impulse', 'wild_impulse'),
        ('perfected_wrath_of_elements', 'wrath_of_elements'),
        ('perfected_xoryns_masterpiece', 'xoryns_masterpiece'),
        ('perfected_yandirs_might', 'yandirs_might')
),
rebuilt AS (
    SELECT
        el.encounter_id,
        el.slot,
        COALESCE(
            array_agg(COALESCE(m.base, u.elem) ORDER BY u.ord)
                FILTER (WHERE COALESCE(m.base, u.elem) <> ''),
            '{}'::TEXT[]
        ) AS new_gear
    FROM encounter_loadouts el
    CROSS JOIN LATERAL unnest(el.gear) WITH ORDINALITY AS u(elem, ord)
    LEFT JOIN map m ON m.perfected = u.elem
    WHERE el.gear && (SELECT array_agg(perfected) FROM map)
    GROUP BY el.encounter_id, el.slot
)
UPDATE encounter_loadouts el
SET gear = rebuilt.new_gear
FROM rebuilt
WHERE el.encounter_id = rebuilt.encounter_id
  AND el.slot = rebuilt.slot;
