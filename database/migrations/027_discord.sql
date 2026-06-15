-- Migration 027: Discord bot integration
--
-- Idempotent. Adds the schema the Discord bot needs:
--   * users.discord_user_id / discord_username — links an app account to a
--     Discord identity (set when the user runs /coreteam link with a one-time
--     code). Nullable; a user may never link.
--   * discord_link_codes — short-lived, single-use codes generated in the web
--     UI and consumed by /coreteam link. Only the SHA-256 hash is stored, so a
--     database read yields no usable code (mirrors password_resets).
--   * discord_channels — binds a Discord channel to a core team, so /coreteam
--     post knows which team's roster to render.

-- 1. Account link fields on users.
ALTER TABLE users
    ADD COLUMN IF NOT EXISTS discord_user_id  TEXT,
    ADD COLUMN IF NOT EXISTS discord_username TEXT NOT NULL DEFAULT '';

-- One app account per Discord identity (allow many unlinked rows = NULL).
CREATE UNIQUE INDEX IF NOT EXISTS users_discord_user_id_key
    ON users (discord_user_id)
    WHERE discord_user_id IS NOT NULL;

-- 2. One-time link codes (hashed, single-use, time-limited).
CREATE TABLE IF NOT EXISTS discord_link_codes (
    id         BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id    BIGINT      NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    -- Hex-encoded SHA-256 of the opaque link code.
    code_hash  TEXT        NOT NULL UNIQUE,
    expires_at TIMESTAMPTZ NOT NULL,
    used_at    TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS discord_link_codes_user_id_idx ON discord_link_codes (user_id);

-- 3. Channel -> team bindings. A channel maps to exactly one team; binding again
-- updates the team (upsert on channel_id).
CREATE TABLE IF NOT EXISTS discord_channels (
    guild_id       TEXT        NOT NULL,
    channel_id     TEXT        PRIMARY KEY,
    team_id        BIGINT      NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    set_by_user_id BIGINT      REFERENCES users(id) ON DELETE SET NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS discord_channels_team_id_idx ON discord_channels (team_id);
