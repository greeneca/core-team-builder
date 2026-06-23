-- Migration 048: rosters layer between teams and their players/encounters/groupings
--
-- Idempotent. Introduces a `rosters` table that sits between a team and its
-- composition. Previously players, encounters, and groupings were keyed directly
-- by team_id; now they belong to a roster, and a team can have several rosters
-- with exactly one designated "active" roster (teams.active_roster_id). The
-- Discord bot always uses the active roster; the web app can edit any roster and
-- create/rename/delete/activate them.
--
-- Existing teams are backfilled with a single "Main" roster that owns all their
-- current players/encounters/groupings and is set active, so behavior is
-- unchanged for data created before this migration.

-- 1. The rosters table.
CREATE TABLE IF NOT EXISTS rosters (
    id         BIGINT       GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    team_id    BIGINT       NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    name       VARCHAR(100) NOT NULL DEFAULT 'Main',
    position   INT          NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ  NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_rosters_team ON rosters(team_id);

DROP TRIGGER IF EXISTS rosters_set_updated_at ON rosters;
CREATE TRIGGER rosters_set_updated_at
    BEFORE UPDATE ON rosters
    FOR EACH ROW
    EXECUTE FUNCTION set_updated_at();

-- 2. The team's pointer to its active roster (nullable to break the
-- chicken/egg; the application always keeps it set).
ALTER TABLE teams
    ADD COLUMN IF NOT EXISTS active_roster_id BIGINT REFERENCES rosters(id) ON DELETE SET NULL;

-- 3. Backfill one "Main" roster per existing team that has none.
INSERT INTO rosters (team_id, name, position)
SELECT t.id, 'Main', 0
FROM teams t
WHERE NOT EXISTS (SELECT 1 FROM rosters r WHERE r.team_id = t.id);

-- Point every team at its (single, backfilled) roster if not already set.
UPDATE teams t
SET active_roster_id = r.id
FROM rosters r
WHERE r.team_id = t.id AND t.active_roster_id IS NULL;

-- 4. Move players from team_id to roster_id.
ALTER TABLE players
    ADD COLUMN IF NOT EXISTS roster_id BIGINT REFERENCES rosters(id) ON DELETE CASCADE;

DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_name = 'players' AND column_name = 'team_id'
    ) THEN
        -- Move every existing player into its team's active "Main" roster (set
        -- above). Joining through teams.active_roster_id keeps this unambiguous
        -- even if a team somehow already has more than one roster, so no legacy
        -- player is lost or attached to the wrong roster.
        UPDATE players p
        SET roster_id = t.active_roster_id
        FROM teams t
        WHERE t.id = p.team_id
          AND p.roster_id IS NULL
          AND t.active_roster_id IS NOT NULL;

        ALTER TABLE players ALTER COLUMN roster_id SET NOT NULL;
        -- Dropping team_id also drops its UNIQUE (team_id, slot) constraint.
        ALTER TABLE players DROP COLUMN team_id;
    END IF;
END $$;

CREATE UNIQUE INDEX IF NOT EXISTS players_roster_slot_key ON players(roster_id, slot);

-- 5. Move encounters from team_id to roster_id.
ALTER TABLE encounters
    ADD COLUMN IF NOT EXISTS roster_id BIGINT REFERENCES rosters(id) ON DELETE CASCADE;

DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_name = 'encounters' AND column_name = 'team_id'
    ) THEN
        UPDATE encounters e
        SET roster_id = t.active_roster_id
        FROM teams t
        WHERE t.id = e.team_id
          AND e.roster_id IS NULL
          AND t.active_roster_id IS NOT NULL;

        ALTER TABLE encounters ALTER COLUMN roster_id SET NOT NULL;
        ALTER TABLE encounters DROP COLUMN team_id;
    END IF;
END $$;

-- Replace the old team-keyed indexes (the (team_id, name) unique index is
-- dropped with the column above; guard anyway for re-runs).
DROP INDEX IF EXISTS idx_encounters_team;
DROP INDEX IF EXISTS idx_encounters_team_name;
CREATE INDEX IF NOT EXISTS idx_encounters_roster ON encounters(roster_id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_encounters_roster_name ON encounters(roster_id, name);

-- 6. Move groupings from team_id to roster_id.
ALTER TABLE groupings
    ADD COLUMN IF NOT EXISTS roster_id BIGINT REFERENCES rosters(id) ON DELETE CASCADE;

DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_name = 'groupings' AND column_name = 'team_id'
    ) THEN
        UPDATE groupings g
        SET roster_id = t.active_roster_id
        FROM teams t
        WHERE t.id = g.team_id
          AND g.roster_id IS NULL
          AND t.active_roster_id IS NOT NULL;

        ALTER TABLE groupings ALTER COLUMN roster_id SET NOT NULL;
        ALTER TABLE groupings DROP COLUMN team_id;
    END IF;
