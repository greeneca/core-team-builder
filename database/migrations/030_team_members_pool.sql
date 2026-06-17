-- Migration 030: team member pool + signup (interest) post text
--
-- Idempotent. Adds the concept of "roster members": a per-team pool of people
-- (separate from the 12 fixed player slots and from team_members app-account
-- sharing) who expressed interest via the Discord bot's /coreteam signup post.
-- The bot gathers each interested user's availability, roles, and classes
-- through an interactive DM flow and stores it here. The web app visualizes the
-- pool and can assign a member into a player slot.
--
--   * teams.signup_post     -> free-form text the bot posts with /coreteam signup
--   * team_roster_members   -> the gathered availability/role/class pool

ALTER TABLE teams
    ADD COLUMN IF NOT EXISTS signup_post TEXT NOT NULL DEFAULT '';

CREATE TABLE IF NOT EXISTS team_roster_members (
    id               BIGINT      GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    team_id          BIGINT      NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    -- Discord identity for bot-sourced members (NULL for manual web entries).
    discord_user_id  TEXT,
    discord_username TEXT        NOT NULL DEFAULT '',
    -- Display name shown on the web page.
    display_name     TEXT        NOT NULL DEFAULT '',
    -- IANA timezone the availability hours are expressed in.
    timezone         TEXT        NOT NULL DEFAULT '',
    -- Days of the week the member is available (mon..sun).
    days             TEXT[]      NOT NULL DEFAULT '{}',
    -- Per-day availability windows: { "mon": { "start": 18, "end": 22 }, ... }
    -- where start/end are hours (0-23) in `timezone`.
    availability     JSONB       NOT NULL DEFAULT '{}'::jsonb,
    -- Roles the member is comfortable playing (tank/healer/dps).
    roles            TEXT[]      NOT NULL DEFAULT '{}',
    -- Classes per role: { "tank": ["dragonknight"], "healer": [...] }.
    classes_by_role  JSONB       NOT NULL DEFAULT '{}'::jsonb,
    -- 'draft' while the DM intake is in progress, 'complete' once finished.
    status           TEXT        NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'complete')),
    -- Current intake step for the bot's DM flow (days/timezone/times/roles/classes/done).
    step             TEXT        NOT NULL DEFAULT '',
    -- 'discord' (gathered by the bot) or 'manual' (added in the web app).
    source           TEXT        NOT NULL DEFAULT 'discord' CHECK (source IN ('discord', 'manual')),
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- One Discord identity maps to at most one member per team (re-running signup
-- updates the same row). Manual entries (NULL discord_user_id) are unconstrained.
CREATE UNIQUE INDEX IF NOT EXISTS team_roster_members_team_discord_idx
    ON team_roster_members (team_id, discord_user_id)
    WHERE discord_user_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS team_roster_members_team_idx
    ON team_roster_members (team_id);

DROP TRIGGER IF EXISTS set_team_roster_members_updated_at ON team_roster_members;
CREATE TRIGGER set_team_roster_members_updated_at
    BEFORE UPDATE ON team_roster_members
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();
