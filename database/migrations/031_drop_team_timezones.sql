-- Migration 031: drop the team display-timezone list
--
-- Idempotent. `team_timezones` (migration 009) was meant to render the trial
-- time in several extra zones, but the app now always shows the time in each
-- viewer's own current timezone (the client converts UTC->local), and the
-- Discord bot uses dynamic per-viewer timestamps. The column was never read for
-- display, so it is removed.

ALTER TABLE teams
    DROP COLUMN IF EXISTS team_timezones;
