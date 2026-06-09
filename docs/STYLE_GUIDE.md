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
- Class naming follows a light **BEM-ish** convention:
  - Block: `.card`, `.btn`, `.tab`
  - Modifier: `.card--narrow`, `.btn--primary`, `.tab.is-active`
  - State: `.is-active`, `.is-hidden`
- Utilities are minimal and prefixed by intent: `.text-muted`, `.text-center`,
  `.mt-4`.

### JavaScript

- Vanilla ES (no framework, no build step). Use `const`/`let`, never `var`.
- Each page has one script (`auth.js`, `app.js`) wrapped in an IIFE to avoid
  globals. Shared API access goes through the `api` object in `api.js`.
- Network calls go through `api.request(...)`; do not call `fetch` directly in
  page scripts.
- Two-space indentation; semicolons required; double quotes for strings.

### HTML

- Two-space indentation. Always set `lang`, `charset`, and a responsive
  `viewport` meta. Associate `<label for>` with inputs and set sensible
  `autocomplete` attributes.

## Theme

Dark "Elder Scrolls" aesthetic: deep slate backgrounds (`--color-bg`,
`--color-surface`) with an Aldmeri gold accent (`--color-accent`). Adjust the
palette by editing the tokens in `:root` — components inherit automatically.
