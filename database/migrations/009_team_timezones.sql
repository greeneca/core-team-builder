-- Migration 009: team timezones
--
-- Idempotent. Adds the set of IANA timezones a team cares about, used to print
-- the trial time in each member's local zone.
--
-- The existing `schedule_timezone` column now stores the *reference* zone the
-- schedule time is expressed in (auto-captured from whoever set it, in their
-- current timezone) rather than a manually picked one. `team_timezones` is the
-- additional list of zones the team wants the time shown in.

ALTER TABLE teams
    ADD COLUMN IF NOT EXISTS team_timezones TEXT[] NOT NULL DEFAULT '{}';
