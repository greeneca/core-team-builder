-- Migration 015: admin users + application settings
--
-- Idempotent. Adds an is_admin flag to users (admins can manage other users and
-- toggle registration) and a key/value app_settings table holding global config.
-- Registration is enabled by default; admins can disable it from the UI.

ALTER TABLE users
    ADD COLUMN IF NOT EXISTS is_admin BOOLEAN NOT NULL DEFAULT false;

CREATE TABLE IF NOT EXISTS app_settings (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);

INSERT INTO app_settings (key, value)
VALUES ('registration_enabled', 'true')
ON CONFLICT (key) DO NOTHING;
