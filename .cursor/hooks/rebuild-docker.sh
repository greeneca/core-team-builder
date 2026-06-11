#!/usr/bin/env bash
# stop hook: rebuild and restart the Docker Compose stack, but only if files
# actually changed during the turn (afterFileEdit drops a sentinel file).
#
# Build output is captured to .cursor/docker-rebuild.log (gitignored) so the
# agent turn stays clean; check that file or the Hooks output channel for the
# result of the last rebuild.
#
# Project hooks run from the repo root, so relative paths are anchored there.

cat >/dev/null 2>&1   # drain the hook's JSON stdin

pending=".cursor/.docker-rebuild-pending"
[ -f "$pending" ] || exit 0   # no edits this turn; nothing to rebuild
rm -f "$pending"

log=".cursor/docker-rebuild.log"
{
  echo "=== docker rebuild started $(date '+%Y-%m-%d %H:%M:%S') ==="

  if ! command -v docker >/dev/null 2>&1; then
    echo "docker not found on PATH; skipping rebuild."
    echo "=== skipped ==="
    exit 0
  fi

  if [ ! -f .env ]; then
    echo "no .env file found (copy .env.example to .env first); skipping rebuild."
    echo "=== skipped ==="
    exit 0
  fi

  docker compose up --build -d
  status=$?
  echo "=== docker rebuild finished (exit $status) $(date '+%Y-%m-%d %H:%M:%S') ==="
} >"$log" 2>&1

exit 0
