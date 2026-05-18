---
name: pgmanager
description: Manage PostgreSQL databases with project-based organization using pgmanager. Use when creating, listing, or deleting PostgreSQL databases, managing projects, setting up PR review databases, configuring CI/CD database workflows, managing API tokens, or operating a remote pgmanager VPS deployment.
argument-hint: "[command] [args]"
allowed-tools: Bash(pgmanager *), Bash(curl *), Bash(~/bin/pgmanager *), Bash(docker compose *), Bash(jq *), Bash(openssl *)
---

# pgmanager

pgmanager is a CLI + HTTP API for managing PostgreSQL databases per project. It creates isolated DBs with dedicated users across four environments (`prod`, `dev`, `staging`, `pr`). Metadata lives in a `pgmanager` schema inside the same Postgres server. DB passwords stored there are AES-256-GCM encrypted at rest.

## Architecture (read first)

There are **two modes** the CLI can run in. Pick by where you're standing:

| Mode | When to use | Caller holds |
|------|-------------|--------------|
| **API mode** | Laptops, CI, anywhere that isn't the VPS itself | Just `(api_url, scoped_token)` |
| **Local mode** | The VPS itself, or a dev machine running its own Postgres | Direct Postgres credentials + encryption key |

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

---

## Use Case 1 — First-time VPS server setup

Use this when standing up a fresh remote pgmanager. The shipped deploy is docker-compose with Caddy fronting it for automatic HTTPS.

```bash
ssh root@vps
git clone https://github.com/subhanmahmood/pgmanager.git
cd pgmanager/examples/deploy
cp .env.example .env

# Fill in:
#   POSTGRES_PASSWORD=$(openssl rand -hex 24)
#   PGMANAGER_ENCRYPTION_KEY=$(openssl rand -base64 32)   # or: pgmanager keygen
#   PGMANAGER_API_DOMAIN=pgm.example.com
$EDITOR .env

docker compose up -d

# Grab the bootstrap admin token (printed once)
docker compose exec pgmanager cat /var/lib/pgmanager/bootstrap-token.txt
docker compose exec pgmanager rm /var/lib/pgmanager/bootstrap-token.txt
```

Port 5432 is **never** exposed to the public internet — only Caddy on 80/443.

## Use Case 2 — First-time laptop setup (API mode)

```bash
pgmanager login https://pgm.example.com
# paste the bootstrap admin token from Use Case 1

pgmanager auth whoami       # confirm: token + scopes
pgmanager profile show      # confirm current profile
```

Profile is stored at `~/.config/pgmanager/credentials.yaml` (mode 0600). Token is never printed by `profile show`.

## Use Case 3 — Local development (no remote server)

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

## Use Case 4 — Migrating from an old version (plaintext passwords)

If you ran pgmanager before scoped tokens + encryption existed:

```bash
# On the VPS
export PGMANAGER_ENCRYPTION_KEY=$(pgmanager keygen)
# Persist this — losing it loses access to stored DB passwords.

docker compose pull
docker compose up -d
docker compose logs pgmanager     # confirm migration completed
```

The server reads each plaintext row, encrypts it, drops the legacy column. Idempotent. Application databases and their users are untouched.

If you start without `PGMANAGER_ENCRYPTION_KEY` and legacy rows exist, the server refuses to start with: `encryption key required: set PGMANAGER_ENCRYPTION_KEY`.

---

## CLI reference

