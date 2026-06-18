# Development Guide

## Prerequisites

- Docker + Docker Compose (primary workflow)
- Go 1.25+ (optional, for running/building the backend outside Docker)

## Running with Docker (recommended)

```bash
cp .env.example .env          # then edit values as needed
docker compose up --build     # starts db, backend, seed, frontend
```

- Frontend: <http://localhost:8081>
- The `seed` service runs once on startup. Re-run it any time with:

```bash
docker compose run --rm seed
```

Tear down (and wipe the database volume):

```bash
docker compose down -v
```

## Environment variables

All configuration is via environment variables (see `.env.example`).

| Variable          | Used by   | Description                                        |
|-------------------|-----------|----------------------------------------------------|
| `POSTGRES_USER`   | db/backend| Database user                                      |
| `POSTGRES_PASSWORD`| db/backend| Database password                                 |
| `POSTGRES_DB`     | db/backend| Database name                                      |
| `DATABASE_URL`    | backend   | Full connection string (composed in compose)       |
| `HTTP_ADDR`       | backend   | Listen address (default `:8080`)                   |
| `JWT_SECRET`      | backend   | **Required (≥ 32 bytes).** Signing secret for tokens |
| `JWT_TTL`         | backend   | Access-token lifetime (default `15m`)              |
| `REFRESH_TTL`     | backend   | Refresh-token lifetime (default `720h` = 30 days)  |
| `CORS_ORIGIN`     | backend   | Allowed browser origin                             |
| `APP_BASE_URL`    | backend   | Public site URL for email links (default `CORS_ORIGIN`) |
| `PASSWORD_RESET_TTL` | backend| Reset-link lifetime (default `1h`)                 |
| `SMTP_HOST`       | backend   | SMTP relay host; empty → reset emails are logged (dev) |
| `SMTP_PORT`       | backend   | SMTP port (default `587`; `465` = implicit TLS)    |
| `SMTP_USERNAME`   | backend   | SMTP auth username (empty → no auth)               |
| `SMTP_PASSWORD`   | backend   | SMTP auth password                                 |
| `SMTP_FROM`       | backend   | From address, e.g. `Name <noreply@example.com>`    |
| `FRONTEND_PORT`   | frontend  | Host port for the site                             |
| `SEED_USERNAME`   | seed      | **Required.** Test user username                   |
| `SEED_EMAIL`      | seed      | **Required.** Test user email                      |
| `SEED_PASSWORD`   | seed      | **Required.** Test user password                   |

Seed credentials have no hardcoded defaults — they must be supplied via the
environment so they stay isolated in `.env`. The seeder never logs the plaintext
password.

Generate a strong secret:

```bash
openssl rand -base64 48
```

## Running the backend without Docker

You still need a Postgres instance. Point `DATABASE_URL` at it:

```bash
cd backend
export DATABASE_URL="postgres://ctb:change-me-in-prod@localhost:5432/core_team_builder?sslmode=disable"
export JWT_SECRET="dev-only-insecure-secret-change-me"
export MIGRATIONS_DIR="../database/migrations"

# Seed credentials are required (no defaults):
export SEED_USERNAME="testuser"
export SEED_EMAIL="test@example.com"
export SEED_PASSWORD="changeme123"

go run ./cmd/seed      # apply schema + create test user
go run ./cmd/server    # start the API on :8080
```

Build binaries:

```bash
cd backend
go build ./...
go vet ./...
```

## API reference

Base path: `/api`. All bodies are JSON.

### `GET /api/health`

Returns `{ "status": "ok" }`.

### `GET /api/registration-status`

Public. Returns `{ "enabled": true|false }` so the login page can hide the
Register tab when an admin has disabled self-registration.

### `POST /api/register`

Request:

```json
{ "username": "alice", "email": "alice@example.com", "password": "supersecret" }
```

Response `200`:

```json
{ "token": "<access-jwt>", "refresh_token": "<opaque>", "expires_in": 900, "user": { "id": 1, "username": "alice", "email": "alice@example.com", "is_admin": true, "created_at": "...", "updated_at": "..." } }
```

