-- Migration 028: Discord trial RSVPs
--
-- Idempotent. Backs the ✅/❌ attendance buttons on a posted trial overview.
-- Each row is one Discord user's response to a specific posted message, so a
-- user has at most one RSVP per post (upsert on (message_id, discord_user_id)).
-- Keyed by message_id (not the channel/team) so re-posting starts a fresh tally.

CREATE TABLE IF NOT EXISTS discord_rsvps (
    -- The posted overview message the buttons live on.
    message_id       TEXT        NOT NULL,
    -- The channel the message is in (kept for housekeeping / scoping).
    channel_id       TEXT        NOT NULL,
    discord_user_id  TEXT        NOT NULL,
    -- Display name captured at RSVP time (mentions use the ID; this is a fallback).
    discord_username TEXT        NOT NULL DEFAULT '',
    -- 'yes' (coming) or 'no' (not coming).
    status           TEXT        NOT NULL CHECK (status IN ('yes', 'no')),
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (message_id, discord_user_id)
);

CREATE INDEX IF NOT EXISTS discord_rsvps_message_id_idx ON discord_rsvps (message_id);
