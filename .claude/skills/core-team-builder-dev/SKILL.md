---
name: core-team-builder-dev
description: Development, testing, and security guide for the Core Team Builder repo — an ESO trial-roster web app (Go stdlib API, build-free static frontend + nginx, PostgreSQL, Discord bot, Docker Compose). Use when adding or changing API endpoints, database migrations, frontend HTML/CSS/JS, ESO reference data, or /coreteam bot commands in this repo, when writing or updating Go unit/integration tests for it, and when reviewing, debugging, or security-reviewing that code.
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

## Tests come with the change

**Every code change ships with the tests that prove it.** A feature, fix, or
refactor is not done when it compiles and runs — it is done when a test would
catch it breaking. Do not defer tests to a follow-up: untested behaviour has to
be re-derived by whoever breaks it next, and the security rules above are
exactly the kind that regress silently.

What is required, by the kind of change:

| You changed | The test that comes with it |
|-------------|-----------------------------|
| Pure logic (validation, allow-lists, formatting, arithmetic) | A table-driven unit test beside it, covering what it **rejects** as well as what it accepts |
| An authorization or permission rule | One case per outcome: allowed, denied, and denied-when-the-lookup-fails |
| An HTTP endpoint | An integration test in `internal/handlers`: the success path, the anonymous caller (401), the non-member (404), and the wrong-role caller (403) |
| A store method or migration | Exercise it through an endpoint integration test — the harness rebuilds the schema from `database/migrations`, so a bad migration fails the whole suite |
| A bot command or interaction | A unit test on the pure part (option builders, ID parsing, permission gates) against a fake store |
| A bug fix | A test that **fails before the fix and passes after** — write it first and watch it fail |
| Frontend only | No JS test harness exists; verify against the running stack and state what you exercised. Any rule that also lives server-side still needs its Go test |

Conventions, non-negotiable:

- **Standard library only.** No assertion or mocking framework. `testify` is in
  `go.sum` transitively — do not start importing it.
- **Fake a store by implementing the consumer-side interface** in
  `cmd/bot/stores.go` or `internal/handlers/stores.go`, embedding the interface
  so an unexpected call panics loudly instead of returning a zero value (see
  `fakeDiscordStore` in `cmd/bot/permissions_test.go`). Never pass a `nil` store
  and lean on a check happening to return early — that test turns into a
  nil-pointer panic the moment someone reorders the checks.
- **Prefer an invariant to an example** when two things must stay in sync.
  `TestHelpCoversEverySubcommand` cross-checks the command list against the help
  entries in both directions, so it cannot go stale.
- **Inject the clock** instead of sleeping or asserting against `time.Now()` —
  see `nextRunUnixAt` in `internal/discordfmt`.
- **Test binaries run bcrypt at `bcrypt.MinCost`.** A package whose tests hash
  passwords calls `auth.SetBcryptCostForTests(bcrypt.MinCost)` from `TestMain`
  (see `internal/auth/main_test.go`, `internal/handlers/main_test.go`) — at the
  production factor the suite spends minutes key-stretching values no assertion
  reads. Because that leaves the cost lowered process-wide, **a test that means
  to assert the production strength must restore it and compare against
  `auth.DefaultBcryptCost`**, never against whatever is currently set.
- **A doc comment on each test says what it defends and why**, not what the code
  does. Failure messages read `got X, want Y` and name the file to edit when
  the fix is obvious.
- **No secret, real credential, or production data in a fixture.**

