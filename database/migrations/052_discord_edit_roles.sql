-- Migration 052: per-guild "edit roles" for restricted run buttons
--
-- Idempotent. Replaces the old app-account gate on a signup run's restricted
-- buttons (Edit run / Delete run, and the actions inside the Edit DM) with a
-- Discord-native rule. A button press is allowed when the presser is:
--
--   * the original poster (matched by Discord ID, no linked web account needed),
--   * a member holding a role designated here for the run's guild, or
--   * a Discord server admin (Administrator or Manage Server).
--
--   * discord_edit_roles — the set of Discord role IDs, per guild, whose holders
--     may use the restricted run buttons. Managed via /coreteam permissions
--     (add/remove/list). Scoped per guild_id so each server keeps its own list.

CREATE TABLE IF NOT EXISTS discord_edit_roles (
    guild_id   TEXT        NOT NULL,
    role_id    TEXT        NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (guild_id, role_id)
);

-- The edit DM flow reuses premade_signup_sessions. A role-holding editor may not
-- have a linked web account, so app_user_id must be allowed to be NULL (it was
-- NOT NULL when only linked creators/editors could open the flow).
ALTER TABLE premade_signup_sessions
    ALTER COLUMN app_user_id DROP NOT NULL;
