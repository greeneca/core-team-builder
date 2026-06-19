-- Migration 042: per-team customizable roster roles
--
-- Idempotent. Adds a per-team set of roster roles so a team can add or remove
-- the roles its players can be assigned (the roster role picker reads this list
-- instead of a fixed global one). Each role is an object {key, label, base}: key
-- is the stable value stored on players.role, label is the display name, and
-- base is the color category (one of tank/healer/dps/support_dps) that drives
-- the roster's role color coding so a custom role still gets a known color.
--
-- Existing teams default to the historical fixed composition (Tank, Healer, DPS,
-- Support DPS), so rosters and validation keep working unchanged.

ALTER TABLE teams
    ADD COLUMN IF NOT EXISTS roles JSONB NOT NULL
        DEFAULT '[{"key": "tank", "label": "Tank", "base": "tank"}, {"key": "healer", "label": "Healer", "base": "healer"}, {"key": "dps", "label": "DPS", "base": "dps"}, {"key": "support_dps", "label": "Support DPS", "base": "support_dps"}]'::jsonb;

ALTER TABLE teams
    ALTER COLUMN roles SET DEFAULT '[{"key": "tank", "label": "Tank", "base": "tank"}, {"key": "healer", "label": "Healer", "base": "healer"}, {"key": "dps", "label": "DPS", "base": "dps"}, {"key": "support_dps", "label": "Support DPS", "base": "support_dps"}]'::jsonb;

-- Backfill: ensure every stored role object carries a "base" color category.
-- Idempotent — only rebuilds rows that still have a role missing "base". A role
-- whose key is itself a known base maps to that color; anything else falls back
-- to "dps".
UPDATE teams t
SET roles = sub.new_roles
FROM (
    SELECT teams.id,
           jsonb_agg(
               CASE
                   WHEN elem ? 'base' THEN elem
                   ELSE elem || jsonb_build_object(
                       'base',
                       CASE
                           WHEN elem->>'key' IN ('tank', 'healer', 'dps', 'support_dps')
                               THEN elem->>'key'
                           ELSE 'dps'
                       END
                   )
               END
               ORDER BY ord
           ) AS new_roles
    FROM teams,
         LATERAL jsonb_array_elements(teams.roles) WITH ORDINALITY AS r(elem, ord)
    GROUP BY teams.id
) sub
WHERE t.id = sub.id
  AND EXISTS (
      SELECT 1
      FROM jsonb_array_elements(t.roles) AS e
      WHERE NOT (e ? 'base')
  );
