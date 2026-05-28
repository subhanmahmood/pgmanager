---
name: pgmanager
description: Manage PostgreSQL databases with project-based organization using the pgmanager CLI. Use when creating, listing, or deleting databases, managing projects, setting up PR review databases, configuring CI/CD database workflows, managing API tokens, or working with multi-environment / multi-VPS profiles. For VPS-side operator tasks (deploying, upgrading the Server, swapping the Postgres image, recovering a lost admin token), use the `pgmanager-server` skill instead.
argument-hint: "[command] [args]"
allowed-tools: Bash(pgmanager *), Bash(curl *), Bash(~/bin/pgmanager *), Bash(jq *), Bash(openssl *)
---

# pgmanager (client / CLI)

The CLI side of pgmanager. Creates and manages PostgreSQL databases per project across four environments (`prod`, `dev`, `staging`, `pr`), backed by an HTTP API. For day-to-day developer + CI workflows.

For VPS operator tasks — deploying, upgrading the Server image, swapping the Postgres image to add extensions, audit logs, recovering a lost admin token, editing `pgmanager.yaml` — see the **`pgmanager-server`** skill.

## Vocabulary

- **CLI** — the `pgmanager` binary on a laptop or in CI. The subject of this skill.
- **Server** — `pgmanager serve` running on a VPS. Holds Postgres credentials. The CLI talks to it over HTTPS.
- **Deployment** — the bundled docker-compose stack (Postgres + Server + Caddy) at `examples/deploy/`.

## Architecture (read first)

There are **two modes** the CLI can run in. Pick by where you're standing:

| Mode | When to use | Caller holds |
|------|-------------|--------------|
| **API mode** | Laptops, CI, anywhere that isn't the VPS itself | Just `(api_url, scoped_token)` |
| **Local mode** | A dev machine running its own Postgres | Direct Postgres credentials + encryption key |

**Default to API mode.** Postgres credentials should only live on the VPS. Issue scoped tokens to laptops/CI.

```
Laptop ──HTTPS+token──► Caddy ─► pgmanager serve ─► Postgres (localhost on VPS)
                       (443)        (127.0.0.1:8080)         (never exposed)
```

## Installation

```bash
# Linux/macOS
curl -sSL https://raw.githubusercontent.com/subhanmahmood/pgmanager/master/install.sh | bash

# Direct binary download (macOS arm64 example)
curl -sSL https://github.com/subhanmahmood/pgmanager/releases/latest/download/pgmanager-darwin-arm64 -o ~/bin/pgmanager
chmod +x ~/bin/pgmanager
```

Self-update later with `pgmanager update` (see below).

---

## Use Case 1 — First-time laptop setup (API mode)

You have an API URL and a bootstrap (or shared admin) token from whoever runs the VPS.

```bash
pgmanager login https://pgm.example.com
# paste the admin token

pgmanager auth whoami       # confirm: token + scopes
pgmanager profile show      # confirm current profile
```

Profile is stored at `~/.config/pgmanager/credentials.yaml` (mode 0600). Token is never printed by `profile show`.

If you don't have a token yet, ask the VPS operator to mint one for you. They follow `pgmanager-server` skill → "Use Case: First-time VPS setup" to get the bootstrap token, then `pgmanager auth create-token --name <you> --scope admin --expires 365d` to issue one for you.

## Use Case 2 — Local development (no remote server)

You have Postgres running locally and just want pgmanager as a CLI in front of it. No `serve`, no tokens.

```bash
mkdir -p ~/.config/pgmanager
cat > ~/.config/pgmanager/credentials.yaml <<'EOF'
current: local
profiles:
  local:
    postgres:
      host: localhost
      port: 5432
      user: postgres
      password: postgres
      ssl_mode: disable
    crypto:
      key: "PASTE_OUTPUT_OF_pgmanager_keygen_HERE"
EOF
chmod 600 ~/.config/pgmanager/credentials.yaml

pgmanager keygen   # paste output into the file above

pgmanager project create myapp
pgmanager db create myapp dev
```

The `crypto.key` is required even in local mode — DB passwords are encrypted at rest in the metadata table.

---

## CLI reference

Add `--json` to any read command for machine-parseable output. `--profile <name>` on any command overrides the active profile for that invocation.

### Projects

