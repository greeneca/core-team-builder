-- Migration 050: capture the Discord global name on RSVPs
--
-- Idempotent. The ✅/❌ attendance buttons (discord_rsvps, 028_discord_rsvps.sql)
-- stored only a single captured name (the responder's display name). The post's
-- inline green/red status marks match each RSVP back to a roster slot by the
-- player's discord_handle, which may be set to the user's *username* (distinct
-- from their display/global name). With only the display name stored, a handle
-- set to the username never matched, so a coming player got no ✅ — even though
-- the "Get My Build Details" button (which uses the live user object) matched.
--
-- Storing the username (in discord_username) AND the global name lets the match
-- mirror the live user across all handle forms (id/mention, username, global
-- name). Existing rows keep their old captured name until the user re-RSVPs.

ALTER TABLE discord_rsvps
    ADD COLUMN IF NOT EXISTS discord_global_name TEXT NOT NULL DEFAULT '';
