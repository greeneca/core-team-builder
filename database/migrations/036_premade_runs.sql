-- Migration 036: pre-made trial runs + per-slot signups
--
-- Idempotent. Backs the Discord bot's /coreteam premade flow.
--
--   * premade_runs    — one row per posted run. The row also tracks the bot's
--     time-based actions (thread creation 15 min before; cleanup 2 h after) so
--     they survive bot restarts and can be caught up on if the bot was offline
--     at the trigger time. scheduled_at is stored in UTC.
--   * premade_signups — one row per claimed roster slot. A UNIQUE on
--     (run_id, slot) locks a slot to one claimant; a UNIQUE on
--     (run_id, discord_user_id) enforces one slot per user (switching releases
--     the prior claim).

CREATE TABLE IF NOT EXISTS premade_runs (
    id                BIGINT      GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    team_id           BIGINT      NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    guild_id          TEXT        NOT NULL DEFAULT '',
    channel_id        TEXT        NOT NULL DEFAULT '',
    -- Set once the announcement message is posted.
    message_id        TEXT        NOT NULL DEFAULT '',
    -- Set when the 15-minutes-before thread is created.
    thread_id         TEXT        NOT NULL DEFAULT '',
    title             TEXT        NOT NULL DEFAULT '',
    -- The trial start time, in UTC (converted from the runner's timezone).
    scheduled_at      TIMESTAMPTZ NOT NULL,
    -- App user (users.id) who ran the command.
    created_by        BIGINT      REFERENCES users(id) ON DELETE SET NULL,
    -- Scheduler bookkeeping: NULL until the corresponding action has run.
    thread_started_at TIMESTAMPTZ,
    cleaned_up_at     TIMESTAMPTZ,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- The scheduler scans for due runs by scheduled_at; index it.
CREATE INDEX IF NOT EXISTS premade_runs_scheduled_at_idx ON premade_runs (scheduled_at);

CREATE TABLE IF NOT EXISTS premade_signups (
    id               BIGINT      GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    run_id           BIGINT      NOT NULL REFERENCES premade_runs(id) ON DELETE CASCADE,
    slot             SMALLINT    NOT NULL CHECK (slot BETWEEN 1 AND 12),
    discord_user_id  TEXT        NOT NULL,
    discord_username TEXT        NOT NULL DEFAULT '',
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- One claimant per slot (locks the slot) and one slot per claimant (switching
-- releases the prior claim in the same transaction).
CREATE UNIQUE INDEX IF NOT EXISTS premade_signups_run_slot_idx ON premade_signups (run_id, slot);
CREATE UNIQUE INDEX IF NOT EXISTS premade_signups_run_user_idx ON premade_signups (run_id, discord_user_id);

-- Keep updated_at current on premade_runs updates (reuses the shared trigger fn
-- defined in 001_init.sql).
DROP TRIGGER IF EXISTS premade_runs_set_updated_at ON premade_runs;
CREATE TRIGGER premade_runs_set_updated_at
    BEFORE UPDATE ON premade_runs
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();