```bash
pgmanager project create <name>      # name: ^[a-z][a-z0-9_]*$, 2-32 chars
pgmanager project list
pgmanager project delete <name>      # cascades: drops every DB + user
```

Reserved names: `postgres`, `template0`, `template1`, `admin`, `root`, `system`.

### Databases

```bash
pgmanager db create <project> <env> [pr-number]       # env: prod | dev | staging | pr
pgmanager db create <project> <env> -x vector -x pg_trgm   # install Postgres extensions
pgmanager db list [project]                            # all DBs, or just one project
pgmanager db info <project> <env> [pr-number]          # connection info WITHOUT password
pgmanager db credentials <project> <env> [pr-number]   # WITH password + connection string
pgmanager db delete <project> <env> [pr-number]        # drops DB + user, removes metadata
```

Naming: `{project}_{env}` (prod/dev/staging) or `{project}_pr_{number}` (PR).

PR databases auto-expire after `cleanup.default_ttl` (default 168h / 7 days) and are deleted by `pgmanager cleanup`.

#### Extensions on db create

Pass `--extension <name>` (or `-x <name>`) one or more times to install Postgres extensions into the new database immediately after creation. The server runs `CREATE EXTENSION IF NOT EXISTS "<name>"` as admin; if any fails the new DB is dropped so you don't end up with a half-provisioned DB in the metadata store.

```bash
pgmanager db create content pr 42 -x vector
pgmanager db create geoapp dev -x postgis -x pg_trgm -x uuid-ossp
```

Names accept letters, digits, `_`, `-` (so `uuid-ossp` works) and must start with a letter. The extension's `.so` must be present on the Server's Postgres image — `db create -x foo` will error if it's not. To add extensions that aren't bundled, see the `pgmanager-server` skill → "Server Postgres image & extensions".

### Tokens

```bash
pgmanager auth whoami
pgmanager auth list-tokens
pgmanager auth create-token --name <n> --scope <s> [--scope <s2> ...] [--expires 90d]
pgmanager auth revoke-token <prefix>
```

Scope grammar:
- `admin` — everything
- `tokens` — only token CRUD (no DB access)
- `project:*` — all projects, all envs
- `project:<name>` — one project, all envs
- `project:<name>:env:<env>` — one project, one specific env
- `project:<name>:pr:*` — one project, only PR DBs (this is the CI scope)

The plaintext token is returned ONCE at creation. The Server stores only its SHA-256 plus a 16-char display prefix.

### Cleanup

```bash
pgmanager cleanup --older-than 7d           # default: 7d
pgmanager cleanup --older-than 24h
```

Deletes expired DBs (TTL passed) and PR DBs older than the duration. Duration: `Ns`, `Nm`, `Nh`, `Nd`, `Nw`.

### Profiles

```bash
pgmanager profile list
pgmanager profile use <name>
pgmanager profile show          # never prints the token
pgmanager login <api-url> [--name <profile-name>]
pgmanager logout [profile]
```

### Self-update

```bash
pgmanager update              # to the latest stable release
pgmanager update --check      # report if an update is available (exit 1 if so)
pgmanager update --version v0.2.0   # pin to a specific tag
pgmanager update --prerelease       # include prereleases
pgmanager update --dry-run          # show what would change
```

The downloaded binary is verified against the release `checksums.txt` before the running binary is atomically replaced.

### Other

```bash
pgmanager keygen                # new 32-byte base64 encryption key
pgmanager doctor                # diagnose: profile, token, server reachability, scopes
pgmanager tui                   # interactive terminal UI
pgmanager init --mode=client    # create credentials.yaml shell
pgmanager version
```

(`pgmanager serve` and `pgmanager init --mode=server` are operator commands — see the `pgmanager-server` skill.)

---

## CI/CD recipes

CI never holds Postgres credentials — just the API URL and a scoped token. Set them as repo/org variables and secrets:
- `vars.PGMANAGER_API_URL` (e.g. `https://pgm.example.com`)
- `secrets.PGMANAGER_CI_TOKEN` (created with `pgmanager auth create-token --scope project:myapp:pr:*`)
- `secrets.PGMANAGER_CLEANUP_TOKEN` (created with `--scope project:*` if you want one CI to clean up all projects, or use `--scope admin` if it manages tokens too)

