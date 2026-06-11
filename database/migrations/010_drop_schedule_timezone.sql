-- Migration 010: drop the schedule reference timezone
--
-- Idempotent. The trial time is now stored in UTC ("HH:MM", '' when unset) and
-- converted to/from each viewer's current timezone in the client, so the
-- per-team reference zone is no longer needed. `team_timezones` (migration 009)
-- still holds the extra zones the time is displayed in.

ALTER TABLE teams
    DROP COLUMN IF EXISTS schedule_timezone;