Add `--json` to any read command for machine-parseable output.

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
pgmanager db list [project]                            # all DBs, or just one project
pgmanager db info <project> <env> [pr-number]          # connection info WITHOUT password
pgmanager db credentials <project> <env> [pr-number]   # WITH password + connection string
pgmanager db delete <project> <env> [pr-number]        # drops DB + user, removes metadata
```

Naming: `{project}_{env}` (prod/dev/staging) or `{project}_pr_{number}` (PR).

PR databases auto-expire after `cleanup.default_ttl` (default 168h / 7 days) and are deleted by `pgmanager cleanup`.

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

The plaintext token is returned ONCE at creation. The server stores only its SHA-256.

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

`--profile <name>` on any command overrides the current one for that invocation.

### Other

```bash
pgmanager keygen                # new 32-byte base64 key
pgmanager doctor                # diagnose: profile, token, server reachability, scopes
pgmanager tui                   # interactive terminal UI
pgmanager serve                 # run the API server (VPS)
pgmanager init --mode=client    # create credentials.yaml shell
pgmanager init --mode=server    # create pgmanager.yaml + fresh crypto.key
pgmanager version
```

---

## CI/CD recipes

CI never holds Postgres credentials — just the API URL and a scoped token. Set them as repo/org variables and secrets:
- `vars.PGMANAGER_API_URL` (e.g. `https://pgm.example.com`)
- `secrets.PGMANAGER_CI_TOKEN` (created with `pgmanager auth create-token --scope project:myapp:pr:*`)
- `secrets.PGMANAGER_CLEANUP_TOKEN` (created with `--scope project:*` if you want one CI to clean up all projects, or use `--scope admin` if it manages tokens too)

### Use Case 5 — Create a PR database on every PR

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
        run: pgmanager db create myapp pr "${{ github.event.pull_request.number }}" --json > db.json
        env:
          PGMANAGER_API_URL: ${{ vars.PGMANAGER_API_URL }}
          PGMANAGER_API_TOKEN: ${{ secrets.PGMANAGER_CI_TOKEN }}

      - name: Expose DATABASE_URL for subsequent steps
        run: echo "DATABASE_URL=$(jq -r .connection_string db.json)" >> $GITHUB_ENV

      - run: ./run-migrations.sh
      - run: ./run-tests.sh
```

### Use Case 6 — Delete the PR database when the PR closes

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

### Use Case 7 — Reusable PR DB across job re-runs

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

### Use Case 8 — Nightly cleanup

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

## Auth & token operations

### Use Case 9 — Issue a CI token

```bash
pgmanager auth create-token \
  --name "github-ci-myapp" \
  --scope "project:myapp:pr:*" \
  --expires 90d
# → pgm_live_xxxxxxxxxxxxxx... (shown once)
```

Copy the token into the CI secret store. The shown prefix (first 16 chars) is the handle for `revoke-token` and `list-tokens`.

### Use Case 10 — Issue an admin token for another teammate

```bash
pgmanager auth create-token --name "alice-laptop" --scope admin --expires 365d
```

Send the plaintext via a one-time-secret channel. They run `pgmanager login https://pgm.example.com` and paste it.

### Use Case 11 — Revoke a leaked or unused token

```bash
pgmanager auth list-tokens           # find the prefix
pgmanager auth revoke-token pgm_live_abc12345
```

Revoked tokens are 401 immediately on the next request.

### Use Case 12 — Audit who's accessed what

Tail the server logs (each authenticated request emits one structured line):

```bash
docker compose logs -f pgmanager | grep audit
# audit method=POST path=/api/projects/myapp/databases status=201 duration=124ms ip=1.2.3.4 token=pgm_live_abcd1234 scopes=project:myapp:pr:*
```

### Use Case 13 — Lost the admin token

If no admin token works anymore (lost, revoked, expired all):

```bash
# On the VPS, drop into the pgmanager container and reseed.
docker compose exec pgmanager sh -c "
  # Revoke all current admin tokens to remove any stale ones, then auto-bootstrap.
  echo \"UPDATE pgmanager.tokens SET revoked_at = NOW() WHERE 'admin' = ANY(scopes) AND revoked_at IS NULL\" \
    | psql -h postgres -U postgres
"
docker compose restart pgmanager
docker compose exec pgmanager cat /var/lib/pgmanager/bootstrap-token.txt
```

Alternative: set `PGMANAGER_BOOTSTRAP_TOKEN=pgm_live_...` in `.env` and restart — pgmanager registers it as the new admin.

---

## Multi-environment / multi-VPS

### Use Case 14 — Switch between staging and prod pgmanager instances

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

### Use Case 15 — Run a one-off command without saving a profile (CI-ish from your laptop)

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

