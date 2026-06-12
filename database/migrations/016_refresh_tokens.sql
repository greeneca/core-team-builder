-- Migration 016: refresh tokens
--
-- Idempotent. Backs the short-lived access token / long-lived refresh token
-- scheme. Access tokens are stateless JWTs; refresh tokens are opaque random
-- strings stored here ONLY as a SHA-256 hash (never in plaintext) so a database
-- read does not yield usable credentials. Rows support rotation and explicit
-- revocation (logout / "sign out everywhere").

CREATE TABLE IF NOT EXISTS refresh_tokens (
    id         BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id    BIGINT      NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    -- Hex-encoded SHA-256 of the opaque refresh token.
    token_hash TEXT        NOT NULL UNIQUE,
    expires_at TIMESTAMPTZ NOT NULL,
    revoked_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Fast lookup of a presented token and of all tokens for a user (revoke-all).
CREATE INDEX IF NOT EXISTS refresh_tokens_token_hash_idx ON refresh_tokens (token_hash);
CREATE INDEX IF NOT EXISTS refresh_tokens_user_id_idx ON refresh_tokens (user_id);
