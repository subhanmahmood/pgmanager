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

`pgmanager serve` is the only thing that ever holds Postgres credentials. The
CLI always talks to it over HTTP — there is **no** direct-Postgres client mode.
Two ways to reach it, chosen by where you're standing:

| Mode | When to use | Caller holds |
|------|-------------|--------------|
| **API** | Laptops, CI, anywhere that isn't the server itself | Just `(api_url, scoped_token)` |
| **Socket** | On the box running `serve` | Nothing — permission to open the socket *is* the authorization |

A third caller exists but is not the CLI's business: a **human in the admin
UI**, who signs in with an allowlisted email and password and gets a session
cookie. Humans get sessions, machines get tokens — `pgmanager users` (server-
side only) manages people, `pgmanager auth create-token` manages machines.

Both CLI transports run through the same handlers, so every request is
scope-checked and recorded in the audit log.

```
Laptop ──HTTPS+token──► Caddy ─► pgmanager serve ─► Postgres (localhost on VPS)
                       (443)        (127.0.0.1:8080)         (never exposed)
                                          ▲
On the VPS: pgmanager ──unix socket───────┘  (no token; file permissions gate it)
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

## Use Case 1 — First-time laptop setup

You have an API URL. You do **not** need a token in hand — `login` runs a device
authorization, the same shape as `gh auth login`:

```bash
pgmanager login https://pgm.example.com [--scope project:myapp]
#   First copy your one-time code: WXYZ-2468
#   Press Enter to open https://pgm.example.com/device in your browser...

pgmanager auth whoami       # confirm: token + scopes
pgmanager profile show      # confirm current profile
```

Give the code to whoever administers the server (or approve it yourself in the
admin UI's Devices view, in a browser already signed in). They run
`pgmanager auth approve WXYZ-2468 --scope <what you should have>`, and this
machine receives its own token. Nothing secret is sent between machines. Codes
expire after 10 minutes.

`--scope` on `login` only *requests* — the approver decides what is granted.

Use `pgmanager login <url> --with-token` to paste an existing token instead:
that is the path for CI, and for the very first login against a brand new
server (nobody can approve a device yet). `--no-browser` prints the URL rather
than opening one; it is chosen automatically over SSH.

Profile is stored at `~/.config/pgmanager/credentials.yaml` (mode 0600). Token is never printed by `profile show`.

## Use Case 2 — Local development (no remote server)

You have Postgres running locally and want pgmanager in front of it. Run your
own `serve` with a socket; the CLI finds it with no profile and no token.

```bash
pgmanager init --mode=server        # writes pgmanager.yaml + a fresh crypto key
$EDITOR pgmanager.yaml              # set postgres.password, and:
#   api:
#     socket: /tmp/pgmanager.sock
#   postgres:
#     ssl_mode: disable

pgmanager serve &                   # leave it running