### Use Case 3 — Create a PR database on every PR

```yaml
name: PR database
on:
  pull_request:
    types: [opened, synchronize, reopened]

jobs:
  create-db:
    runs-on: ubuntu-latest
    steps:
      - name: Install pgmanager
        run: curl -sSL https://raw.githubusercontent.com/subhanmahmood/pgmanager/master/install.sh | bash

      - name: Create PR database
        # Add `-x <extension>` per extension the app needs (e.g. `-x vector`)
        run: pgmanager db create myapp pr "${{ github.event.pull_request.number }}" --json > db.json
        env:
          PGMANAGER_API_URL: ${{ vars.PGMANAGER_API_URL }}
          PGMANAGER_API_TOKEN: ${{ secrets.PGMANAGER_CI_TOKEN }}

      - name: Expose DATABASE_URL for subsequent steps
        run: echo "DATABASE_URL=$(jq -r .connection_string db.json)" >> $GITHUB_ENV

      - run: ./run-migrations.sh
      - run: ./run-tests.sh
```

### Use Case 4 — Delete the PR database when the PR closes

```yaml
name: Cleanup PR database
on:
  pull_request:
    types: [closed]

jobs:
  delete-db:
    runs-on: ubuntu-latest
    steps:
      - name: Install pgmanager
        run: curl -sSL https://raw.githubusercontent.com/subhanmahmood/pgmanager/master/install.sh | bash
      - name: Delete PR database
        run: pgmanager db delete myapp pr "${{ github.event.pull_request.number }}" || true
        env:
          PGMANAGER_API_URL: ${{ vars.PGMANAGER_API_URL }}
          PGMANAGER_API_TOKEN: ${{ secrets.PGMANAGER_CI_TOKEN }}
```

The `|| true` covers the case where someone already deleted it.

### Use Case 5 — Reusable PR DB across job re-runs

Re-running a CI job shouldn't try to re-create the DB. Pattern:

```yaml
- name: Get-or-create PR database
  run: |
    PR=${{ github.event.pull_request.number }}
    pgmanager db credentials myapp pr "$PR" --json > db.json 2>/dev/null \
      || pgmanager db create     myapp pr "$PR" --json > db.json
    echo "DATABASE_URL=$(jq -r .connection_string db.json)" >> $GITHUB_ENV
  env:
    PGMANAGER_API_URL: ${{ vars.PGMANAGER_API_URL }}
    PGMANAGER_API_TOKEN: ${{ secrets.PGMANAGER_CI_TOKEN }}
```

`db credentials` returns the same password used at creation time — connections stay valid.

### Use Case 6 — Nightly cleanup

```yaml
name: Cleanup old PR databases
on:
  schedule: [{ cron: '0 3 * * *' }]   # 03:00 UTC daily
  workflow_dispatch:

jobs:
  cleanup:
    runs-on: ubuntu-latest
    steps:
      - run: curl -sSL https://raw.githubusercontent.com/subhanmahmood/pgmanager/master/install.sh | bash
      - run: pgmanager cleanup --older-than 7d
        env:
          PGMANAGER_API_URL: ${{ vars.PGMANAGER_API_URL }}
          PGMANAGER_API_TOKEN: ${{ secrets.PGMANAGER_CLEANUP_TOKEN }}
```

---

## Token operations

### Use Case 7 — Issue a CI token

```bash
pgmanager auth create-token \
  --name "github-ci-myapp" \
  --scope "project:myapp:pr:*" \
  --expires 90d
# → pgm_live_xxxxxxxxxxxxxx... (shown once)
```

Copy the token into the CI secret store. The shown prefix (first 16 chars) is the handle for `revoke-token` and `list-tokens`.

### Use Case 8 — Issue an admin token for another teammate

```bash
pgmanager auth create-token --name "alice-laptop" --scope admin --expires 365d
```

Send the plaintext via a one-time-secret channel. They run `pgmanager login https://pgm.example.com` and paste it.

### Use Case 9 — Revoke a leaked or unused token

```bash
pgmanager auth list-tokens           # find the prefix
pgmanager auth revoke-token pgm_live_abc12345
```

Revoked tokens are 401 immediately on the next request.

---

## Multi-environment / multi-VPS

### Use Case 10 — Switch between staging and prod pgmanager instances

