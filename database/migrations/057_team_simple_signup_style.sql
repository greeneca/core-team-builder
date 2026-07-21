-- Migration 057: simple-signup visual style
--
-- Idempotent. Adds a per-team setting that controls how the pre-made run post
-- presents SIMPLE (role-based) signups (only meaningful when pre_made and
-- simple_signup are on). Advanced/specific signup is unaffected.
--
--   * 'dropdown'  (default) — the current layout: one consolidated signup
--     dropdown listing each role.
--   * 'buttons'             — one color-coded button per role (tank/healer/dps),
--     plus a separate "Maybe" (tentative) button.
--   * 'ephemeral'           — a single green "Sign up" button that opens the
--     consolidated signup dropdown privately (ephemerally) for the presser,
--     instead of showing the dropdown on the post.
--
-- Any other value normalizes to 'dropdown' in the app (the column is a free TEXT
-- value; validation lives in models.NormalizeSimpleSignupStyle). Defaults to
-- 'dropdown' so existing teams keep their current appearance.

ALTER TABLE teams
    ADD COLUMN IF NOT EXISTS simple_signup_style TEXT NOT NULL DEFAULT 'dropdown';

ALTER TABLE teams
    ALTER COLUMN simple_signup_style SET DEFAULT 'dropdown';