`token` is a short-lived access JWT (`expires_in` seconds); `refresh_token` is a
long-lived opaque token used with `POST /api/refresh` (see below).

The **first account ever registered** bootstraps the system: it is always
allowed and becomes an admin (`is_admin: true`). After that, registration is
gated by the `registration_enabled` setting — when an admin has disabled it,
this returns `403`.

Errors: `400` (validation / password too short), `403` (registration disabled),
`409` (username or email taken).

### `POST /api/login`

Request:

```json
{ "username": "alice", "password": "supersecret" }
```

Response `200`: same shape as register. Errors: `401` (invalid credentials).

### `POST /api/refresh`

Request `{ "refresh_token": "<opaque>" }`. Rotates the token pair (single-use):
consumes/revokes the presented refresh token and returns a fresh
`{ token, refresh_token, expires_in, user }`. Errors: `400` (missing
`refresh_token`), `401` (expired/revoked/unknown refresh token).

### `POST /api/logout`

Request `{ "refresh_token": "<opaque>" }`. Revokes the presented refresh token so
it can no longer be used to refresh. The access token remains valid until it
expires (stateless). Idempotent — always returns `204`.

### `POST /api/forgot-password`

Request `{ "email": "<email>" }`. **Always** returns `200` with a generic
`{ "message": ... }` regardless of whether the email is registered (no account
enumeration). When it matches a user, the backend invalidates that user's prior
reset tokens, issues a new single-use token, and emails a link to
`<APP_BASE_URL>/reset.html?token=…`. In dev (no `SMTP_HOST`) the email — link
included — is written to the backend log. Errors: `400` (missing `email`).

### `POST /api/reset-password`

Request `{ "token": "<from email>", "password": "<new>" }`. Validates the new
password against the policy, consumes the token (single-use), updates the hash,
and revokes all of the user's refresh tokens (sign-out-everywhere). Returns `200`
with a confirmation message. Errors: `400` (missing fields, weak password, or an
invalid/expired/used token).

### `GET /api/me` (protected)

Header: `Authorization: Bearer <jwt>`. Returns the current `user` (including
`is_admin`). Errors: `401`.

### Admin (all protected, admin-only)

Require `Authorization: Bearer <jwt>` **and** `is_admin = true` on the caller;
non-admins get `403`. Used by the topbar "Manage Users" modal.

| Method & path                        | Description                                            |
|--------------------------------------|--------------------------------------------------------|
| `GET /api/admin/users`               | List all users (`{ "users": [...] }`).                 |
| `POST /api/admin/users`              | Create a user `{ username, email, password, is_admin }` → `201` (bypasses the registration toggle). Errors `400`/`409`. |
| `DELETE /api/admin/users/{id}`       | Remove a user (cascades to their owned teams). Cannot delete yourself or the last admin (`400`). |
| `PUT /api/admin/users/{id}/admin`    | Set/clear the admin flag `{ "is_admin": true|false }`. Cannot demote the last admin (`400`). |
| `GET /api/admin/settings`            | Read `{ "registration_enabled": true|false }`.         |
| `PUT /api/admin/settings`            | Update `{ "registration_enabled": true|false }`.       |

### Teams (all protected)

All routes require `Authorization: Bearer <jwt>`. A team is accessible to its
owner and any user it has been shared with; inaccessible teams return `404`.
Mutating endpoints return the full refreshed team (with `players` and `members`).

| Method & path                              | Who      | Description                          |
|--------------------------------------------|----------|--------------------------------------|
| `GET /api/teams`                           | member   | List teams you own or that are shared with you (`{ "teams": [...] }`). |
| `POST /api/teams`                          | any user | Create a team `{ "name": "...", "copy_from"?: <teamID> }` → `201`. Without `copy_from`: 12 empty slots + a `Default` encounter. With `copy_from` (a team you can access): copies its schedule, roster, and encounters/loadouts (never its sharing). |
| `GET /api/teams/{id}`                      | viewer+  | Get one team with `players` + `members`. |
| `PUT /api/teams/{id}`                      | editor+  | Save everything: `{ name, schedule_days, schedule_time, encounters_enabled, post_footer, dm_footer, signup_post, players }`. |
| `DELETE /api/teams/{id}`                   | owner    | Delete the team (cascades).          |
| `POST /api/teams/{id}/share`              | owner    | Share/update role `{ "username": "...", "role": "viewer"\|"editor" }` (role defaults to `editor`; upsert). |
| `DELETE /api/teams/{id}/members/{userID}` | owner    | Revoke a member's access.            |

