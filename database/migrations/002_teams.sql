-- Migration 002: teams, sharing, and players
--
-- Idempotent (like 001) so it is safe to apply from both the Postgres
-- docker-entrypoint init process and the seed command on every run.
--
-- A team is owned by one user, can be shared with other users, and always has
-- exactly 12 player slots. Player role/class values are validated in the
-- backend; the columns stay flexible (and default to '') so a slot can be empty.

CREATE TABLE IF NOT EXISTS teams (
    id         BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name       VARCHAR(100) NOT NULL,
    owner_id   BIGINT       NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ  NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_teams_owner ON teams(owner_id);

DROP TRIGGER IF EXISTS teams_set_updated_at ON teams;
CREATE TRIGGER teams_set_updated_at
    BEFORE UPDATE ON teams
    FOR EACH ROW
    EXECUTE FUNCTION set_updated_at();

-- Team membership / sharing. The owner is also recorded here (role 'owner') so
-- access checks are a single membership lookup.
CREATE TABLE IF NOT EXISTS team_members (
    team_id  BIGINT      NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    user_id  BIGINT      NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role     VARCHAR(20) NOT NULL DEFAULT 'member',
    added_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (team_id, user_id)
);

CREATE INDEX IF NOT EXISTS idx_team_members_user ON team_members(user_id);

-- Players occupy one of 12 slots on a team. Empty slots have blank fields.
CREATE TABLE IF NOT EXISTS players (
    id             BIGINT      GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    team_id        BIGINT      NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    slot           SMALLINT    NOT NULL CHECK (slot BETWEEN 1 AND 12),
    name           VARCHAR(100) NOT NULL DEFAULT '',
    discord_handle VARCHAR(100) NOT NULL DEFAULT '',
    -- role:  '' | tank | healer | dps | support_dps
    role           VARCHAR(20)  NOT NULL DEFAULT '',
    -- class: '' | arcanist | dragonknight | necromancer | nightblade | sorcerer | templar | warden
    class          VARCHAR(30)  NOT NULL DEFAULT '',
    created_at     TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ  NOT NULL DEFAULT now(),
    UNIQUE (team_id, slot)
);

DROP TRIGGER IF EXISTS players_set_updated_at ON players;
CREATE TRIGGER players_set_updated_at
    BEFORE UPDATE ON players
    FOR EACH ROW
    EXECUTE FUNCTION set_updated_at();