Integration tests are opt-in via `TEST_DATABASE_URL` and skip without it, so
`go test ./...` passing locally does **not** mean you ran them. Run them against
a throwaway database before calling a handler or migration change done — see
[Verify](#verify).

If a change genuinely cannot be tested — pure wiring, a doc edit, or it needs
infrastructure this repo does not have — say so explicitly and say what you
verified instead. Silence reads as "forgot".

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
.github/workflows/ci.yml          gofmt + build + vet + go test -race, with a Postgres service
```

Tests sit beside the code they cover. The two pieces worth knowing before you
write one:

```
backend/cmd/bot/stores.go                        narrow store interfaces the bot depends on
backend/internal/handlers/stores.go              ditto for the API — implement these to fake a store
backend/internal/handlers/testdb_test.go         integration harness: newTestAPI(t), registerUser, createTeam
backend/internal/handlers/*_integration_test.go  HTTP tests against a live Postgres (opt-in)
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

Tests that are part of the endpoint, not a follow-up: add cases to the matching
`internal/handlers/*_integration_test.go` (or a new one for a new area) using
`newTestAPI(t)`, `registerUser`, and `createTeam` from the harness. Cover the
success path, the anonymous caller, the non-member, and — for anything
team-scoped — a viewer attempting the write. Every validation rule you wrote
needs a request that trips it, because a rule with no test is a rule someone
will "simplify" away. Add team-scoped routes to the table in
`TestTeamEndpointsRejectAnonymousCallers` so a missing `protected(...)` wrapper
shows up as a failing test rather than an open endpoint. Assert on status codes
and on the response body **not** containing data the caller shouldn't see.

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

Tests that are part of the migration: the integration harness drops and rebuilds
the schema from `database/migrations` on its first test, so a syntax error, a
bad constraint, or a non-idempotent statement fails the entire handler suite —
run it (not just `go test ./...`) before you call the migration done. Cover the
new column through the endpoint that reads and writes it, and add a case for
each `CHECK` bound or `UNIQUE` constraint you added, so the handler-side
validation and the database agree.

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

Tests for a frontend change: there is no JS test harness, and adding one is a
design decision to raise rather than something to introduce mid-task. So verify
against the running stack and **state which interactions you exercised** — that
statement is the deliverable in place of a test. The exception is not optional:
if the change relies on a rule the server also enforces (a role, a bound, an
allow-list), that rule gets a Go test regardless of which side you edited, since
the client-side half is convenience only.

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

Tests that are part of the bot change: interaction handlers need a live Discord
session, so **push the decision out of the handler into a pure function and test
that** — a permission gate, a custom-ID parser, a select-option builder, a
formatter. Those are the parts that carry the bugs; the `discordgo` call around
them is not. When the logic needs a store, fake the interface from
`cmd/bot/stores.go` rather than reaching for the live one. A permission change
must cover allowed, denied, and lookup-failure. Discord's own limits (25 select
options, 100-character custom IDs, embed field caps) are invisible locally and
break in production, so pin each one you touch with a test that pushes past it.

**ESO reference data (gear sets, skills, bosses, labels).** Edit
`frontend/js/gear-skills.js` or `frontend/js/data.js`, then regenerate the Go
copy so the bot renders the same names:

```bash
node tools/gen-esoref/gen.js     # writes backend/internal/esoref/data_gen.go (committed)
```

Keys in the frontend tables mirror the backend allow-lists in
`internal/models/eso.go` and `encounter.go`; keep them in sync.

Nothing currently verifies that `data_gen.go` still matches the frontend data it
came from, so **re-run the generator and commit the result in the same change** —
an edit that skips it ships a bot rendering stale labels with no test to catch
it. If you touch a key that a backend validator reads, cover it in that
validator's unit test. A drift check for the generated file is a known gap worth
closing if you are already in this area.

## Non-negotiables

Security rules — a change that breaks one of these is wrong even if it works:

- **Parameterized SQL only**; never concatenate or format a value into a query.
- **No hardcoded secrets or credentials.** Everything comes from the environment
  (`backend/internal/config/config.go` → `docker-compose.yml` → `.env`).
  `.env` is gitignored; `.env.example` documents the keys with placeholder
  values. Never commit a real secret, and never print one in output or a log.
- **Never store or log plaintext passwords**, tokens, link codes, or reset
  codes. Passwords are bcrypt (`auth.DefaultBcryptCost` = 12; length 12–72
  **bytes** — `auth.MinPasswordLength`/`MaxPasswordLength`, the upper bound
  being bcrypt's own limit past which it silently ignores input). Refresh
  tokens, reset tokens, and Discord link codes are persisted only as SHA-256
  hashes. The work factor is lowerable **only from a test binary**
  (`auth.SetBcryptCostForTests`, guarded by `testing.Testing()`); do not add an
  env var or config field for it, and do not route production through the
  setter.
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

- **Behaviour ships with a test.** Adding or changing an endpoint, a validation
  rule, a permission check, a migration, or a bot interaction without a test is
  an incomplete change, not a fast one — see
  [Tests come with the change](#tests-come-with-the-change). Tests are standard
  library only; fake a store through its consumer-side interface.
- **Never weaken a test to make it pass.** If a test fails, either the code is
  wrong or the behaviour changed deliberately — in the second case update the
  test *and* say what behaviour moved. Deleting a case, loosening an assertion,
  or adding `t.Skip` to get green is a silent regression with extra steps.

Craft rules:

- Migrations are idempotent and never edited after the fact — add a new file.
- `gofmt` (tabs) for Go; two-space indent, semicolons, double quotes for JS/HTML.
- Comments explain *why*, not *what*. The same goes for test doc comments: name
  the behaviour being defended.
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
- **Test coverage** — Does every authorization and validation rule in this diff
  have a test that fails when the rule is removed? Were any existing tests
  deleted, loosened, or skipped, and is that justified in the summary? Did the
  integration tests actually run (`TEST_DATABASE_URL` set), or did they skip?

If a finding is real, fix it in the same change when you can, and state it
plainly when you cannot.

## Verify

```bash
cd backend && gofmt -l . && go build ./... && go vet ./... && go test ./...
```

That last command runs the **unit tests only** — the handler integration tests
skip silently without `TEST_DATABASE_URL`. If you touched a handler, a store, or
a migration, point it at a throwaway database and run them for real:

```bash
docker run -d --name ctb-test-pg -p 55432:5432 \
  -e POSTGRES_USER=test -e POSTGRES_PASSWORD=test -e POSTGRES_DB=ctb_test \
  postgres:16-alpine

cd backend && TEST_DATABASE_URL='postgres://test:test@localhost:55432/ctb_test?sslmode=disable' \
  go test -race ./...

docker rm -f ctb-test-pg
```

The harness drops and recreates the schema, so give it a database of its own —
never the compose `db` service or anything holding data you want. CI
(`.github/workflows/ci.yml`) runs exactly these checks plus `gofmt -l` on every
push and PR, with its own Postgres service, so a skipped local run surfaces
there instead of in review.

Then exercise the change against the running stack:

```bash
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
- **Backend tests exist and are enforced** — `internal/auth` (password policy,
  JWT, middleware), `internal/discordfmt` (schedule arithmetic), `cmd/bot`
  (help sync, permission gate, signup pickers), and HTTP integration tests in
  `internal/handlers` covering the auth flows and the team permission matrix.
  Coverage is thin outside those areas, which is a reason to add to it, not a
  precedent for skipping it — see
  [Tests come with the change](#tests-come-with-the-change).
- **Stores are injected as interfaces.** `bot` and `handlers.Server`/`Config`
  hold the narrow interfaces declared in `cmd/bot/stores.go` and
  `internal/handlers/stores.go`, not `*models.XStore`; `main()` passes the
  concrete ones. Add a method to an interface only when a consumer calls it, and
  keep the `var _ iface = (*models.XStore)(nil)` assertions at the bottom of
  each file — they turn a signature drift in `internal/models` into a clear
  compile error instead of a confusing one at the wiring site.