```bash
pgmanager login https://pgm-staging.example.com --name staging
pgmanager login https://pgm.example.com         --name prod

pgmanager profile list
# * prod      api  https://pgm.example.com
#   staging   api  https://pgm-staging.example.com

pgmanager profile use staging
pgmanager db list                                    # talks to staging

pgmanager --profile prod db list                     # one-shot override
```

### Use Case 11 — Run a one-off command without saving a profile

```bash
PGMANAGER_API_URL=https://pgm.example.com \
PGMANAGER_API_TOKEN=pgm_live_xxx \
pgmanager db list
```

`PGMANAGER_API_URL` synthesizes an in-memory `env` profile that bypasses the file. Useful for scripts.

---

## TUI walkthrough

```bash
pgmanager tui
```

- `↑/k`, `↓/j` — move cursor
- `enter` — drill into project → databases → database info
- `b` / `esc` — back
- `r` — refresh
- `p` (on the database info view) — reveal password (fetches via `db credentials`)
- `q` — quit

Works in both API mode and local mode.

---

## REST API

Base: `<api_url>/api`. All endpoints except `/api/health` require `Authorization: Bearer pgm_live_...`.

| Method | Endpoint | Required scope |
|--------|----------|----------------|
| GET    | `/health` | — (anonymous) |
| GET    | `/auth/whoami` | any token |
| GET    | `/auth/tokens` | `tokens` or `admin` |
| POST   | `/auth/tokens` | `tokens` or `admin` |
| DELETE | `/auth/tokens/{prefix}` | `tokens` or `admin` |
| GET    | `/projects` | (filtered by scope) |
| POST   | `/projects` | `project:<name>` or wider |
| DELETE | `/projects/{name}` | `project:<name>` or wider |
| GET    | `/projects/{name}/databases` | `project:<name>` |
| POST   | `/projects/{name}/databases` | `project:<name>[:env:X\|:pr:*]` |
| GET    | `/projects/{name}/databases/{env}` | as above, no password in response |
| GET    | `/projects/{name}/databases/{env}/credentials` | as above, with password |
| DELETE | `/projects/{name}/databases/{env}` | as above |
| POST   | `/cleanup` | `project:*` or `admin` |

PR databases use `env` path segment `pr_<number>` (e.g., `/projects/myapp/databases/pr_42`).

### Bodies

```jsonc
// POST /projects
{ "name": "myapp" }

// POST /projects/myapp/databases
{ "env": "dev" }
{ "env": "pr", "pr_number": 42 }
{ "env": "dev", "extensions": ["vector", "pg_trgm"] }   // install extensions after create

// POST /auth/tokens
{ "name": "github-ci-myapp", "scopes": ["project:myapp:pr:*"], "expires": "90d" }
// Response: { "token": "pgm_live_...", "info": { "name": ..., "scopes": ..., ... } }

// POST /cleanup
{ "older_than": "7d" }
```

### Direct curl examples

```bash
TOKEN=pgm_live_xxx
API=https://pgm.example.com

curl -sS -H "Authorization: Bearer $TOKEN" $API/api/auth/whoami | jq

curl -sS -X POST -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"env":"pr","pr_number":42}' \
  $API/api/projects/myapp/databases | jq

curl -sS -H "Authorization: Bearer $TOKEN" \
  $API/api/projects/myapp/databases/pr_42/credentials | jq -r .connection_string
```

---

## Client config reference

### `credentials.yaml`

Default path: `$XDG_CONFIG_HOME/pgmanager/credentials.yaml` → falls back to `~/.config/pgmanager/credentials.yaml`. Override with `PGMANAGER_CONFIG_DIR`. Mode 0600.

```yaml
current: prod
profiles:
  prod:
    api_url: https://pgm.example.com
    token: pgm_live_xxxxxxxxxxxxxxxx
  staging:
    api_url: https://pgm-staging.example.com
    token: pgm_live_yyyyyyyyyyyyyyyy
  local:
    postgres:
      host: localhost
      port: 5432
      user: postgres
      password: postgres
      ssl_mode: disable
    crypto:
      key: <base64 32-byte key>
```

### Client environment variables

