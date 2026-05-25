#!/usr/bin/env bash
# pgmanager Deployment upgrade
#
# Pulls the Server image at the tag pinned in docker-compose.yml and
# recreates only the Server container. Postgres is untouched.
#
# Usage:
#   ./upgrade.sh                 # pull current pinned tag and recreate
#   ./upgrade.sh --tag v0.2      # bump the pin first, then pull and recreate
#
# Equivalent manual form (no script needed):
#   docker compose pull pgmanager && docker compose up -d pgmanager
#
# Requires: docker accessible directly or via passwordless sudo. Run
# from this directory (next to docker-compose.yml).

set -euo pipefail

DEPLOY_DIR="${DEPLOY_DIR:-$(cd "$(dirname "$0")" && pwd)}"
COMPOSE_FILE="$DEPLOY_DIR/docker-compose.yml"

TAG=""
while [[ $# -gt 0 ]]; do
  case "$1" in
    --tag)
      TAG="${2:-}"
      if [[ -z "$TAG" ]]; then
        echo "error: --tag requires a value (e.g. --tag v0.2)" >&2
        exit 1
      fi
      shift 2
      ;;
    -h|--help)
      sed -n '2,15p' "$0" | sed 's/^# \{0,1\}//'
      exit 0
      ;;
    *)
      echo "error: unknown argument: $1" >&2
      exit 1
      ;;
  esac
done

if [[ -n "$TAG" ]]; then
  if [[ ! -f "$COMPOSE_FILE" ]]; then
    echo "error: $COMPOSE_FILE not found" >&2
    exit 1
  fi
  echo "==> Pinning image to ghcr.io/subhanmahmood/pgmanager:$TAG"
  # Match `image: ghcr.io/subhanmahmood/pgmanager:<anything>` and replace the tag.
  # Backup left at docker-compose.yml.bak so the previous pin is recoverable.
  sed -i.bak -E \
    "s|(image:[[:space:]]*ghcr\.io/subhanmahmood/pgmanager):[^[:space:]]+|\1:${TAG}|" \
    "$COMPOSE_FILE"
  if ! grep -qE "image:[[:space:]]*ghcr\.io/subhanmahmood/pgmanager:${TAG}" "$COMPOSE_FILE"; then
    echo "error: failed to pin tag in $COMPOSE_FILE (restoring backup)" >&2
    mv "$COMPOSE_FILE.bak" "$COMPOSE_FILE"
    exit 1
  fi
fi

DOCKER="${DOCKER:-}"
if [[ -z "$DOCKER" ]]; then
  if docker info >/dev/null 2>&1; then
    DOCKER="docker"
  elif sudo -n docker info >/dev/null 2>&1; then
    DOCKER="sudo -n docker"
  else
    echo "error: cannot run docker (neither direct nor passwordless sudo)" >&2
    exit 1
  fi
fi

echo "==> Pulling Server image"
$DOCKER compose --project-directory "$DEPLOY_DIR" pull pgmanager

echo "==> Recreating Server container"
$DOCKER compose --project-directory "$DEPLOY_DIR" up -d pgmanager

sleep 2

echo "==> Status"
$DOCKER compose --project-directory "$DEPLOY_DIR" ps pgmanager

echo
echo "==> Recent logs"
$DOCKER compose --project-directory "$DEPLOY_DIR" logs pgmanager --tail 5
