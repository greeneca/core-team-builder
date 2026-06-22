# Agent Context — read this first

This file exists so a future AI session (or new contributor) can get up to speed
quickly. Keep it current when the architecture changes.

## What this project is

`core-team-builder` helps design and organize a **trial core team** for *The
Elder Scrolls Online*. It provides accounts + login and **teams**: a user can
own multiple teams and share them with others (viewer/editor roles). Each team
has a trial schedule (days + a UTC time shown in each viewer's own zone) and a
fixed 12-player roster — name,
discord handle, role, ESO class, and a per-player build (either a subclassed set
of 3 skill lines or 2 class masteries). Teams also have **encounters** (Default,
Trash, or a trial boss), each holding a per-player gear/skills loadout, and
**groupings** (named sets of numbered groups for mechanics, e.g. ice cages or
slayer stacks). A team can set two **Discord bot footers** (free-form text the
bot appends to its `/coreteam post` overview and its build-details DM). Each
team also keeps a **member pool** — prospective players who signed up via the
bot's `/coreteam recruit` post (an interactive DM gathers their availability,
roles, and classes) or were added manually, visualized on a Members page. The
UI autosaves changes (no Save buttons).

## Stack at a glance

| Layer    | Tech                         | Location     |
|----------|------------------------------|--------------|
| Frontend | static HTML/CSS/JS + nginx   | `frontend/`  |
| Backend  | Go (stdlib `net/http` mux)   | `backend/`   |
| Database | PostgreSQL 16                | `database/`  |
| Orchestr | Docker Compose               | `docker-compose.yml` |

- Go module path: `github.com/core-team-builder/backend` (Go 1.25).
- Key deps: `jackc/pgx/v5` (Postgres), `golang-jwt/jwt/v5` (tokens),
  `golang.org/x/crypto/bcrypt` (passwords), `bwmarrin/discordgo` (Discord bot).
- Binaries (`backend/cmd/`): `server` (API), `seed` (migrations + test user),
  `bot` (Discord bot — see "Discord bot" below).

## How auth works (current)

1. `POST /api/register` or `POST /api/login` → backend verifies/creates user,
   returns a short-lived **access token** (JWT) plus a long-lived **refresh
   token** and the user (`{ token, refresh_token, user }`). The `user` includes
   an `is_admin` flag.
2. Frontend stores both tokens in `localStorage` (`ctb_token` /
   `ctb_refresh_token`) and sends the access token as
   `Authorization: Bearer <token>` on protected calls.
3. `auth.Middleware` validates the access token and injects the user ID into the
   request context. `GET /api/me` is the example protected route.
4. **Refresh / logout** (`016_refresh_tokens.sql`): access tokens are stateless
   JWTs (default 15m, `JWT_TTL`); refresh tokens are opaque random strings stored
   **only as a SHA-256 hash** in `refresh_tokens` (default 30d, `REFRESH_TTL`).
   `POST /api/refresh` rotates the pair (revokes the old refresh row, issues a new
   one); `POST /api/logout` revokes the presented refresh token. A background
   hourly sweep (`startTokenCleanup` in `cmd/server/main.go`) prunes expired
   refresh **and** password-reset rows. `RefreshTokenStore`
   (`backend/internal/models/refresh_token.go`) handles persistence.
5. **Forgot / reset password** (`021_password_resets.sql`): `POST /api/forgot-password`
   takes an email and **always** returns a generic message (no account
   enumeration). When the email matches a user it invalidates that user's prior
   reset tokens, mints a new opaque token (stored **only as a SHA-256 hash** in
   `password_resets`, single-use, default 1h `PASSWORD_RESET_TTL`), and emails a
   link to `<APP_BASE_URL>/reset.html?token=…` in the background.
   `POST /api/reset-password` validates the new password against the policy,
   consumes the token (atomic single-use), updates the hash, and revokes all of
   the user's refresh tokens (sign-out-everywhere). Email goes through the
   `email.Mailer` abstraction (`backend/internal/email/`): an `SMTPMailer` when
   `SMTP_HOST` is set, else a `LogMailer` that logs the message (dev). Handlers
   live in `backend/internal/handlers/password_reset.go`; persistence in
   `PasswordResetStore` (`backend/internal/models/password_reset.go`).

