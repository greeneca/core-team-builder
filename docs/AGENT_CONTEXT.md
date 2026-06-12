# Agent Context — read this first

This file exists so a future AI session (or new contributor) can get up to speed
quickly. Keep it current when the architecture changes.

## What this project is

`core-team-builder` helps design and organize a **trial core team** for *The
Elder Scrolls Online*. It provides accounts + login and **teams**: a user can
own multiple teams and share them with others (viewer/editor roles). Each team
has a trial schedule (days + a UTC time shown in each viewer's own zone, plus
optional team display timezones) and a fixed 12-player roster — name,
discord handle, role, ESO class, and a per-player build (either a subclassed set
of 3 skill lines or 2 class masteries). Teams also have **encounters** (Default,
Trash, or a trial boss), each holding a per-player gear/skills loadout. The UI
autosaves changes (no Save buttons).

## Stack at a glance

| Layer    | Tech                         | Location     |
|----------|------------------------------|--------------|
| Frontend | static HTML/CSS/JS + nginx   | `frontend/`  |
| Backend  | Go (stdlib `net/http` mux)   | `backend/`   |
| Database | PostgreSQL 16                | `database/`  |
| Orchestr | Docker Compose               | `docker-compose.yml` |

- Go module path: `github.com/core-team-builder/backend` (Go 1.25).
- Key deps: `jackc/pgx/v5` (Postgres), `golang-jwt/jwt/v5` (tokens),
  `golang.org/x/crypto/bcrypt` (passwords).

## How auth works (current)

1. `POST /api/register` or `POST /api/login` → backend verifies/creates user,
   returns a signed JWT (`{ token, user }`). The `user` includes an `is_admin`
   flag.
2. Frontend stores the token in `localStorage` (`ctb_token`) and sends it as
   `Authorization: Bearer <token>` on protected calls.
3. `auth.Middleware` validates the token and injects the user ID into the
   request context. `GET /api/me` is the example protected route.

Passwords: bcrypt cost 12, min length 8. Hashes only ever leave via `password_hash`
column; the `User` JSON model hides it (`json:"-"`).

## Admin & user management (current)

- **Admin flag** (`015_admin_and_settings.sql`): `users.is_admin BOOLEAN`. Admins
  can manage other users and toggle public self-registration. The `User` model
  exposes it as `is_admin`; `GET /api/me` returns it so the UI can gate features.
- **Becoming an admin**: the **first account ever registered** bootstraps the
  system and is always allowed *and* made admin (regardless of the registration
  toggle). The **seed/test user is always admin** — the seeder upserts it with
  `is_admin = true` (promoting an existing test user on re-run). Otherwise admins
  grant/revoke the flag via the UI.
- **Registration toggle**: a key/value `app_settings` table holds global config;
  the `registration_enabled` key (default `'true'`) controls public
  self-registration. `SettingsStore` (`backend/internal/models/settings.go`)
  reads/writes it. `POST /api/register` honors the toggle for every account after
  the first (returns `403` when disabled). The unauthenticated
  `GET /api/registration-status` lets the login page hide the Register tab when
  it's off (the backend still enforces it).
- **Admin endpoints** (JWT-protected; `requireAdmin` in
  `backend/internal/handlers/admin.go` returns `403` for non-admins):
  - `GET /api/admin/users` — list all users.
  - `POST /api/admin/users` — create a user `{ username, email, password,
    is_admin }` (bypasses the registration toggle).
  - `DELETE /api/admin/users/{id}` — remove a user (cascades to their teams).
  - `PUT /api/admin/users/{id}/admin` — set/clear a user's admin flag.
  - `GET/PUT /api/admin/settings` — read/update `{ registration_enabled }`.
  - **Guards**: you cannot delete your own account, and you cannot delete or
    demote the **last remaining admin**.
- **UI** (`app.js` / `index.html`): admins see a **Manage Users** button in the
  topbar that opens `#admin-modal` — a self-registration toggle, an add-user
  form (with an Admin checkbox), and a user list with per-row admin toggle +
  Remove. The login page (`auth.js`) hides the Register tab when registration is
  disabled.

