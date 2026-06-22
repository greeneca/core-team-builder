-- Migration 046: trial-post fill signups
--
-- Idempotent. Backs the signup dropdown on a /coreteam post trial overview.
-- Players can sign up to fill a roster slot that has no Discord handle set (an
-- "open" slot), or join a general "fill list" (slot 0). Each row is one Discord
-- user's signup for a specific posted message, so a user holds at most one
-- signup per post (upsert on (message_id, discord_user_id)) — picking a new slot
-- or the fill list replaces their prior choice. Keyed by message_id (not the
-- channel/team) so re-posting starts a fresh set, mirroring discord_rsvps.
--
-- slot semantics: 0 = the general fill list (many users allowed); > 0 = a
-- specific open roster slot (at most one user, enforced by the partial unique
-- index below).

CREATE TABLE IF NOT EXISTS discord_post_fills (
    -- The posted overview message the signup dropdown lives on.
    message_id       TEXT        NOT NULL,
    -- The channel the message is in (kept for housekeeping / scoping).
    channel_id       TEXT        NOT NULL,
    -- 0 = general fill list; > 0 = a specific open roster slot number.
    slot             INT         NOT NULL DEFAULT 0,
    discord_user_id  TEXT        NOT NULL,
    -- Display name captured at signup time (shown in the roster as plain text).
    discord_username TEXT        NOT NULL DEFAULT '',
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (message_id, discord_user_id)
);

-- A specific slot can be filled by at most one user per post; the fill list
-- (slot 0) is excluded so it can hold many users.
CREATE UNIQUE INDEX IF NOT EXISTS discord_post_fills_slot_idx
    ON discord_post_fills (message_id, slot) WHERE slot <> 0;

CREATE INDEX IF NOT EXISTS discord_post_fills_message_idx ON discord_post_fills (message_id);