END $$;

DROP INDEX IF EXISTS idx_groupings_team;
CREATE INDEX IF NOT EXISTS idx_groupings_roster ON groupings(roster_id);

-- 7. Rewrite the realtime change-notify function so it still resolves a team id
-- for every collaborative table now that players/encounters/groupings hang off
-- rosters. players/encounters/groupings resolve team_id via the rosters table;
-- loadouts and grouping_* resolve it via their parent → rosters → teams. A new
-- rosters trigger emits a 'team' change so the web app refetches when a roster is
-- added/renamed/removed or the active roster changes.
CREATE OR REPLACE FUNCTION notify_team_change()
RETURNS TRIGGER AS $$
DECLARE
    kind TEXT := TG_ARGV[0];
    tid  BIGINT;
BEGIN
    IF TG_OP = 'DELETE' THEN
        CASE TG_TABLE_NAME
            WHEN 'teams'               THEN tid := OLD.id;
            WHEN 'rosters'             THEN tid := OLD.team_id;
            WHEN 'players'             THEN
                SELECT r.team_id INTO tid FROM rosters r WHERE r.id = OLD.roster_id;
            WHEN 'encounters'          THEN
                SELECT r.team_id INTO tid FROM rosters r WHERE r.id = OLD.roster_id;
            WHEN 'groupings'           THEN
                SELECT r.team_id INTO tid FROM rosters r WHERE r.id = OLD.roster_id;
            WHEN 'team_members'        THEN tid := OLD.team_id;
            WHEN 'team_roster_members' THEN tid := OLD.team_id;
            WHEN 'encounter_loadouts'  THEN
                SELECT r.team_id INTO tid
                FROM encounters e JOIN rosters r ON r.id = e.roster_id
                WHERE e.id = OLD.encounter_id;
            WHEN 'grouping_groups'     THEN
                SELECT r.team_id INTO tid
                FROM groupings g JOIN rosters r ON r.id = g.roster_id
                WHERE g.id = OLD.grouping_id;
            WHEN 'grouping_members'    THEN
                SELECT r.team_id INTO tid
                FROM groupings g JOIN rosters r ON r.id = g.roster_id
                WHERE g.id = OLD.grouping_id;
            ELSE tid := NULL;
        END CASE;
    ELSE
        CASE TG_TABLE_NAME
            WHEN 'teams'               THEN tid := NEW.id;
            WHEN 'rosters'             THEN tid := NEW.team_id;
            WHEN 'players'             THEN
                SELECT r.team_id INTO tid FROM rosters r WHERE r.id = NEW.roster_id;
            WHEN 'encounters'          THEN
                SELECT r.team_id INTO tid FROM rosters r WHERE r.id = NEW.roster_id;
            WHEN 'groupings'           THEN
                SELECT r.team_id INTO tid FROM rosters r WHERE r.id = NEW.roster_id;
            WHEN 'team_members'        THEN tid := NEW.team_id;
            WHEN 'team_roster_members' THEN tid := NEW.team_id;
            WHEN 'encounter_loadouts'  THEN
                SELECT r.team_id INTO tid
                FROM encounters e JOIN rosters r ON r.id = e.roster_id
                WHERE e.id = NEW.encounter_id;
            WHEN 'grouping_groups'     THEN
                SELECT r.team_id INTO tid
                FROM groupings g JOIN rosters r ON r.id = g.roster_id
                WHERE g.id = NEW.grouping_id;
            WHEN 'grouping_members'    THEN
                SELECT r.team_id INTO tid
                FROM groupings g JOIN rosters r ON r.id = g.roster_id
                WHERE g.id = NEW.grouping_id;
            ELSE tid := NULL;
        END CASE;
    END IF;

    IF tid IS NOT NULL THEN
        PERFORM pg_notify('team_changed',
            json_build_object('team_id', tid, 'kind', kind)::text);
    END IF;
    RETURN NULL;
END;
$$ LANGUAGE plpgsql;

-- Emit a 'team' change whenever a roster row changes (add/rename/delete or an
-- active-roster switch via teams update — the latter fires teams_notify_change).
DROP TRIGGER IF EXISTS rosters_notify_change ON rosters;
CREATE TRIGGER rosters_notify_change
    AFTER INSERT OR UPDATE OR DELETE ON rosters
    FOR EACH ROW EXECUTE FUNCTION notify_team_change('team');