6. **Sign in with Discord** (OAuth2; `backend/internal/handlers/discord_oauth.go`):
   when `DISCORD_CLIENT_ID`/`DISCORD_CLIENT_SECRET` are set the login page shows a
   "Continue with Discord" button. `GET /api/auth/discord/login` sets a short-lived
   HttpOnly CSRF **state cookie** and redirects to Discord;
   `GET /api/auth/discord/callback` verifies the state, exchanges the code for the
   Discord identity (`identify email` scopes), then resolves the app account:
   (a) if the Discord ID is **already linked**, sign that user in; (b) else if the
   Discord **email (verified)** matches an existing account, **auto-link** and sign
   in (refused if that account is already linked to a different Discord, or if the
   email is unverified); (c) else **create a new account** (honoring the
   registration toggle; first-ever user still bootstraps as admin). New accounts
   are **passwordless** (`UserStore.CreateDiscordUser` stores an unusable
   `password_hash`; users can set one later via forgot-password) and have their
   `discord_user_id` set, so **the bot's `/coreteam` commands work with no manual
   `/coreteam link`**. The callback redirects to `<APP_BASE_URL>/discord.html#…`
   with the freshly issued tokens in the **URL fragment** (never sent to a server);
   `frontend/js/discord.js` stores the session and continues into the app. Errors
   come back as `login.html?discord_error=<code>`.

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
  it's off (the backend still enforces it). The same response includes
  `discord_enabled` so the page can show/hide the "Continue with Discord" button.
  Discord sign-up also honors the toggle (new Discord accounts are blocked when
  registration is disabled; existing/linked users can still sign in).
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
  The server embeds `time/tzdata` so zone conversions work in the Alpine image.
  Note: recurring weekly times have no date, so conversions near a DST boundary
  can be off by an hour (acceptable trade-off). (`009_team_timezones.sql` once
  added a `team_timezones TEXT[]` list of extra display zones, but it was never
  read for display — viewers always see their own zone — so it is dropped in
  `031_drop_team_timezones.sql`.)
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
  subclassing build (`006_player_subclass.sql`). Empty fields = unset. Classes
  are validated against the allow-list in
  `backend/internal/models/eso.go` (`ValidClasses`) — the ESO game
  reference data + build validators live in `eso.go`, separate from the team
  persistence layer in `team.go`:
  - roles: **per-team and customizable** (`042_team_roles.sql`). A team owns a
    `roles JSONB` set of `{key, label, base}` objects (default Tank/Healer/DPS/
    Support DPS, see `models.DefaultTeamRoles`). `base` is the **color category**
    (one of `tank`/`healer`/`dps`/`support_dps`, `models.ValidRoleBases`) that
    drives the roster's role color coding, so a **custom** role with an arbitrary
    key still renders in a known `--role-*` color. The roster role picker reads
    from this set, and `handleUpdateTeam` validates each player's role against the
    team's own role keys (not a global allow-list) and each role's `base` against
    `ValidRoleBases` (empty base derives from the key, else `DefaultRoleBase`).
    Add/remove roles from the **Main panel** ("Roster Roles"); the add row has a
    base/color picker, and each role chip is accented with its base color. A role
    can't be removed while a player is assigned to it. The editor is shown in
    every mode, including simple-signup pre-made runs (players pick one of the
    team's roster roles to be auto-placed against). The **Discord post flows** are role-aware: both
    the `/coreteam post` overview and the pre-made `/coreteam signup` post +
    controls render each role using the team's own role set via `Team.RoleLabel`,
    `Team.OrderedRoleKeys`, and `Team.EffectiveRoles` (see `cmd/bot/premade.go`
    and `internal/discordfmt`), so custom roles show their labels. Roles are
    always **ordered by color base** — tank, then healer, support DPS, DPS, then
    anything else (`models.roleBaseOrder`); within a base the team's defined order
    is kept. The web UI mirrors this for display via `orderedTeamRoles()` /
    `ROLE_BASE_ORDER` (the stored order in `currentTeam.roles` is left as added).
    Both Discord post flows also show a per-role emoji by base (`Team.RoleEmoji` /
    `models.roleBaseEmoji`: tank 🛡️, healer ❇️, support DPS ⚒️, DPS ⚔️) on the
    roster group headers; the signup posts add it to waitlist lines and the
    claim/waitlist selects too.
    `ValidRoles` in `eso.go` remains the fixed set only for the member pool and
    the Discord recruit intake flow (`cmd/bot/intake.go`), which are intentionally
    left on the standard roles.
  - classes: `arcanist`, `dragonknight`, `necromancer`, `nightblade`,
    `sorcerer`, `templar`, `warden` (plus `""`). The frontend mirrors these
    plus display labels in `frontend/js/data.js` (`CLASSES`); `DEFAULT_TEAM_ROLES`
    holds the fallback roster roles (each with a color `base`) and `ROLE_BASES`
    the color-category options for the picker.
  - New teams default the 12 roles to 2 tanks, 2 healers, 8 dps
    (`defaultPlayerRole` in `team.go`).
  - Each roster slot is color-coded by role: the slot carries a `data-role`
    attribute (set in `renderRoster` and updated on change) holding the role's
    **base** color category (via `roleBase()` in `app.js`, not the raw role key)
    and the `.player-slot[data-role="…"]` CSS applies a tinted background + left
    accent bar using the `--role-*` tokens in `styles.css`. The player jump-nav
    and the role-chip editor are colored the same way.
  - **Copy from slot**: each slot (editors only) has a **"Copy from…"** dropdown
    that pulls another slot's build + per-encounter loadout **into** this slot —
    everything **except** name and discord handle (role/class/race/subclass +
    active build + gear/skills/potions/CP/crit dmg/pen sources/mundus/armor). It
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
- **Werewolf** (`043_player_werewolf.sql`): each player has a `werewolf` bool with
  a roster toggle next to "Subclassed". When checked it adds the default werewolf
  skills (`models.WerewolfDefaultSkills` / `WEREWOLF_DEFAULT_SKILLS`) to that
  slot's `encounter_loadouts.skills`; unchecking removes the full Werewolf skill
  line (`models.WerewolfSkills`). The flag applies to **every** encounter: the UI
  updates the currently-shown encounter's skill chips for immediate feedback, and
  `TeamStore.Save` reconciles all of the team's encounters for that slot
  (`reconcileWerewolfSkillsTx`). The `/coreteam post` overview and `/coreteam
  signup` post tag a werewolf slot with **`WW`** before its gear. Note: because
  reconciliation runs on every team save, a non-werewolf slot can't keep a
  manually-added werewolf-line skill.
- **Endpoints** (all JWT-protected): `GET/POST /api/teams`,
  `GET/PUT/DELETE /api/teams/{id}`, `POST /api/teams/{id}/share`,
  `DELETE /api/teams/{id}/members/{userID}` (owner removes a member),
  `DELETE /api/teams/{id}/membership` (a shared member leaves the team
  themselves; the owner can't — they delete it instead; returns 204). Mutations
  return the full refreshed team.
- **Encounters toggle** (`017_team_encounters_enabled.sql`):
  `teams.encounters_enabled BOOLEAN` (default **false**) controls whether the UI
  surfaces the multi-encounter section. When off, the encounters card + chip
  selector are hidden and only the first encounter is shown; the team still keeps
  ≥1 encounter in the DB. An editor opts in per team via the topbar/section
  toggle.
- **Auto-share with member pool** (`034_team_auto_share_pool.sql`):
  `teams.auto_share_pool_viewers BOOLEAN` (default **false**) — a "Team Features"
  checkbox. When **on**, the team is automatically shared as **viewer** with the
  app accounts of everyone in its **member pool** (`team_roster_members`), current
  and future. A pool member only becomes a viewer once their Discord identity is
  tied to an app account (i.e. they've signed in / linked via Discord), since
  sharing needs a real `users` row. Reconciliation happens at three points, all
  idempotent and **non-destructive** (`ON CONFLICT DO NOTHING`, so owner/editor
  roles are never downgraded): on team save while the flag is on
  (`TeamStore.SharePoolMembers`), when a user signs in / links via Discord
  (`TeamStore.ShareAutoTeamsForDiscord`, called from the OAuth callback and bot
  `/coreteam link`), and when a member finishes the bot signup flow
  (`SharePoolMembers` from `cmd/bot` `signupFinish`). Turning the flag **off**
  does nothing — it never revokes already-granted shares; it just stops new pool
  members from being shared with unless re-enabled.
- **Pre-made trial run / template** (`035_team_premade.sql`): `teams.pre_made
  BOOLEAN` (default **false**) + `teams.premade_post TEXT` (default `''`, ≤2000
  runes). A pre-made team is a "signup template". Template status can be set **at
  creation time** via the teams page's **+ New Template** button (creates the
  team, then promotes it with a save that sets `pre_made=true` and
  `simple_signup=true` — simple signup is the template default), **or toggled
  later** with the **Convert to template / Convert to team** button in the team
  detail page's "Team Features" section (editors only). Convert flips only
  `pre_made` and persists it via the normal team save; the roster and all other
  data are preserved, and `simple_signup` is left unchanged (so a converted team
  keeps its per-slot builds unless simple signup is turned on). The teams page
  lists templates in their own collapsible **Templates** section, separate from
  standard teams. When a team is a template, the detail UI shows its own
  "Pre-made Run Post" card and **hides** (non-destructively) the trial schedule,
  Discord Bot Texts, per-player Discord handles, the Members Pool button, and the
  auto-share toggle — none of that applies. The bot side (signups, scheduling) is
  documented under "Discord bot" → "Pre-made trial runs" below.
- **Simple signup** (`037_team_simple_signup.sql`): `teams.simple_signup BOOLEAN`
  (default **false**, but **on** by default for new templates) — a "Team
  Features" checkbox shown only when the team is a template. Off = "specific"
  signup (the post shows each slot's class/gear, a "get build details" dropdown is
  offered, and players claim an exact slot). On = "simple" signup: the post hides
  class/gear and the details dropdown, the claim select lists **roles** (with open
  counts), and claiming takes the first open slot matching the chosen role
  (`handlePremadeClaim` → `claimSimple`, retrying on slot races). When simple
  signup is on, the **Encounters Enabled** toggle is hidden (encounters don't
  apply to a name/role-only template).
- **Waitlist** (`038_premade_waitlist.sql`): `teams.waitlist_enabled BOOLEAN`
  (default **false**) — a "Team Features" checkbox shown only when **Pre-made** is
  on. When on, a "Join a waitlist (role is full)" select appears on the post for
  any **full** role; joining queues the user (`premade_waitlist`, FIFO per role).
  When a claimed slot frees up — on **leave** (`handlePremadeLeave`) **or** when a
  user **switches** to a different slot (`handlePremadeClaim`/`claimSimple`, which
  vacates their prior slot) — `promoteFreedSlot` calls `PromoteToSlot` to move the
  head of that slot's role queue into the open slot and the bot DMs them. Holding
  a slot supersedes waiting (claiming clears your waitlist entry).
- **Discord bot footers** (`018_team_signup_note.sql`,
  `019_team_detailed_header.sql`, renamed by `029_team_bot_footers.sql`): each
  team carries `post_footer TEXT` (appended to the bot's `/coreteam post`
  overview) and `dm_footer TEXT` (appended to the "Get My Build Details" DM),
  both free-form (default `''`, ≤2000 runes, validated in the team handler) and
  edited from the "Discord bot footers" controls on the team page. The footers
  are consumed by the bot only (`discordfmt.BuildPost` / `discordfmt.PlayerDetail`);
  the old web-app clipboard export (detailed post / condensed list) was removed.
- **Save-all**: `PUT /api/teams/{id}` is the single "save everything" call —
  body is `{ name, schedule_days, schedule_time,
  encounters_enabled, post_footer, dm_footer, signup_post, auto_share_pool_viewers, pre_made, premade_post, simple_signup, waitlist_enabled, players: [{slot,name,discord_handle,role,class,subclassed,skill_line_1..3,mastery_1..2,werewolf}] }`
  and the backend (`TeamStore.Save`) updates team meta + roster in one
  transaction (there is no per-player save endpoint). `schedule_time` is sent in
  UTC (the UI converts from the viewer's current zone,
  `Intl.DateTimeFormat().resolvedOptions().timeZone`, before saving). Groupings
  are **not** part of this call — they have their own endpoints (see below).
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
  `crit_dmg TEXT[]` (crit-damage source passives; added as `weapons`, renamed by
  `022_loadout_crit_dmg_rename.sql`), `mundus TEXT`, and `armor_heavy/medium/light
  SMALLINT` (0–7). `cp_blue`/`crit_dmg` reuse the chip machinery
  (`LOADOUT_TYPES.cp_blue`/`.crit_dmg`); mundus is a `<select>` and armor are
  number steppers. These feed the crit calculator (see "Crit damage model").
  `players.race` (`013_player_race.sql`, validated by `models.ValidRace`) is the
  roster-level crit input.
- **Penetration input on the loadout** (`014_loadout_pen_extra.sql`): each
  `(encounter, slot)` loadout also carries `pen_extra TEXT[]` — a chip column
  (`LOADOUT_TYPES.pen_extra`, master data `PEN_EXTRA_SOURCES` in `data.js`) for
  flat penetration sources that aren't otherwise derivable (Crusher enchant,
  Sharpened trait, Mace/Maul, generic set-piece bonuses). These plus reused
  inputs feed the penetration calculator (see "Penetration model").
- **Scribing inputs** (`032_loadout_scribed_buffs.sql`,
  `033_loadout_banner_bearer_focus.sql`): each `(encounter, slot)` loadout also
  carries `scribed_buffs TEXT[]` and `banner_bearer_focus TEXT`. ESO **scribing**
  grimoires let a player attach a group buff to a slotted scribed skill; when a
  loadout slots a grimoire skill (`GRIMOIRE_SKILLS` in `data.js`) the roster
  reveals a **Scribed buffs** chip column (`LOADOUT_TYPES.scribed_buffs`, options
  `SCRIBED_BUFFS`) recording which group buffs that skill provides — these count
  toward the Group Buffs coverage card (the `scribed` source category; note
  `minor_breach` instead feeds the penetration calculator as a group source).
  When the **Banner Bearer** grimoire is slotted, a single `banner_bearer_focus`
  `<select>` (`BANNER_BEARER_FOCUS`) records the chosen Focus Script; it is
  **informational only** (shown in the UI and Discord export, feeds no
  calculation).
- **Loadout items** (gear sets, skills, potions, cp_blue, crit_dmg, pen_extra,
  scribed_buffs): stored as keys; the backend does **not**
  validate them against a master list (free-form, defensively sanitized via
  `SanitizeLoadoutItems`: trimmed, non-empty, ≤100 chars, ≤30 items). The
  searchable dropdowns, labels, and gear tooltips live entirely in the frontend
  master data (`GEAR_SET_GROUPS` — gear grouped by set type (5pc, monster,
  arena, mythic) — and `SKILL_GROUPS` — skills grouped by skill line — in
  `frontend/js/gear-skills.js` (split out from `data.js` for ease of updating;
  loaded before it), each with a flat `GEAR_SETS`/`SKILLS` derived from it for
  lookups);
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

## Groupings model (current)

- **What it is** (`020_groupings.sql`): a **grouping** splits a team's roster
  into a set of numbered groups for trial mechanics (e.g. "ice cages", "slayer
  stacks"). A team may have several groupings; each has a `name`, a `group_count`
  (1–12), and a `position` for ordering. Each numbered group has an optional
  `name` (blank → UI shows "Group N") and any number of player slots. A player
  may belong to **at most one group per grouping** — enforced by the
  `grouping_members` primary key `(grouping_id, player_slot)`.
- **Tables**: `groupings` (per-team, name/group_count/position),
  `grouping_groups` (`(grouping_id, group_number)` PK → per-group name), and
  `grouping_members` (`(grouping_id, player_slot)` PK → which group a slot is in).
  All cascade on team/grouping delete. `GroupingStore`
  (`backend/internal/models/grouping.go`) always returns a full set of
  `group_count` groups (blanks filled in) so the client gets a complete shape.
- **Limits**: `maxGroupingsPerTeam` (10, in `handlers.go`) caps groupings per
  team (`409` when exceeded); `MaxGroupsPerGrouping` (12) and `clampGroupCount`
  bound the group count; grouping/group names are capped (`maxGroupingNameLen`
  100, `maxGroupNameLen` 50). The update handler rejects a slot appearing in two
  groups (`400`).
- **Copy on create**: `copyGroupingsTx` (in `grouping.go`) copies all groupings
  (names, group names, member assignments) when a team is created with
  `copy_from`, alongside the schedule/roster/encounters copy.
- **Access/permissions**: mirror the roster — any role reads; editors/owner add,
  rename, delete, and edit; viewers are read-only.
- **Endpoints** (all JWT-protected, nested under a team):
  `GET/POST /api/teams/{id}/groupings`,
  `GET/PUT/DELETE /api/teams/{id}/groupings/{gid}`. The `PUT` body is
  `{ name, group_count, groups: [{ group_number, name, slots: [...] }] }` and
  replaces the grouping's name, count, per-group names, and **all** member
  assignments in one transaction (`GroupingStore.Save`).
- **UI** (`app.js` / `index.html`): a **Groupings** card on the team detail page
  lists each grouping as its own sub-card (group-count control + per-group blocks,
  each with a name input, removable player chips, and an "+ Add player…" dropdown
  of slots not yet assigned in that grouping). Each grouping autosaves
  on its own debounce (`scheduleGroupingSave` / `saveGroupingNow`); structural
  edits (add/remove player, change group count) re-render while name edits save
  without re-rendering to preserve focus. `renderGroupings` reads the live roster
  for slot labels. Groupings are also included in the Discord bot's post overview.

## Discord bot (current)

- **What it is** (`027_discord.sql`): a separate Go binary (`backend/cmd/bot`,
  built into the same image and run via the `bot` compose **profile**) that
  connects out to the Discord gateway with `bwmarrin/discordgo`. It shares the
  database (same stores) but exposes **no inbound port**. It registers the
  `/coreteam` slash command (plus two top-level aliases `/post` and `/signup`
  that dispatch to the same handlers as `/coreteam post` and `/coreteam signup`
  — see `botCommands` in `commands.go` and the `data.Name` switch in
  `onCommand`). `/coreteam` subcommands:
  - `link code:<code>` — links the invoking Discord user to an app account using
    a one-time code generated in the web UI.
  - `setup` — (Manage Channels) binds the current channel to one of the linked
    user's teams, or creates a new team. Shows a select menu; "Create a new team"
    opens a modal for the name.
  - `post` — posts the team's **overview** as a boxed embed: title (team name),
    a single dynamic schedule timestamp (`<t:unix:F>`/`<t:unix:R>`, shown in each
    viewer's own timezone — no more per-tz list), the roster grouped by role with
    abbreviated gear (Markdown lines, one player each, RSVP icon beside the name;
    each role header shows a `(filled/total)` count, where a slot is "filled" when
    someone is covering it — an assigned player who hasn't declined, or any slot a
    filler took; an open slot, or an assigned player who marked "not coming" with
    no filler yet, still needs a signup). Each
    player's name is the **resolved Discord display name** for their handle:
    mention/ID handles are looked up live (guild nick → global name → username,
    cached in `handleNameCache`, resolved by `resolveRosterNames`), and plain
    `@username` text handles are shown as the username (minus the `@`).
    a **Fill list** section, and groupings. Carries a button row
    (**✅ Coming**, **❌ Not coming** (RSVP), **Get My Build Details**) plus a
    **signup dropdown** (`post_fill_select`) whenever the roster has any fillable
    slots (open slots, or slots whose assigned player marked themselves **not
    coming**). Built by `discordfmt.BuildPost`; the bot wraps the parts in the
    embed and attaches the controls via `postComponents(team, fills, marks)`.
  - `recruit` — posts the team's **recruitment post** as an embed (the team's
    free-form `signup_post` body, or a default prompt) with a single **I'm
    Interested** button. Pressing it starts an interactive **DM intake flow**
    (see Member pool below). Built by `handleSignupPost` / `signupComponents`.
    (Formerly named `signup`; the command's internal `signup_*` component custom
    IDs and `signup_post` column are unchanged.)
  - `signup` — posts a one-off **pre-made trial run** (see "Pre-made trial runs"
    below). Implemented in `backend/cmd/bot/premade.go`. (Formerly named
    `premade`; the internal `premade_*` custom IDs and tables are unchanged.)
  - `roll` — posts a **randomly chosen ESO trial** as a boxed embed (trial name +
    its bosses) with a single **Re-roll** button that re-picks in place. The post
    is public, but **only the poster can re-roll**: their Discord ID is encoded in
    the button's custom ID (`roll_reroll:<id>`) and checked on press (others get
    an ephemeral notice). The pool is every group in `models.EncounterNameGroups`
    except `General`. Needs no team binding. Implemented in
    `backend/cmd/bot/roll.go` (`handleRoll` / `handleRollReroll`).
  - `login` — posts a public message linking to the web app (`APP_BASE_URL`).
    Replies ephemerally if `APP_BASE_URL` is unconfigured (`handleLogin`).
  - `status` / `unset` — show / remove the channel's team binding.
  - **Get My Build Details** button (`get_my_details`) → matches the presser to a
    roster slot (by Discord ID/mention in `players.discord_handle`, else
    case-insensitive username/global name); if no handle matches, it falls back to
    the open slot the user signed up to fill on this post (`fillSignupPlayer` over
    `discord_post_fills`), so fillers get their build too. Users on the general
    fill list (no specific slot) get an ephemeral note that there's no build to
    send yet. DMs them their build as a **boxed embed** (title + description) with underlined per-data-type headers
    (`discordfmt.PlayerDetail` returns `(title, description)`); falls back to an
    ephemeral embed if DMs are closed. Order: Player, Class & Race, Build, then one
    section per encounter (the encounter-name header is omitted when there's only
    one), and finally a **Requirements** section holding **Self-Required (after
    group buffs)** (penetration + crit damage) and, when the team doesn't cover
    them group-wide, a **Self Buffs** list of the self-providable Major/Minor buffs
    each player must bring themselves (`BUFFS` entries flagged `selfBuff: true` in
    `data.js`).
  - **✅ Coming / ❌ Not coming** buttons → record the presser's attendance for
    that specific post (`discord_rsvps`, keyed by message ID), then edit the post
    in place (`InteractionResponseUpdateMessage`). The post is fully re-rendered
    so each responder's status shows as a **✅/❌ icon beside their name** in the
    roster (matched to a slot by Discord ID/handle; no-response shows ▫️). The
    roster is plain Markdown (not a code block) so the icons render; there is no
    separate Attendance list, and responders who don't match a roster slot are
    omitted. A user has one RSVP per post; pressing the other button switches it.
    Re-posting starts a fresh tally.
  - **Signup dropdown** (`post_fill_select`, `handlePostFill`) → lets anyone sign
    up to cover a **fillable slot** or join the general **fill list**. A slot is
    fillable when it's **open** (a roster slot with no `discord_handle`) or its
    assigned player marked themselves **not coming** (RSVP ❌, an "absent" slot —
    `isFillableSlot` checks the live roster + RSVP marks). Options list each
    fillable, unclaimed slot (absent slots labelled "Fill for <name>") plus
    **Join the fill list** and **Remove my signup**; the dropdown is **always
    shown** (even on a fully staffed post) so people can volunteer as backups when
    no slot is open. Users already on the roster (matched
    via `matchPlayer`) are blocked from filling a slot or joining the fill list
    (they don't need to). Picking a slot stores a row in `discord_post_fills`
    (validated against the live roster + RSVPs; a taken slot returns
    `ErrSlotTaken`); a filled slot then renders the filler's name with a
    `` `fill` `` tag (or `` `fill for <name>` `` when covering an absent player)
    and an **automatic ✅** (signing up to fill counts as coming, independent of
    RSVPs). Fill-list backups appear in the **Fill list** section. A user holds at
    most one signup per post, so each choice replaces the prior one; the post is
    re-rendered in place via `renderPostUpdate` (shared with the RSVP buttons). No
    account link is required (like RSVPs).
  - **Returning player reclaims their slot**: when a roster player presses **✅
    Coming** and someone had signed up to fill their slot while they were out,
    `displaceFillerForReturningPlayer` moves that filler to the fill list
    (`DiscordStore.MoveFillToList`, slot → `PostFillList`) and DMs them that
    they've been bumped to backup (`dmFillerDisplaced`). Best-effort: failures are
    logged and never block the RSVP or post refresh.
  - **Slot opens for backups**: when a roster player presses **❌ Not coming**,
    `notifyFillListOfOpening` DMs everyone on the general fill list that their
    slot opened so they can sign up from the post (`dmFillListOpening`). Skipped
    when the presser isn't a roster player or the slot already has a filler.
    Best-effort (logged only).
- **"Posted by" footer**: both the `/coreteam post` overview and the premade
  `/coreteam signup` run post carry a Discord **embed footer** ("Posted by
  <name>") noting who posted. The overview uses the invoking user's display name
  (preserved across RSVP/fill updates by carrying the existing embed footer
  forward in `renderPostUpdate`); the run post resolves it from the run's
  `created_by` via `DiscordStore.GetLink` (`premadePoster`), so it survives the
  DB-driven re-renders too.
- **Account linking**: `users.discord_user_id` (unique) / `discord_username` link
  an app account to a Discord identity. The web UI ("Link Discord" topbar button,
  `#discord-modal`) calls `POST /api/discord/link-code` to mint a short,
  single-use code stored **only as a SHA-256 hash** in `discord_link_codes`
  (15-min TTL, mirrors `password_resets`); the bot's `/coreteam link` consumes it
  (`DiscordStore.ConsumeLinkCode` → `LinkUser`). `GET`/`DELETE /api/discord/link`
  report/clear the link. The hourly `startTokenCleanup` sweep also prunes expired
  link codes. **Note**: accounts created via "Sign in with Discord" (see the auth
  section) already have `discord_user_id` set, so they skip the link-code step
  entirely; the code flow remains for password accounts that want to link.
- **Channel bindings**: `discord_channels` maps `channel_id` → `team_id` (upsert;
  `DiscordStore.BindChannel`/`GetChannelTeam`/`UnbindChannel`).
- **RSVPs**: `discord_rsvps` (`028_discord_rsvps.sql`) stores one row per
  `(message_id, discord_user_id)` with a `'yes'`/`'no'` status
  (`DiscordStore.SetRSVP`/`ListRSVPs`).
- **Post fill signups**: `discord_post_fills` (`046_discord_post_fills.sql`)
  stores one row per `(message_id, discord_user_id)` with a `slot` (`0` =
  `models.PostFillList` general fill list; `> 0` = a specific open roster slot,
  unique per message via a partial index). Backs the post's signup dropdown
  (`DiscordStore.ClaimFill`/`LeaveFill`/`ListFills`); like RSVPs it's keyed by
  message ID so re-posting starts fresh.
- **Label data (codegen)**: the bot formats posts using
  `backend/internal/discordfmt` (`BuildPost` for the overview embed + `PlayerDetail`
  for the build-details DM, plus the GROUP-source half of `computePenCoverage` /
  `computeCritCoverage` for the DM's self-required pen/crit and the missing
  self-buffs list), which reads
  labels/abbreviations and the crit/pen coverage tables from
  `backend/internal/esoref`. `esoref/data_gen.go` is **code-generated** from the
  frontend's single-source data (`frontend/js/gear-skills.js` + `data.js`) by
  `tools/gen-esoref/gen.js` — it emits the label maps plus the structured
  `CritGroupSources` / `PenGroupSources` / `PenExtraSources` / `Buffs` tables and
  the `CritCap` / `CritBase` / `PenTarget` / … constants (types are hand-written
  in `esoref/pencrit.go`). Run `node tools/gen-esoref/gen.js` (or `go generate
  ./internal/esoref`) whenever that frontend data changes, then commit the result.
- **Config**: `DISCORD_BOT_TOKEN` (required to run the bot), optional
  `DISCORD_APP_ID` and `DISCORD_GUILD_ID` (set the guild ID for instant,
  dev-friendly command registration; empty = global), and `APP_BASE_URL` (the
  public web-app URL the bot links to when inviting a finished signup to sign in;
  stored as `bot.appBaseURL`). Loaded in `config.go`
  (`Config.Discord`), wired via `docker-compose.yml` + `.env`. Run the bot with
  `docker compose --profile bot up`. See `docs/DEPLOYMENT.md` for the Discord
  developer-portal setup (create app + bot, invite with the `bot` and
  `applications.commands` scopes). "Sign in with Discord" is a **separate,
  server-side** feature configured with `DISCORD_CLIENT_ID` /
  `DISCORD_CLIENT_SECRET` / `DISCORD_OAUTH_REDIRECT_URL` (`config.DiscordOAuth`,
  used by `cmd/server`, not the bot).

## Pre-made trial runs (current)

A **pre-made trial run** is a one-off, scheduled event built from a team that has
the `pre_made` flag on (see "Pre-made trial run" under Teams). Tables in
`036_premade_runs.sql`; store in `backend/internal/models/premade.go`
(`PremadeStore`); bot flow + scheduler in `backend/cmd/bot/premade.go` and
`backend/cmd/bot/scheduler.go`.

- **Command** (`/coreteam signup`, `handlePremade`): resolves the runner's
  **linked** app account (`GetUserByDiscordID`), confirms they have at least one
  **runnable template** here (`listRunnablePremadeTeams` = owned/editable
  pre-made teams **plus** templates published to this guild), then opens a **DM
  conversation** (the slash command itself only replies "check your DMs"). The
  conversation lives in `backend/cmd/bot/premade_dm.go`, driven by gateway DM
  messages (`onMessageCreate`) plus one component select for the one-time
  timezone pick. State is persisted per Discord user in `premade_signup_sessions`
  (`040_dm_signup.sql`) so a half-finished signup survives a bot restart; the
  `step` column names the awaited answer: **team** (reply a number when >1
  runnable) → **tz** (timezone select, only when `users.timezone` is unset —
  reuses the intake's `signupTimezones`/`tzOffsetLabel`, then remembered; users
  can change it later with `/coreteam timezone`) →
  **title** (free text) → **when** (free-text date/time parsed by
  `github.com/olebedev/when` in the user's zone; `parseWhen`/`normalizeMilitaryTime`
  also handles `2100`-style military times) → **confirm** ("yes" or send a new
  time) → **body** (free-text post-body override, or "skip" for the template
  default). On confirm, `finishPremadeDM` re-checks owner/editor **or**
  published-to-guild, creates the run, and posts the announcement publicly in the
  originating channel via `ChannelMessageSendComplex`. Requires the privileged
  **MESSAGE CONTENT** intent (see `cmd/bot/main.go`).
- **Post** (`discordfmt.BuildPremadePost`): title + a single `<t:unix:F>`/`:R`
  schedule timestamp + the run's body (`premade_runs.post_override` when set,
  else the team's `premade_post`) + a per-slot roster showing
  each slot's name/role/class and either the claimant's mention or "open". Each
  role header shows a `(claimed/total)` signup count so it's easy to see how many
  slots are still open.
  Controls (`premadeComponents`): a **claim** select listing only open slots
  (`premade_claim`; disabled "all taken" placeholder when full), a **details**
  select listing all slots (`premade_details`), and a final button row
  (`premadeActionRow`) with **Un-Sign** (`premade_leave`), **Edit run**
  (`premade_edit`), and **Delete run** (`premade_delete`).
- **Edit** (`premade_edit` → `handlePremadeEdit`, `cmd/bot/premade_edit.go`):
  visible to everyone but gated to the run's creator or the team's owner/editor
  (`canEditRun`). It opens a DM and reuses the `premade_signup_sessions` row in
  **edit mode** (`mode='edit'`, `run_id` set; `041_premade_run_edit.sql`) to walk
  a field menu (`premade_edit_field`: title / when / body / done). Each applied
  field calls `PremadeStore.UpdateRun` and re-renders the posted announcement in
  place via `refreshPremadePostMessage` (`ChannelMessageEditComplex`), then
  re-shows the menu so several fields can be edited in one sitting.
- **Delete** (`premade_delete` → `handlePremadeDelete`, `cmd/bot/premade_edit.go`):
  visible to everyone but gated to the run's creator or the team's owner/editor
  (`canEditRun`; non-editors get an ephemeral rejection). Deletes the posted
  message (and thread, if any) and marks the run cleaned up (`MarkCleanedUp`) so
  it's no longer active.
- **Cancel** (`isCancel` in `cmd/bot/premade_dm.go`): typing a cancel word
  (cancel / stop / quit / abort / exit / nevermind) in the DM aborts whatever
  conversation is active. `onMessageCreate` checks it before the step switch, so
  it works at any step of both the create and edit flows — it deletes the
  session and confirms (nothing is posted or changed).
- **Simple signup** (`teams.simple_signup`, see Teams above): when on, the post
  hides class/gear (`premadeRosterLines`) and drops the **details** select; the
  **claim** select instead lists **roles** with open counts
  (`premadeSimpleComponents`) and claiming a role takes the first open matching
  slot (`claimSimple` → `firstOpenSlotForRole`, retrying on `ErrSlotTaken`).
- **Waitlist** (`teams.waitlist_enabled`, see Teams above): when on,
  `premadeWaitlistRow` adds a **join waitlist** select (`premade_wait`) listing
  roles that are currently full; the post shows a per-role "__Waitlist__" block
  (`premadeWaitlistLines`). `handlePremadeWaitlist` queues the user
  (`JoinWaitlist`; refuses if they already hold a slot). When a slot frees up —
  on **leave** (`handlePremadeLeave`) or when a user **switches** to a different
  slot (`handlePremadeClaim`/`claimSimple`) — the freed slot's role queue is
  auto-promoted via `promoteFreedSlot`→`PromoteToSlot` and the promotee is DM'd
  (`dmPromoted`). The 15-min thread
  still tags **claimed** players only.
- **Claims** (`PremadeStore.ClaimSlot`): per-slot, **one slot per user** —
  claiming releases the user's prior claim in the same transaction; the
  `(run_id, slot)` unique index locks a slot (a clash returns `ErrSlotTaken` →
  ephemeral "already taken"). **Leave** drops the claim. **Details** DMs that
  slot's `discordfmt.PlayerDetail` (ephemeral fallback if DMs are closed). Claim
  and leave re-render the post in place (`InteractionResponseUpdateMessage`).
- **Scheduler** (`runScheduler`, started as a goroutine from `cmd/bot/main.go`,
  tied to the shutdown context) — the bot's **only** time-based worker. It polls
  every `schedulerInterval` (60s, plus an immediate pass on startup):
  - **At post time** (`finishPremadeDM` → `createRunThread`): a discussion thread
    is created off the post (`MessageThreadStartComplex`) with an intro message
    inviting players to chat; the id is stored via `SetRunThread` (leaving
    `thread_started_at` NULL).
  - **15 min before** (`DueThreadRuns`): posts a message in that thread pinging
    every signup; `MarkThreadStarted` (records the ping ran). If the thread is
    somehow missing (older run, or post-time creation failed) it's created here as
    a fallback.
  - **2 h after** (`DueCleanupRuns`): deletes the thread + post; `MarkCleanedUp`.
  Both are tracked by timestamp columns on `premade_runs`, so each fires **exactly
  once** and **catches up** if the bot was offline at the trigger time (cleanups
  are processed before threads, so a long-offline finished run is removed rather
  than getting a late thread). Thread deletion (both here and the manual **Delete
  run** button) uses `threadCleanupID`: a thread is started *off the post*, so its
  channel id equals the post's message id — when `thread_id` wasn't recorded but a
  start was attempted (`thread_started_at` set), cleanup falls back to the message
  id so an out-of-band thread (bot restarted mid-start, or a channel that
  auto-creates threads) is still removed rather than orphaned. The bot needs
  **Create Public Threads / Manage Threads / Manage Messages** permissions for
  these (see `docs/DEPLOYMENT.md`).

## Member pool / recruitment (current)

The **member pool** (`team_roster_members`) is a per-team list of prospective
players, **separate** from the 12 fixed roster slots (`players`) and from
app-account sharing (`team_members`). It captures availability/role/class
interest gathered via Discord, plus manual web entries. (A team can opt into
**auto-sharing** the team as viewer with everyone in this pool — see
`auto_share_pool_viewers` under Teams above.)

- **Schema** (`030_team_members_pool.sql`): adds `teams.signup_post TEXT` (the
  free-form `/coreteam recruit` body) and the `team_roster_members` table —
  `discord_user_id` (NULL for manual entries), `discord_username`,
  `display_name`, `timezone` (IANA; the zone the hours are expressed in), `days
  TEXT[]` (`mon..sun`), `availability JSONB` (`{ "mon": { "start": 18, "end":
  22 } }`, where `start` is 0–23 and `end` is 1–24 in `timezone`; **24 means
  midnight / end of day** so a window can run to the day's end), `roles TEXT[]`,
  `classes_by_role JSONB`
  (`{ "tank": ["dragonknight"] }`), `status` (`draft` while the DM intake is in
  progress → `complete`), `step` (current intake step), and `source`
  (`discord`/`manual`). A partial unique index on `(team_id, discord_user_id)`
  means re-running signup **updates** the same row; manual entries are
  unconstrained. Standard `set_updated_at` trigger.
- **Backend**: `models.RosterMember` + `MemberStore`
  (`backend/internal/models/member.go`) own persistence and the intake
  step/status constants (`MemberStatus*`, `MemberStep*`). Web endpoints
  (`backend/internal/handlers/members.go`, all JWT-protected, capped at
  `maxRosterMembers = 200`): `GET /api/teams/{id}/roster-members` (any team
  role), `POST /api/teams/{id}/roster-members` (editors; manual add),
  `PUT /api/teams/{id}/roster-members/{memberID}` (editors; edit any member's
  availability/roles/classes — `MemberStore.Update`, leaving intake
  status/step/source untouched), and
  `DELETE /api/teams/{id}/roster-members/{memberID}` (editors). Days/roles/
  classes are normalized/validated against the same allow-lists as the roster;
  availability start hours clamp to 0–23 and end hours to 1–24.
- **Discord DM intake** (`backend/cmd/bot/intake.go`): the **I'm Interested**
  button (`onSignupComponent`/`handleSignupJoin`) creates or reuses the user's
  draft row and walks a **5-step**, component-only questionnaire (select menus —
  no privileged message intents): **1** days → **2** timezone (options show the
  current UTC offset via `tzOffsetLabel`) → **3** start/end hours per chosen day
  (start 00:00–23:00; end 01:00–**24:00**, where 24:00 = midnight) plus
  quick-apply buttons (`signup_span` — an **All day** preset and one button per
  distinct window already entered on an earlier day, via `quickSpanRows`) → **4**
  roles → **5** classes per chosen role, then a
  summary and `status = complete`. When the team has **auto-share** enabled
  (`AutoSharePoolEnabled`), the summary closes with an **optional** link to the web
  app (`APP_BASE_URL/login.html`, via `signupWebAppInvite`) inviting the user to
  create their account with "Continue with Discord" (which links automatically and
  surfaces the team); omitted when auto-share is off or `APP_BASE_URL` is unset.
  Each step persists progress; the component
  custom IDs (prefixed `signupPrefix`) carry the member row id plus the current
  day/role so each stateless interaction can resume.
- **Frontend — Members page**: a **Members** button on the team detail toolbar
  opens `#members-view` (`showView("members")` / `openMembers` /
  `renderRosterMembers` in `app.js`). It shows aggregate **role coverage**
  chips, an **availability heatmap** (days × 24h, each member's hours shifted
  from their timezone into the **viewer's** local zone and summed — same DST
  caveat as the trial schedule), and per-member **cards** (availability +
  roles/classes, with a Discord/Manual/“In progress” badge; editors can **edit**
  or remove each one). Editors can **add a member manually** or **edit** any
  member (including Discord-sourced ones — e.g. to set/adjust availability time
  limits) via the same in-page form (timezone, day windows with start 00:00–23:00
  and end 01:00–24:00, role/class checkboxes). The pool is also loaded on `openTeam` so the
  roster's **Discord handle** field is an open **combobox** (`createComboBox` in
  `components.js`) — its suggestions come from the pool but free-form text is
  still allowed. New CSS lives under the "Combobox" / "Member pool" blocks in
  `styles.css` (heatmap, role chips, member cards, combobox panel).

## Detail page layout (collapsible sections)

- The team detail page is a stack of **collapsible** cards (`.collapsible` /
  `.section-collapsible` with a `.collapse-toggle` chevron in the head and a
  `.collapsible-body`). A topbar **Collapse all / Expand all** toggles the
  sections; the Roster card has its own **Collapse / Expand players** (each
  `.player-slot` is independently collapsible). `setCollapsed` /
  `expandAncestors` in `app.js` drive this; jumping to a target (side nav)
  auto-expands its collapsed ancestors.
- The **Group Stats** card consolidates the **Group Buffs**, **Crit Damage**, and
  **Penetration** sub-panels (formerly three separate cards) into one collapsible
  section. The left **floating jump nav** links to Top, Group Stats, Groupings,
  and each player.

## Buffs model (current)

- **What it is**: a team wants to cover a fixed list of ESO buffs. The app shows
  how many are covered **for the selected encounter** plus a per-buff breakdown.
- **No backend/DB**: buffs are pure **frontend reference data + a computed view**.
  The only persisted change buffs required is the per-encounter `potions` loadout
  (above). Coverage is recomputed client-side from data already in memory.
- **Data** (`frontend/js/data.js`): `BUFFS` is an array of
  `{ value, label, desc, sources, selfBuff? }` where `sources` maps a category to
  providing keys: `gear`, `skills`, `potions` (per-encounter loadout) and
  `masteries`, `classes`, `skillLines` (roster build). The seeded source keys are
  **sensible placeholders** — adjust them to the exact ESO sources without
  changing the shape. Keys reference the existing master data (gear sets, skills,
  potions, class masteries/classes/skill lines). The optional `selfBuff: true`
  flag marks a personal Major/Minor buff a player can self-maintain; the Discord
  bot lists self-buffs the team doesn't cover group-wide in the build-details DM.
- **Coverage rule** (`computeBuffCoverage(players, loadoutBySlot)` in `data.js`):
  a buff is **met** if at least one player provides at least one of its sources.
  Build sources honor subclassing — a `subclassed` player contributes their
  `skill_line_*`; a non-subclassed player contributes their `class` + `mastery_*`.
  Loadout sources (gear/skills/potions) always count. A player whose loadout
  slots a grimoire skill also covers any buff listed in its `scribed_buffs` (the
  `scribed` category). Returns `{ total, met,
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
  base (`CRIT_BASE`) is modelled as a group source. A few sources raise an
  individual player's cap above 125 — `CRIT_CAP_BONUS_SOURCES` (e.g. Above and
  Beyond's +30%, 125 → 155), applied per player via `playerCritCap(ctx)`.
- **Three buckets**: `group` (whole team — any one player providing it counts),
  `target` (a debuff on the boss — any one player applies it), and `self` (only
  that player). Per player, effective crit = `group + target + self`; they meet
  their own cap when that reaches it (125, or higher with a cap bonus). **Solo
  required** = `125 - group - target` (the standard cap; cap-bonus players need
  more from their own sources to re-cap).
- **No backend math**: like buffs, this is frontend reference data + a computed
  view. The only persisted inputs are the per-encounter crit columns on
  `encounter_loadouts` (cp_blue/crit_dmg/mundus/armor) and `players.race`.
- **Data** (`frontend/js/data.js`): `CRIT_GROUP_SOURCES`, `CRIT_TARGET_SOURCES`
  (each `{value,label,pct,detect}` where `detect` maps a category to keys), and
  `CRIT_SELF_SOURCES` (each `{label,pct,type,...}`; `type` ∈
  `mundus|cp|gear|race|classPassive`). Medium-armor Dexterity is
  `CRIT_MEDIUM_PER_PIECE` (2%) × medium pieces; crit-damage sources
  (`CRIT_DMG_SOURCES`, the crit weapon-line passives) take the MAX of selected
  entries (one active bar). Several source keys (Minor Force, Minor
  Brittle) are **placeholders** — one-line edits.
- **Rule** (`computeCritCoverage(players, loadoutBySlot)`): class-passive
  detection honors subclassing (non-subclassed → `class`; subclassed → the linked
  `skill_line_*`). Returns `{ cap, base, group, target, soloRequired,
  groupSources, targetSources, players:[{slot, self, total, cap, met, deficit,
  sources}] }` — `cap` is the team-wide base, each player's `cap` is their
  effective max (≥ base).
- **UI** (`app.js`/`index.html`): a **Crit Damage** card shows group/target/solo
  stats with a `Details` button (`#crit-modal` lists detected group/target
  sources + each player's breakdown). Each roster slot has crit inputs (Blue CP +
  Crit Dmg sources chip columns, Mundus select, H/M/L armor steppers) and a `.crit-label`
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
  `type` ∈ `cp|gear|mundus|race|classPassive`; an optional `scaled`
  `{per,ctxKey,unit}` multiplies a per-unit pen by a per-loadout count instead of
  a flat `pen` — `splintered_secrets` uses `splinteredSecretsSkills`
  (`splintered_secrets_skills`, 0–5, default 2; 1240 each), `force_of_nature` uses
  `forceOfNatureStatus` (`force_of_nature_status`, 0–5, default 5 = 3300)),
  `PEN_EXTRA_SOURCES` (the
  `pen_extra` chip options, each `{value,label,pen,bucket}` where `bucket` ∈
  `self|group`; an optional `maxStack` (e.g. `set_piece_bonuses` = 5) lets a self
  source be added multiple times, stored as the key repeated once per stack and
  summed in the calculator). Light armor is `PEN_LIGHT_PER_PIECE` (939) × light pieces; arena
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
  `refreshCritCoverage()`). Conditional per-slot inputs are shown only
  when relevant: `catalyst_elements` (Elemental Catalyst equipped),
  `weapon_damage` (Anthelmir's Construct equipped),
  `splintered_secrets_skills` (player has Herald of the Tome — `slotHasHeraldOfTome`),
  `force_of_nature_status` (the Force of Nature blue CP chip is slotted),
  `scribed_buffs` (a grimoire skill is slotted), and `banner_bearer_focus`
  (the Banner Bearer grimoire is slotted).

## Live collaboration (current)

Multiple editors can work on the same team at once; everyone's view stays fresh
and concurrent edits don't silently clobber each other. Three cooperating pieces:

- **Live refresh (SSE + Postgres LISTEN/NOTIFY)**. Migration 044 adds a
  `notify_team_change()` trigger on every collaborative table (teams, players,
  encounters, encounter_loadouts, groupings + groups/members, team_members,
  team_roster_members). Each write `pg_notify`s the `team_changed` channel with
  `{team_id, kind}` where `kind` is the coarse area that changed
  (`team`/`encounter`/`grouping`/`members`/`pool`). The payload is row-agnostic
  so Postgres collapses the many per-row notifications of a bulk save into one.
  Because the **trigger** does the publishing, writes from *any* process count —
  including the **Discord bot**, a separate process. `internal/realtime.Hub`
  (started in `cmd/server`) LISTENs on a dedicated connection and fans each
  notification out to the per-team **Server-Sent Events** subscribers. The
  endpoint is `GET /api/teams/{id}/events`; since `EventSource` can't send an
  `Authorization` header it authenticates via the `access_token` query param
  (validated in `handlers/events.go`, not the bearer middleware). nginx has a
  dedicated `location` for it with buffering off + a long read timeout; the
  server sends a `: ping` keepalive every 25s and clears its write deadline for
  that connection. The client (`app.js`) holds one `EventSource` while a team is
  open, and on a change event **refetches + re-renders** — but only when the user
  isn't mid-edit (`isBusyEditing`), so a collaborator's change never interrupts
  in-progress typing. It ignores the brief echo of its own just-saved write
  (`localSaveQuietUntil`) and reconnects with a refreshed token if the stream
  drops (e.g. token expiry).
- **Presence**. The Hub tracks who is connected per team (username from the SSE
  token) and broadcasts a `kind:"presence"` event (the viewer list) whenever the
  set changes. The client shows a small `#presence-indicator` badge ("N others
  here"). In-process only — fine for the single-backend deployment.
- **Optimistic concurrency**. `teams`, `players`, and `encounter_loadouts` each
  have an `updated_at` token. Version-checked saves (`TeamStore.Save`,
  `TeamStore.SavePlayer`, `EncounterStore.SaveLoadoutSlot`) update only if the
  caller's `expected_updated_at` still matches, else return
  `models.ErrVersionConflict` → **409**. The client sends the token it last saw
  and, on 409, discards its now-superseded local edits and reloads the latest.
- **Finer-grained autosave (fewer conflicts)**. The client tracks dirty areas
  separately (`dirtyMeta`, `dirtyPlayerSlots`, `dirtyLoadoutSlots`) and saves
  only what changed via per-slot endpoints — `PUT /api/teams/{id}/players/{slot}`
  and `PUT /api/teams/{id}/encounters/{eid}/loadouts/{slot}` — plus a meta-only
  team `PUT` (empty `players`). The old whole-team / whole-encounter PUTs still
  exist (back-compat) but the UI no longer uses them. So two editors changing
  *different* slots never collide. A request also carries an `X-Client-Id` header
  (a per-tab id, useful for correlation).

## Request flow

Browser → nginx (`frontend`, port 80→`FRONTEND_PORT`) → `/api/*` proxied to
`backend:8080` → `db:5432`. Because `/api` is same-origin via the proxy, CORS is
generally not triggered (the backend still sets CORS headers as defense). The
live-collaboration SSE stream (`/api/teams/{id}/events`) is a long-lived
streaming response on the same path, proxied with buffering off.

## Where to make changes

- **New API endpoint**: add a handler in `backend/internal/handlers/` (handlers
  are split by area: `handlers.go` for the `Server`/routing/auth + shared
  helpers, `teams.go`, `encounters.go`, `groupings.go`, `password_reset.go`, and
  admin in `admin.go`) and register it in `Routes()`. Protected routes wrap with
  `s.tokens.Middleware(...)`. `Server` is constructed from `handlers.Config`
  (a struct), so adding a dependency means adding a field there and in `New`.
- **New table / query**: add migration in `database/migrations/` (idempotent),
  add a store + methods in `backend/internal/models/`.
- **New page / UI**: add an `.html` file in `frontend/`, a script in
  `frontend/js/`, and reuse tokens/classes from `frontend/css/styles.css`
  (see `docs/STYLE_GUIDE.md`). Keep concerns separated: network calls/endpoint
  helpers go in `js/api.js`; shared reference data + display helpers go in
  `js/data.js`; reusable widgets go in `js/components.js`.
- **Config**: env vars are read in `backend/internal/config/config.go` and wired
  through `docker-compose.yml` + `.env`.
- **Discord bot**: command/interaction handlers live in `backend/cmd/bot`
  (`main.go` wiring, `commands.go` handlers). Post formatting lives in
  `backend/internal/discordfmt`; the labels it uses are code-generated into
  `backend/internal/esoref/data_gen.go` from the frontend JS — re-run
  `node tools/gen-esoref/gen.js` after changing `gear-skills.js`/`data.js`.

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
docker compose up --build          # run the whole stack (without the bot)
docker compose --profile bot up    # also run the Discord bot (needs DISCORD_BOT_TOKEN)
docker compose run --rm seed       # (re)apply migrations + ensure test user
cd backend && go build ./...       # compile backend (server + seed + bot)
cd backend && go vet ./...         # static checks
node tools/gen-esoref/gen.js       # regenerate Go label data from frontend JS
```

## Status / TODO ideas

- [x] Token refresh / logout server-side invalidation (short-lived access JWT +
      DB-backed rotating refresh tokens; `/api/refresh`, `/api/logout`).
- [x] Forgot/reset password via email (single-use hashed reset tokens;
      `/api/forgot-password`, `/api/reset-password`; SMTP or dev log mailer).
- [x] Teams: ownership, sharing, and a 12-player roster (name/discord/role/class).
- [x] Encounters: per-team named fights with per-player gear/skill loadouts
      (+ per-team encounters-enabled toggle).
- [x] Groupings: per-team named sets of numbered groups (e.g. ice cages).
- [x] Admin users + user management (list/add/remove/promote) + registration toggle.
- [x] Discord bot: `/coreteam` post overview + DM per-player details, account
      linking, channel→team binding, per-team post/DM footers (`backend/cmd/bot`).
- [x] Rate limiting on auth endpoints (at the nginx edge; see `docs/DEPLOYMENT.md`).
- [ ] Expand the gear-set/skill/boss seed data to full ESO coverage.
- [ ] Tests (handlers, auth, models).