| Variable | Purpose |
|----------|---------|
| `PGMANAGER_API_URL` / `PGMANAGER_API_TOKEN` | Synthesize an `env` profile; bypasses the file (CI path) |
| `PGMANAGER_PROFILE` | Pick a saved profile by name |
| `PGMANAGER_CONFIG_DIR` / `XDG_CONFIG_HOME` | Override credentials.yaml location |

(Server-side env vars — `POSTGRES_*`, `PGMANAGER_LISTEN`, `PGMANAGER_ENCRYPTION_KEY`, etc. — are in the `pgmanager-server` skill.)

---

## Common workflows (quick recipes)

### Project bootstrap with all envs

```bash
pgmanager project create myapp
pgmanager db create myapp dev
pgmanager db create myapp staging
pgmanager db create myapp prod
```

### Get the connection string for a DB you forgot

```bash
pgmanager db credentials myapp dev
# or, for piping:
pgmanager db credentials myapp dev --json | jq -r .connection_string
```

### Test an app against a fresh PR DB locally

```bash
PR=42
DATABASE_URL=$(pgmanager db create myapp pr "$PR" --json | jq -r .connection_string)
export DATABASE_URL
./run-migrations.sh
./run-app.sh
# When done:
pgmanager db delete myapp pr "$PR"
```

### List everything across projects

```bash
pgmanager db list                   # all DBs across all projects
pgmanager db list --json | jq       # machine-parseable
pgmanager project list
pgmanager auth list-tokens
```

---

## Troubleshooting

Start with `pgmanager doctor` — it prints active profile, mode, whether the token is set, and what the server reports for `whoami`.

| Symptom | Likely cause | Fix |
|---------|--------------|-----|
| `no profile configured` | Fresh laptop with no `credentials.yaml` | `pgmanager login <api-url>` or export `PGMANAGER_API_URL`+`PGMANAGER_API_TOKEN` |
| `401 invalid token` | Token revoked, expired, or never created | `pgmanager auth list-tokens`; re-issue with `auth create-token` |
| `403 insufficient scope` | Token's scopes don't authorize this action | `pgmanager auth whoami`; create a token with a broader scope |
| `connection refused` to API | Server / Caddy not running, or wrong URL | `curl <api>/api/health` from your machine; ask the operator to check `docker compose ps` |
| `database already exists for myapp/dev` | Idempotent retry needed | `pgmanager db credentials myapp dev` to fetch existing creds |
| `project not found` | Project deleted or never created | `pgmanager project list`; `project create <name>` |
| TUI shows empty password | List endpoints strip passwords by design | Press `p` to reveal (calls `db credentials`) |
| CI re-run fails on `db create` | DB already exists from first run | Use the get-or-create pattern in Use Case 5 |
| `db create -x foo` fails: `extension "foo" is not available` | The Server's Postgres image doesn't ship that extension's `.so` | Operator task — see `pgmanager-server` skill → "Server Postgres image & extensions" |

For Server-side troubles (won't start, `encryption key required`, ghcr pull denied, image swap), see the `pgmanager-server` skill.

---

## Decision tree for agents

1. **No `pgmanager` available?** → install via the curl one-liner.
2. **`pgmanager doctor` reports `no profile configured`?**
   - If you have an API URL and admin token → `pgmanager login <url>` and paste.
   - If running locally with Postgres → Use Case 2.
3. **Task is "create a DB for X env"** → `pgmanager db create <project> <env> [pr]` with `--json` if downstream needs the connection string.
4. **Task is "DB needs extension X" (e.g., the app uses pgvector)** →
   - First try `pgmanager db create … -x X`. If it errors with `extension … is not available`, the Server's Postgres image doesn't ship it — that's an operator task. Hand off to the `pgmanager-server` skill.
5. **Task is "give me the connection string"** → `pgmanager db credentials <project> <env> [pr] --json | jq -r .connection_string`.
6. **Task is "set up CI for PR DBs"** → Use Case 3+4 (or 5 if jobs re-run). Create a `project:<name>:pr:*` token first.
7. **Task is "I lost my admin token / no admin token works"** → operator task; `pgmanager-server` skill.
8. **Task is "who's been calling the API"** → operator task; `pgmanager-server` skill.
9. **Task mentions security review** → `pgmanager auth list-tokens` to enumerate; revoke stale ones; rotate admin tokens via create-then-revoke.
