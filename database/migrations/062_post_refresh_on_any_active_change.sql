-- Migration 062: refresh upcoming /coreteam posts on ANY change to the active
-- roster or the team
--
-- Idempotent. Migration 061 marked upcoming posts for refresh only when the
-- active roster *switched*. A post also shows the active roster's players, its
-- primary encounter, and its groupings, so an edit to any of those must refresh
-- it too. This replaces 061's narrow trigger with:
--
--   * teams: any real change to the team row (name, schedule, roles, the active
--     roster pointer, ...) ignoring updated_at alone, and
--   * rosters / players / encounters / encounter_loadouts / groupings /
--     grouping_groups / grouping_members: any change, but only when the row
--     belongs to the team's ACTIVE roster (edits to a roster that isn't posted
--     don't touch any post).
--
-- Only posts whose run has not started are marked (run_at > now()); the bot's
-- scheduler re-renders them and clears the mark (see 061 and
-- DiscordStore.DuePostRefreshes).

-- The team trigger from 061 only watched active_roster_id; replace it.
DROP TRIGGER IF EXISTS teams_active_roster_refresh_posts ON teams;
DROP TRIGGER IF EXISTS teams_refresh_posts ON teams;
CREATE TRIGGER teams_refresh_posts
    AFTER UPDATE ON teams
    FOR EACH ROW
    WHEN ((to_jsonb(OLD) - 'updated_at') IS DISTINCT FROM (to_jsonb(NEW) - 'updated_at'))
    EXECUTE FUNCTION mark_posts_for_refresh();

-- Marks the team's upcoming posts when a roster-scoped row of the ACTIVE roster
-- changes. The roster is resolved per table like notify_team_change() does; a
-- parent that is already gone (a cascade from a delete) resolves to NULL and
-- marks nothing, which is right: the delete's own row-level trigger fires first.
CREATE OR REPLACE FUNCTION mark_posts_for_refresh_by_roster() RETURNS trigger AS $$
DECLARE
    rid BIGINT;
BEGIN
    IF TG_OP = 'DELETE' THEN
        CASE TG_TABLE_NAME
            WHEN 'rosters'             THEN rid := OLD.id;
            WHEN 'players'             THEN rid := OLD.roster_id;
            WHEN 'encounters'          THEN rid := OLD.roster_id;
            WHEN 'groupings'           THEN rid := OLD.roster_id;
            WHEN 'encounter_loadouts'  THEN
                SELECT e.roster_id INTO rid FROM encounters e WHERE e.id = OLD.encounter_id;
            WHEN 'grouping_groups'     THEN
                SELECT g.roster_id INTO rid FROM groupings g WHERE g.id = OLD.grouping_id;
            WHEN 'grouping_members'    THEN
                SELECT g.roster_id INTO rid FROM groupings g WHERE g.id = OLD.grouping_id;
            ELSE rid := NULL;
        END CASE;
    ELSE
        CASE TG_TABLE_NAME
            WHEN 'rosters'             THEN rid := NEW.id;
            WHEN 'players'             THEN rid := NEW.roster_id;
            WHEN 'encounters'          THEN rid := NEW.roster_id;
            WHEN 'groupings'           THEN rid := NEW.roster_id;
            WHEN 'encounter_loadouts'  THEN
                SELECT e.roster_id INTO rid FROM encounters e WHERE e.id = NEW.encounter_id;
            WHEN 'grouping_groups'     THEN
                SELECT g.roster_id INTO rid FROM groupings g WHERE g.id = NEW.grouping_id;
            WHEN 'grouping_members'    THEN
                SELECT g.roster_id INTO rid FROM groupings g WHERE g.id = NEW.grouping_id;
            ELSE rid := NULL;
        END CASE;
    END IF;

    IF rid IS NOT NULL THEN
        UPDATE discord_posts
           SET refresh_requested_at = now()
         WHERE run_at > now()
           AND auto_deleted_at IS NULL
           AND channel_id IN (
                SELECT c.channel_id
                  FROM rosters r
                  JOIN teams t ON t.id = r.team_id AND t.active_roster_id = r.id
                  JOIN discord_channels c ON c.team_id = t.id
                 WHERE r.id = rid);
    END IF;
    RETURN NULL;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS rosters_refresh_posts ON rosters;
CREATE TRIGGER rosters_refresh_posts
    AFTER INSERT OR UPDATE OR DELETE ON rosters
    FOR EACH ROW EXECUTE FUNCTION mark_posts_for_refresh_by_roster();

DROP TRIGGER IF EXISTS players_refresh_posts ON players;
CREATE TRIGGER players_refresh_posts
    AFTER INSERT OR UPDATE OR DELETE ON players
    FOR EACH ROW EXECUTE FUNCTION mark_posts_for_refresh_by_roster();

DROP TRIGGER IF EXISTS encounters_refresh_posts ON encounters;
CREATE TRIGGER encounters_refresh_posts
    AFTER INSERT OR UPDATE OR DELETE ON encounters
    FOR EACH ROW EXECUTE FUNCTION mark_posts_for_refresh_by_roster();

DROP TRIGGER IF EXISTS encounter_loadouts_refresh_posts ON encounter_loadouts;
CREATE TRIGGER encounter_loadouts_refresh_posts
    AFTER INSERT OR UPDATE OR DELETE ON encounter_loadouts
    FOR EACH ROW EXECUTE FUNCTION mark_posts_for_refresh_by_roster();

DROP TRIGGER IF EXISTS groupings_refresh_posts ON groupings;
CREATE TRIGGER groupings_refresh_posts
    AFTER INSERT OR UPDATE OR DELETE ON groupings
    FOR EACH ROW EXECUTE FUNCTION mark_posts_for_refresh_by_roster();

DROP TRIGGER IF EXISTS grouping_groups_refresh_posts ON grouping_groups;
CREATE TRIGGER grouping_groups_refresh_posts
    AFTER INSERT OR UPDATE OR DELETE ON grouping_groups
    FOR EACH ROW EXECUTE FUNCTION mark_posts_for_refresh_by_roster();

DROP TRIGGER IF EXISTS grouping_members_refresh_posts ON grouping_members;
CREATE TRIGGER grouping_members_refresh_posts
    AFTER INSERT OR UPDATE OR DELETE ON grouping_members
    FOR EACH ROW EXECUTE FUNCTION mark_posts_for_refresh_by_roster();