PGMANAGER_SOCKET=/tmp/pgmanager.sock pgmanager project create myapp
PGMANAGER_SOCKET=/tmp/pgmanager.sock pgmanager db create myapp dev
```

Export `PGMANAGER_SOCKET` in your shell profile, or set `api.socket` to the
default `/run/pgmanager/pgmanager.sock` and drop the prefix entirely.

There is no direct-Postgres client mode — it was removed because it bypassed
scope checks and the audit log. Going through `serve` costs one background
process and gets you the same authorization story as production.

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

### Backups

```bash
pgmanager db backup <project> <env> [pr-number]           # back up now
pgmanager db backup <project> <env> --enable               # turn on the scheduled sweep instead
pgmanager db backup <project> <env> --disable               # turn it back off
pgmanager db backups <project> <env> [pr-number]            # list snapshots: id, status, size, started, finished
pgmanager db restore <project> <env> <backup-id>             # restore into a brand-new database
```

Never available for `env=pr` (400 — PR databases aren't backed up). `--enable`/`--disable` only flip
the scheduled-sweep flag; they don't themselves run a backup, and are mutually exclusive with each
other and with the default (run-now) action. `db backup`/`db backups` return 503 if the Server's
`backup:` config isn't set up — that's an operator task, see the `pgmanager-server` skill.

`db restore` never touches the source database — it always creates a new one,
`{project}_{env}_restore_{timestamp}` (its own role, its own password), and prints its credentials
the same way `db create` does. **The env in that output is not the `<env>` you restored — copy it
verbatim** (it's what every other `db` command needs to reach the restore afterwards):

```bash
pgmanager db restore myapp prod 7 --json > restored.json
jq -r .env restored.json                       # e.g. "prod_restore_20260823T101500"
pgmanager db credentials myapp "$(jq -r .env restored.json)"
pgmanager db delete myapp "$(jq -r .env restored.json)"     # when you're done with it
```

Extensions come across with the data: the Server installs whatever the snapshot's archive names
(`postgis`, `pg_trgm`, ...) into the new database before restoring into it, because Postgres lets
only a superuser create most extensions while the restore itself runs as the new database's own
role. A restored database is not itself backed up — the backup routes reject its
`{env}_restore_{timestamp}` address, and the admin UI hides the Backups card for it. Back up the
source database instead.

Restored databases never expire and are never PR databases, so `pgmanager cleanup` never touches
them — delete them explicitly when you're done. `db list` shows a restored row's `env` as that same
addressable segment, and a `restored_from` field naming the backup it came from.

### Tokens

```bash
pgmanager auth whoami
pgmanager auth list-tokens
pgmanager auth create-token --name <n> --scope <s> [--scope <s2> ...] [--expires 90d]
pgmanager auth revoke-token <prefix>
pgmanager auth devices                      # devices awaiting authorization
pgmanager auth approve <code> --scope <s> [--name <n>] [--expires 90d]
pgmanager auth deny <code>
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
pgmanager login <api-url> [--name <profile-name>] [--scope <s>] [--with-token] [--no-browser]
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

Prefer the device flow — it means no token ever travels over chat or email.
Alice runs:

```bash
pgmanager login https://pgm.example.com
#   First copy your one-time code: WXYZ-2468
```

She sends you the code (it is useless without your approval, and it dies in 10
minutes). You check what is asking, then grant:

```bash
pgmanager auth devices        # confirm the client name and IP match Alice
pgmanager auth approve WXYZ-2468 --scope admin --expires 365d
```

Her CLI unblocks and saves the token itself.

Only fall back to `pgmanager auth create-token --name "alice-laptop" --scope admin --expires 365d`
plus a one-time-secret channel when she can't run the flow interactively.

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

Works over both transports (remote API and local socket).

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
| PUT    | `/projects/{name}/databases/{env}/backup` | as above; 400 if `env` is `pr`; 503 if backups aren't configured |
| POST   | `/projects/{name}/databases/{env}/backups` | as above; same 400/503 |
| GET    | `/projects/{name}/databases/{env}/backups` | as above; same 400/503 |
| DELETE | `/projects/{name}/databases/{env}/backups/{id}` | as above; 404 if `{id}` doesn't belong to this database |
| POST   | `/projects/{name}/databases/{env}/backups/{id}/restore` | as above; 400 if the backup hasn't `succeeded` yet |
| POST   | `/cleanup` | `project:*` or `admin` |

PR databases use `env` path segment `pr_<number>` (e.g., `/projects/myapp/databases/pr_42`).
Backups never exist for PR databases — all five backup routes 400 on `env=pr`. A restored
database is reached by putting its full addressable segment in `{env}` on the *non*-backup routes
above, e.g. `GET /projects/myapp/databases/prod_restore_20260823T101500/credentials` — the token's
scope is checked against the *source* env (`prod`), not the literal segment.

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

// PUT /projects/myapp/databases/prod/backup
{ "enabled": true }

// Responses from POST/GET .../backups (BackupResponse):
// { "id": 7, "database_name": "myapp_prod", "object_key": "pgmanager/myapp/myapp_prod/....dump",
//   "size_bytes": 1048576, "status": "succeeded", "started_at": "...", "finished_at": "..." }

// POST .../backups/{id}/restore returns the same shape as POST .../databases
// (project/env/database_name/user_name/password/host/port/connection_string/created_at),
// but env is "{source-env}_restore_{timestamp}", not the env you posted to.
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

curl -sS -X POST -H "Authorization: Bearer $TOKEN" \
  $API/api/projects/myapp/databases/prod/backups | jq          # back up now

