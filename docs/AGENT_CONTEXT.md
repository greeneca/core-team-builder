# Agent Context — read this first

This file exists so a future AI session (or new contributor) can get up to speed
quickly. Keep it current when the architecture changes.

## What this project is

`core-team-builder` helps design and organize a **trial core team** for *The
Elder Scrolls Online*. Today it provides accounts + login. Future work will add
roster, role, and build planning on top of the same stack.

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
   returns a signed JWT (`{ token, user }`).
2. Frontend stores the token in `localStorage` (`ctb_token`) and sends it as
   `Authorization: Bearer <token>` on protected calls.
3. `auth.Middleware` validates the token and injects the user ID into the
   request context. `GET /api/me` is the example protected route.

Passwords: bcrypt cost 12, min length 8. Hashes only ever leave via `password_hash`
column; the `User` JSON model hides it (`json:"-"`).

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
  (see `docs/STYLE_GUIDE.md`).
- **Config**: env vars are read in `backend/internal/config/config.go` and wired
  through `docker-compose.yml` + `.env`.

## Conventions

- Migrations are **idempotent** (`IF NOT EXISTS`, `ON CONFLICT`) so both the
  Postgres init dir and the `seed` command can apply them safely.
- The `seed` binary applies all `*.sql` in `MIGRATIONS_DIR` (sorted) then
  upserts the test user. It is safe to run repeatedly.
- Secrets come from the environment only; never hardcode. `.env` is git-ignored.

## Common commands

```bash
docker compose up --build          # run the whole stack
docker compose run --rm seed       # (re)apply migrations + ensure test user
cd backend && go build ./...       # compile backend
cd backend && go vet ./...         # static checks
```

## Status / TODO ideas

- [ ] Token refresh / logout server-side invalidation (currently stateless JWT).
- [ ] Trial roster, role, and build planning models + UI.
- [ ] Tests (handlers, auth, models).
- [ ] Rate limiting on auth endpoints.
