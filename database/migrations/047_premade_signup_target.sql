-- Migration 047: "sign up another player" target on the DM edit session
--
-- Idempotent. The "Edit run" DM flow gains a "Sign up a player" option that lets
-- a run's creator/editor add someone else to a slot. Resolving the chosen player
-- (a matched guild member or a free-typed name) spans two component interactions,
-- so the pending target is parked on the per-user DM session between them:
--
--   * signup_user_id   — the target's Discord user id, or '' for a free-typed
--     name with no matched account (a synthetic id is generated at claim time).
--   * signup_user_name — the display name to store on the signup.

ALTER TABLE premade_signup_sessions
    ADD COLUMN IF NOT EXISTS signup_user_id TEXT NOT NULL DEFAULT '';

ALTER TABLE premade_signup_sessions
    ADD COLUMN IF NOT EXISTS signup_user_name TEXT NOT NULL DEFAULT '';