## Teams model (current)

- **Tables** (`002_teams.sql`): `teams` (owner + name), `team_members`
  (sharing; the owner is a row with role `owner`), `players` (12 rows per team,
  one per `slot` 1–12). `004_team_schedule.sql` adds the trial schedule to
  `teams`: `schedule_days TEXT[]` (e.g. `{mon,wed}`) and `schedule_time`
  (`"HH:MM"` 24h **in UTC**, `''` when unset). Day keys validated against
  `ValidDays`. (`005_team_timezone.sql` added a `schedule_timezone` column that
  `010_drop_schedule_timezone.sql` later removes — see Timezones below.)
- **Timezones**: `schedule_time` is stored in **UTC** and is **always set and
  shown in the viewer's own current timezone** — there is no manual timezone
  picker. The frontend converts the time UTC→local on read and local→UTC on save
  (`convertWallTime` / `formatSchedule` in `data.js`); the server just stores the
  UTC `"HH:MM"`. (Earlier the time was stored in a per-team reference zone
  `schedule_timezone`; that column is dropped in `010_drop_schedule_timezone.sql`.)
  `009_team_timezones.sql` adds `team_timezones TEXT[]` — extra IANA zones the
  team wants the time shown in (managed via removable chips + a searchable
  add-picker on the team page). The handler validates + de-dupes them via
  `normalizeTimezones` (`time.LoadLocation`; the server embeds `time/tzdata` so
  this works in the Alpine image). Note: recurring weekly times have no date, so conversions
  near a DST boundary can be off by an hour (acceptable trade-off).
