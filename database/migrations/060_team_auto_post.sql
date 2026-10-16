-- Migration 060: auto post and auto delete for /coreteam post overviews
--
-- Idempotent. Adds the settings and bookkeeping the bot's scheduler needs to
--   * auto post: post the team's run overview in a chosen bound channel a set
--     time before the next run, and
--   * auto delete: delete a posted overview (and its thread) a set time after
--     the run.
--
-- Settings live in their own table (one row per team, created on first save)
-- rather than as more columns on teams, so the long positional team queries are
-- untouched. Durations are stored in minutes; the web UI shows them as hours or
-- days. The CHECK bounds back up the handler-side validation.

-- 1. A display name for each bound channel, so the Configure page can offer a
-- dropdown of readable names. Set by the bot on /coreteam setup/bind and
-- backfilled at startup for older bindings; '' until then.
ALTER TABLE discord_channels
    ADD COLUMN IF NOT EXISTS channel_name TEXT NOT NULL DEFAULT '';

-- 2. Per-team auto post / auto delete settings.
CREATE TABLE IF NOT EXISTS team_auto_post (
    team_id BIGINT PRIMARY KEY REFERENCES teams(id) ON DELETE CASCADE,
    auto_post_enabled BOOLEAN NOT NULL DEFAULT FALSE,
    -- A channel bound to this team (checked on save, and again by the scheduler
    -- against discord_channels so a re-bound channel is never posted to).
    auto_post_channel_id TEXT REFERENCES discord_channels(channel_id) ON DELETE SET NULL,
    -- How long before the next run to post: 1 hour .. 30 days.
    auto_post_lead_minutes INTEGER NOT NULL DEFAULT 1440
        CHECK (auto_post_lead_minutes BETWEEN 60 AND 43200),
    -- The run instant the last auto post was for, so each run posts once.
    auto_posted_run_at TIMESTAMPTZ,
    auto_delete_enabled BOOLEAN NOT NULL DEFAULT FALSE,
    -- How long after the run to delete the post: 1 hour .. 30 days.
    auto_delete_delay_minutes INTEGER NOT NULL DEFAULT 120
        CHECK (auto_delete_delay_minutes BETWEEN 60 AND 43200),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- 3. Marks a tracked post as auto-deleted (once-only, restart-safe).
ALTER TABLE discord_posts
    ADD COLUMN IF NOT EXISTS auto_deleted_at TIMESTAMPTZ;

CREATE INDEX IF NOT EXISTS discord_posts_auto_delete_idx
    ON discord_posts (run_at) WHERE auto_deleted_at IS NULL AND run_at IS NOT NULL;
