-- Migration 041: edit a posted signup run via DM
--
-- Idempotent. Lets the "Edit" button on a posted run reuse the DM conversation
-- machinery to change a run's title, time, or description. The existing
-- per-user DM session (premade_signup_sessions) gains two columns so the same
-- row can drive an edit conversation instead of a create one:
--
--   * mode   — '' (default) for the create flow, 'edit' when editing a run.
--   * run_id — the run being edited (NULL for the create flow). ON DELETE
--     CASCADE so a cleaned-up run drops any dangling edit session.

ALTER TABLE premade_signup_sessions
    ADD COLUMN IF NOT EXISTS mode TEXT NOT NULL DEFAULT '';

ALTER TABLE premade_signup_sessions
    ADD COLUMN IF NOT EXISTS run_id BIGINT REFERENCES premade_runs(id) ON DELETE CASCADE;
