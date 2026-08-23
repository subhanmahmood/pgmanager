# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What pgmanager is

A small Go service + CLI for creating, listing, and deleting PostgreSQL databases per project.

Two kinds of caller, deliberately kept apart: **humans get sessions, machines get tokens.** A person signs in to the admin UI with an allowlisted email and password; a CLI or CI job holds a scoped bearer token, which a person mints for it.

`pgmanager serve` is the only thing that ever holds Postgres credentials. Every client reaches it over HTTP, in one of two ways:

1. **Remote (laptops + CI)** — HTTPS to a `pgmanager serve` on a VPS, with a scoped bearer token.
2. **Local socket (admin work on the server itself)** — a unix socket that `serve` optionally listens on. Opening it *is* the authorization, so there is no token; see `api.socket`.

Both go through the same handlers, so every request is scope-checked and audited. There is no direct-Postgres client path — it was removed because it bypassed both.

All metadata (projects, databases, tokens) lives in a `pgmanager` schema inside the same Postgres server. DB passwords are AES-GCM encrypted at rest with an operator-supplied key.

## Installation

### Quick install (Linux/macOS)

```bash
curl -sSL https://raw.githubusercontent.com/subhanmahmood/pgmanager/master/install.sh | bash
```

### Manual install

Download the binary for your platform from [GitHub Releases](https://github.com/subhanmahmood/pgmanager/releases).

## Quick start — laptop

```bash
pgmanager login https://pgm.example.com
# prints a one-time code; approve it from the admin UI (or `pgmanager auth
# approve <code>` on the server). --with-token pastes an existing token instead.

pgmanager auth whoami        # confirm
pgmanager project create myapp
pgmanager db create myapp dev
```

## Quick start — server (running it on a VPS)

The expected deploy is `examples/deploy/`: docker-compose with Caddy in front for automatic HTTPS. See `examples/deploy/README.md`.

Manual:

```bash
pgmanager init --mode=server         # writes pgmanager.yaml + a fresh encryption key
# edit pgmanager.yaml: set postgres.password
export PGMANAGER_ENCRYPTION_KEY=$(pgmanager keygen)  # or move crypto.key out of YAML
pgmanager serve
# First boot writes the admin token to $PGMANAGER_DATA_DIR/bootstrap-token.txt (mode 0600)
```

`pgmanager serve` binds to `127.0.0.1:8080` by default — put a reverse proxy (Caddy/nginx/Traefik) in front for TLS. Use `--listen 0.0.0.0:8080` to expose it directly (not recommended for the public internet).

## CI usage

CI never holds Postgres credentials. It only needs `(api_url, scoped_token)`:

```yaml
- name: Install pgmanager
  run: curl -sSL https://raw.githubusercontent.com/subhanmahmood/pgmanager/master/install.sh | bash

- name: Create PR database
  run: pgmanager db create myapp pr "${{ github.event.pull_request.number }}" --json > db.json
  env:
    PGMANAGER_API_URL: ${{ vars.PGMANAGER_API_URL }}
    PGMANAGER_API_TOKEN: ${{ secrets.PGMANAGER_CI_TOKEN }}   # scope: project:myapp:pr:*

- name: Expose DATABASE_URL to subsequent steps
  run: echo "DATABASE_URL=$(jq -r .connection_string db.json)" >> $GITHUB_ENV
```

Create the scoped token on your laptop (or wherever your admin token lives):

```bash
pgmanager auth create-token \
  --name "github-ci-myapp" \
  --scope "project:myapp:pr:*" \
  --expires 90d
```

A leaked CI token can only create/delete PR databases in one project — it cannot touch prod, other projects, or admin endpoints.

## Build & Test Commands

### With Go installed locally

```bash
go test ./...                          # all tests
go test -v ./internal/...              # verbose
go test -run TestValidateName ./internal/project
go build -o pgmanager ./cmd/pgmanager
```

### With Docker (preferred if Go not installed)

```bash
docker run --rm -v "$(pwd):/app" -w /app golang:1.25-alpine \
  sh -c "apk add --no-cache gcc musl-dev && go test ./..."

docker build -t pgmanager:latest .
```

## Config files & precedence

There are **two** config files; they serve different audiences and never mix.

### `pgmanager.yaml` (server-side, used by `pgmanager serve`)

Auto-discovered in this order: cwd → `~` → `~/.config/pgmanager/` → `/etc/pgmanager/`. Filenames searched: `pgmanager.yaml`, `pgmanager.yml`, `.pgmanager.yaml`, `.pgmanager.yml`, `config.yaml`.

Key fields:

```yaml
postgres:        # how serve connects to Postgres
  host: localhost
  port: 5432
  user: postgres
  password: ""
  database: postgres
  ssl_mode: require       # use 'disable' only for local development
  # public_host / public_port — host/port advertised to *clients* in
  # `db create` / `db info` responses. Unset = derive from the inbound
  # request Host header (port stripped); last resort is `host`/`port`.
  # The server's own Postgres connection always uses host/port.
  public_host: ""
  public_port: 0
api:
  listen: 127.0.0.1:8080  # bind address; put a proxy in front for TLS
  require_token: true
  # web_dir — directory holding the built admin UI. Empty = "./web/dist" if it
  # exists; "-" disables the UI and serves the JSON API only.
  web_dir: ""
  # socket — optional unix socket for local admin access. Anyone who can open
  # it is treated as admin (file permissions are the authorization), so it is
  # created mode 0660. Empty = disabled.
  socket: ""
  socket_group: ""        # optional group to own the socket
  # session_ttl — how long an admin-UI sign-in lasts. Zero = 14 days.
  session_ttl: 0
crypto:
  key: ""                 # base64, 32 bytes — `pgmanager keygen` to create one
  # key_file: /run/secrets/pgmanager_key  # alternative
data_dir: /var/lib/pgmanager
cleanup:
  default_ttl: 168h
backup:                   # per-database backups to S3-compatible storage; opt-in
  enabled: false           # false = the manager runs with backups disabled entirely
  endpoint: ""              # empty = AWS S3; set for R2/B2/MinIO/etc.
  region: ""
  bucket: ""
  prefix: "pgmanager/"      # always normalized to end in "/"
  access_key_id: ""
  secret_access_key: ""     # never logged, never put in an API response
  # secret_access_key_file: /run/secrets/pgmanager_backup_key  # alternative
  schedule: 24h              # how often the in-process scheduler sweeps due databases
  retention: 7                # succeeded snapshots kept per database (failed ones separately)
```

`serve` validates `backup.*` at startup only when `enabled: true`; a bad bucket/key/secret disables
backups for that run (three `ERROR` log lines, `DisableBackups`) rather than refusing to start —
database provisioning must not go down over a bucket typo. Never available for `pr` databases.

### `credentials.yaml` (client-side, used by every other command)

Stored at `$XDG_CONFIG_HOME/pgmanager/credentials.yaml` (default `~/.config/pgmanager/credentials.yaml`), mode `0600`. A profile sets either `api_url` (+ `token`) or `socket`:

```yaml
current: prod
profiles:
  prod:
    api_url: https://pgm.example.com
    token_source: keyring    # secret is in the macOS Keychain, not this file
  legacy:
    api_url: https://old.example.com
    token: pgm_live_xxxxxxxxxxxxxxxx   # still supported; pre-keychain profiles
  server:
    socket: /run/pgmanager/pgmanager.sock
```

**Where the token lives.** On macOS `pgmanager login` stores it in the Keychain
(service `pgmanager`, account = profile name) and the file keeps only
`token_source: keyring`. Everywhere else it stays in the file at `0600`, because
the Linux Secret Service is often absent and, where present, is readable by any
process running as the same user anyway — it would add a failure mode without
adding a boundary. `PGMANAGER_NO_KEYRING=1` forces the file on macOS too.

A plaintext `token:` is always read, so profiles saved before this keep working.
`pgmanager auth migrate-keychain` moves them into the Keychain; it writes the
secret there before removing it from the file, so an interrupted run cannot lose
the token. `pgmanager logout` deletes the Keychain entry along with the profile.

CI is unaffected: `PGMANAGER_API_URL`/`PGMANAGER_API_TOKEN` bypass the file and
the keychain entirely.

On the server you usually need no profile at all — the CLI probes for the socket by itself.

Managed via `pgmanager login / logout / profile use / profile show`.

### Precedence (highest wins)

1. CLI flags (`--socket`, `--config`, `--profile`).
2. `PGMANAGER_API_URL` + `PGMANAGER_API_TOKEN` env vars (synthesizes an `env` profile, bypasses the file entirely — this is the CI path).
3. `PGMANAGER_PROFILE` env var.
4. `current:` in `credentials.yaml`.
5. A local admin socket, if one exists at `$PGMANAGER_SOCKET` (default `/run/pgmanager/pgmanager.sock`). This is the "running on the server itself" path — no profile needed.
6. `pgmanager.yaml` (server-side only).

### Useful env vars

- `POSTGRES_*` — override server-side `postgres.*` fields.
- `POSTGRES_SSLMODE` — disable, require, verify-ca, verify-full (default: disable).
- `POSTGRES_PUBLIC_HOST` / `POSTGRES_PUBLIC_PORT` — what clients see in `db create` / `db info` responses. Falls back to inbound request Host header, then `POSTGRES_HOST` / `POSTGRES_PORT`.
- `PGMANAGER_LISTEN` — server bind address.
- `PGMANAGER_REQUIRE_TOKEN` — `true` to require auth (default true).
- `PGMANAGER_ALLOWED_ORIGINS` — comma-separated CORS list.
- `PGMANAGER_WEB_DIR` — directory for the static admin UI (`-` disables it).
- `PGMANAGER_SOCKET` — server: local admin socket path. Client: where to look for one (`-` disables the probe).
- `PGMANAGER_SOCKET_GROUP` — group that owns the admin socket.
- `PGMANAGER_SESSION_TTL` — how long an admin-UI sign-in lasts (Go duration; default `336h`).
- `PGMANAGER_ENCRYPTION_KEY` — base64 32-byte key for at-rest encryption.
- `PGMANAGER_DATA_DIR` — where the bootstrap-token file is written.
- `PGMANAGER_BOOTSTRAP_TOKEN` — operator-supplied initial admin token (skip auto-generation).
- `PGMANAGER_API_URL` / `PGMANAGER_API_TOKEN` — client-side; bypass profile file.
- `PGMANAGER_PROFILE` — pick a saved profile by name.
- `PGMANAGER_NO_KEYRING` — client-side; set to anything to keep tokens in `credentials.yaml` instead of the macOS Keychain.
- `PGMANAGER_BACKUP_ENABLED` — `true` to turn on per-database backups (default false).
- `PGMANAGER_BACKUP_ENDPOINT` / `_REGION` / `_BUCKET` / `_PREFIX` — where backups go; empty endpoint means AWS S3.
- `PGMANAGER_BACKUP_ACCESS_KEY_ID` / `_SECRET_ACCESS_KEY` / `_SECRET_ACCESS_KEY_FILE` — how `serve` authenticates to the bucket; the secret is never logged and never enters an API response.
- `PGMANAGER_BACKUP_SCHEDULE` — how often the in-process scheduler sweeps for due databases (Go duration; default `24h`). A bad value is silently ignored, same as `PGMANAGER_SESSION_TTL`.
- `PGMANAGER_BACKUP_RETENTION` — succeeded snapshots kept per database (default `7`); failed snapshots are trimmed to the same count independently.

## Architecture

### Layer structure

- `cmd/pgmanager/main.go` — Cobra CLI; resolves a profile, constructs a `client.Client`, and dispatches.
- `internal/client/` — `Client` interface plus its one implementation, `HTTPClient`. `NewHTTP` talks to a remote `pgmanager serve`; `NewUnix` dials a local admin socket. Pure transport: it imports no Postgres code at all.
- `internal/project/project.go` — core business logic. The implementation behind every API handler.
- `internal/db/postgres.go` — actual `CREATE DATABASE`/`CREATE USER`/`DROP …` operations via pgx.
- `internal/meta/postgres.go` — metadata persistence (projects, databases, tokens). Encrypts/decrypts DB passwords using the key passed to `NewPostgresStore`. Idempotent schema migration.
- `internal/meta/store.go` — Store interface; `mock.go` is the in-memory implementation used in tests.
- `internal/api/` — HTTP server, auth middleware, scoped-token enforcement, audit log. `setupRoutes` builds the route table twice: once behind bearer-token auth (TCP) and once behind `localAuthMiddleware` (the unix socket, where opening the socket *is* the authorization and the caller gets `admin`). Handlers are shared; they only read the principal out of the request context. Request deadlines are per-route (`requestTimeoutMiddleware` in `middleware.go`, which replaced chi's `middleware.Timeout`): every route gets `defaultRequestTimeout` (60s) except `POST .../backups` and `POST .../backups/{id}/restore`, which get `backupRequestTimeout` (60m) *and* a lifted connection write deadline via `http.NewResponseController`, because both wait synchronously on `pg_dump`/`pg_restore` plus an S3 transfer. A nested `middleware.Timeout` could not have done this — a derived context can only shorten its parent's deadline, never lengthen it. `internal/client/http.go` mirrors the split with a second `http.Client` (`longHTTP`, `backupTimeout`), used only by `CreateBackup` and `RestoreBackup`.
- `internal/auth/token.go` — token generation, SHA-256 hashing, scope grammar and authorization.
- `internal/auth/password.go` — argon2id hashing for admin-UI users (PHC-encoded, so parameters can be raised without invalidating existing hashes), plus email normalization/validation and the dummy hash that keeps unknown-address logins as expensive as real ones.
- `internal/auth/session.go` — session cookie name, default TTL, and session-token generation (32 random bytes; only the SHA-256 is stored).
- `internal/api/session_handlers.go` — `login` / `logout` / `changePassword`. The cookie is `HttpOnly`, `SameSite=Lax` (that is the CSRF defence — no separate token) and `Secure` whenever the request arrived over TLS.
- The email allowlist has **no HTTP handler at all**: `pgmanager users` (in `cmd/pgmanager/main.go`) opens the store from the server config and edits it directly, the same way `serve` does. That is both the security property (no request can add a user) and the availability one (provisioning never depends on the API, the socket, or an existing account). `TestNoUserManagementOverHTTP` guards it.
- `internal/auth/device.go` — device-authorization codes: the secret `device_code` the CLI polls with, and the short human-typed `user_code` (`XXXX-XXXX`, ambiguous characters excluded).
- `internal/api/device_handlers.go` — the RFC 8628-shaped device flow. `POST /api/auth/device` and `POST /api/auth/device/token` are unauthenticated by design (see `anonymousPaths` in `auth.go`); listing/approving/denying requires the `token` scope.
- `internal/db/explore.go` + `internal/api/explore_handlers.go` — the data explorer: list tables, page rows, insert/update/delete one row. It connects as the *database's own role* (`connectAs`), never the configured admin user, so it can only reach what those credentials already reach. Every identifier that ends up in SQL is checked against the live catalog first (`describe` → `checkColumn`); values are always `$n` parameters. Update and delete require a key naming *exactly* the primary key, so a request can never widen the match beyond one row, and a table without a primary key is read-only. Routes hang off `/projects/{name}/databases/{env}/tables…` and reuse the same `databaseTarget` + `requireScope` idiom as the backup routes below (this helper was `exploreTarget` before backups shared it).
- `internal/backup/` — the S3 engine, with no callers of its own. `ObjectStore` (`Put`/`Get`/`Delete`) is the seam: `s3.go`'s `NewS3Store` is the only thing that talks to S3 (`aws-sdk-go-v2`), and `memory.go`'s `MemoryStore` is what every other package's tests use instead, with injectable failure knobs (`PutErr`/`GetErr`/`DeleteErr`/`FailPutAfterBytes`) to simulate a bucket that vanishes mid-upload. `pgtools.go`'s `Dumper` shells out to `pg_dump -Fc`/`pg_restore`, connecting as the *database's own role* like `explore.go`'s `connectAs`, and always puts the password in `PGPASSWORD` (env), never argv. `key.go`'s `ObjectKey` lays out `{prefix}{project}/{dbName}/{RFC3339-ish timestamp}-{random nonce}.dump` — the nonce is load-bearing, since the timestamp only has second resolution and a manual backup racing the scheduler would otherwise share a key (and so overwrite, and later delete, each other's object). Nothing parses a key; it is an opaque handle stored in `backups.object_key`.
- `internal/project/backup.go` + `internal/project/restore.go` — the manager's backup/restore surface. `Manager.EnableBackups`/`DisableBackups` (called once at `serve` startup, never mid-request) wire in an `ObjectStore` and `Dumper`; every other backup method 503s via `ErrBackupsDisabled` until that happens, and 400s via `ErrBackupsNotForPR` for `env=pr`. `BackupNow`/`RunDueBackups` share one `io.Pipe`-based flow (`pg_dump` writes into the pipe from a goroutine, `ObjectStore.Put` reads the other end) — every exit path must `CloseWithError` both ends or the dump goroutine leaks forever; `RunDueBackups` runs on an in-process ticker (`backup.schedule`) and is never fatal per-database. `applyRetention` deletes the S3 object before the metadata row, keeping only the newest `retention` succeeded rows (and, independently, the newest `retention` failed rows). `RestoreBackup` always creates a **new** database (`{project}_{env}_restore_{timestamp}`, its own role, `CreateRestoredDatabase`) and never opens the source; on any failure after the create it drops what it made. Every compensating action — that rollback, the partial-object delete and `FinishBackup` in `runBackup`, and the object sweep in `DeleteDatabase`/`DeleteProject` — runs on `cleanupContext`, i.e. `context.WithoutCancel` + a fresh 30s bound, because the usual reason cleanup is running at all is that the request context died mid-dump; inheriting it would make every cleanup fail instantly and leave exactly the debris it exists to remove. Deleting a database or project also deletes its stored objects: the keys are read *before* the metadata goes (`pgmanager.backups` cascades on `databases(id)`), the objects are deleted *after* the drop succeeds, and any failure is logged with the orphaned key rather than blocking the deletion. Restored rows are invisible to `GetDatabase` (`restored_from IS NULL`) and reachable only by name — `resolveDatabase` tries the name probe first, then falls back to the normal `(project, env, pr)` lookup, so `GetDatabase`/`RotatePassword`/`DeleteDatabase` and the explorer all work unchanged against a restore once you have its addressable env segment.
- `internal/api/backup_handlers.go` — `PUT .../backup` (toggle the schedule), `POST .../backups` (back up now), `GET .../backups` (list), `DELETE .../backups/{id}`, `POST .../backups/{id}/restore`, all under `/projects/{name}/databases/{env}/...`. `{env}` on all five is always the *source* database's env (never a restore segment — backing up a restore isn't supported); a restored database is reached by putting its full addressable segment (`prod_restore_20260823T101500`) in `{env}` on every *other* database route instead. `handlers.go`'s `scopeEnv` maps that segment back to its source env (`prod`) for the scope check, while the manager still gets the raw segment — a token scoped to one env can't reach a restore of a different one just because the restore's name doesn't literally match. Errors: `ErrBackupsDisabled`→503, `ErrBackupsNotForPR`→400, `ErrBackupNotFound`→404, `ErrBackupNotRestorable`→400 (backup exists but isn't `succeeded` yet); the configured S3 secret is asserted to never appear in a response body.
- `internal/crypto/aesgcm.go` — AES-256-GCM for at-rest secrets.
- `internal/config/config.go` — server config (`pgmanager.yaml`) loader.
- `internal/config/client.go` — client config (`credentials.yaml`) loader + profile resolution.
- `internal/tui/app.go` — Bubble Tea terminal UI; uses `client.Client`, so it works against either transport.
- `web/` — the admin UI: a Vite + React + TypeScript + Tailwind v4 + shadcn/ui app, served by `pgmanager serve` on the same origin as `/api`. Source in `web/src`, build output in `web/dist`, **which is committed** so `go run` and the Docker image need no Node toolchain (CI fails if it is stale). `npm --prefix web run dev` proxies `/api` to `:8080`.
  - Layout: `src/lib` (typed API client mirroring the Go structs, query client, scope predicates mirroring `internal/auth`, formatters), `src/hooks/queries.ts` (all TanStack Query hooks), `src/components/ui` (generated shadcn, left untouched), `src/components` (app-specific), `src/routes` (one file per screen).
  - **`script-src` stays `'self'`.** No inline scripts, ever — that is why the no-flash theme bootstrap is a real file (`web/public/theme-init.js`) and why `vite.config.ts` sets `modulePreload.polyfill: false` and `assetsInlineLimit: 0` (an inline polyfill and `data:` URIs would both be blocked). `style-src` allows `'unsafe-inline'` because Radix injects a `<style>` element to lock scrolling behind modals; see the comment in `internal/api/middleware.go`.
  - **401 signs the user out; 403 does not.** A bearer token scoped to one project legitimately gets 403 from `/auth/tokens`, so 403 renders a per-view "not permitted" state. `whoami` and the login mutation opt out of the global 401 handler — those 401s are answers, not expiry.
  - Secrets are fetched only on explicit action, never on page load, and the credentials query uses `gcTime: 0`. `SecretDialog` cannot be dismissed by backdrop or Escape.

### Key design rules

- Project names: regex `^[a-z][a-z0-9_]*$`, length 2–32. Reserved: `postgres`, `template0`, `template1`, `admin`, `root`, `system`.
- Environments: `prod`, `dev`, `staging`, `pr`.
- Database naming: `{project}_{env}` or `{project}_pr_{number}`; a restored database is `{project}_{env}_restore_{timestamp}` (`20060102T150405`, UTC) and is addressed by that whole string in the `{env}` slot of every database route — it is never itself backed up or swept by `cleanup`.
- PR DBs get a TTL (default 7 days); `pgmanager cleanup` removes expired ones. Backups never exist for `pr` databases.
- Token format: `pgm_live_<32 url-safe bytes>`. Only SHA-256 stored. Display prefix is the first 16 chars.
- SQL injection prevention everywhere via `pgx.Identifier{}.Sanitize()` and `quoteLiteral` for passwords.
- DB passwords in metadata are AES-GCM encrypted; the key never lives in the DB.
- Admin UI identity: `pgmanager.users` is an allowlist of emails, writable only by `pgmanager users` on the server (no HTTP route exists). Every allowlisted human is admin — being in a table only the server can write *is* the authorization. Sessions live in `pgmanager.sessions` (SHA-256 of the cookie value; rows cascade on user delete, so removing a person ends their access immediately). Changing or resetting a password drops that user's sessions, in one transaction; `CreateSession` additionally refuses to insert against a `password_changed_at` that has moved, so a login racing a reset can't leave a survivor. Login is throttled by `LoginLimiter` (5 attempts / 15 min, per IP *and* per email) because argon2 is expensive enough to be a DoS lever as well as a guessing target.
- Device authorization: codes live in `pgmanager.device_requests`, expire after 10 minutes, and yield their token exactly once (`ConsumeDeviceToken` reads and clears in a single statement). The issued plaintext is AES-GCM encrypted while it waits to be collected — it is the one secret the server has to hand back in the clear.

### Scope grammar

- `admin` — everything.
- `tokens` — only token CRUD (no DB access).
- `project:*` — all projects, all envs.
- `project:<name>` — one project, all envs.
- `project:<name>:env:<env>` — one project, one env (e.g. `dev`).
- `project:<name>:pr:*` — one project, only PR DBs (the CI scope).

## Testing patterns

Tests are table-driven. The mock store (`internal/meta/mock.go`) is used by API and project tests; it satisfies the same `Store` interface as `PostgresStore`. When adding tests that exercise authentication, seed an admin token into the mock store and pass it as `Authorization: Bearer <plain>` — see `internal/api/handlers_test.go` `setupTestServer`.
