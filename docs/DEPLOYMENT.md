# Deployment Guide — behind an upstream nginx (TLS)

This describes a production deployment where a separate **upstream nginx**
terminates TLS and proxies to this project's containers. The project's own
`frontend` nginx is internal (plain HTTP on the private network) and serves
static files + proxies `/api` to the backend.

```
          ┌────────────┐  HTTPS   ┌──────────────────┐  HTTP   ┌───────────────┐  HTTP  ┌──────────┐
 Browser ─┤ Upstream   ├─────────►│ frontend (nginx  ├────────►│ backend (Go   ├───────►│ db        │
          │ nginx (TLS)│  :443    │ unprivileged)    │  :8080  │ API)          │ :8080  │ Postgres  │
          └────────────┘          │  host:FRONTEND_  │         │  (ctb-net)    │        │ (ctb-net) │
                                  │  PORT → 8080     │         └───────────────┘        └──────────┘
                                  └──────────────────┘
```

Only the `frontend` container publishes a host port; `backend` and `db` are
reachable on the `ctb-net` bridge only. See `docs/ARCHITECTURE.md`.

---

## 1. Secrets & environment (`.env`)

- [x] `cp .env.example .env` on the target host (the file is git-ignored).
- [x] Generate a strong **`JWT_SECRET`** (≥ 32 bytes or the backend refuses to
      start): `openssl rand -base64 48`.
- [x] Set a strong **`POSTGRES_PASSWORD`** (not `change-me-in-prod`):
      `openssl rand -base64 24`.
- [x] Set a strong, unique **`SEED_PASSWORD`** (≥ 12 chars — the password policy
      rejects shorter). This account is created as an **admin**; treat it as a
      bootstrap credential to be rotated/removed (see §5).
- [x] Set **`CORS_ORIGIN`** to the real public origin, e.g.
      `https://teams.example.com` (scheme + host, no trailing slash).
- [x] Set **`FRONTEND_PORT`** to the host port the upstream nginx will proxy to
      (e.g. `8081`). This maps to the container's `8080`.
- [x] Review token lifetimes: `JWT_TTL` (access token, default `15m`) and
      `REFRESH_TTL` (refresh token, default `720h` = 30 days).
- [x] Confirm `.env` is **not** world-readable: `chmod 600 .env`.

Full variable reference:

| Variable            | Service   | Notes                                              |
|---------------------|-----------|----------------------------------------------------|
| `POSTGRES_USER`     | db/backend| Database user                                      |
| `POSTGRES_PASSWORD` | db/backend| **Strong, unique.**                                |
| `POSTGRES_DB`       | db/backend| Database name                                      |
| `JWT_SECRET`        | backend   | **Required, ≥ 32 bytes.** Signing secret           |
| `JWT_TTL`           | backend   | Access-token lifetime (default `15m`)              |
| `REFRESH_TTL`       | backend   | Refresh-token lifetime (default `720h`)            |
| `CORS_ORIGIN`       | backend   | **Public `https://` origin**                       |
| `FRONTEND_PORT`     | frontend  | Host port → container `8080`                        |
| `SEED_USERNAME`     | seed      | Bootstrap admin username                            |
| `SEED_EMAIL`        | seed      | Bootstrap admin email                              |
| `SEED_PASSWORD`     | seed      | **Strong, ≥ 12 chars.** Bootstrap admin password   |

---

## 2. Project nginx: trust the upstream (real client IP)

The `frontend` container's rate limiting and logging key on the client IP. Behind
a proxy that IP arrives in `X-Forwarded-For`, so `frontend/nginx.conf` must trust
the upstream and **only** the upstream.

- [x] Edit `set_real_ip_from` in `frontend/nginx.conf` to the upstream proxy's
      real address/CIDR (replace the broad RFC1918 defaults). Example:

```nginx
set_real_ip_from 10.20.0.5/32;   # the upstream nginx host only
real_ip_header   X-Forwarded-For;
real_ip_recursive on;
```

> If untrusted hosts can reach the `frontend` container directly, broad ranges
> let them spoof `X-Forwarded-For` and bypass rate limits. Keep it narrow, and
> do not publish the backend/db ports.

---

## 3. Upstream nginx (TLS termination)

