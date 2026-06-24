# Core Team Builder

A tool to help design and organize a **trial core team** for *The Elder Scrolls
Online (ESO)*: build rosters, plan per-encounter loadouts and groupings, recruit
members, and post schedules and signups to Discord.

> **Note on authorship:** this project is **mostly written using AI** (coding
> agents). Changes are made through AI sessions guided by the docs under
> [`docs/`](docs/) — start with [`docs/AGENT_CONTEXT.md`](docs/AGENT_CONTEXT.md).
> Treat the generated code accordingly: review before deploying.

The stack:

- **Frontend** — static HTML/CSS/JavaScript served by nginx. Live collaboration
  uses Server-Sent Events (SSE).
- **Backend** — Go HTTP API (standard-library router) with JWT auth.
- **Discord bot** — separate Go binary (same module) that posts trial overviews,
  recruitment signups, and pre-made runs, and runs DM intake flows.
- **Database** — PostgreSQL.

Everything runs via Docker Compose.

---

## Quick start

```bash
# 1. Configure environment
cp .env.example .env
# (edit .env — at minimum set a strong JWT_SECRET for non-local use)

# 2. Build and start the core services (db, backend, seed, frontend)
docker compose up --build

# 3. Seed the database (init schema + create the test user)
#    The `seed` service runs automatically with `up`, but you can re-run it:
docker compose run --rm seed
```

Then open the app:

- Frontend: <http://localhost:8081> (or `FRONTEND_PORT` from `.env`)
- API health check: <http://localhost:8081/api/health>

**Default test user** — seeded as an **admin**. Credentials come from `.env`
(`SEED_USERNAME` / `SEED_PASSWORD`); the `.env.example` defaults are:

- username: `testuser`
- password: `change-me-please`

The first account ever registered also becomes an admin. Admins get a **Manage
Users** button in the topbar to add/remove users, grant admin, and enable or
disable self-registration.

---

## Optional services (Compose profiles)

These services are kept out of a plain `docker compose up` behind profiles:

```bash
# Discord bot (requires DISCORD_BOT_TOKEN in .env)
docker compose --profile bot up

# On-demand database backup -> ./backups/<db>-<timestamp>.dump
docker compose --profile backup run --rm backup

# Restore a dump (set RESTORE_FILE to a filename under ./backups)
RESTORE_FILE=core_team_builder-20260623-143000.dump \
  docker compose --profile restore run --rm restore
```

See [`docs/DEPLOYMENT.md`](docs/DEPLOYMENT.md) for production guidance and the
backup/restore details.

---

## Project layout

```
core-team-builder/
├── backend/              Go module: API server, Discord bot, and seed command
│   ├── cmd/
│   │   ├── server/       HTTP API entrypoint
│   │   ├── bot/          Discord bot entrypoint (commands, intake, scheduler)
│   │   └── seed/         DB seed/init entrypoint
│   ├── internal/
│   │   ├── auth/         password hashing (bcrypt) + JWT + middleware
│   │   ├── config/       env-based configuration
│   │   ├── db/           connection pool
│   │   ├── handlers/     HTTP routes (auth, admin, teams, rosters, encounters,
│   │   │                 groupings, members, discord, password reset, events)
│   │   ├── models/       data access (users, settings, teams, rosters, …)
│   │   ├── discordfmt/   renders rosters into Discord post/DM text
│   │   ├── esoref/       generated ESO reference data (labels, gear, skills)
│   │   ├── realtime/     SSE hub for live collaboration + presence
│   │   └── email/        outbound mail (password-reset links)
│   └── Dockerfile
├── frontend/             Static site (nginx)
│   ├── css/styles.css    design system / tokens
│   ├── js/               api client, master data, components, page scripts
│   ├── *.html            index, login, reset, discord
│   ├── nginx.conf        serves static + proxies /api -> backend
│   └── Dockerfile
├── database/
│   ├── migrations/       SQL schema (auto-applied on first DB boot)
│   └── Dockerfile        postgres + migrations
├── docs/                 architecture, dev, deployment, style, agent context
├── docker-compose.yml
└── .env.example
```

---

## Documentation

- [`docs/AGENT_CONTEXT.md`](docs/AGENT_CONTEXT.md) — **start here** for a fast
  orientation (especially for AI sessions).
- [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md) — components, data flow, auth.
- [`docs/DEVELOPMENT.md`](docs/DEVELOPMENT.md) — local dev, env vars, API reference.
- [`docs/DEPLOYMENT.md`](docs/DEPLOYMENT.md) — production checklist and operations.
- [`docs/STYLE_GUIDE.md`](docs/STYLE_GUIDE.md) — coding & UI conventions.

## Security notes

- Passwords are hashed with **bcrypt** (cost 12) and never stored or logged in
  plaintext.
- Auth uses signed **JWT** bearer tokens; set a strong `JWT_SECRET` (the server
  refuses to start with one shorter than 32 bytes).
- Login returns a generic error and runs a constant-time comparison whether or
  not the user exists, to limit user enumeration and timing attacks.
- Team access is role-based (owner/editor/viewer); user-management routes
  (`/api/admin/*`) are admin-only and re-checked server-side.

## License

Released under the [GNU Affero General Public License v3.0](LICENSE) (AGPL-3.0).

This is a strong copyleft license: anyone who runs a modified version of this
software to provide a network service must make their modified source available
to its users.
