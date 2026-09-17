---
name: core-team-builder-dev
description: Development and security guide for the Core Team Builder repo — an ESO trial-roster web app (Go stdlib API, build-free static frontend + nginx, PostgreSQL, Discord bot, Docker Compose). Use when adding or changing API endpoints, database migrations, frontend HTML/CSS/JS, ESO reference data, or /coreteam bot commands in this repo, and when reviewing, debugging, or security-reviewing that code.
---

# Core Team Builder development

## Security comes first

This app holds user accounts, credentials, and private team data, and it exposes
a Discord bot to untrusted servers. **Every change — feature, refactor, bug fix,
or review — is also a security change.** Before you call any task done, walk the
[Security review checklist](#security-review-checklist) below.

Three rules override convenience, always:

1. **Trust nothing from the client.** Every request body, query param, path
   value, Discord interaction payload, and uploaded file is attacker-controlled.
   Validate and authorize server-side even when the UI already prevents it.
2. **Fail closed.** An unknown role, a missing membership row, an unparseable
   id, or an error mid-check denies the request. Never default to allow.
3. **Don't leak.** Inaccessible teams return `404` (not `403`) so other users'
   data is not revealed; `/api/forgot-password` always returns the same generic
   message; login failures are constant-time and generically worded. Preserve
   these behaviours — they are deliberate anti-enumeration measures, not sloppy
   error handling.

When a requirement and a security control conflict, raise it rather than
weakening the control silently.

## Orient before editing

`docs/AGENT_CONTEXT.md` is the authoritative context file, but it is ~1550 lines —
do not read it end to end. Grep for the section you need and read that slice:

```bash
rg -n '^## ' docs/AGENT_CONTEXT.md        # section index
```

Sections worth knowing exist: auth, admin & user management, teams/rosters/
encounters/groupings models, positioning images, Discord bot, pre-made trial
runs, member pool, buffs/crit/penetration models, live collaboration, request
flow, "Where to make changes".

| Doc | Use it for |
|-----|------------|
| `docs/AGENT_CONTEXT.md` | Feature-by-feature behaviour; read the matching section first |
| `docs/ARCHITECTURE.md` | Component diagram + full table-by-table data model |
| `docs/DEVELOPMENT.md` | Env vars, full API reference, migration how-to, curl recipes |
| `docs/STYLE_GUIDE.md` | Go/SQL/CSS/JS/HTML conventions — follow it, don't invent style |
| `docs/DEPLOYMENT.md` | Production posture, nginx rate limits, TLS |

## Layout

```
backend/cmd/{server,seed,bot}     entrypoints; each main.go delegates to run() error
backend/internal/handlers/        HTTP handlers, split by area; Routes() wires them
backend/internal/models/          stores (data access) + eso.go allow-lists/validators
backend/internal/{auth,config,db,email,realtime,discordfmt,esoref}/
database/migrations/NNN_*.sql     idempotent, applied by seed and by first-boot init
frontend/{index,login,reset,discord}.html + js/ + css/styles.css   no build step
tools/gen-esoref/gen.js           frontend data -> backend/internal/esoref/data_gen.go
```

Script load order matters in the frontend: `api.js` → `gear-skills.js` →
`data.js` → `components.js` → page entry (`app.js` / `auth.js`). There is no
module system; files expose top-level `const`s.

## Playbooks

**New or changed API endpoint.** Add the handler to the file for its area
(`teams.go`, `encounters.go`, `groupings.go`, `members.go`, `rosters.go`,
`roster_images.go`, `discord.go`, `admin.go`, `password_reset.go`) and register
it in `Routes()` in `handlers.go` with a method-aware pattern (`"POST /api/..."`).
Protected routes wrap with `s.tokens.Middleware(...)`; admin routes additionally
go through `requireAdmin`. New dependencies become fields on `handlers.Config`
and are wired in `New`. JSON goes through `writeJSON` / `writeError`. Update the
API table in `docs/DEVELOPMENT.md`.

Security work that is part of the endpoint, not a follow-up: decide the required
role before writing the body and enforce it with the same `teams.Access` /
`requireAdmin` helpers the neighbouring handlers use — a new route that only
checks authentication is an authorization bug. Validate every field against an
allow-list or explicit bounds (see `internal/models/eso.go` for the pattern)
rather than trusting the UI. Never echo another user's data, internal errors, or
SQL text back in a response. If the route is sensitive (auth, sharing, password,
uploads), give it an nginx `limit_req` zone in `frontend/nginx.conf` alongside
the existing `auth` / `share` / `apigen` zones. All bodies are capped by
`withMaxBytes` (1 MiB default, 6 MiB for image uploads) — if a new route needs
more, raise it deliberately in both nginx and the backend, never remove the cap.

**New table or column.** Add `database/migrations/NNN_description.sql`, zero-padded
and idempotent (`CREATE TABLE IF NOT EXISTS`, `ADD COLUMN IF NOT EXISTS`,
`ON CONFLICT`) — there is no migration-version table, so every file must be safe
to re-run. Add or extend a store in `backend/internal/models/` with parameterized
queries only. Apply with `docker compose run --rm seed`. Document the columns in
`docs/ARCHITECTURE.md`. If the table participates in collaborative editing, check
migration `044`/`048` and add the `notify_team_change()` trigger so SSE clients
refresh.

Security work that is part of the migration: give the table the `FOREIGN KEY`
and `ON DELETE CASCADE` relationships that keep a deleted user's or team's data
from being orphaned and later served to someone else, and add the `UNIQUE`
constraints and `CHECK` bounds that back up handler-side validation — the DB is
the last line of defence when a handler is wrong. Any column holding a
credential, token, or code stores a **SHA-256 hash only**, never the value
(follow `refresh_tokens`, `password_resets`, `discord_link_codes`). Tokens also
need an `expires_at` and a single-use or revocation marker, plus pruning in the
hourly sweep in `cmd/server/main.go`. Never put a secret or a real user's data
in a migration's seed values.

**Frontend change.** Keep concerns split: `api.js` is the API client only (never
`fetch` directly from page scripts), `gear-skills.js`/`data.js` hold reference
data, `components.js` holds reusable widgets, `app.js`/`auth.js` are page logic.
Style with the `:root` tokens in `css/styles.css` — no hardcoded hex or pixel
values. Then **bump the `?v=` query on every asset you touched** in the HTML files
that reference it (`index.html`, `login.html`, `reset.html`, `discord.html`);
versions are tracked per asset, so bump that asset's number past its current one.
The frontend is baked into the nginx image, so a rebuild is required to see it.

Security work that is part of the frontend change: **never interpolate
user-controlled data into `innerHTML`.** The established pattern is to build the
static markup with `innerHTML` and then assign the value with `textContent` —
see the member row in `app.js`, which writes `<strong></strong>` and then sets
`label.querySelector("strong").textContent = m.username`. Player names, team
names, captions, Discord handles, and bot footers are all attacker-controlled.
The Content-Security-Policy in `frontend/security-headers.conf` forbids inline
and third-party scripts (`script-src 'self'`), so do not add inline `<script>`,
inline event handlers, `eval`, or a CDN dependency — if a change seems to need a
CSP relaxation, that is a design problem to raise, not a header to edit. Because
nginx `add_header` does not inherit into a location that sets its own headers,
any new `location` block must re-`include` that snippet. Tokens live in
`localStorage` and are read only by `api.js`; don't copy them into the DOM, a
URL, a log line, or a query string.

**Discord bot change.** Handlers live in `backend/cmd/bot`: `commands.go` holds
`botCommands` plus the `onInteraction` / `onCommand` / `onComponent` /
`onModalSubmit` dispatchers, with feature files alongside (`premade*.go`,
`post_*.go`, `intake.go`, `actionlog.go`, `roll.go`, `scheduler.go`). Post
formatting belongs in `internal/discordfmt`, not in the command handlers.
Component custom IDs must stay under Discord's 100-character limit — that is why
IDs are encoded compactly (see `postOrigin`) — and existing IDs are deliberately
kept after renames so already-posted messages keep routing. **Adding, renaming,
or removing a `/coreteam` subcommand requires updating `helpCommands` in
`backend/cmd/bot/help.go` in the same change**; `TestHelpCoversEverySubcommand`
enforces it both ways.

Security work that is part of the bot change: the bot runs in servers you do not
control, so **re-check permission on every interaction, not just when the
message was posted**. A custom ID is client-supplied — anyone can press a button
or replay an ID with edited content — so re-derive the team and the caller's
rights server-side (`canPressRestricted`, `canActAsRunAdmin`,
`hasDesignatedEditRole`, the channel binding) instead of trusting whatever the
ID encodes, and re-check time-based locks like `postSignupsClosed` even when the
control rendered disabled. Confirm the Discord user is linked to an app account
and has access to that team before revealing roster or build details. Keep
responses that contain private data **ephemeral**, and send build details by DM
as the existing flows do. Never put a token, link code, or internal id in a
public message or embed.

**ESO reference data (gear sets, skills, bosses, labels).** Edit
`frontend/js/gear-skills.js` or `frontend/js/data.js`, then regenerate the Go
copy so the bot renders the same names:

```bash
node tools/gen-esoref/gen.js     # writes backend/internal/esoref/data_gen.go (committed)
```

Keys in the frontend tables mirror the backend allow-lists in
`internal/models/eso.go` and `encounter.go`; keep them in sync.

## Non-negotiables

Security rules — a change that breaks one of these is wrong even if it works:

- **Parameterized SQL only**; never concatenate or format a value into a query.
- **No hardcoded secrets or credentials.** Everything comes from the environment
  (`backend/internal/config/config.go` → `docker-compose.yml` → `.env`).
  `.env` is gitignored; `.env.example` documents the keys with placeholder
  values. Never commit a real secret, and never print one in output or a log.
- **Never store or log plaintext passwords**, tokens, link codes, or reset
  codes. Passwords are bcrypt (cost 12, min length 8); refresh tokens, reset
  tokens, and Discord link codes are persisted only as SHA-256 hashes.
- **Authorization is server-side.** Team access goes through the `team_members`
  role (owner/editor/viewer); admin routes re-check `users.is_admin` via
  `requireAdmin`. Frontend and Discord-side gating are convenience only.
- **Never render user-controlled data through `innerHTML`** — use `textContent`.
- **Preserve the anti-enumeration behaviours**: `404` for inaccessible teams,
  generic forgot-password response, constant-time generic login failure.
- **Keep the defence-in-depth layers intact**: request-body caps
  (`withMaxBytes`), nginx rate-limit zones, the CSP and security headers,
  content-type sniffing on uploads, and the container hardening in
  `docker-compose.yml` (read-only rootfs, dropped capabilities,
  `no-new-privileges`, memory and pid limits). Removing one needs a stated
  reason, not silence.
- **New third-party dependencies are a security decision.** Prefer the standard
  library; justify any addition to `go.mod`, and add no frontend dependencies at
  all (the CSP and the no-build-step design both forbid them).

Craft rules:

- Migrations are idempotent and never edited after the fact — add a new file.
- `gofmt` (tabs) for Go; two-space indent, semicolons, double quotes for JS/HTML.
- Comments explain *why*, not *what*.
- Update the relevant doc (`AGENT_CONTEXT`, `ARCHITECTURE`, `DEVELOPMENT`,
  `STYLE_GUIDE`) in the same change when behaviour or conventions move.

## Security review checklist

Run this over your own diff before finishing, and over any code you are asked to
review. Call out what you checked and anything you could not verify.

- **AuthN/AuthZ** — Is every new route, bot interaction, and SSE stream behind
  the right check? Is the *role* right (viewer vs editor vs owner vs admin), not
  just "logged in"? Can a user reach another user's team, roster, encounter,
  image, or member pool by supplying a different id? Are ids re-resolved
  server-side rather than trusted from the request?
- **Input validation** — Is every field bounded and allow-listed where the
  domain allows it (roles, classes, skill lines, encounter names, days, slots
  1–12, armor 0–7)? Are lengths capped? Are uploads size-capped and
  content-type-sniffed rather than trusting the declared type or filename?
- **Injection** — Parameterized SQL everywhere? No user data in `innerHTML`, in
  a shell command, in a URL built by string concatenation, or in a log line that
  a reader would mistake for trusted output?
- **Secrets & tokens** — Nothing plaintext at rest or in logs. New tokens are
  random, hashed, expiring, single-use or revocable, and pruned. No secret in a
  test fixture, comment, doc, or error message.
- **Data exposure** — Does the response include only what this caller may see?
  Are errors generic? Does a Discord reply that contains private data use
  ephemeral or DM delivery? Did any new field leak into `GET /api/teams` for
  viewers who shouldn't have it?
- **Abuse & resource limits** — Rate limit on sensitive routes? Per-team and
  per-roster caps respected (50 rosters, 10 groupings, 10 images, 200 pool
  members, 30 loadout items)? Can a loop or query be driven unbounded by input?
- **Regressions** — Did this change weaken CSP, headers, container hardening,
  body caps, or a cascade/unique constraint? Is any previously enforced check
  now only in the UI?

If a finding is real, fix it in the same change when you can, and state it
plainly when you cannot.

## Verify

```bash
cd backend && gofmt -l . && go build ./... && go vet ./... && go test ./...
docker compose up --build -d                    # db, backend, seed, frontend
docker compose run --rm seed                    # re-apply migrations + test user
docker compose --profile bot up                 # bot too (needs DISCORD_BOT_TOKEN)
docker compose logs -f backend                  # dev password-reset links land here
```

Services set `pull_policy: build`, so a plain `docker compose up` rebuilds.
The app serves on `http://localhost:${FRONTEND_PORT}` (8081 by default); `/api/*`
is proxied to `backend:8080`, so use the frontend port for curl too:

```bash
TOKEN=$(curl -s localhost:8081/api/login -H 'Content-Type: application/json' \
  -d '{"username":"'"$SEED_USERNAME"'","password":"'"$SEED_PASSWORD"'"}' | jq -r .token)
curl -s localhost:8081/api/me -H "Authorization: Bearer $TOKEN" | jq
```

Before finishing, confirm the diff carries no secret and that authorization
still holds for a non-owner:

```bash
git diff | rg -n 'password|secret|token|BEGIN .*PRIVATE KEY'   # expect only intended matches
git status --porcelain | rg -n '\.env$'                        # .env must never be staged
```

A Cursor stop-hook (`.cursor/hooks/rebuild-docker.sh`) rebuilds the stack after
edits and logs to `.cursor/docker-rebuild.log`. Outside Cursor, rebuild manually.
`docker compose down -v` wipes the database volume; `--profile backup` /
`--profile restore` dump and restore it instead.

## Gotchas

- **Rosters own the composition, not teams.** Since migration `048`, `players`,
  `encounters`, and `groupings` hang off `roster_id`. A team has many rosters and
  exactly one active one (`teams.active_roster_id`); roster-scoped endpoints take
  an optional `?roster_id=` and default to the active roster. The bot always uses
  the active roster.
- **Roles are per-team**, stored as `teams.roles` JSONB (migration `042`) — validate
  a player's role against its team's own set, not a global list.
- **Concurrent editing is live.** Writes fire a `notify_team_change()` trigger →
  `pg_notify` → `internal/realtime.Hub` → SSE at `GET /api/teams/{id}/events`.
  That route authenticates from an `access_token` query param rather than the
  bearer middleware, because `EventSource` can't set headers — a token in a URL
  can land in proxy and server logs, so keep the exception to this one route and
  still run the normal team-access check inside the handler. Saves are
  version-checked against `expected_updated_at` and return **409** on conflict.
  The UI autosaves per slot (`PUT .../players/{slot}`,
  `PUT .../encounters/{eid}/loadouts/{slot}`); the whole-team PUTs still exist
  for back-compat but are unused by the UI.
- **Schedule times are stored in UTC** as `"HH:MM"`; the browser converts to and
  from the viewer's zone. There is no per-team timezone column anymore.
- **Subclassing is mutually exclusive**: `subclassed` true → three `skill_line_*`
  and blank masteries; false → two `mastery_*` from the player's class and blank
  skill lines. `ValidateSkillLines` also enforces uniqueness and the one-line-per-
  other-class rule.
- **Loadout item keys are sanitized, not allow-listed** (gear, skills, potions, CP,
  pen sources). Duplicates in `pen_extra` are intentional — they represent stacks.
- **Buffs, crit, and penetration are frontend-only models** computed in `data.js`
  from the roster plus the selected encounter's loadout; the backend only stores
  the inputs.
- **Image bytes are served through an authenticated route.** `GET
  .../images/{imgID}/raw` is bearer-protected and the frontend fetches it via
  `api._sendRaw` into an object URL. Don't "simplify" it into a public URL; the
  images are private team data.
- **There are almost no backend tests** (`cmd/bot` only). Prefer adding one when
  you touch pure logic — especially for an authorization or validation rule,
  where a test is the cheapest guard against a silent regression — and otherwise
  verify against the running stack.
