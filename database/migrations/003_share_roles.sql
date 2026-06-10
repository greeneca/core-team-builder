-- Migration 003: viewer/editor share roles
--
-- Idempotent. Team members can now be shared as a 'viewer' (read-only) or
-- 'editor' (can edit the roster). The 'owner' role is unchanged and is only
-- assigned at team creation.
--
-- Previously, every shared member had role 'member' with full edit rights, so
-- existing rows are migrated to 'editor' to preserve their current access.

UPDATE team_members SET role = 'editor' WHERE role = 'member';

-- Default new shares to 'editor' (matches prior behaviour); the API always sets
-- an explicit role, this default only guards direct inserts.
ALTER TABLE team_members ALTER COLUMN role SET DEFAULT 'editor';
