-- Migration 055: pre-made run cleanup backoff + give-up bookkeeping
--
-- Idempotent. The bot's scheduler deletes a run's post + thread ~2 h after the
-- run. When the thread can't be removed (almost always a missing Manage Threads
-- permission), cleanup used to be retried on every 60 s tick forever. These
-- columns let the scheduler back off exponentially between retries and, once the
-- attempt cap is reached, mark cleanup permanently failed so it stops revisiting
-- the run instead of retry-storming indefinitely.
--
--   * cleanup_attempts  — count of failed cleanup attempts so far (drives backoff).
--   * cleanup_next_at    — earliest time the next retry is allowed; NULL = now.
--   * cleanup_failed_at  — set when retries are exhausted; excludes the run from
--                          the due-cleanup scan (cleaned_up_at stays NULL because
--                          the post/thread may still exist).

ALTER TABLE premade_runs
    ADD COLUMN IF NOT EXISTS cleanup_attempts  INTEGER     NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS cleanup_next_at   TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS cleanup_failed_at TIMESTAMPTZ;
