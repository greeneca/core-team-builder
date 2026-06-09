# AGENTS.md

Quick pointer for AI/code agents working in this repo.

**Read [`docs/AGENT_CONTEXT.md`](docs/AGENT_CONTEXT.md) first** — it summarizes
the stack, how auth works, where to make changes, and project conventions.

Other key docs:

- [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md)
- [`docs/DEVELOPMENT.md`](docs/DEVELOPMENT.md)
- [`docs/STYLE_GUIDE.md`](docs/STYLE_GUIDE.md)

Ground rules:

- Follow `docs/STYLE_GUIDE.md` for Go, SQL, CSS, JS, and HTML conventions.
- Keep SQL migrations idempotent.
- Never hardcode secrets; configuration is environment-based.
- Never store or log plaintext passwords.
- Keep the docs above up to date when you change architecture or conventions.
