-- Migration 004: team trial schedule
--
-- Idempotent. Adds when a team's trial runs:
--   schedule_days  the days of the week (e.g. {mon,wed}); validated in backend.
--   schedule_time  time of day as "HH:MM" (24h), '' when unset.

ALTER TABLE teams
    ADD COLUMN IF NOT EXISTS schedule_days TEXT[]     NOT NULL DEFAULT '{}';

ALTER TABLE teams
    ADD COLUMN IF NOT EXISTS schedule_time VARCHAR(5) NOT NULL DEFAULT '';
