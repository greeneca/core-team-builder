-- Migration 044: realtime collaboration support
--
-- Idempotent. Adds two things that let several editors work on a team at once:
--
--  1. A pg_notify-based change feed. A trigger function NOTIFYs the
--     'team_changed' channel with the affected team id and a coarse "kind" on
--     every insert/update/delete to the collaborative tables. The API server
--     LISTENs on this channel and fans the change out to connected browsers over
--     Server-Sent Events, so a change by one user (or the Discord bot) refreshes
--     everyone else's view. The payload deliberately omits row-level detail, so
--     Postgres collapses the many per-row notifications of a bulk save (e.g. a
--     12-row roster update) into a single delivered notification per kind.
--
--  2. An updated_at column on encounter_loadouts so a single loadout slot has its
--     own optimistic-concurrency token (mirrors players.updated_at), used to
--     reject a stale per-slot save with a 409 instead of silently clobbering a
--     concurrent edit.

-- Per-slot loadout version token.
ALTER TABLE encounter_loadouts
    ADD COLUMN IF NOT EXISTS updated_at TIMESTAMPTZ NOT NULL DEFAULT now();

DROP TRIGGER IF EXISTS encounter_loadouts_set_updated_at ON encounter_loadouts;
CREATE TRIGGER encounter_loadouts_set_updated_at
    BEFORE UPDATE ON encounter_loadouts
    FOR EACH ROW
    EXECUTE FUNCTION set_updated_at();

-- notify_team_change emits a 'team_changed' notification carrying the affected
-- team id and the coarse change kind passed as the trigger argument
-- (TG_ARGV[0]). It resolves the team id from whichever table fired it. Keeping
-- the payload row-agnostic lets Postgres de-duplicate the per-row notifications
-- of a multi-row statement within one transaction.
CREATE OR REPLACE FUNCTION notify_team_change()
RETURNS TRIGGER AS $$
DECLARE
    kind TEXT := TG_ARGV[0];
    tid  BIGINT;
BEGIN
    IF TG_OP = 'DELETE' THEN
        CASE TG_TABLE_NAME
            WHEN 'teams'               THEN tid := OLD.id;
            WHEN 'players'             THEN tid := OLD.team_id;
            WHEN 'encounters'          THEN tid := OLD.team_id;
            WHEN 'groupings'           THEN tid := OLD.team_id;
            WHEN 'team_members'        THEN tid := OLD.team_id;
            WHEN 'team_roster_members' THEN tid := OLD.team_id;
            WHEN 'encounter_loadouts'  THEN
                SELECT e.team_id INTO tid FROM encounters e WHERE e.id = OLD.encounter_id;
            WHEN 'grouping_groups'     THEN
                SELECT g.team_id INTO tid FROM groupings g WHERE g.id = OLD.grouping_id;
            WHEN 'grouping_members'    THEN
                SELECT g.team_id INTO tid FROM groupings g WHERE g.id = OLD.grouping_id;
            ELSE tid := NULL;
        END CASE;
    ELSE
        CASE TG_TABLE_NAME
            WHEN 'teams'               THEN tid := NEW.id;
            WHEN 'players'             THEN tid := NEW.team_id;
            WHEN 'encounters'          THEN tid := NEW.team_id;
            WHEN 'groupings'           THEN tid := NEW.team_id;
            WHEN 'team_members'        THEN tid := NEW.team_id;
            WHEN 'team_roster_members' THEN tid := NEW.team_id;
            WHEN 'encounter_loadouts'  THEN
                SELECT e.team_id INTO tid FROM encounters e WHERE e.id = NEW.encounter_id;
            WHEN 'grouping_groups'     THEN
                SELECT g.team_id INTO tid FROM groupings g WHERE g.id = NEW.grouping_id;
            WHEN 'grouping_members'    THEN
                SELECT g.team_id INTO tid FROM groupings g WHERE g.id = NEW.grouping_id;
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

-- Per-table change triggers. The kind groups related tables so a client only has
-- to refresh the affected area (team/encounter/grouping/members/pool).
DROP TRIGGER IF EXISTS teams_notify_change ON teams;
CREATE TRIGGER teams_notify_change
    AFTER INSERT OR UPDATE OR DELETE ON teams
    FOR EACH ROW EXECUTE FUNCTION notify_team_change('team');

DROP TRIGGER IF EXISTS players_notify_change ON players;
CREATE TRIGGER players_notify_change
    AFTER INSERT OR UPDATE OR DELETE ON players
    FOR EACH ROW EXECUTE FUNCTION notify_team_change('team');

DROP TRIGGER IF EXISTS encounters_notify_change ON encounters;
CREATE TRIGGER encounters_notify_change
    AFTER INSERT OR UPDATE OR DELETE ON encounters
    FOR EACH ROW EXECUTE FUNCTION notify_team_change('encounter');

DROP TRIGGER IF EXISTS encounter_loadouts_notify_change ON encounter_loadouts;
CREATE TRIGGER encounter_loadouts_notify_change
    AFTER INSERT OR UPDATE OR DELETE ON encounter_loadouts
    FOR EACH ROW EXECUTE FUNCTION notify_team_change('encounter');

DROP TRIGGER IF EXISTS groupings_notify_change ON groupings;
CREATE TRIGGER groupings_notify_change
    AFTER INSERT OR UPDATE OR DELETE ON groupings
    FOR EACH ROW EXECUTE FUNCTION notify_team_change('grouping');

DROP TRIGGER IF EXISTS grouping_groups_notify_change ON grouping_groups;
CREATE TRIGGER grouping_groups_notify_change
    AFTER INSERT OR UPDATE OR DELETE ON grouping_groups
    FOR EACH ROW EXECUTE FUNCTION notify_team_change('grouping');

DROP TRIGGER IF EXISTS grouping_members_notify_change ON grouping_members;
CREATE TRIGGER grouping_members_notify_change
    AFTER INSERT OR UPDATE OR DELETE ON grouping_members
    FOR EACH ROW EXECUTE FUNCTION notify_team_change('grouping');

DROP TRIGGER IF EXISTS team_members_notify_change ON team_members;
CREATE TRIGGER team_members_notify_change
    AFTER INSERT OR UPDATE OR DELETE ON team_members
    FOR EACH ROW EXECUTE FUNCTION notify_team_change('members');

DROP TRIGGER IF EXISTS team_roster_members_notify_change ON team_roster_members;
CREATE TRIGGER team_roster_members_notify_change
    AFTER INSERT OR UPDATE OR DELETE ON team_roster_members
    FOR EACH ROW EXECUTE FUNCTION notify_team_change('pool');
