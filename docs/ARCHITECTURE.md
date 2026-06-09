# Architecture

## Overview

Core Team Builder is a three-tier application:

```
            ┌─────────────────────────────────────────────┐
            │                  Browser                     │
            │   login.html / index.html + js/*.js          │
            └───────────────┬─────────────────────────────┘
                            │ HTTP (same-origin)
                            ▼
            ┌─────────────────────────────────────────────┐
            │  frontend (nginx)                            │
            │   • serves static assets                     │
            │   • proxies /api/* → backend:8080            │
            └───────────────┬─────────────────────────────┘
                            │ HTTP
                            ▼
            ┌─────────────────────────────────────────────┐
            │  backend (Go, net/http)                      │
            │   • /api/register, /api/login (public)       │
            │   • /api/me (JWT-protected)                   │
            │   • bcrypt password hashing, JWT issuance     │
            └───────────────┬─────────────────────────────┘
                            │ pgx connection pool
                            ▼
            ┌─────────────────────────────────────────────┐
            │  db (PostgreSQL 16)                           │
            │   • users table                              │
            └─────────────────────────────────────────────┘
```

## Components

### Frontend (`frontend/`)

Plain static files — no build step. nginx serves them and reverse-proxies
`/api/*` to the backend, keeping API calls same-origin (so the browser does not
hit CORS). The JS is split into:

- `js/api.js` — fetch wrapper, token storage, typed-ish endpoint helpers.
- `js/auth.js` — login/register page logic.
- `js/app.js` — dashboard logic + route guard.

### Backend (`backend/`)

A small Go service using the standard library router (`http.ServeMux` with
method-aware patterns, Go 1.22+). Layout:

- `cmd/server` — HTTP server entrypoint with graceful shutdown.
- `cmd/seed` — one-shot migration + test-user seeder.
- `internal/config` — environment configuration (12-factor).
- `internal/db` — pgx pool with startup retry.
- `internal/models` — domain types + data access (`UserStore`).
- `internal/auth` — bcrypt hashing, JWT issuing/parsing, auth middleware.
- `internal/handlers` — HTTP handlers, JSON helpers, CORS.

### Database (`database/`)

PostgreSQL. The schema in `migrations/001_init.sql` is applied two ways:

1. Baked into the image's `/docker-entrypoint-initdb.d/`, so a fresh volume is
   initialized automatically on first boot.
2. By the `seed` command at runtime (idempotent), which then ensures the test
   user exists.

## Data model

### `users`

| column         | type          | notes                              |
|----------------|---------------|------------------------------------|
| id             | bigint (IDENTITY) | primary key                    |
| username       | varchar(50)   | unique, not null                   |
| email          | varchar(255)  | unique, not null                   |
| password_hash  | text          | bcrypt hash, not null              |
| created_at     | timestamptz   | default `now()`                    |
| updated_at     | timestamptz   | auto-updated via trigger           |

## Authentication & security

- **Hashing**: bcrypt (cost 12) with per-hash random salt. Minimum password
  length 8. Plaintext is never stored or logged.
- **Tokens**: HS256-signed JWT containing the user ID (`sub`) and username, with
  an expiry (`JWT_TTL`, default 24h). Stateless — there is no server-side
  session store yet.
- **Login hardening**: a constant-time bcrypt comparison runs even when the
  username does not exist, and all failures return the same generic message, to
  reduce user enumeration and timing side channels.
- **Transport**: terminate TLS at a proxy/load balancer in production. Tokens in
  `localStorage` are acceptable for this tool; revisit if XSS surface grows.

## Deployment model

`docker-compose.yml` defines four services: `db`, `backend`, `seed` (one-shot),
and `frontend`. The backend waits for the database healthcheck before starting.
Only the frontend publishes a host port; backend and db are reachable on the
internal compose network.
