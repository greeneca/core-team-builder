-- Migration 051: default new teams to simple (advanced signup off)
--
-- Idempotent. The pre-made signup mode is stored in teams.simple_signup
-- (037_team_simple_signup.sql). The web UI now presents this as an "Advanced
-- signup (per slot)" toggle — the inverse of simple_signup — that is disabled by
-- default. To match, new teams default to simple_signup = true (advanced off):
--
--   * true  (new default) — "simple" signup: the post hides class/gear and the
--     details dropdown, players pick a ROLE, and claiming takes the first empty
--     slot matching that role. The "Advanced signup" toggle is unchecked.
--   * false               — "advanced"/"specific" signup: players claim an exact
--     slot and the post shows class/gear plus a build-details dropdown. The
--     "Advanced signup" toggle is checked.
--
-- Only the column default changes; existing rows keep their stored value, so
-- current teams are unaffected.

ALTER TABLE teams
    ALTER COLUMN simple_signup SET DEFAULT true;
