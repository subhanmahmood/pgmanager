#!/usr/bin/env bash
# pgmanager Deployment upgrade
#
# Pulls the Server image at the tag pinned in docker-compose.yml and
# recreates only the Server container. Postgres is untouched.
#
# Usage:
#   ./upgrade.sh
#
# To change the image tag (e.g. move from :v0.1 to :v0.2), edit
# docker-compose.yml first, then run this script.
#
# Requires: docker accessible directly or via passwordless sudo. Run
# from this directory (next to docker-compose.yml).

set -euo pipefail

DEPLOY_DIR="${DEPLOY_DIR:-$(cd "$(dirname "$0")" && pwd)}"

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
