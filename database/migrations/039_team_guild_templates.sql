-- Migration 039: publish signup templates to a Discord server
--
-- Idempotent. Lets a template (a pre_made team) be made available to everyone in
-- a Discord guild without sharing edit access to the team. When a template is
-- published to a guild, any linked member of that guild can run /coreteam signup
-- from it.
--
--   * team_guild_templates — one row per (template, guild) grant. published_by is
--     the app user (users.id) who published it. The team must be pre_made; this
--     is enforced in application code (a non-template team simply won't appear in
--     the publish picker or the runnable list).

CREATE TABLE IF NOT EXISTS team_guild_templates (
    team_id      BIGINT      NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    guild_id     TEXT        NOT NULL,
    published_by BIGINT      REFERENCES users(id) ON DELETE SET NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (team_id, guild_id)
);

-- The runnable-templates lookup scans by guild.
CREATE INDEX IF NOT EXISTS team_guild_templates_guild_idx ON team_guild_templates (guild_id);
