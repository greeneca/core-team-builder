-- Migration 021: password reset tokens
--
-- Idempotent. Backs the "forgot password" flow. A reset token is an opaque
-- random string emailed to the user; only its SHA-256 hash is stored here, so a
-- database read does not yield a usable token. Rows are single-use (consumed by
-- setting used_at) and time-limited (expires_at), and cascade with the user.

CREATE TABLE IF NOT EXISTS password_resets (
    id         BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id    BIGINT      NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    -- Hex-encoded SHA-256 of the opaque reset token.
    token_hash TEXT        NOT NULL UNIQUE,
    expires_at TIMESTAMPTZ NOT NULL,
    used_at    TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Fast lookup of a presented token and of all tokens for a user.
CREATE INDEX IF NOT EXISTS password_resets_token_hash_idx ON password_resets (token_hash);
CREATE INDEX IF NOT EXISTS password_resets_user_id_idx ON password_resets (user_id);
