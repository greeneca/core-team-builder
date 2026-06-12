# Style Guide

Consistent conventions across the codebase. Keep this in sync with reality.

## General

- Prefer clarity over cleverness. Small, focused functions and files.
- Configuration comes from the environment, never hardcoded secrets.
- Comments explain *why* (intent, trade-offs), not *what* the code obviously does.

## Go (backend)

- **Formatting**: always `gofmt` (tabs for indentation). Run `go vet ./...`
  before committing.
- **Package layout**: business logic lives under `internal/`; entrypoints under
  `cmd/<name>/main.go`. Each `main.go` delegates to a `run() error` so errors
  are handled in one place.
- **Naming**: exported identifiers documented with a leading comment beginning
  with the identifier name. Acronyms keep their case (`ID`, `URL`, `JWT`).
- **Errors**: wrap with context using `fmt.Errorf("...: %w", err)`. Return
  errors up the stack; only `main`/`run` logs fatal. Never log secrets or
  plaintext passwords.
- **HTTP**: use method-aware `ServeMux` patterns (`"POST /api/login"`). JSON I/O
  goes through the `writeJSON` / `writeError` helpers. Protected routes wrap with
  `tokens.Middleware`.
- **Database**: parameterized queries only (never string-concatenate SQL). Data
  access lives in `internal/models` store types.

## SQL

- Lowercase keywords are acceptable, but this project uses **UPPERCASE
  keywords** and `snake_case` identifiers.
- Migrations are idempotent and named `NNN_description.sql` (zero-padded order).
- Always store password hashes — never plaintext.

## Frontend

### CSS

- A single design system in `frontend/css/styles.css`. **All colors, spacing,
  typography, radius, and shadow come from CSS custom properties (tokens) in
  `:root`.** Do not hardcode hex colors or pixel spacing in components.
- Roster slots are color-coded by player role via `--role-*` tokens
  (tank=blue, healer=green, dps=red, support_dps=purple), kept muted so they sit
  calmly against the slate/gold theme.
- Class naming follows a light **BEM-ish** convention:
  - Block: `.card`, `.btn`, `.tab`
  - Modifier: `.card--narrow`, `.btn--primary`, `.tab.is-active`
  - State: `.is-active`, `.is-hidden`
- Utilities are minimal and prefixed by intent: `.text-muted`, `.text-center`,
  `.mt-4`.

### JavaScript

- Vanilla ES (no framework, no build step). Use `const`/`let`, never `var`.
- Each page has one entry script (`auth.js`, `app.js`) wrapped in an IIFE to
  avoid globals. Shared, page-agnostic code lives in dedicated modules that load
  before the entry script. Keep concerns separated:
  - `api.js` — **only** the `api` client object (token storage + endpoint
    helpers). No domain data lives here.
  - `data.js` — all shared reference data + display helpers: roles, classes,
    races, share roles, days, timezone/schedule helpers, the subclassing skill
    lines and class masteries (+ option/lookup helpers), and the ESO encounter
    master/seed data (boss names grouped by trial, gear sets with tooltips,
    skills grouped by skill line, potions, mundus stones, weapon lines, blue CP,
    extra penetration sources). Also the `BUFFS`/`CRIT_*`/`PEN_*` source tables
    and their coverage calculators. Keys mirror the backend allow-lists in
    `internal/models` (`eso.go`, `encounter.go`).
  - `components.js` — reusable, framework-free UI components
    (`createSearchableSelect`, `initTooltips`). Tooltips are shown via a
    `data-tip` attribute (not native `title`), so they work consistently on any
    element, including dynamically-added chips.
- These modules expose plain top-level `const`s/functions (no module system);
  rely on script load order in the HTML rather than `import`/`export`.
- Network calls go through `api.request(...)`; do not call `fetch` directly in
  page scripts.
- Two-space indentation; semicolons required; double quotes for strings.

### HTML

- Two-space indentation. Always set `lang`, `charset`, and a responsive
  `viewport` meta. Associate `<label for>` with inputs and set sensible
  `autocomplete` attributes.
- **Cache-busting**: reference CSS/JS assets with a shared version query string
  (e.g. `app.js?v=N`) and bump `N` on every frontend change so browsers (and the
  baked nginx image) pick up updates. Keep the version consistent across
  `index.html`, `login.html`, and `reset.html`. nginx serves HTML/JS/CSS with
  `Cache-Control: no-cache` (see `frontend/nginx.conf`). Note the frontend is
  baked into the nginx image, so changes require
  `docker compose up -d --build frontend` to deploy.

## Theme

Dark "Elder Scrolls" aesthetic: deep slate backgrounds (`--color-bg`,
`--color-surface`) with an Aldmeri gold accent (`--color-accent`). Adjust the
palette by editing the tokens in `:root` — components inherit automatically.