- **Access**: a single `team_members` lookup yields the caller's role —
  `owner`, `editor`, or `viewer`. Owners and editors can rename a team and edit
  players; **viewers are read-only**; **only the owner** can delete the team or
  manage sharing (add/remove members, change a member's role). Inaccessible
  teams return `404` (not `403`) so other users' teams are not revealed; viewers
  attempting an edit get `403`.
- **Sharing roles**: `POST /api/teams/{id}/share` takes `{ username, role }`
  where role is `viewer` or `editor` (defaults to `editor`). It is an upsert, so
  re-sharing changes an existing member's role. Allow-list: `ValidShareRoles`
  in `team.go`; role constants `RoleOwner`/`RoleEditor`/`RoleViewer`. Migration
  `003_share_roles.sql` converts legacy `member` rows to `editor`.
- **Copy on create**: `POST /api/teams` accepts an optional `copy_from` team id;
  when set, `TeamStore.Create` seeds the new team from that source — its trial
  schedule, the full 12-player roster, and every encounter with its per-player
  loadouts (`copyEncountersTx` in `encounter.go`). Sharing/membership is **never**
  copied; the new team is owned solely by the creator. The handler validates the
  caller can access the source (`teams.Access`) and reports a missing/forbidden
  source as a generic error so other users' teams stay hidden. The new-team form
  has a **"Copy from team"** picker whose first option, "None (empty team)",
  creates a blank team.
- **Players**: each slot has `name`, `discord_handle`, `role`, `class`, plus a
  subclassing build (`006_player_subclass.sql`). Empty fields = unset. Roles and
  classes are validated against allow-lists in
  `backend/internal/models/eso.go` (`ValidRoles`, `ValidClasses`) — the ESO game
  reference data + build validators live in `eso.go`, separate from the team
  persistence layer in `team.go`:
  - roles: `tank`, `healer`, `dps`, `support_dps` (the backend still accepts
    `""`, but the UI no longer offers an unset role — every slot always has a
    concrete role).
  - classes: `arcanist`, `dragonknight`, `necromancer`, `nightblade`,
    `sorcerer`, `templar`, `warden` (plus `""`). The frontend mirrors these
    plus display labels in `frontend/js/data.js` (`ROLES`, `CLASSES`).
  - New teams default the 12 roles to 2 tanks, 2 healers, 8 dps
    (`defaultPlayerRole` in `team.go`).
  - Each roster slot is color-coded by role: the slot carries a `data-role`
    attribute (set in `renderRoster` and updated on change) and the
    `.player-slot[data-role="…"]` CSS applies a tinted background + left accent
    bar using the `--role-*` tokens in `styles.css`.
  - **Copy from slot**: each slot (editors only) has a **"Copy from…"** dropdown
    that pulls another slot's build + per-encounter loadout **into** this slot —
    everything **except** name and discord handle (role/class/race/subclass +
    active build + gear/skills/potions/CP/weapons/pen sources/mundus/armor). It
    reads the live DOM (so unsaved edits copy too) and saves the team + the
    current encounter (`copyPlayerToSlot` in `app.js`). Loadout copies only the
    selected encounter.
- **Floating jump nav** (desktop only, `≥1280px`): `#player-nav` is fixed to the
  left edge with quick links to the top, the Group Buffs card, and each player
  slot (name + role, role-colored). Built by `renderPlayerNav()` from the live
  roster and refreshed on name/role edits; clicking scrolls the target into view.
- **Subclassing** (`006_player_subclass.sql`): each player has `subclassed`
  (bool) plus two mutually exclusive build sets:
  - `subclassed = true` → `skill_line_1..3`, each one of the **21 class skill
    lines** (3 per class). Validated via `ValidSkillLines` / `ValidSkillLine`,
    plus `ValidateSkillLines(class, lines)`: lines must be unique; if the class
    is set and ≥1 line is chosen, ≥1 line must be from that class (fully-empty
    subclass builds are allowed); and ≤1 line from any other class
    (`SkillLineClass` maps line→class). The UI mirrors this via `skillLineClass`
    + `validateBuilds` before each autosave.
  - `subclassed = false` → `mastery_1..2`, drawn from the **5 class masteries of
    the player's class** (`MasteriesByClass` / `ValidMastery(class, m)`).
  - The backend validates only the active set and **blanks the inactive set** on
    save, so the two never coexist. The UI (`frontend/js/data.js`) mirrors these
    as `SKILL_LINE_GROUPS`/`SKILL_LINES` and `MASTERIES_BY_CLASS`/`MASTERIES`,
    and the roster shows a "Subclassed" checkbox that swaps between 3 skill-line
    dropdowns and 2 class-mastery dropdowns (mastery options follow the class).
- **Endpoints** (all JWT-protected): `GET/POST /api/teams`,
  `GET/PUT/DELETE /api/teams/{id}`, `POST /api/teams/{id}/share`,
  `DELETE /api/teams/{id}/members/{userID}`. Mutations return the full refreshed
  team.
- **Save-all**: `PUT /api/teams/{id}` is the single "save everything" call —
  body is `{ name, schedule_days, schedule_time, team_timezones, players: [{slot,name,discord_handle,role,class,subclassed,skill_line_1..3,mastery_1..2}] }`
  and the backend (`TeamStore.Save`) updates team meta + roster in one
  transaction (there is no per-player save endpoint). `schedule_time` is sent in
  UTC (the UI converts from the viewer's current zone,
  `Intl.DateTimeFormat().resolvedOptions().timeZone`, before saving).
- **Autosave (UI)**: there are no Save buttons. Changes are persisted
  automatically and debounced/coalesced (~700ms) via `scheduleAutosave` in
  `app.js`: text inputs save on `change` (blur — "input finished"), while
  selects/checkboxes/toggles and loadout chips save immediately. Autosaves do
  **not** re-render the view (so focus / in-progress edits are preserved); a small
  inline `save-status` shows "Saving…/Saved", errors use a toast, and Ctrl/Cmd+S
  forces an immediate save.

## Encounters model (current)

- **Tables** (`007_encounters.sql`): `encounters` (per-team named fights, with
  `position` for ordering) and `encounter_loadouts` (one row per
  `(encounter_id, slot 1–12)` holding `gear TEXT[]` and `skills TEXT[]` —
  ordered, free-form lists of master-data keys). Both cascade on team/encounter
  delete. Every team always has **at least one** encounter named `Default`:
  `TeamStore.Create` creates it for new teams (`createDefaultEncounterTx`), and
  the migration backfills existing teams.
- **Names**: an encounter's name must be in `models.ValidEncounterNames` —
  `Default`, `Trash`, or any ESO trial boss, grouped by trial in
  `models.EncounterNameGroups`. The frontend mirrors this in
  `frontend/js/data.js` (`ENCOUNTER_NAME_GROUPS`). This is **seed** data meant to
  grow; keep the Go groups and the JS groups in sync.
- **Selection rules** (`models.ValidateEncounterSelection`, enforced on
  create/rename): names are **unique** per team, and all non-`General`
  encounters must belong to a **single trial** (the `General` group — Default,
  Trash — is always allowed alongside one trial). A unique index on
  `encounters(team_id, name)` (`008_…sql`) backstops uniqueness. The frontend
  filters the add/rename dropdown to only valid choices via `validEncounterGroups`
  / `encounterTrial` in `data.js` (used by `populateEncounterNameSelect`).
- **Copy on create**: the create request accepts an optional `copy_from`
  encounter id; when set, `EncounterStore.Create` copies that encounter's
  per-player gear/skills slot-for-slot into the new one (the SQL join on
  `encounters.team_id` guarantees same-team copies only; the handler also
  validates the source belongs to the team). The add-encounter form has a
  **"Copy gear & skills from"** picker whose first option, "None (empty
  encounter)", creates a blank encounter.
