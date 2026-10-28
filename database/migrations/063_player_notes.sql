-- Migration 063: per-player notes
--
-- Idempotent. Adds a free-form notes field to each roster slot. When non-empty,
-- the Discord "Build Details" DM includes it. Existing players default to ''.

ALTER TABLE players
    ADD COLUMN IF NOT EXISTS notes TEXT NOT NULL DEFAULT '';
