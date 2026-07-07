-- Migration 054: pre-made "tentative" (maybe) list
--
-- Idempotent. Adds a per-run "tentative" list to pre-made trial runs, parallel
-- to premade_waitlist. A tentative signup means the player might play a given
-- role but isn't committing to a slot: they don't hold a slot and aren't in the
-- waitlist queue, but they're listed on the post and pinged with everyone else
-- ~15 minutes before the run.
--
--   * premade_tentative — one row per tentative user per run. role is the role
--     they might play; created_at orders the list. A UNIQUE on
--     (run_id, discord_user_id) enforces one tentative entry per user (switching
--     roles replaces the entry). A user is only ever in one of signups /
--     waitlist / tentative at a time; the bot clears the others on transition.

CREATE TABLE IF NOT EXISTS premade_tentative (
    id               BIGINT      GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    run_id           BIGINT      NOT NULL REFERENCES premade_runs(id) ON DELETE CASCADE,
    role             TEXT        NOT NULL DEFAULT '',
    discord_user_id  TEXT        NOT NULL,
    discord_username TEXT        NOT NULL DEFAULT '',
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- One tentative entry per user per run (switching roles replaces it).
CREATE UNIQUE INDEX IF NOT EXISTS premade_tentative_run_user_idx ON premade_tentative (run_id, discord_user_id);

-- Listing a run's tentative players in a stable order.
CREATE INDEX IF NOT EXISTS premade_tentative_run_role_idx ON premade_tentative (run_id, role, created_at);