curl -sS -X POST -H "Authorization: Bearer $TOKEN" \
  $API/api/projects/myapp/databases/prod/backups/7/restore | jq   # restore into a new database
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
    token_source: keyring    # macOS: secret is in the Keychain, not here
  staging:
    api_url: https://pgm-staging.example.com
    token: pgm_live_yyyyyyyyyyyyyyyy   # non-macOS, or a pre-keychain profile
  server:
    socket: /run/pgmanager/pgmanager.sock
```

A profile sets either `api_url` (+ a token) or `socket` — never Postgres
credentials.

**Token storage.** On macOS `login` puts the token in the Keychain (service
`pgmanager`, account = profile name) and writes `token_source: keyring` here;
elsewhere it stays in this file at 0600. A plaintext `token:` is always read, so
older profiles keep working — `pgmanager auth migrate-keychain` moves them, and
`logout` deletes the Keychain entry. `profile show` and `doctor` report which
store is in use as `token_store`. On the server itself you usually need no profile at all: the CLI
probes `$PGMANAGER_SOCKET` (default `/run/pgmanager/pgmanager.sock`) when no
profile is configured.

### Client environment variables

| Variable | Purpose |
|----------|---------|
| `PGMANAGER_API_URL` / `PGMANAGER_API_TOKEN` | Synthesize an `env` profile; bypasses the file (CI path) |
| `PGMANAGER_PROFILE` | Pick a saved profile by name |
| `PGMANAGER_CONFIG_DIR` / `XDG_CONFIG_HOME` | Override credentials.yaml location |
| `PGMANAGER_NO_KEYRING` | Keep tokens in credentials.yaml instead of the macOS Keychain |
| `PGMANAGER_SOCKET` | Local admin socket to use, when standing on the server (`-` disables the probe) |

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
| `no profile configured` | Fresh laptop with no `credentials.yaml` | `pgmanager login <api-url>` or export `PGMANAGER_API_URL`+`PGMANAGER_API_TOKEN`. On the server itself, check the admin socket exists (`$PGMANAGER_SOCKET`, default `/run/pgmanager/pgmanager.sock`) |
| `login` hangs at "Waiting for approval" | Nobody has approved the code yet | Ask an operator to run `pgmanager auth approve <code> --scope <s>`, or approve it in the admin UI's Devices view |
| `device code expired before it was approved` | Codes live 10 minutes | Re-run `pgmanager login` and get the code approved promptly |
| `device authorization was denied` | An operator rejected it | Ask why; re-run `login` if it was a mistake |
| `401 invalid token` | Token revoked, expired, or never created | `pgmanager auth list-tokens`; re-issue with `auth create-token` |
| `403 insufficient scope` | Token's scopes don't authorize this action | `pgmanager auth whoami`; create a token with a broader scope |
| `connection refused` to API | Server / Caddy not running, or wrong URL | `curl <api>/api/health` from your machine; ask the operator to check `docker compose ps` |
| `database already exists for myapp/dev` | Idempotent retry needed | `pgmanager db credentials myapp dev` to fetch existing creds |
| `project not found` | Project deleted or never created | `pgmanager project list`; `project create <name>` |
| TUI shows empty password | List endpoints strip passwords by design | Press `p` to reveal (calls `db credentials`) |
| CI re-run fails on `db create` | DB already exists from first run | Use the get-or-create pattern in Use Case 5 |
| `db create -x foo` fails: `extension "foo" is not available` | The Server's Postgres image doesn't ship that extension's `.so` | Operator task — see `pgmanager-server` skill → "Server Postgres image & extensions" |
| `db backup`/`db backups`/`db restore` fails: `backups are not configured on this server` (503) | The Server's `backup:` block isn't set, or failed validation at startup | Operator task — see `pgmanager-server` skill → backup config / failed-backup runbook |
| `db backup <project> pr ...` fails: `backups are not available for PR databases` (400) | Backups don't exist for `pr` databases, by design | Not a bug — back up the source env instead, if there is one |
| `db restore` fails: `backup has not completed successfully` (400) | The chosen backup is still `running` or ended `failed` | `pgmanager db backups <project> <env>` to find one with `status: succeeded` |

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
