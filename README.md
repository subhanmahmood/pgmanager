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
#   First copy your one-time code: WXYZ-2468
#   Press Enter to open https://pgm.example.com/device in your browser...

pgmanager auth whoami           # sanity check
pgmanager project create myapp
pgmanager db create myapp dev
pgmanager db credentials myapp dev   # shows the connection string
```

`login` runs a device authorization, the same shape as `gh auth login`: the CLI
prints a one-time code, you approve it from the admin UI in a browser that is
already signed in, and this machine receives its own token. No secret is ever
copy-pasted between machines. Add `--scope project:myapp` to say what you want;
the approver decides what you actually get.

Two variations:

- `pgmanager login <url> --with-token` — paste an existing token instead. This
  is the path for CI, and for the bootstrap token on a brand new server (there
  is nobody to approve the first device yet).
- `pgmanager login <url> --no-browser` — print the URL instead of opening one.
  Chosen automatically over SSH.

Profile lives at `~/.config/pgmanager/credentials.yaml` (mode 0600). On macOS the
token itself goes in the Keychain instead of that file — see
[Configuration](#configuration).

To authorize someone else's laptop, have them run `pgmanager login`, then
approve their code from the Devices view in the admin UI — or from a terminal:

```bash
pgmanager auth devices                                  # who is waiting
pgmanager auth approve WXYZ-2468 --scope project:myapp --expires 90d
pgmanager auth deny WXYZ-2468                           # or turn them away
```

Codes expire after 10 minutes, and each one yields its token exactly once.

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
docker compose exec pgmanager pgmanager auth whoami   # admin, via the local socket
```

Server-side admin needs no token at all — see below. The bootstrap token is
still written to `/var/lib/pgmanager/bootstrap-token.txt` for signing into the
admin UI the first time.

Full walkthrough — including TLS, secrets, and the `upgrade.sh` workflow — in [`examples/deploy/README.md`](examples/deploy/README.md).

## Managing admin users

Who may sign in to the UI is an allowlist of email addresses. `pgmanager users`
edits it in the database directly, using the *server* config — it is not an API
call, and there is no HTTP route that can change the allowlist.

That cuts both ways on purpose. No request, however authenticated, can add a
user: a leaked admin token cannot mint itself a UI login that outlives the
token. And provisioning never depends on the API being up, the admin socket
being enabled, or an account already existing, so there is no state in which
the first account can't be created. Run these on the machine hosting
pgmanager (`docker compose exec pgmanager …` for the bundled Deployment).

```bash
pgmanager users add subhan@example.com          # prints a generated password once
pgmanager users add ci@example.com --password-stdin
pgmanager users list
pgmanager users set-password subhan@example.com # the forgot-password path
pgmanager users remove ci@example.com           # their sessions die with them
```

Passwords are hashed with argon2id and never stored or logged in the clear.
Recovery is deliberately an operator action on the box, which is why pgmanager
needs no outbound email. Users can change their own password from the UI;
doing so signs out every browser, as does an operator reset.

Humans get sessions, machines get tokens: `pgmanager users` never issues a
bearer token, and `pgmanager auth create-token` never creates a login.

## Admin from the server itself

Set `api.socket` (`PGMANAGER_SOCKET`) and `pgmanager serve` also listens on a
unix socket. The CLI on that box finds it with no configuration at all — no
token on disk, nothing to rotate:

```bash
pgmanager auth whoami          # local:uid=0,pid=1234 — scopes: admin
pgmanager auth approve WXYZ-2468 --scope project:myapp
```

Being able to open the socket *is* the authorization, so it is created mode
`0660`; set `api.socket_group` to hand access to a group instead of root only.
Requests still run through the same handlers, scope checks and audit log as
HTTP ones, and each line records the calling uid/pid. `--socket <path>` targets
one explicitly; `PGMANAGER_SOCKET=-` disables the probe.

This is what makes the bootstrap token disposable: approve your first laptop
from the server, then `rm bootstrap-token.txt`.

## Admin UI

`pgmanager serve` also hosts a browser UI from `./web/dist` — projects,
databases, connection credentials, password rotation, a data explorer, token
management and PR cleanup, against the same `/api` endpoints the CLI uses.

It is a React app (Vite + Tailwind + shadcn/ui) whose source lives in `web/`;
the built assets in `web/dist` are committed, so a plain `go run` or the Docker
image serves the UI with no Node toolchain involved. See "Working on the admin
UI" below if you want to change it. Dark and light themes both ship, following
your system preference unless you pick one.

The bundled Deployment serves it on the same hostname as the API, at `/`. One
DNS record, one certificate, and the UI calls `/api` same-origin — so no CORS
configuration is needed. `PGMANAGER_WEB_DIR=-` disables the UI and serves the
JSON API only.

Sign in with an email address and password. Accounts come from an allowlist
that can only be edited on the server (see below), and the session is an
`HttpOnly` cookie — no token is ever pasted into a browser, and script on the
page cannot read the credential.

Each database has its own page (`/projects/<project>/databases/<env>`) with its
connection details, a table listing, and the destructive actions. **Rotate
password** issues a new password and optionally terminates open connections;
it asks you to type the database name first whenever the blast radius warrants
it (production, or terminating connections).

