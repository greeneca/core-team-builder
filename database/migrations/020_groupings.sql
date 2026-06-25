-- Migration 020: team groupings
--
-- Idempotent. A grouping splits a team's roster into a set of numbered groups
-- (e.g. "ice cages" or "slayer stacks"). A team may have several groupings; each
-- grouping has a name and a group count. Each numbered group has an optional
-- name (defaults to "Group N" in the UI when blank) and any number of players.
-- A player may belong to at most one group per grouping — enforced by the
-- grouping_members primary key (grouping_id, player_slot).

CREATE TABLE IF NOT EXISTS groupings (
    id          BIGINT       GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    team_id     BIGINT       NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    name        VARCHAR(100) NOT NULL DEFAULT 'Grouping',
    group_count INT          NOT NULL DEFAULT 2 CHECK (group_count BETWEEN 1 AND 12),
    position    INT          NOT NULL DEFAULT 0,
    created_at  TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ  NOT NULL DEFAULT now()
);

-- Guarded: migration 048 re-keys groupings to roster_id and drops team_id (and
-- this index). On a re-run over an already-migrated DB the column is gone, so
-- only (re)create the team-keyed index while team_id still exists.
DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_name = 'groupings' AND column_name = 'team_id'
    ) THEN
        CREATE INDEX IF NOT EXISTS idx_groupings_team ON groupings(team_id);
    END IF;
END $$;

DROP TRIGGER IF EXISTS groupings_set_updated_at ON groupings;
CREATE TRIGGER groupings_set_updated_at
    BEFORE UPDATE ON groupings
    FOR EACH ROW
    EXECUTE FUNCTION set_updated_at();

-- Per-numbered-group name (blank = use the default "Group N" label in the UI).
CREATE TABLE IF NOT EXISTS grouping_groups (
    grouping_id  BIGINT      NOT NULL REFERENCES groupings(id) ON DELETE CASCADE,
    group_number INT         NOT NULL CHECK (group_number BETWEEN 1 AND 12),
    name         VARCHAR(50) NOT NULL DEFAULT '',
    PRIMARY KEY (grouping_id, group_number)
);

-- A player's membership in a grouping. The (grouping_id, player_slot) primary
-- key guarantees a player is in at most one group per grouping.
CREATE TABLE IF NOT EXISTS grouping_members (
    grouping_id  BIGINT   NOT NULL REFERENCES groupings(id) ON DELETE CASCADE,
    group_number INT      NOT NULL CHECK (group_number BETWEEN 1 AND 12),
    player_slot  SMALLINT NOT NULL CHECK (player_slot BETWEEN 1 AND 12),
    PRIMARY KEY (grouping_id, player_slot)
);

CREATE INDEX IF NOT EXISTS idx_grouping_members_grouping ON grouping_members(grouping_id);
