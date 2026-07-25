# pgmanager

A small Go service + CLI for creating, listing, and deleting PostgreSQL databases per project. Built for the case where you want isolated databases per environment (`prod`, `dev`, `staging`) plus ephemeral PR databases for CI — without handing Postgres credentials to laptops or pipelines.

## What you actually run

Four pieces, easy to keep distinct:

- **CLI** (`pgmanager`) — what you install on your laptop and in CI. Talks to the Server over HTTPS with a scoped bearer token.
- **Server** (`pgmanager serve`) — the HTTP API. Holds the Postgres credentials. Lives behind a reverse proxy on a VPS.
- **Admin UI** — a static browser UI the Server hosts alongside the API, for the same operations without the CLI. See [Admin UI](#admin-ui).
- **Deployment** — the bundled docker-compose stack: Postgres + Server + Caddy (auto-TLS). See [`examples/deploy/`](examples/deploy/).

```
Laptop / CI ──HTTPS + scoped token──► Caddy ─► pgmanager serve ─► Postgres
Browser ─────HTTPS + scoped token──►  (443)    (127.0.0.1:8080)   (never exposed)
```

All metadata (projects, databases, tokens) lives in a `pgmanager` schema inside the same Postgres server. DB passwords stored there are AES-256-GCM encrypted at rest.

## Install the CLI

```bash
curl -sSL https://raw.githubusercontent.com/subhanmahmood/pgmanager/master/install.sh | bash
```

Or grab a binary from [Releases](https://github.com/subhanmahmood/pgmanager/releases). Self-update later with `pgmanager update`.

## Quick start — laptop

```bash
pgmanager login https://pgm.example.com
# paste the bootstrap admin token

pgmanager auth whoami           # sanity check
pgmanager project create myapp
pgmanager db create myapp dev
pgmanager db credentials myapp dev   # shows the connection string
```

Profile lives at `~/.config/pgmanager/credentials.yaml` (mode 0600).

## Quick start — CI

CI never holds Postgres credentials, only `(api_url, token)`:

```yaml
- run: curl -sSL https://raw.githubusercontent.com/subhanmahmood/pgmanager/master/install.sh | bash

- name: Create PR database
  run: pgmanager db create myapp pr "${{ github.event.pull_request.number }}" --json > db.json
  env:
    PGMANAGER_API_URL: ${{ vars.PGMANAGER_API_URL }}
    PGMANAGER_API_TOKEN: ${{ secrets.PGMANAGER_CI_TOKEN }}

- run: echo "DATABASE_URL=$(jq -r .connection_string db.json)" >> $GITHUB_ENV
```

Mint the CI token from your laptop:

```bash
pgmanager auth create-token \
  --name "github-ci-myapp" \
  --scope "project:myapp:pr:*" \
  --expires 90d
```

A leaked CI token can only create/delete PR databases in one project — it cannot touch prod, other projects, or admin endpoints.

## Quick start — VPS (Deployment)

The shipped Deployment is docker-compose with Caddy fronting it for HTTPS:

```bash
ssh root@vps
git clone https://github.com/subhanmahmood/pgmanager.git
cd pgmanager/examples/deploy
cp .env.example .env       # then $EDITOR .env
docker compose up -d
docker compose exec pgmanager cat /var/lib/pgmanager/bootstrap-token.txt
```

Full walkthrough — including TLS, secrets, and the `upgrade.sh` workflow — in [`examples/deploy/README.md`](examples/deploy/README.md).

## Admin UI

`pgmanager serve` also hosts a static browser UI from its `./web` directory —
projects, databases, connection credentials, token management and PR cleanup,
against the same `/api` endpoints the CLI uses.

The bundled Deployment serves it on `admin.<your API domain>` (override with
`PGMANAGER_ADMIN_DOMAIN`). Point a DNS record at the VPS and Caddy issues the
certificate on first request; both hostnames reach the same process, so the UI
calls the API same-origin and needs no CORS configuration.

Sign in by pasting an API token. It is kept in that browser's `localStorage`
and sent as a bearer token — so it is exactly as privileged as the token you
paste. Prefer a `project:<name>` token over `admin` where the person only needs
one project, and revoke it from the Tokens view when they're done.

Set `PGMANAGER_WEB_DIR` (or `api.web_dir`) to serve the UI from elsewhere, or to
`-` to run API-only.

## CLI commands

```
pgmanager login <url>              Save an API token under a profile
pgmanager logout [profile]         Remove a profile
pgmanager profile list|use|show    Manage saved profiles
pgmanager doctor                   Diagnose: profile, token, server reachability
pgmanager auth whoami              Show current token + scopes
pgmanager auth list-tokens         Enumerate tokens (admin/tokens scope)
pgmanager auth create-token        Mint a scoped token
pgmanager auth revoke-token <pfx>  Revoke a token by its prefix
pgmanager project create|list|delete
pgmanager db create|list|info|credentials|delete
                                   db create accepts -x/--extension (repeatable)
pgmanager cleanup --older-than 7d  Delete expired PR DBs
pgmanager tui                      Interactive terminal UI
pgmanager keygen                   New 32-byte base64 encryption key
pgmanager update                   Self-update binary
pgmanager serve                    Run the Server (VPS-side)
pgmanager version
```

Add `--json` to any read command for machine-parseable output. `--profile <name>` overrides the active profile for one invocation.

## Scoped tokens

| Scope | Authorizes |
|-------|------------|
| `admin` | Everything |
| `tokens` | Token CRUD only (no DB access) |
| `project:*` | All projects, all environments |
| `project:<name>` | One project, all environments |
| `project:<name>:env:<env>` | One project, one specific environment |
| `project:<name>:pr:*` | One project, PR databases only (the CI scope) |

Plaintext tokens are returned **once** at creation; the Server stores only their SHA-256 plus a 16-char display prefix.

## Configuration

Two config files; they serve different audiences and never mix.

**`credentials.yaml`** (client) — `~/.config/pgmanager/credentials.yaml`, mode 0600. Holds named profiles managed via `login` / `logout` / `profile use`.

**`pgmanager.yaml`** (server) — read only by `pgmanager serve`. Auto-discovered in `cwd → ~ → ~/.config/pgmanager/ → /etc/pgmanager/`.

The most important environment overrides:

| Variable | Side | Purpose |
|---|---|---|
| `PGMANAGER_API_URL` + `PGMANAGER_API_TOKEN` | client | Synthesize an `env` profile; bypasses `credentials.yaml` (CI path) |
| `PGMANAGER_PROFILE` | client | Pick a saved profile by name |
| `POSTGRES_*` | server | Override `postgres.*` (host, port, user, password, database, sslmode) |
| `POSTGRES_PUBLIC_HOST` / `POSTGRES_PUBLIC_PORT` | server | What clients see in `db create` / `db info` responses |
| `PGMANAGER_LISTEN` | server | Bind address (default `127.0.0.1:8080`) |
| `PGMANAGER_ENCRYPTION_KEY` | server | base64 32-byte at-rest encryption key |
| `PGMANAGER_BOOTSTRAP_TOKEN` | server | Pre-seed initial admin token (skip auto-generation) |
| `PGMANAGER_DATA_DIR` | server | Where `bootstrap-token.txt` is written |

Full reference in the agent skill ([`.claude/skills/pgmanager/SKILL.md`](.claude/skills/pgmanager/SKILL.md) for client, [`.claude/skills/pgmanager-server/SKILL.md`](.claude/skills/pgmanager-server/SKILL.md) for VPS operations).

## REST API

Same surface the CLI talks to. All endpoints under `/api`; everything except `/api/health` requires `Authorization: Bearer pgm_live_...`.

```bash
TOKEN=pgm_live_xxx
API=https://pgm.example.com

curl -sS -H "Authorization: Bearer $TOKEN" $API/api/auth/whoami | jq

curl -sS -X POST -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"env":"pr","pr_number":42}' \
  $API/api/projects/myapp/databases | jq
```

Full endpoint table + scope requirements in the skill files.

## Self-updating

```bash
pgmanager update              # to the latest stable release
pgmanager update --check      # report if an update is available (exit 1 if so)
pgmanager update --version v0.2.0   # pin to a specific tag
```

The downloaded binary is verified against the release `checksums.txt` before the running binary is atomically replaced.

## Development

```bash
go test ./...                          # all tests
go test -v ./internal/...              # verbose
go test -run TestValidateName ./internal/project
go build -o pgmanager ./cmd/pgmanager
```

Or with Docker:

```bash
docker run --rm -v "$(pwd):/app" -w /app golang:1.23-alpine \
  sh -c "go test ./..."
```

## License

MIT
