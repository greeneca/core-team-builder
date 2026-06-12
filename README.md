# Core Team Builder

A tool to help design and organize a **trial core team** for *The Elder Scrolls
Online (ESO)*.

The stack:

- **Frontend** — static HTML/CSS/JavaScript served by nginx.
- **Backend** — Go HTTP API (standard library router) with JWT auth.
- **Database** — PostgreSQL.

Everything runs via Docker Compose.

---

## Quick start

```bash
# 1. Configure environment
cp .env.example .env
# (edit .env — at minimum set a strong JWT_SECRET for non-local use)

# 2. Build and start everything
docker compose up --build

# 3. Seed the database (init schema + create the test user)
#    The `seed` service runs automatically with `up`, but you can re-run it:
docker compose run --rm seed
```

Then open the app:

- Frontend: <http://localhost:8081> (or `FRONTEND_PORT` from `.env`)
- API health check: <http://localhost:8081/api/health>

**Default test user** (from `.env`) — seeded as an **admin**:

- username: `testuser`
- password: `changeme123`

The first account ever registered also becomes an admin. Admins get a **Manage
Users** button in the topbar to add/remove users, grant admin, and enable or
disable self-registration.

---

## Project layout

```
core-team-builder/
├── backend/              Go API server + seed command
│   ├── cmd/
│   │   ├── server/       HTTP API entrypoint
│   │   └── seed/         DB seed/init entrypoint
│   ├── internal/
│   │   ├── auth/         password hashing (bcrypt) + JWT + middleware
│   │   ├── config/       env-based configuration
│   │   ├── db/           connection pool
│   │   ├── handlers/     HTTP routes (auth, admin, teams, encounters)
│   │   └── models/       data access (users, settings, teams, encounters)
│   └── Dockerfile
├── frontend/             Static site (nginx)
│   ├── css/styles.css    design system / tokens
│   ├── js/               api client, master data, components, page scripts
│   ├── *.html
│   ├── nginx.conf        serves static + proxies /api -> backend
│   └── Dockerfile
├── database/
│   ├── migrations/       SQL schema (auto-applied on first DB boot)
│   └── Dockerfile        postgres + migrations
├── docs/                 architecture, dev, style, and agent context
├── docker-compose.yml
└── .env.example
```

---

## Documentation

- [`docs/AGENT_CONTEXT.md`](docs/AGENT_CONTEXT.md) — **start here** for a fast
  orientation (especially for AI sessions).
- [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md) — components, data flow, auth.
- [`docs/DEVELOPMENT.md`](docs/DEVELOPMENT.md) — local dev, env vars, API reference.
- [`docs/STYLE_GUIDE.md`](docs/STYLE_GUIDE.md) — coding & UI conventions.

## Security notes

- Passwords are hashed with **bcrypt** (cost 12) and never stored or logged in
  plaintext.
- Auth uses signed **JWT** bearer tokens; set a strong `JWT_SECRET`.
- Login returns a generic error and runs a constant-time comparison whether or
  not the user exists, to limit user enumeration and timing attacks.
- Team access is role-based (owner/editor/viewer); user-management routes
  (`/api/admin/*`) are admin-only and re-checked server-side.

## License

Released under the [GNU Affero General Public License v3.0](LICENSE) (AGPL-3.0).

This is a strong copyleft license: anyone who runs a modified version of this
software to provide a network service must make their modified source available
to its users.
