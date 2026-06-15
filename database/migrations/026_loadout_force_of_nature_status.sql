-- Migration 026: add encounter_loadouts.force_of_nature_status
--
-- Idempotent. The Warfare champion star "Force of Nature" grants flat Offensive
-- Penetration for each negative status effect on the enemy (660 each, up to 5 =
-- 3300). This per-slot value lets the client-side penetration calculator scale
-- that contribution when the Force of Nature blue CP is slotted. Defaults to 5
-- (the maximum / full bonus).

ALTER TABLE encounter_loadouts
    ADD COLUMN IF NOT EXISTS force_of_nature_status INTEGER NOT NULL DEFAULT 5;