- **Loadouts hold three lists**: each `(encounter, slot)` loadout has `gear`,
  `skills`, **and `potions`** (`011_loadout_potions.sql` adds the `potions TEXT[]`
  column). All three are free-form, ordered key lists treated identically by the
  store/handler (`Loadout.Potions`, sanitized via `SanitizeLoadoutItems`) and the
  copy-on-create logic. The frontend renders a third searchable picker/chip
  column per slot driven by `LOADOUT_TYPES.potions` (master data `POTION_GROUPS`/
  `POTIONS` in `data.js`).
- **Crit inputs on the loadout** (`012_loadout_crit.sql`): each `(encounter,
  slot)` loadout also carries `cp_blue TEXT[]` (slotted blue/Warfare CP),
  `weapons TEXT[]` (weapon lines), `mundus TEXT`, and `armor_heavy/medium/light
  SMALLINT` (0–7). `cp_blue`/`weapons` reuse the chip machinery
  (`LOADOUT_TYPES.cp_blue`/`.weapons`); mundus is a `<select>` and armor are
  number steppers. These feed the crit calculator (see "Crit damage model").
  `players.race` (`013_player_race.sql`, validated by `models.ValidRace`) is the
  roster-level crit input.
- **Penetration input on the loadout** (`014_loadout_pen_extra.sql`): each
  `(encounter, slot)` loadout also carries `pen_extra TEXT[]` — a chip column
  (`LOADOUT_TYPES.pen_extra`, master data `PEN_EXTRA_SOURCES` in `data.js`) for
  flat penetration sources that aren't otherwise derivable (Crusher enchant,
  Sharpened trait, Mace/Maul, generic set-piece bonuses). These plus reused
  inputs feed the penetration calculator (see "Penetration model").
- **Loadout items** (gear sets, skills, potions, cp_blue, weapons, pen_extra): stored as keys; the backend does **not**
  validate them against a master list (free-form, defensively sanitized via
  `SanitizeLoadoutItems`: trimmed, non-empty, ≤100 chars, ≤30 items). The
  searchable dropdowns, labels, and gear tooltips live entirely in the frontend
  master data (`GEAR_SET_GROUPS` — gear grouped by set type (5pc, monster,
  arena, mythic) — and `SKILL_GROUPS` — skills grouped by skill line — in
  `data.js`, each with a flat `GEAR_SETS`/`SKILLS` derived from it for lookups);
  unknown keys fall back to the raw value. Both pickers use the in-house
  `createSearchableSelect` component (`js/components.js`) — a dropdown with full
  free-text search **and** group headers. Skills supply one header per skill
  line; gear one header per set type. Expand the seed there. Both gear sets and
  skills carry a `desc`, shown as a floating tooltip (`initTooltips` in
  `components.js`, driven by a `data-tip` attribute) on both the picker options
  **and** the selected chips. Tooltips can be turned off via the topbar
  **Tooltips** checkbox; the choice persists in `localStorage`
  (`ctb_tooltips_disabled`) via `setTooltipsEnabled`. The **Encounters** heading
  and the **Active Encounter** panel title each carry a small circled-`i`
  `.info-indicator` (focusable, with a `data-tip`) that explains how encounters
  work; these use the same tooltip engine, so they also respect the Tooltips
  toggle.