The upstream owns TLS, HSTS, and the HTTP→HTTPS redirect. The project's nginx
already emits CSP / `X-Frame-Options` / `X-Content-Type-Options` /
`Referrer-Policy` / `Permissions-Policy`; make sure the upstream **forwards**
them (don't strip or override).

- [x] Obtain/manage a TLS certificate for the public hostname.
- [x] Configure the redirect + TLS server blocks and forward the real client IP
      and scheme. Example:

```nginx
# HTTP → HTTPS
server {
    listen 80;
    server_name teams.example.com;
    return 301 https://$host$request_uri;
}

server {
    listen 443 ssl;
    http2 on;
    server_name teams.example.com;

    ssl_certificate     /etc/nginx/certs/fullchain.pem;
    ssl_certificate_key /etc/nginx/certs/privkey.pem;
    ssl_protocols       TLSv1.2 TLSv1.3;

    server_tokens off;

    # HSTS belongs on the TLS-terminating layer (browsers ignore it over HTTP).
    add_header Strict-Transport-Security "max-age=31536000; includeSubDomains" always;

    # Bound bodies at the edge too (the project nginx caps at 256k; backend 1 MiB).
    client_max_body_size 256k;

    location / {
        proxy_pass         http://127.0.0.1:8081;   # = FRONTEND_PORT
        proxy_http_version 1.1;
        proxy_set_header   Host              $host;
        proxy_set_header   X-Real-IP         $remote_addr;
        proxy_set_header   X-Forwarded-For   $proxy_add_x_forwarded_for;
        proxy_set_header   X-Forwarded-Proto $scheme;
    }
}
```

- [x] Verify the upstream does **not** add a duplicate/looser CSP (it would
      override the app's). Optionally add `Permissions-Policy`/`Referrer-Policy`
      here too if you prefer them centralized — but avoid conflicting CSPs.
- [x] If the upstream→frontend hop crosses an untrusted network, use TLS (or
      mTLS) on that hop as well.

---

## 4. Build & start

- [ ] `docker compose build`
- [ ] `docker compose up -d db` and wait for healthy: `docker compose ps`.
- [ ] `docker compose run --rm seed` (one-shot: applies migrations + creates the
      bootstrap admin). Re-runnable; it's idempotent.
- [ ] `docker compose up -d backend frontend`
- [ ] Confirm only the frontend port is published: `docker compose ps` should
      show no host ports for `backend`/`db`.

---

## 5. Lock down the bootstrap admin

The `seed` service creates a permanent **admin** account. After bootstrapping:

- [ ] Log in as the seed admin and create your real admin account (or register
      the first real user — note the very first registered user auto-becomes
      admin).
- [ ] In the admin UI, **disable open registration** if you don't want public
      signups (the backend enforces this).
- [ ] Rotate or remove the seed credential: change `SEED_PASSWORD`, or stop the
      seed account from being recreated by not re-running the `seed` service.
- [ ] Do not leave `seed` in the default `docker compose up` set for routine
      restarts (it only needs to run on first bootstrap / migrations).

---

## 6. Post-deploy verification

- [ ] **HTTPS + redirect:** `curl -I http://teams.example.com` → `301` to
      `https://`.
- [ ] **HSTS (from upstream):** `curl -sI https://teams.example.com | grep -i strict-transport-security`.
- [ ] **App security headers (from project nginx):**
      `curl -sI https://teams.example.com/login.html` shows
      `Content-Security-Policy`, `X-Frame-Options: DENY`,
      `X-Content-Type-Options: nosniff`, `Referrer-Policy`.
- [ ] **Health:** `curl https://teams.example.com/api/health` → `{"status":"ok"}`.
- [ ] **Auth round-trip:** register/login returns `token` + `refresh_token`;
      `POST /api/refresh` rotates them; `POST /api/logout` revokes.
- [ ] **Rate limiting works per client:** rapid repeated `POST /api/login`
      returns `429` (and the limit is per real client IP, not shared — confirms
      §2 is correct).
- [ ] **Body cap:** an oversized request body is rejected (`413`/`400`).

---

## 7. Ongoing operations

- [ ] **Refresh-token cleanup** runs in-process (hourly sweep in the backend) —
      no cron needed.
- [ ] **Database backups:** snapshot the `db-data` volume (or `pg_dump`) on a
      schedule.
- [ ] **Log monitoring:** watch backend logs for `audit: share lookup miss`
      bursts (username enumeration attempts) and repeated `429`s at the edge.
- [ ] **Image updates:** rebuild periodically to pick up base-image
      (`golang`, `nginx-unprivileged`, `postgres`) security patches; pin by
      digest if you want reproducible deploys.
- [ ] **Secret rotation:** rotating `JWT_SECRET` invalidates all access tokens
      immediately; clients re-authenticate via refresh (refresh tokens are
      DB-backed and unaffected by the signing-secret change).

---

## 8. Hardening already baked in (no action needed)

- TLS-adjacent: CSP + security headers, real-IP support, nginx rate limiting
  (auth / share / general zones), edge + backend body caps.
- Auth: bcrypt (cost 12), constant-time login, short access tokens + rotating
  DB-backed refresh tokens with revocation/logout, HS256-pinned JWTs with
  `iss`/`aud`/required-`exp` validation, ≥ 32-byte `JWT_SECRET` enforced at boot.
- Abuse limits: per-owner team cap, per-team encounter cap, timezone/roster caps.
- Containers: backend on `scratch` as non-root, unprivileged non-root nginx,
  read-only root filesystems + tmpfs, `cap_drop: [ALL]` (minimal caps for db),
  `no-new-privileges`, and per-service memory/PID limits.

See `docs/AGENT_CONTEXT.md` and `docs/ARCHITECTURE.md` for system details.
