#!/usr/bin/env bash
# afterFileEdit hook: record that files changed during this turn so the `stop`
# hook knows it needs to rebuild the Docker stack. Kept intentionally cheap — the
# actual (slow) rebuild runs at most once per turn from rebuild-docker.sh.
#
# Project hooks run from the repo root, so relative paths are anchored there.

cat >/dev/null 2>&1   # drain the hook's JSON stdin
touch .cursor/.docker-rebuild-pending 2>/dev/null
exit 0