- **Access/permissions**: mirror the roster — any role can read; editors/owner
  can add, rename, delete, and edit loadouts; viewers are read-only. A team
  cannot delete its **last** encounter.
- **Endpoints** (all JWT-protected, nested under a team):
  `GET/POST /api/teams/{id}/encounters`,
  `GET/PUT/DELETE /api/teams/{id}/encounters/{eid}`,
  `PUT /api/teams/{id}/encounters/{eid}/loadouts`. Mutations return the refreshed
  encounter (with its 12 loadouts).
- **UI**: encounters are integrated into the single team detail page (there is
  **no** separate encounter screen). The **Encounters** card holds a bar of
  selectable chips (the active one is highlighted), an `+ Add Encounter` picker,
  and an `#encounter-controls` row (current name, rename dropdown, delete, save
  status). Selecting a chip (`selectEncounter`) loads that encounter's loadouts
  and refreshes the per-player gear/skill chips inline in the roster — each
  roster slot renders a `[data-loadout]` block (Gear + Skills searchable lists)
  below its subclass/class-mastery section (`renderRosterLoadouts`). Loadouts
  autosave on chip add/remove (or Ctrl/Cmd+S); `selectEncounter` flushes any
  pending loadout autosave before switching so unsaved edits are never dropped.
  The chip selector sits in its own titled box (`#encounters-panel`,
  `.encounters-panel`, header "Active Encounter") that lives outside the
  encounters card (a direct child of the detail section) so its containing block
  spans the roster, letting it stay pinned while scrolling. By default it
  attaches flush beneath the encounters card — the card's bottom corners are
  squared (`.encounters-manage-card`) and the panel overlaps the border with a
  `-1px` top margin and squared top corners, so they read as one box. Only this
  panel is `position: sticky`; the rest of the encounters card (heading, add
  form, rename/delete controls) scrolls away normally. It pins just beneath the
  **sticky topbar** at `top: var(--topbar-height)` (the topbar is also
  `position: sticky`; `syncTopbarHeight` in `app.js` measures it into that CSS
  var on load/resize). `setupEncounterStickiness` watches a zero-height
  `#encounters-sentinel` via `IntersectionObserver` (top `rootMargin` = topbar
  height) and toggles an `.is-stuck` class once the panel pins, which rounds all
  corners and adds elevation so it visibly **splits off** into a floating bar.
  The topbar brand ("Core Team Builder", `#brand-home`) is a link back to the
  teams list (SPA navigation, with an `index.html` no-JS fallback). See
  **Autosave (UI)** above.

## Buffs model (current)

- **What it is**: a team wants to cover a fixed list of ESO buffs. The app shows
  how many are covered **for the selected encounter** plus a per-buff breakdown.
- **No backend/DB**: buffs are pure **frontend reference data + a computed view**.
  The only persisted change buffs required is the per-encounter `potions` loadout
  (above). Coverage is recomputed client-side from data already in memory.
- **Data** (`frontend/js/data.js`): `BUFFS` is an array of
  `{ value, label, desc, sources }` where `sources` maps a category to providing
  keys: `gear`, `skills`, `potions` (per-encounter loadout) and `masteries`,
  `classes`, `skillLines` (roster build). The seeded source keys are **sensible
  placeholders** — adjust them to the exact ESO sources without changing the
  shape. Keys reference the existing master data (gear sets, skills, potions,
  class masteries/classes/skill lines).
