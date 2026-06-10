-- Migration 007: encounters + per-player loadouts
--
-- Idempotent. Each team has one or more encounters (a boss/trash/Default fight).
-- Every encounter has 12 loadout rows (one per player slot), each holding a
-- free-form ordered list of gear sets and skills.
--
-- A team always has at least one encounter ("Default"); new teams create it in
-- the backend, and this migration backfills existing teams.

CREATE TABLE IF NOT EXISTS encounters (
    id         BIGINT       GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    team_id    BIGINT       NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    name       VARCHAR(100) NOT NULL DEFAULT 'Default',
    position   INT          NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ  NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_encounters_team ON encounters(team_id);

DROP TRIGGER IF EXISTS encounters_set_updated_at ON encounters;
CREATE TRIGGER encounters_set_updated_at
    BEFORE UPDATE ON encounters
    FOR EACH ROW
    EXECUTE FUNCTION set_updated_at();

-- One loadout per (encounter, player slot). gear/skills are ordered lists of
-- canonical keys (validated/looked up against the frontend master data).
CREATE TABLE IF NOT EXISTS encounter_loadouts (
    encounter_id BIGINT   NOT NULL REFERENCES encounters(id) ON DELETE CASCADE,
    slot         SMALLINT NOT NULL CHECK (slot BETWEEN 1 AND 12),
    gear         TEXT[]   NOT NULL DEFAULT '{}',
    skills       TEXT[]   NOT NULL DEFAULT '{}',
    PRIMARY KEY (encounter_id, slot)
);

-- Backfill: ensure every existing team has a Default encounter with 12 slots.
DO $$
DECLARE
    t   RECORD;
    eid BIGINT;
    s   INT;
BEGIN
    FOR t IN SELECT id FROM teams LOOP
        IF NOT EXISTS (SELECT 1 FROM encounters WHERE team_id = t.id) THEN
            INSERT INTO encounters (team_id, name, position)
            VALUES (t.id, 'Default', 0)
            RETURNING id INTO eid;

            FOR s IN 1..12 LOOP
                INSERT INTO encounter_loadouts (encounter_id, slot) VALUES (eid, s);
            END LOOP;
        END IF;
    END LOOP;
END $$;
