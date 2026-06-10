-- Migration 005: team schedule timezone
--
-- Idempotent. Stores the IANA timezone the trial time is expressed in
-- (e.g. "America/New_York"), '' when unset. Validated in the backend via
-- time.LoadLocation so only real zone names are accepted.

ALTER TABLE teams
    ADD COLUMN IF NOT EXISTS schedule_timezone VARCHAR(64) NOT NULL DEFAULT '';