- **Coverage rule** (`computeBuffCoverage(players, loadoutBySlot)` in `data.js`):
  a buff is **met** if at least one player provides at least one of its sources.
  Build sources honor subclassing — a `subclassed` player contributes their
  `skill_line_*`; a non-subclassed player contributes their `class` + `mastery_*`.
  Loadout sources (gear/skills/potions) always count. Returns `{ total, met,
  items: [{ buff, met, providers:[{slot, category, key}] }] }`.
- **UI** (`app.js` / `index.html`): a **Group Buffs** card on the team detail
  page shows `met / total` plus a pip bar; a **Details** button opens
  `#buffs-modal` listing each buff (met/unmet + which players/sources provide it,
  with a tooltip on each buff name listing its known sources).
  `refreshBuffCoverage()` reads the live DOM (`collectPlayers()` +
  `collectLoadouts()`) and repaints without a full re-render, so it stays correct
  after autosaves; it is called on detail render, encounter switch, roster/build
  changes, and loadout chip add/remove.

## Crit damage model (current)

- **What it is**: a per-encounter critical-damage calculator shown below the
  Buffs card. Critical damage caps at **125% total** (`CRIT_CAP`); the **50%**
  base (`CRIT_BASE`) is modelled as a group source.
- **Three buckets**: `group` (whole team — any one player providing it counts),
  `target` (a debuff on the boss — any one player applies it), and `self` (only
  that player). Per player, effective crit = `group + target + self`; they meet
  the cap when that reaches 125. **Solo required** = `125 - group - target`.
- **No backend math**: like buffs, this is frontend reference data + a computed
  view. The only persisted inputs are the per-encounter crit columns on
  `encounter_loadouts` (cp_blue/weapons/mundus/armor) and `players.race`.
- **Data** (`frontend/js/data.js`): `CRIT_GROUP_SOURCES`, `CRIT_TARGET_SOURCES`
  (each `{value,label,pct,detect}` where `detect` maps a category to keys), and
  `CRIT_SELF_SOURCES` (each `{label,pct,type,...}`; `type` ∈
  `mundus|cp|gear|race|classPassive`). Medium-armor Dexterity is
  `CRIT_MEDIUM_PER_PIECE` (2%) × medium pieces; weapon-line crit takes the MAX of
  selected lines (one active bar). Several source keys (Minor Force, Minor
  Brittle) are **placeholders** — one-line edits.
- **Rule** (`computeCritCoverage(players, loadoutBySlot)`): class-passive
  detection honors subclassing (non-subclassed → `class`; subclassed → the linked
  `skill_line_*`). Returns `{ cap, base, group, target, soloRequired,
  groupSources, targetSources, players:[{slot, self, total, met, deficit,
  sources}] }`.
- **UI** (`app.js`/`index.html`): a **Crit Damage** card shows group/target/solo
  stats with a `Details` button (`#crit-modal` lists detected group/target
  sources + each player's breakdown). Each roster slot has crit inputs (Blue CP +
  Weapons chip columns, Mundus select, H/M/L armor steppers) and a `.crit-label`
  showing that player's total with a met/unmet indicator. `refreshCritCoverage()`
  repaints live (same trigger points as `refreshBuffCoverage()`, plus mundus/
  armor/race changes).

## Penetration model (current)

- **What it is**: a per-encounter penetration calculator shown below the Crit
  card, built on the same pattern. A player wants their Offensive Penetration to
  reach the target's Resistance (`PEN_TARGET = 18200`, the standard trial value).
- **Two buckets**: `group` (team-wide debuffs/buffs — Breaches, Alkosh, Crimson
  Oath, Tremorscale, Crusher, etc.; any one player providing it counts for all)
  and `self` (only that player — CP Piercing, light armor, The Lover mundus,
  Velothi, Arcanist Splintered Secrets, Wood Elf, arena 1pc, plus `pen_extra`).
  Per player, total = `group + self`; they meet it when total ≥ 18200. **Self
  required** = `18200 - group`.