Roles: `owner` and `editor` can edit the team/roster; `viewer` is read-only
(edit attempts return `403`). Only the owner can delete or manage sharing.

`PUT /api/teams/{id}` body (the UI autosaves all of this together; see Autosave
in `docs/AGENT_CONTEXT.md`):

```json
{
  "name": "Sunday Trial Core",
  "schedule_days": ["mon", "wed"],
  "schedule_time": "00:00",
  "encounters_enabled": false,
  "post_footer": "Voice: Discord. Loot: need-before-greed.",
  "dm_footer": "Double-check your CP and potions before the run.",
  "signup_post": "Recruiting for our Sunday vet trial core — press below to sign up.",
  "players": [
    {
      "slot": 1, "name": "Aedric", "discord_handle": "aedric#1234",
      "role": "tank", "class": "dragonknight",
      "subclassed": true,
      "skill_line_1": "ardent_flame", "skill_line_2": "draconic_power", "skill_line_3": "grave_lord",
      "mastery_1": "", "mastery_2": ""
    }
  ]
}
```

- `schedule_days` ⊆ `mon,tue,wed,thu,fri,sat,sun` (validated, de-duped, ordered).
- `schedule_time` is `""` or `"HH:MM"` (24h) **in UTC**; anything else returns
  `400`. There is no manual timezone picker — the UI converts the time from the
  viewer's current zone to UTC before saving and back to the viewer's zone on
  load, so each viewer sees the time in their own zone.
- `encounters_enabled` (bool, default `false`) toggles whether the UI surfaces
  the multi-encounter section; the team always keeps ≥1 encounter regardless.
- `post_footer` / `dm_footer` are free-form text (≤2000 runes each, trailing
  whitespace trimmed; over-length returns `400`) the Discord bot appends to its
  output — `post_footer` to the `/coreteam post` overview, `dm_footer` to the
  "Get My Build Details" DM.
- `signup_post` is free-form text (same limits) the Discord bot posts as the body
  of its `/coreteam recruit` recruitment embed.
- `players` is optional; omitted slots are left unchanged. Invalid slot/role/
  class/skill-line/mastery returns `400` and the whole save is rolled back.

Player slot body:

```json
{
  "name": "Aedric", "discord_handle": "aedric#1234", "role": "tank", "class": "dragonknight",
  "subclassed": false, "mastery_1": "booming_voice", "mastery_2": "rousing_roar"
}
```

- `role` ∈ `""`, `tank`, `healer`, `dps`, `support_dps`.
- `class` ∈ `""`, `arcanist`, `dragonknight`, `necromancer`, `nightblade`,
  `sorcerer`, `templar`, `warden`.
- `subclassed` (bool) selects which build set applies:
  - `true` → `skill_line_1..3`, each one of the 21 class skill lines (or `""`).
  - `false` → `mastery_1..2`, each one of the **selected class's** 5 masteries
    (or `""`).
  - The backend validates only the active set and blanks the inactive one, so a
    player never has both skill lines and masteries stored.
- Subclass skill-line rules (`models.ValidateSkillLines`, enforced on save):
  - selected skill lines must be **unique**;
  - if `class` is set **and at least one line is chosen**, **at least one**
    selected line must be from that class (a fully-empty subclass build is
    allowed);
  - if `class` is set, **at most one** selected line may come from any single
    other class. Class checks are skipped when `class` is `""`. Violations
    return `400` (the UI pre-checks and names the offending slot).

Invalid `role`/`class`/`skill_line_*`/`mastery_*` values return `400`.

### Encounters (all protected)

Nested under a team; the same access rules apply (viewer reads; editor/owner
edits). Mutations return the refreshed encounter with its 12 loadouts.

