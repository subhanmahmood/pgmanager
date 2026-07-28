#!/usr/bin/env bash
# pgmanager Deployment upgrade
#
# Pulls the Server image at the tag pinned in docker-compose.yml and
# recreates only the Server container. Postgres is untouched. If the
# Caddyfile on disk no longer matches the one the running Caddy is using,
# Caddy is restarted too — see "Caddy config drift" below.
#
# Usage:
#   ./upgrade.sh                 # pull current pinned tag and recreate
#   ./upgrade.sh --tag v0.2      # bump the pin first, then pull and recreate
#
# Equivalent manual form (no script needed):
#   docker compose pull pgmanager && docker compose up -d pgmanager
#
# Caddy config drift: the Caddyfile is bind-mounted as a *single file*, and
# Docker resolves such a mount to an inode at container start. `git pull` (and
# `sed -i`, and most editors) replace the file rather than rewrite it in place,
# so the new content lands on a new inode and the running container keeps
# reading the old one. `caddy reload` does not help: it re-reads the same stale
# inode and reports success. Only restarting the container re-resolves the
# mount, which is why this script diffs the host file against the container's
# view and restarts Caddy when they differ.
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
      # Print the header comment block (everything between the shebang and
      # the first non-comment line), minus the leading "# ".
      awk 'NR>1 && /^#/ { sub(/^# ?/, ""); print; next } NR>1 { exit }' "$0"
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

CADDY_FILE="$DEPLOY_DIR/Caddyfile"
if [[ -f "$CADDY_FILE" ]] && [[ -n "$($DOCKER compose --project-directory "$DEPLOY_DIR" ps -q caddy 2>/dev/null)" ]]; then
  # Compare the host file with what the running container actually sees. A
  # `git pull` that rewrote the Caddyfile leaves the container on the old
  # inode (see the header), and `caddy reload` would not notice.
  if $DOCKER compose --project-directory "$DEPLOY_DIR" exec -T caddy \
       cat /etc/caddy/Caddyfile 2>/dev/null | diff -q - "$CADDY_FILE" >/dev/null 2>&1; then
    echo "==> Caddy config in sync; leaving Caddy running"
  else
    # `restart` is enough — it re-resolves the bind mount — and is cheaper than
    # `up -d --force-recreate`, which would drop every site Caddy fronts for
    # longer than necessary.
    echo "==> Caddyfile changed on disk; restarting Caddy to pick it up"
    $DOCKER compose --project-directory "$DEPLOY_DIR" restart caddy
  fi
fi

sleep 2

echo "==> Status"
$DOCKER compose --project-directory "$DEPLOY_DIR" ps pgmanager

echo
echo "==> Recent logs"
$DOCKER compose --project-directory "$DEPLOY_DIR" logs pgmanager --tail 5