- **No backend math**: frontend reference data + a computed view. Reuses existing
  per-encounter inputs (cp_blue, armor_light, mundus, gear) and `players.race`;
  the only new persisted input is `pen_extra` on `encounter_loadouts`.
- **Data** (`frontend/js/data.js`): `PEN_GROUP_SOURCES` (each
  `{value,label,pen,detect}`), `PEN_SELF_SOURCES` (each `{label,pen,type,...}`;
  `type` ∈ `cp|gear|mundus|race|classPassive`), `PEN_EXTRA_SOURCES` (the
  `pen_extra` chip options, each `{value,label,pen,bucket}` where `bucket` ∈
  `self|group`). Light armor is `PEN_LIGHT_PER_PIECE` (939) × light pieces; arena
  1pc is detected from any equipped gear in the `Arena Weapons` group. Several
  group keys (Major/Minor Breach, Runic Sunder, Dismember) are **placeholders** —
  one-line edits.
- **Rule** (`computePenCoverage(players, loadoutBySlot)`): class-passive detection
  honors subclassing like crit; group detection reuses `critSourceProviders`.
  Returns `{ target, group, selfRequired, groupSources, players:[{slot, self,
  total, met, deficit, sources}] }`.
- **UI** (`app.js`/`index.html`): a **Penetration** card shows target/group/self
  stats with a `Details` button (`#pen-modal`). Each roster slot has a `pen_extra`
  chip column and a penetration label with a met/unmet indicator.
  `refreshPenCoverage()` repaints live (same trigger points as
  `refreshCritCoverage()`).

## Request flow

Browser → nginx (`frontend`, port 80→`FRONTEND_PORT`) → `/api/*` proxied to
`backend:8080` → `db:5432`. Because `/api` is same-origin via the proxy, CORS is
generally not triggered (the backend still sets CORS headers as defense).

## Where to make changes

- **New API endpoint**: add a handler in `backend/internal/handlers/handlers.go`
  and register it in `Routes()`. Protected routes wrap with
  `s.tokens.Middleware(...)`.
- **New table / query**: add migration in `database/migrations/` (idempotent),
  add a store + methods in `backend/internal/models/`.
- **New page / UI**: add an `.html` file in `frontend/`, a script in
  `frontend/js/`, and reuse tokens/classes from `frontend/css/styles.css`
  (see `docs/STYLE_GUIDE.md`). Keep concerns separated: network calls/endpoint
  helpers go in `js/api.js`; shared reference data + display helpers go in
  `js/data.js`; reusable widgets go in `js/components.js`.
- **Config**: env vars are read in `backend/internal/config/config.go` and wired
  through `docker-compose.yml` + `.env`.

## Conventions

- Migrations are **idempotent** (`IF NOT EXISTS`, `ON CONFLICT`) so both the
  Postgres init dir and the `seed` command can apply them safely.
- The `seed` binary applies all `*.sql` in `MIGRATIONS_DIR` (sorted) then
  upserts the test user (always as an **admin** — `is_admin = true`, promoting an
  existing test user on re-run). It is safe to run repeatedly. The test-user
  credentials (`SEED_USERNAME`, `SEED_EMAIL`, `SEED_PASSWORD`) are **required
  from the environment** — there are no hardcoded defaults — and the plaintext
  password is never logged.
- Secrets/credentials come from the environment only; never hardcode. `.env` is
  git-ignored.

## Common commands

```bash
docker compose up --build          # run the whole stack
docker compose run --rm seed       # (re)apply migrations + ensure test user
cd backend && go build ./...       # compile backend
cd backend && go vet ./...         # static checks
```

## Status / TODO ideas

- [ ] Token refresh / logout server-side invalidation (currently stateless JWT).
- [x] Teams: ownership, sharing, and a 12-player roster (name/discord/role/class).
- [x] Encounters: per-team named fights with per-player gear/skill loadouts.
- [x] Admin users + user management (list/add/remove/promote) + registration toggle.
- [ ] Expand the gear-set/skill/boss seed data to full ESO coverage.
- [ ] Tests (handlers, auth, models).
- [ ] Rate limiting on auth endpoints.
