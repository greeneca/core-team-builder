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
- [x] **Email (password reset):** set `APP_BASE_URL` to the public site URL (used
      to build reset links; defaults to `CORS_ORIGIN`) and configure the `SMTP_*`
      relay so reset emails actually send. With `SMTP_HOST` empty the reset link
      is only written to the backend log, so the flow won't work for real users.
      Optionally tune `PASSWORD_RESET_TTL` (default `1h`).
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
| `APP_BASE_URL`      | backend   | Public site URL for email links (default `CORS_ORIGIN`) |
| `PASSWORD_RESET_TTL`| backend   | Reset-link lifetime (default `1h`)                 |
| `SMTP_HOST`         | backend   | SMTP relay host (empty → reset emails only logged) |
| `SMTP_PORT`         | backend   | SMTP port (default `587`; `465` = implicit TLS)    |
| `SMTP_USERNAME`     | backend   | SMTP auth username (empty → no auth)               |
| `SMTP_PASSWORD`     | backend   | SMTP auth password                                 |
| `SMTP_FROM`         | backend   | From address, e.g. `Name <noreply@example.com>`    |
| `FRONTEND_PORT`     | frontend  | Host port → container `8080`                        |
| `SEED_USERNAME`     | seed      | Bootstrap admin username                            |
| `SEED_EMAIL`        | seed      | Bootstrap admin email                              |
| `SEED_PASSWORD`     | seed      | **Strong, ≥ 12 chars.** Bootstrap admin password   |
| `DISCORD_BOT_TOKEN` | bot       | Discord bot token (only needed to run the bot)     |
| `DISCORD_APP_ID`    | bot       | Discord application/client ID (optional)           |
| `DISCORD_GUILD_ID`  | bot       | Register commands to one guild (optional; empty = global) |
| `DISCORD_CLIENT_ID` | backend   | Discord OAuth2 client ID for "Sign in with Discord" (empty → button hidden) |
| `DISCORD_CLIENT_SECRET` | backend | Discord OAuth2 client **secret** (keep secret)   |
| `DISCORD_OAUTH_REDIRECT_URL` | backend | OAuth callback URL; must match a portal redirect (default `APP_BASE_URL` + `/api/auth/discord/callback`) |

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

## 8. Discord bot (optional)

The Discord bot is a separate container behind the `bot` compose profile (so it
is **not** started by a plain `docker compose up`). It opens an outbound
connection to the Discord gateway and reaches the database over `ctb-net`; it
publishes **no** host port and is independent of the upstream nginx / TLS setup.

Discord-side setup (one-time):

- [ ] Create an application at <https://discord.com/developers/applications>, then
      add a **Bot** to it and copy the **bot token** into `DISCORD_BOT_TOKEN`.
- [ ] Copy the application's **Application (client) ID** into `DISCORD_APP_ID`
      (optional — the bot falls back to its own user ID for command registration).
- [ ] Invite the bot to your server with the **`bot`** and
      **`applications.commands`** OAuth2 scopes (the developer portal's URL
      generator builds the invite link). No special gateway intents/permissions
      are required beyond sending messages and using slash commands.
- [ ] (Dev/fast) set `DISCORD_GUILD_ID` to your server's ID so `/coreteam`
      registers instantly to that guild. Leave it empty in production to register
      globally (the first global registration can take up to ~1h to appear).

Run it:

- [ ] `docker compose --profile bot up -d bot` (after `db`/migrations are up).
- [ ] In Discord, a user runs `/coreteam link code:<code>` with a code generated
      from the web UI ("Link Discord" button), then `/coreteam setup` in a channel
      to bind a team, and `/coreteam post` to share the trial. See
      `docs/AGENT_CONTEXT.md` "Discord bot".

> Treat `DISCORD_BOT_TOKEN` like any other secret (keep it in `.env`, `chmod 600`).
> Rotating it in the developer portal requires updating `.env` and restarting the
> bot container.

### Sign in with Discord (OAuth2, optional)

This is a **backend** feature (served by `cmd/server`, independent of the bot) that
adds a "Continue with Discord" button to the login page. Signing up this way stores
the user's Discord ID on their account, so they can use the bot's `/coreteam`
commands **without** running `/coreteam link`.

- [ ] In the same Discord application, open **OAuth2** and copy the **Client ID**
      into `DISCORD_CLIENT_ID` and the **Client Secret** into `DISCORD_CLIENT_SECRET`.
- [ ] Under **OAuth2 → Redirects**, add your callback URL **exactly**:
      `https://teams.example.com/api/auth/discord/callback` (it must match
      `DISCORD_OAUTH_REDIRECT_URL`; when unset it defaults to `APP_BASE_URL` +
      `/api/auth/discord/callback`).
- [ ] The flow requests the `identify` and `email` scopes. An existing account is
      auto-linked only when the Discord email is **verified**; new sign-ups honor the
      `registration_enabled` toggle.
- [ ] Restart the backend. With the secrets set, the login page shows the button
      (verify via `GET /api/registration-status` → `"discord_enabled": true`).

> The client secret is as sensitive as `JWT_SECRET`. The OAuth callback issues the
> app's own tokens via the URL **fragment** (never sent to the server), and a
> short-lived HttpOnly state cookie guards against CSRF.

---

## 9. Hardening already baked in (no action needed)

- TLS-adjacent: CSP + security headers, real-IP support, nginx rate limiting
  (auth / share / general zones), edge + backend body caps.
- Auth: bcrypt (cost 12), constant-time login, short access tokens + rotating
  DB-backed refresh tokens with revocation/logout, HS256-pinned JWTs with
  `iss`/`aud`/required-`exp` validation, ≥ 32-byte `JWT_SECRET` enforced at boot.
- Abuse limits: per-owner team cap, per-team encounter cap, per-team grouping and
  member-pool caps, and per-loadout item caps.
- Containers: backend on `scratch` as non-root, unprivileged non-root nginx,
  read-only root filesystems + tmpfs, `cap_drop: [ALL]` (minimal caps for db),
  `no-new-privileges`, and per-service memory/PID limits.

See `docs/AGENT_CONTEXT.md` and `docs/ARCHITECTURE.md` for system details.
