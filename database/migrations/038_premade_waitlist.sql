-- Migration 038: pre-made per-role waitlist
--
-- Idempotent. Adds an optional waitlist to pre-made trial runs:
--
--   * teams.waitlist_enabled — per-team toggle (only meaningful with pre_made).
--     When on, players can join a per-ROLE waitlist for a run; when a slot of
--     that role frees up, the head of that role's waitlist is auto-promoted into
--     it and DM'd. Defaults to disabled.
--   * premade_waitlist — one row per waiting user per run. role is the role they
--     want; created_at orders the queue (FIFO). A UNIQUE on
--     (run_id, discord_user_id) enforces one waitlist entry per user (switching
--     roles replaces the entry and resets queue position).

ALTER TABLE teams
    ADD COLUMN IF NOT EXISTS waitlist_enabled BOOLEAN NOT NULL DEFAULT false;

ALTER TABLE teams
    ALTER COLUMN waitlist_enabled SET DEFAULT false;

CREATE TABLE IF NOT EXISTS premade_waitlist (
    id               BIGINT      GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    run_id           BIGINT      NOT NULL REFERENCES premade_runs(id) ON DELETE CASCADE,
    role             TEXT        NOT NULL DEFAULT '',
    discord_user_id  TEXT        NOT NULL,
    discord_username TEXT        NOT NULL DEFAULT '',
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- One waitlist entry per user per run (switching roles replaces it).
CREATE UNIQUE INDEX IF NOT EXISTS premade_waitlist_run_user_idx ON premade_waitlist (run_id, discord_user_id);

-- Promotion scans the head of a given role's queue (FIFO) for a run.
CREATE INDEX IF NOT EXISTS premade_waitlist_run_role_idx ON premade_waitlist (run_id, role, created_at);
