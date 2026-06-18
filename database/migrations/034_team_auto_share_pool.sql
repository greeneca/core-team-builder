-- Migration 034: per-team auto-share with the member pool
--
-- Idempotent. Adds a flag that, when enabled, automatically grants viewer
-- access to the app accounts of everyone in the team's member pool
-- (team_roster_members) — current and future. A pool member only becomes a
-- viewer once their Discord identity is tied to an app account (i.e. they have
-- signed in / linked via Discord), since sharing needs an actual user row.
--
-- Defaults to disabled so existing teams keep their current sharing untouched.
-- Disabling the flag never revokes already-granted shares; it only stops new
-- pool members from being shared with.

ALTER TABLE teams
    ADD COLUMN IF NOT EXISTS auto_share_pool_viewers BOOLEAN NOT NULL DEFAULT false;

ALTER TABLE teams
    ALTER COLUMN auto_share_pool_viewers SET DEFAULT false;