## Configuration reference

### Client (`credentials.yaml`)

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

### Server (`pgmanager.yaml`)

Auto-discovered in this order: cwd → `~` → `~/.config/pgmanager/` → `/etc/pgmanager/`. Override with `--config`. Used only by `pgmanager serve`.

```yaml
postgres:
  host: localhost
  port: 5432
  user: postgres
  password: ""
  database: postgres
  ssl_mode: require       # use 'disable' only for local dev
api:
  listen: 127.0.0.1:8080  # bind address; put Caddy in front for TLS
  require_token: true     # refuse to start without auth
  allowed_origins: []     # CORS list; usually empty
crypto:
  key: ""                 # 32-byte base64; or use crypto.key_file, or env
  # key_file: /run/secrets/pgmanager_key
data_dir: /var/lib/pgmanager   # bootstrap-token.txt lives here
cleanup:
  default_ttl: 168h
```

### Environment variables

| Variable | Side | Purpose |
|----------|------|---------|
| `PGMANAGER_API_URL` / `PGMANAGER_API_TOKEN` | client | Synthesize an `env` profile (CI path) |
| `PGMANAGER_PROFILE` | client | Choose a saved profile by name |
| `PGMANAGER_CONFIG_DIR` / `XDG_CONFIG_HOME` | client | Override credentials.yaml location |
| `POSTGRES_HOST` / `_PORT` / `_USER` / `_PASSWORD` / `_DATABASE` | server | Override `postgres.*` |
| `POSTGRES_SSLMODE` | server | disable / require / verify-ca / verify-full |
| `PGMANAGER_LISTEN` | server | Bind address (default `127.0.0.1:8080`) |
| `PGMANAGER_API_PORT` | server | Legacy; only used if `PGMANAGER_LISTEN` unset |
| `PGMANAGER_REQUIRE_TOKEN` | server | `true` (default) to require auth |
| `PGMANAGER_ENCRYPTION_KEY` | server | base64 32-byte at-rest encryption key |
| `PGMANAGER_DATA_DIR` | server | Where `bootstrap-token.txt` is written |
| `PGMANAGER_BOOTSTRAP_TOKEN` | server | Pre-seed initial admin token instead of auto-generating |
| `PGMANAGER_ALLOWED_ORIGINS` | server | Comma-separated CORS list |

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
| Server won't start: `encryption key required` | New version + legacy plaintext rows | Set `PGMANAGER_ENCRYPTION_KEY` (see `pgmanager keygen`) and restart |
| `connection refused` to API | Caddy/pgmanager not running, or wrong URL | `docker compose ps` on the VPS; `curl https://pgm.example.com/api/health` |
| `database already exists for myapp/dev` | Idempotent retry needed | `pgmanager db credentials myapp dev` to fetch existing creds |
| `project not found` | Project deleted or never created | `pgmanager project list`; `project create <name>` |
| TUI shows empty password | List endpoints strip passwords by design | Press `p` to reveal (calls `db credentials`) |
| CI re-run fails on `db create` | DB already exists from first run | Use the get-or-create pattern in Use Case 7 |

---

## Decision tree for agents

When the user asks you to do something with pgmanager, walk this:

1. **No `pgmanager` available?** → install via the curl one-liner.
2. **`pgmanager doctor` reports `no profile configured`?**
   - If you have an API URL and admin token (e.g., user pasted one) → `pgmanager login <url>` and paste.
   - If running locally with Postgres → Use Case 3.
3. **Task is "create a DB for X env"** → `pgmanager db create <project> <env> [pr]` with `--json` if downstream needs the connection string.
4. **Task is "give me the connection string"** → `pgmanager db credentials <project> <env> [pr] --json | jq -r .connection_string`.
5. **Task is "set up CI for PR DBs"** → Use Case 5+6 (or 7 if jobs re-run). Create a `project:<name>:pr:*` token first.
6. **Task is "I lost access"** → Use Case 13.
7. **Task mentions security review** → `pgmanager auth list-tokens` to enumerate; revoke stale ones; rotate admin tokens via create-then-revoke.
