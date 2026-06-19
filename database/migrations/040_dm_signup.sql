-- Migration 040: DM-driven /coreteam signup conversation
--
-- Idempotent. Reworks the pre-made signup flow into a free-text DM conversation:
--
--   * users.timezone — a remembered per-user IANA timezone. The DM signup flow
--     asks for it once (via a select menu), stores it here, and reuses it
--     silently afterwards so natural-language times like "tomorrow at 10pm" can
--     be resolved without re-asking. Empty until first set.
--   * premade_runs.post_override — an optional per-run body that overrides the
--     team's default premade post body (teams.premade_post). Empty means "use
--     the team default".
--   * premade_signup_sessions — one in-progress DM conversation per Discord user
--     (keyed by discord_user_id). It survives bot restarts so a half-finished
--     signup can be resumed. A new /coreteam signup overwrites any prior session.

ALTER TABLE users
    ADD COLUMN IF NOT EXISTS timezone TEXT NOT NULL DEFAULT '';

ALTER TABLE premade_runs
    ADD COLUMN IF NOT EXISTS post_override TEXT NOT NULL DEFAULT '';

CREATE TABLE IF NOT EXISTS premade_signup_sessions (
    discord_user_id TEXT        PRIMARY KEY,
    app_user_id     BIGINT      NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    team_id         BIGINT      REFERENCES teams(id) ON DELETE CASCADE,
    guild_id        TEXT        NOT NULL DEFAULT '',
    channel_id      TEXT        NOT NULL DEFAULT '',
    dm_channel_id   TEXT        NOT NULL DEFAULT '',
    step            TEXT        NOT NULL DEFAULT '',
    title           TEXT        NOT NULL DEFAULT '',
    scheduled_at    TIMESTAMPTZ,
    post_override   TEXT        NOT NULL DEFAULT '',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
