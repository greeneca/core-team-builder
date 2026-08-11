-- Migration 058: per-guild action log channel
--
-- Idempotent. Records the channel a server has designated as its action log:
-- the bot mirrors every signup-related interaction (sign up, switch, un-sign,
-- waitlist, tentative, RSVP, fill, and editor changes) there as a short entry
-- naming the post (linked to it), the action, and who did it.
--
--   * discord_action_log_channels — one row per guild (guild_id is the primary
--     key), so a server logs to exactly one channel. Absent row = logging off.
--     Managed via /coreteam actionlog (set/off/status).

CREATE TABLE IF NOT EXISTS discord_action_log_channels (
    guild_id               TEXT        PRIMARY KEY,
    channel_id             TEXT        NOT NULL,
    set_by_discord_user_id TEXT        NOT NULL DEFAULT '',
    updated_at             TIMESTAMPTZ NOT NULL DEFAULT now()
);
