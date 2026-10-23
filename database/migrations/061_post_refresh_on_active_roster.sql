-- Migration 061: refresh upcoming /coreteam posts when the active roster changes
--
-- Idempotent. A posted overview renders the team's ACTIVE roster, so switching
-- the active roster (or deleting the active one, which promotes another) leaves
-- already-posted overviews showing the old lineup. This marks every tracked post
-- that has not started yet for a refresh; the bot's scheduler re-renders them in
-- place and clears the mark. A trigger on teams.active_roster_id catches every
-- path that changes it (activate, delete-promotion) without each having to
-- remember.
--
-- Posts are tied to a team through their channel's binding
-- (discord_channels.team_id), the same join the auto delete pass uses.

ALTER TABLE discord_posts
    ADD COLUMN IF NOT EXISTS refresh_requested_at TIMESTAMPTZ;

CREATE INDEX IF NOT EXISTS discord_posts_refresh_idx
    ON discord_posts (run_at) WHERE refresh_requested_at IS NOT NULL;

CREATE OR REPLACE FUNCTION mark_posts_for_refresh() RETURNS trigger AS $$
BEGIN
    UPDATE discord_posts
       SET refresh_requested_at = now()
     WHERE run_at > now()
       AND auto_deleted_at IS NULL
       AND channel_id IN (SELECT channel_id FROM discord_channels WHERE team_id = NEW.id);
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS teams_active_roster_refresh_posts ON teams;
CREATE TRIGGER teams_active_roster_refresh_posts
    AFTER UPDATE OF active_roster_id ON teams
    FOR EACH ROW
    WHEN (OLD.active_roster_id IS DISTINCT FROM NEW.active_roster_id)
    EXECUTE FUNCTION mark_posts_for_refresh();
