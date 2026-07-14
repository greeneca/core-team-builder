-- Migration 056: per-roster fight-positioning images
--
-- Idempotent. Positioning reference images (boss room diagrams, stack points,
-- etc.) uploaded in the web app and posted by the Discord bot into a pre-made
-- run's discussion thread so players know where to stand during fights.
--
-- Images belong to a roster (the bot always uses the team's active roster) and
-- are stored inline as BYTEA: the backend container has no persistent writable
-- volume, while Postgres already persists via the db-data volume. Sizes are
-- small (screenshots, capped at a few MB each and ~10 per roster).
--
--   * position     — display / post order within the roster.
--   * caption       — optional per-image label (e.g. "HRC — boss 3 stacks").
--   * content_type  — MIME type, used for the HTTP response and Discord upload.
--   * byte_size     — stored so listings show size without reading the blob.
--   * data           — the raw image bytes.

CREATE TABLE IF NOT EXISTS roster_images (
    id           BIGINT       GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    roster_id    BIGINT       NOT NULL REFERENCES rosters(id) ON DELETE CASCADE,
    position     INT          NOT NULL DEFAULT 0,
    caption      VARCHAR(200) NOT NULL DEFAULT '',
    content_type TEXT         NOT NULL,
    byte_size    INT          NOT NULL DEFAULT 0,
    data         BYTEA        NOT NULL,
    created_at   TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ  NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_roster_images_roster ON roster_images(roster_id);