| Method & path                                          | Who     | Description                                |
|--------------------------------------------------------|---------|--------------------------------------------|
| `GET /api/teams/{id}/encounters`                       | viewer+ | List a team's encounters (`{ "encounters": [...] }`, no loadouts). |
| `POST /api/teams/{id}/encounters`                      | editor+ | Add an encounter `{ "name": "...", "copy_from"?: <encounterID> }` → `201`; creates 12 loadouts (empty, or copied slot-for-slot from `copy_from` in the same team). |
| `GET /api/teams/{id}/encounters/{eid}`                 | viewer+ | Get one encounter with its 12 `loadouts`.  |
| `PUT /api/teams/{id}/encounters/{eid}`                 | editor+ | Rename `{ "name": "..." }`.                |
| `DELETE /api/teams/{id}/encounters/{eid}`              | editor+ | Delete (cannot delete the team's last one).|
| `PUT /api/teams/{id}/encounters/{eid}/loadouts`        | editor+ | Save loadouts (see body below).            |

Every team always has at least one encounter (`Default`), created with the team.
`name` must be `Default`, `Trash`, or an ESO trial boss (`ValidEncounterNames`).
On create/rename the name is also checked by `ValidateEncounterSelection`: names
are **unique** per team, and all non-`General` encounters must come from a
**single trial** (the `General` group — `Default`/`Trash` — is always allowed
alongside one trial). Violations return `400`.

`PUT .../loadouts` body:

```json
{
  "loadouts": [
    {
      "slot": 1,
      "gear": ["perfected_relequen", "slimecraw"],
      "skills": ["pragmatic_fatecarver"],
      "potions": ["spell_power_potion"],
      "cp_blue": ["fighting_finesse", "backstabber"],
      "crit_dmg": ["dual_wield_double"],
      "mundus": "the_shadow",
      "armor_heavy": 0, "armor_medium": 5, "armor_light": 2,
      "pen_extra": ["sharpened"],
      "catalyst_elements": 3, "weapon_damage": 0,
      "splintered_secrets_skills": 2, "force_of_nature_status": 5,
      "scribed_buffs": ["minor_courage"], "banner_bearer_focus": "mitigation"
    }
  ]
}
```

- `gear`/`skills`/`potions`/`cp_blue`/`crit_dmg`/`pen_extra`/`scribed_buffs` are
  ordered, free-form key lists (the UI constrains choices to the master data in
  `frontend/js/data.js`). The backend sanitizes them (trim, drop empties, ≤100
  chars each, ≤30 items) but does not allow-list the keys. Stackable `pen_extra`
  sources (e.g. `set_piece_bonuses`, up to 5) are stored as the key repeated once
  per stack — duplicates are intentional and summed by the penetration calculator.
- `mundus` and `banner_bearer_focus` are trimmed strings (≤100 chars);
  `armor_heavy/medium/light` are integers clamped to `0–7`. `catalyst_elements`
  clamps to `1–3` (default 3), `weapon_damage` to `0–20000`,
  `splintered_secrets_skills` to `0–5` (default 2) — the Arcanist Splintered
  Secrets passive's slotted Herald of the Tome ability count (1240 pen each) —
  and `force_of_nature_status` to `0–5` (default 5) — negative status effects on
  the enemy for the Force of Nature blue CP star (660 pen each).
- `scribed_buffs` are the group buffs a player's scribed (grimoire) skill
  provides; they count toward the client-side Group Buffs coverage.
  `banner_bearer_focus` records the Banner Bearer grimoire's chosen Focus Script
  and is informational only (surfaced in the UI and Discord export; feeds no
  calculation).
- The crit/penetration inputs (`cp_blue`, `crit_dmg`, `mundus`, armor counts,
  `pen_extra`) feed the client-side crit and penetration calculators — see
  `docs/AGENT_CONTEXT.md` "Crit damage model" / "Penetration model".
- `slot` must be 1–12; an invalid slot or item returns `400`.

### Groupings (all protected)

Nested under a team; the same access rules apply (viewer reads; editor/owner
edits). A grouping splits the roster into numbered groups for mechanics — see
`docs/AGENT_CONTEXT.md` "Groupings model".

| Method & path                                  | Who     | Description                                |
|------------------------------------------------|---------|--------------------------------------------|
| `GET /api/teams/{id}/groupings`                | viewer+ | List a team's groupings (`{ "groupings": [...] }`, each fully populated). |
| `POST /api/teams/{id}/groupings`               | editor+ | Add a grouping `{ "name": "...", "group_count": N }` → `201`. Capped at 10 per team (`409`); `group_count` clamped to 1–12. |
| `GET /api/teams/{id}/groupings/{gid}`          | viewer+ | Get one grouping with its groups + member slots. |
| `PUT /api/teams/{id}/groupings/{gid}`          | editor+ | Save the grouping (body below): name, count, per-group names, and all member assignments. |
| `DELETE /api/teams/{id}/groupings/{gid}`       | editor+ | Delete the grouping → `204`.               |

`PUT .../groupings/{gid}` body:

```json
{
  "name": "Ice cages",
  "group_count": 2,
  "groups": [
    { "group_number": 1, "name": "Left", "slots": [1, 2, 3] },
    { "group_number": 2, "name": "", "slots": [4, 5, 6] }
  ]
}
```

- `name` defaults to `"Grouping"` when blank (≤100 runes); per-group `name` is
  optional (blank → UI shows "Group N", ≤50 runes).
- `group_count` is clamped to 1–12; groups beyond it are dropped.
- `slots` are player slots 1–12. A slot may appear in **at most one** group per
  grouping — a duplicate returns `400` (the `(grouping_id, player_slot)` PK is the
  final guard). The save replaces all members in one transaction.

### Member pool (all protected)

Nested under a team. The per-team **member pool** is a recruitment roster of
prospective players, separate from the 12 fixed `players` slots — see
`docs/AGENT_CONTEXT.md` "Member pool / recruitment". Capped at 200 members per
team.

| Method & path                                              | Who     | Description                                |
|------------------------------------------------------------|---------|--------------------------------------------|
| `GET /api/teams/{id}/roster-members`                       | viewer+ | List the team's member pool (`{ "members": [...] }`). |
| `POST /api/teams/{id}/roster-members`                      | editor+ | Add a member manually → `201`.             |
| `PUT /api/teams/{id}/roster-members/{memberID}`            | editor+ | Edit a member's availability/roles/classes (leaves intake status/step/source untouched). |
| `DELETE /api/teams/{id}/roster-members/{memberID}`         | editor+ | Remove a member → `204`.                   |

Days/roles/classes are normalized/validated against the same allow-lists as the
roster; availability start hours clamp to 0–23 and end hours to 1–24 (24 = end
of day). Discord-sourced members are added by the bot's DM intake flow; the web
endpoints can still edit or remove them.

### Discord account linking (all protected)

Link an app account to a Discord identity so the bot can match the user to a
roster slot — see `docs/AGENT_CONTEXT.md` "Discord bot".

| Method & path                  | Description                                                      |
|--------------------------------|-----------------------------------------------------------------|
| `POST /api/discord/link-code`  | Mint a short, single-use link code (stored only as a SHA-256 hash, 15-min TTL) for `/coreteam link`. |
| `GET /api/discord/link`        | Report the caller's current Discord link (`{ linked, discord_username }`). |
| `DELETE /api/discord/link`     | Clear the caller's Discord link.                                |

### Quick curl test

```bash
# Login as the seeded test user (via the nginx proxy)
TOKEN=$(curl -s localhost:8081/api/login \
  -H 'Content-Type: application/json' \
  -d '{"username":"testuser","password":"changeme123"}' | jq -r .token)

curl -s localhost:8081/api/me -H "Authorization: Bearer $TOKEN" | jq
```

## Adding a database migration

1. Create `database/migrations/00X_description.sql`. Make it idempotent
   (`CREATE TABLE IF NOT EXISTS`, `ON CONFLICT`, etc.).
2. Apply it: `docker compose run --rm seed` (or recreate the db volume for a
   clean first-boot init).

> Note: there is no migration-version tracking table yet. Idempotent files keep
> repeated application safe. Introduce a tool like `golang-migrate` if/when
> ordered, irreversible migrations are needed.
