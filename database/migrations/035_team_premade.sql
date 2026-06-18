-- Migration 035: pre-made trial run mode
--
-- Idempotent. Adds a per-team toggle that turns a team into a "pre-made" trial
-- run: a one-off, scheduled run that players sign up for per slot via the Discord
-- bot (/coreteam premade), rather than a fixed recurring roster.
--
-- When enabled, the web UI hides the recurring trial schedule, the Discord bot
-- texts (post/DM/signup footers), the per-player Discord handles, and the member
-- pool — none of which apply to a pre-made run — and surfaces a single free-form
-- `premade_post` body the bot prepends to the run announcement.
--
-- Defaults to disabled so existing teams keep their current behavior. The toggle
-- is purely presentational/behavioral and non-destructive: the hidden fields are
-- preserved and reappear if the flag is turned back off.

ALTER TABLE teams
    ADD COLUMN IF NOT EXISTS pre_made     BOOLEAN NOT NULL DEFAULT false,
    ADD COLUMN IF NOT EXISTS premade_post TEXT    NOT NULL DEFAULT '';

ALTER TABLE teams
    ALTER COLUMN pre_made SET DEFAULT false;
