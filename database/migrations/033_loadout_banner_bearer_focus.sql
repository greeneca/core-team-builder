-- Migration 033: add encounter_loadouts.banner_bearer_focus
--
-- Idempotent. The Banner Bearer scribing grimoire has a Focus Script that
-- determines its banner morph (e.g. Fortifying Banner / Mitigation). This
-- per-slot value records the chosen Focus Script so the web UI and Discord
-- export can display it. Informational only; empty for builds without Banner
-- Bearer slotted.

ALTER TABLE encounter_loadouts
    ADD COLUMN IF NOT EXISTS banner_bearer_focus TEXT NOT NULL DEFAULT '';
