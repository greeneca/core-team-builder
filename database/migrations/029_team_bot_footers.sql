-- Migration 029: repurpose the Discord export text fields as bot footers
--
-- Idempotent. The web app's Discord text export (detailed post / condensed list)
-- was removed. Its two free-form text columns are repurposed as footers the
-- Discord bot appends to its output:
--   * signup_note     -> post_footer  (appended to the /coreteam post overview)
--   * detailed_header -> dm_footer    (appended to the Get My Build Details DM)
--
-- Renames are guarded so re-running is safe; the ADD COLUMN fallbacks cover any
-- database that never had the old columns. The "both columns exist" branches
-- reconcile databases left in a mixed state by an earlier interrupted run: keep
-- the new column, fold in any non-empty legacy value, then drop the legacy one.

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM information_schema.columns
               WHERE table_name = 'teams' AND column_name = 'signup_note') THEN
        IF EXISTS (SELECT 1 FROM information_schema.columns
                   WHERE table_name = 'teams' AND column_name = 'post_footer') THEN
            UPDATE teams SET post_footer = signup_note
                WHERE post_footer = '' AND signup_note <> '';
            ALTER TABLE teams DROP COLUMN signup_note;
        ELSE
            ALTER TABLE teams RENAME COLUMN signup_note TO post_footer;
        END IF;
    END IF;
    IF EXISTS (SELECT 1 FROM information_schema.columns
               WHERE table_name = 'teams' AND column_name = 'detailed_header') THEN
        IF EXISTS (SELECT 1 FROM information_schema.columns
                   WHERE table_name = 'teams' AND column_name = 'dm_footer') THEN
            UPDATE teams SET dm_footer = detailed_header
                WHERE dm_footer = '' AND detailed_header <> '';
            ALTER TABLE teams DROP COLUMN detailed_header;
        ELSE
            ALTER TABLE teams RENAME COLUMN detailed_header TO dm_footer;
        END IF;
    END IF;
END$$;

ALTER TABLE teams
    ADD COLUMN IF NOT EXISTS post_footer TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS dm_footer   TEXT NOT NULL DEFAULT '';