From there, **Browse data** opens the explorer
(`/projects/<project>/databases/<env>/explore` — a linkable, reloadable URL, and
the selected table and page live in the query string): its tables down the left,
a page of rows on the right, and insert / edit / delete for individual rows.
Editing addresses a row by its primary key, so a table without one is read-only
there. The server connects as that database's own role, never
the Postgres admin role, so the explorer can only reach what those credentials
already reach — and the routes carry the same scope check as the rest of
`/projects/{name}/databases/{env}`, meaning a token scoped to one project's PR
databases can explore exactly those.

The Devices view is where you approve `pgmanager login` requests: enter the
one-time code (or follow the `/device?code=…` link the CLI prints), see what is
asking and from where, then choose the name, scopes and expiry of the token it
receives. Requested scopes are only a suggestion — what you pick is what it gets.
Tokens you mint this way are attributed to you by email, so the audit log names
a person rather than another token.

Set `PGMANAGER_WEB_DIR` (or `api.web_dir`) to serve the UI from elsewhere, or to
`-` to run API-only.

### Working on the admin UI

Only needed if you are changing the UI itself.

```bash
npm --prefix web install
go run ./cmd/pgmanager serve          # API on :8080
npm --prefix web run dev              # UI on :5173, /api proxied to :8080
```

`npm --prefix web run build` writes `web/dist`, **which is committed** — CI
fails if it drifts from the source, so rebuild and commit it with any UI change.

The Vite dev server sends no CSP, so a violation that would break production is
invisible there. `npm --prefix web run preflight` builds and serves through the
Go server; run it before pushing and keep the browser console clean.

## CLI commands

```
pgmanager login <url>              Authorize this device (--with-token to paste one)
pgmanager logout [profile]         Remove a profile
pgmanager profile list|use|show    Manage saved profiles
pgmanager doctor                   Diagnose: profile, token, server reachability
pgmanager auth whoami              Show current token + scopes
pgmanager auth list-tokens         Enumerate tokens (admin/tokens scope)
pgmanager auth create-token        Mint a scoped token
pgmanager auth revoke-token <pfx>  Revoke a token by its prefix
pgmanager users add|list|remove|set-password
                                   Manage admin UI users (server-side only)
pgmanager auth devices             List devices awaiting authorization
pgmanager auth approve <code>      Approve a device and mint its token
pgmanager auth deny <code>         Reject a device
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

On macOS the bearer token is stored in the **Keychain** (service `pgmanager`,
account = `profile (path to credentials.yaml)`, so profiles of the same name in
different config roots stay separate) and the file records only
`token_source: keyring`. On
other platforms it stays in the file, which is why an existing plaintext
`token:` is always still honoured; `pgmanager auth migrate-keychain` moves those
over, and `PGMANAGER_NO_KEYRING=1` opts out. CI is unaffected — it uses the
environment variables below and never reads either store.

**`pgmanager.yaml`** (server) — read only by `pgmanager serve`. Auto-discovered in `cwd → ~ → ~/.config/pgmanager/ → /etc/pgmanager/`.

The most important environment overrides:

| Variable | Side | Purpose |
|---|---|---|
| `PGMANAGER_API_URL` + `PGMANAGER_API_TOKEN` | client | Synthesize an `env` profile; bypasses `credentials.yaml` (CI path) |
| `PGMANAGER_PROFILE` | client | Pick a saved profile by name |
| `PGMANAGER_NO_KEYRING` | client | Keep tokens in `credentials.yaml` instead of the macOS Keychain |
| `POSTGRES_*` | server | Override `postgres.*` (host, port, user, password, database, sslmode) |
| `POSTGRES_PUBLIC_HOST` / `POSTGRES_PUBLIC_PORT` | server | What clients see in `db create` / `db info` responses |
| `PGMANAGER_LISTEN` | server | Bind address (default `127.0.0.1:8080`) |
| `PGMANAGER_SOCKET` | both | server: local admin socket path. client: where to look for one (`-` disables) |
| `PGMANAGER_SESSION_TTL` | server | How long an admin-UI sign-in lasts (default `336h`, i.e. 14 days) |
| `PGMANAGER_SOCKET_GROUP` | server | Group that owns the admin socket |
| `PGMANAGER_ENCRYPTION_KEY` | server | base64 32-byte at-rest encryption key |
| `PGMANAGER_BOOTSTRAP_TOKEN` | server | Pre-seed initial admin token (skip auto-generation) |
| `PGMANAGER_DATA_DIR` | server | Where `bootstrap-token.txt` is written |

Full reference in the agent skill ([`.claude/skills/pgmanager/SKILL.md`](.claude/skills/pgmanager/SKILL.md) for client, [`.claude/skills/pgmanager-server/SKILL.md`](.claude/skills/pgmanager-server/SKILL.md) for VPS operations).

## REST API

Same surface the CLI talks to. All endpoints under `/api`; everything requires
`Authorization: Bearer pgm_live_...` except `/api/health` and the two
device-authorization entry points (`POST /api/auth/device` and
`POST /api/auth/device/token`) and `POST /api/auth/login`, whose callers by
definition have no credential yet. The admin UI authenticates with a session
cookie instead of a bearer token; `/api/users*` is reachable only over the
local socket.

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
docker run --rm -v "$(pwd):/app" -w /app golang:1.25-alpine \
  sh -c "go test ./..."
```

## License

MIT
