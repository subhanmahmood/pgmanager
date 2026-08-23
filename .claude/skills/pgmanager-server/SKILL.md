---
name: pgmanager-server
description: Operate the pgmanager Server (VPS-side). Use when deploying pgmanager to a VPS, upgrading the Server image, swapping the Postgres image (e.g. to add pgvector/postgis/timescaledb), enabling Postgres extensions that need shared_preload_libraries, rotating the bootstrap admin token, auditing access, recovering from a lost admin token, or editing pgmanager.yaml. For day-to-day client/CLI tasks (creating DBs, managing tokens, configuring CI), use the `pgmanager` skill instead.
argument-hint: "[operator task]"
allowed-tools: Bash(docker compose *), Bash(docker *), Bash(sudo docker *), Bash(pgmanager *), Bash(psql *), Bash(curl *), Bash(openssl *), Bash(jq *), Bash(pg_dump *), Bash(pg_dumpall *)
---

# pgmanager-server

Operator-side runbook for the pgmanager Server (the thing running on the VPS). For day-to-day client/CLI usage (creating DBs, listing projects, managing tokens, CI integration), use the **`pgmanager`** skill instead.

## Vocabulary

- **CLI** — the `pgmanager` binary clients install. Not what this skill is about.
- **Server** — `pgmanager serve`. The HTTP API. The container in the Deployment named `pgmanager`.
- **Deployment** — the docker-compose stack: Postgres + Server + Caddy (auto-TLS). Lives at `examples/deploy/` in the repo, typically `/root/postgres/` on the VPS.

```
Internet ──443──► Caddy ─► pgmanager serve ─► Postgres (internal network only)
                            (Server)
```

## Use Case 1 — First-time VPS setup

The shipped Deployment is docker-compose with Caddy fronting it for automatic HTTPS.

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

# Admin from inside the container needs no token — the local socket is the
# credential (see "Server-side admin access" below).
docker compose exec pgmanager pgmanager auth whoami

# The bootstrap token is only needed to sign into the browser admin UI.
docker compose exec pgmanager cat /var/lib/pgmanager/bootstrap-token.txt
docker compose exec pgmanager rm /var/lib/pgmanager/bootstrap-token.txt
```

The Server image is pulled from `ghcr.io/subhanmahmood/pgmanager:v0.1` (minor float). Port 5432 is **never** exposed to the public internet — only Caddy on 80/443.

## Use Case 2 — Update the Server

Releases publish four tags to `ghcr.io/subhanmahmood/pgmanager`:

| Tag | Use it when |
|-----|-------------|
| `vX.Y.Z` | Pinning to an exact, immutable release |
| `vX.Y` | Unattended deploys; patch releases come down on `pull` (recommended) |
| `vX` | You're OK auto-getting new minors |
| `latest` | You're tracking the bleeding edge |

The Deployment ships pinned to `vX.Y`. Update with:

```bash
./upgrade.sh                  # pull current pin and recreate the Server
./upgrade.sh --tag v0.2       # rewrite the pin in compose first, then pull/recreate
```

The script only touches the `pgmanager` container. Postgres and its data volume are untouched, application databases keep running, and the metadata migration is idempotent.

Equivalent manual form (no script needed):

```bash
docker compose pull pgmanager && docker compose up -d pgmanager
```

### Making the ghcr package public (one-time, first release only)

The first time the release workflow pushes to ghcr, the package is created **private** by default. From the repo admin account:

1. Open `https://github.com/users/<owner>/packages/container/pgmanager/settings`
2. Scroll to "Danger Zone" → **Change visibility** → Public

After that, `docker compose pull` works on any machine without authenticating.

## Use Case 3 — Migrate from an old version (plaintext passwords)

If you ran pgmanager before scoped tokens + encryption existed:

```bash
# On the VPS
export PGMANAGER_ENCRYPTION_KEY=$(pgmanager keygen)
# Persist this — losing it loses access to stored DB passwords.

docker compose pull pgmanager
docker compose up -d pgmanager
docker compose logs pgmanager     # confirm migration completed
```

The Server reads each plaintext row, encrypts it, drops the legacy column. Idempotent. Application databases and their users are untouched.

If you start without `PGMANAGER_ENCRYPTION_KEY` and legacy rows exist, the Server refuses to start with: `encryption key required: set PGMANAGER_ENCRYPTION_KEY`.

---

## Server Postgres image & extensions

`pgmanager` itself never installs extensions onto the Postgres server — it can only `CREATE EXTENSION` against `.so` files the underlying image already ships. So "can my app use extension X?" comes down to "is X in `pg_available_extensions` on the VPS Postgres?".

### What's bundled in the shipped Deployment

`examples/deploy/docker-compose.yml` uses **`postgres:17-alpine`** out of the box — that's just stock Postgres + the standard `postgresql-contrib` extensions (pgcrypto, uuid-ossp, hstore, citext, ltree, btree_gin, btree_gist, postgres_fdw, dblink, file_fdw, pg_trgm, fuzzystrmatch, unaccent, tablefunc, isn, intarray, cube, bloom, xml2, pg_stat_statements, pg_buffercache, pg_prewarm, pgstattuple, amcheck, adminpack — ~35 in total).

It does **not** ship pgvector, postgis, timescaledb, pg_cron, pgaudit, or pg_partman.

To see exactly what's available on a given server:

```bash
sudo docker exec postgres psql -U postgres \
  -c "SELECT name, default_version FROM pg_available_extensions ORDER BY name;"
```

Anything in that list is immediately usable via `pgmanager db create <project> <env> -x <name>`.

### Adding extensions that aren't bundled

Common asks and where to get them:

| Extension | Purpose | Lives in |
|-----------|---------|----------|
| `vector` | Vector similarity (embeddings) | `pgvector/pgvector:pg16` |
| `postgis` | Real geospatial (geometry/geography) | `imresamu/postgis-pgvector:16-3.5-0.8.1`, or apt `postgresql-16-postgis-3` |
| `timescaledb` | Time-series, continuous aggregates | `timescale/timescaledb-ha:pg16` (also bundles pgvector + pgvectorscale) |
| `pgvectorscale` | Faster ANN (StreamingDiskANN) than pgvector | Same `timescaledb-ha:pg16` image |
| `pg_cron` | In-DB scheduled jobs | apt `postgresql-16-cron`, **needs `shared_preload_libraries`** |
| `pgaudit` | Audit logging | apt `postgresql-16-pgaudit`, **needs `shared_preload_libraries`** |
| `pg_partman` | Auto partitioning | apt `postgresql-16-partman` |

Two routes:

**Route A — Switch to a prebuilt image that bundles them.** Cleanest when a matching image exists:

```yaml
# /root/postgres/docker-compose.yml
services:
  postgres:
    image: pgvector/pgvector:pg16   # or imresamu/postgis-pgvector:16-3.5-0.8.1, etc.
```

```bash
sudo docker compose --project-directory /root/postgres pull postgres
sudo docker compose --project-directory /root/postgres up -d postgres
```

Stay on the same PG major (binaries aren't cross-major-compatible).

**Route B — Custom Dockerfile layered on the current image.** When no prebuilt image matches, or you need a specific combination:

```dockerfile
# /root/postgres/postgres-custom/Dockerfile
FROM pgvector/pgvector:pg16
RUN apt-get update \
 && apt-get install -y --no-install-recommends \
      postgresql-16-postgis-3 \
      postgresql-16-cron \
      postgresql-16-pgaudit \
 && rm -rf /var/lib/apt/lists/*
```

In `docker-compose.yml`, replace `image:` with `build: ./postgres-custom`, then `docker compose build postgres && docker compose up -d postgres`.

### The `shared_preload_libraries` gotcha

A handful of extensions can't be turned on with just `CREATE EXTENSION` — they need their `.so` loaded at Postgres startup. The ones in the table above marked **needs `shared_preload_libraries`** all fall in this bucket (also `pg_stat_statements`, `timescaledb`, `citus`).

Symptom: `CREATE EXTENSION pg_cron` returns `ERROR: pg_cron must be loaded via shared_preload_libraries`.

Fix: add the setting to the postgres service in `docker-compose.yml`:

```yaml
postgres:
  command: >
    postgres
    -c shared_preload_libraries=pg_stat_statements,pg_cron,pgaudit
    -c pg_cron.database_name=postgres
```

`docker compose up -d postgres` to apply (one restart). Order can matter — `timescaledb` wants to be first if it's in the list. Once loaded, `pgmanager db create … -x pg_cron` works normally.

### Image-swap procedure (safe defaults)

The PGDATA volume is reused across image swaps as long as the PG major stays the same. To minimize risk:

1. **Backup first.** `pg_dumpall` to a file, plus per-DB `pg_dump -Fc` for anything with vector/geom columns the dump can't read (broken `.so` would abort). Keep both — the volume is the source of truth, the dumps are insurance.
2. **Back up the compose file.** `cp /root/postgres/docker-compose.yml /root/postgres/docker-compose.yml.bak.<timestamp>` before editing.
3. **Pull, then recreate just postgres.** `docker compose pull postgres && docker compose up -d postgres`. Other services keep running.
4. **Verify** `pg_available_extensions` shows the new ones, and existing DBs still respond. Check logs for collation-version warnings — none expected within the same glibc family (e.g., Debian → Debian), but Alpine(musl) → Debian(glibc) on the same volume occasionally warrants `REINDEX` per affected DB (or `ALTER DATABASE x REFRESH COLLATION VERSION;`).

### Updating existing DBs after an image change

`CREATE EXTENSION` is per-database. Installing pgvector on the image makes it *available* — existing databases still need to opt in:

```bash
# Existing DB needs the extension:
sudo docker exec postgres psql -U postgres -d content_dev -c "CREATE EXTENSION IF NOT EXISTS vector;"

# Catalog has an older version after image upgrade?
sudo docker exec postgres psql -U postgres -d content_dev -c "ALTER EXTENSION vector UPDATE;"
```

For new DBs, `pgmanager db create -x vector` handles it automatically.

---

## Use Case 4 — Audit who accessed what

Each authenticated request emits one structured audit line:

```bash
docker compose logs -f pgmanager | grep audit
# audit method=POST path=/api/projects/myapp/databases status=201 duration=124ms ip=1.2.3.4 token=pgm_live_abcd1234 scopes=project:myapp:pr:*
```

Pair with `pgmanager auth list-tokens` to map token prefixes to names.

## Server-side admin access (the local socket)

`api.socket` / `PGMANAGER_SOCKET` makes `pgmanager serve` listen on a unix
socket in addition to TCP. The CLI on that box discovers it automatically — no
profile, no token, nothing on disk to rotate:

```bash
docker compose exec pgmanager pgmanager auth whoami
# token: local:uid=0,pid=42
# scopes: admin
```

Being able to open the socket **is** the authorization, so it is created mode
`0660` and lives inside the container by default. Set `api.socket_group` to
grant a group access instead of root only. Requests still go through the same
handlers, scope checks and audit log; each line names the calling uid/pid, so
"who ran this" survives.

Use `--socket <path>` to target one explicitly, or `PGMANAGER_SOCKET=-` to stop
the CLI probing for one.

## Use Case — Manage who can sign in to the admin UI

The UI is gated by an allowlist of email addresses. `pgmanager users` edits it
in the database directly using the server config — there is no API route for
it, so no token can add a user remotely, and equally no amount of API or socket
misconfiguration can stop you provisioning the first account.

```bash
docker compose exec pgmanager pgmanager users add subhan@example.com
# Added subhan@example.com
#
# PASSWORD (save this — it will not be shown again):
#   WxdGciKs8VMRWqL5JVom

docker compose exec pgmanager pgmanager users list
docker compose exec -T pgmanager pgmanager users add hasin@example.com --password-stdin < pw.txt
```

Passwords are argon2id-hashed; nothing stores or logs them in the clear.

**Forgot a password?** There is no email delivery in pgmanager, by design —
recovery is an operator action here:

```bash
docker compose exec pgmanager pgmanager users set-password subhan@example.com
```

That prints a new password and signs out every existing session for that user,
which is what you want if the reason for the reset is that you no longer trust
what is signed in. `pgmanager users remove <email>` does the same and takes the
account with it.

Users change their own password from the admin UI (Maintenance → Change
password); doing so also signs out every browser.

## Use Case — Authorize a teammate's laptop

They run `pgmanager login https://pgm.example.com` and read you the one-time
code it prints. The code is worthless without your approval and expires in 10
minutes, so it is safe to send over chat.

```bash
docker compose exec pgmanager pgmanager auth devices
# CODE         CLIENT           IP            REQUESTED SCOPES
# WXYZ-2468    alices-laptop    203.0.113.9   project:myapp

docker compose exec pgmanager pgmanager auth approve WXYZ-2468 \
  --scope project:myapp --expires 90d
```

Their CLI picks the token up on its next poll and saves it. Check the client
name and IP look right before approving — and grant the narrowest scope that
does the job; what they *requested* is only a suggestion.
`pgmanager auth deny WXYZ-2468` turns down anything you don't recognise.

## Use Case 5 — Lost the admin token

If no admin token works anymore (lost, revoked, expired all):

```bash
# On the VPS, revoke any stale admin tokens, then let auto-bootstrap mint a fresh one.
docker compose exec pgmanager sh -c "
  echo \"UPDATE pgmanager.tokens SET revoked_at = NOW() WHERE 'admin' = ANY(scopes) AND revoked_at IS NULL\" \
    | psql -h postgres -U postgres
"
docker compose restart pgmanager
docker compose exec pgmanager cat /var/lib/pgmanager/bootstrap-token.txt
```

Alternative: set `PGMANAGER_BOOTSTRAP_TOKEN=pgm_live_...` in `.env` and restart — pgmanager registers it as the new admin.

## Use Case 7 — Configure backups to S3-compatible storage

Backups (`prod`/`dev`/`staging`, never `pr`) are off by default. To turn them on: pick a bucket,
create an access key scoped to it (least-privilege: `PutObject`/`GetObject`/`DeleteObject`/`ListBucket`
on that bucket+prefix only), and fill in `.env` or `pgmanager.yaml`:

```bash
# .env — R2/B2/MinIO/etc. set PGMANAGER_BACKUP_ENDPOINT; leave it empty for AWS S3.
PGMANAGER_BACKUP_ENABLED=true
PGMANAGER_BACKUP_BUCKET=my-pgmanager-backups
PGMANAGER_BACKUP_ACCESS_KEY_ID=AKIA...
PGMANAGER_BACKUP_SECRET_ACCESS_KEY=...
# or, to keep the secret off the .env file entirely:
# PGMANAGER_BACKUP_SECRET_ACCESS_KEY_FILE=/run/secrets/pgmanager_backup_key
```

```bash
docker compose up -d pgmanager
docker compose logs pgmanager | grep -i backup
# "Backups enabled: bucket my-pgmanager-backups, schedule 24h0m0s, retention 7"
```

Then, per database that should be swept automatically:

```bash
docker compose exec pgmanager pgmanager db backup myapp prod --enable
```

`--enable` only flips the flag — nothing runs until the next scheduler tick (or `db backup myapp prod`
with no flags, for an immediate one-off). Verify a snapshot landed:

```bash
docker compose exec pgmanager pgmanager db backups myapp prod
```

### Runbook — a backup failed

```bash
docker compose exec pgmanager pgmanager db backups myapp prod
# ID   STATUS    SIZE    STARTED              FINISHED
# 9    failed    -       2026-08-23 03:00:01  2026-08-23 03:00:04
#      error: pg_dump: connection to server failed: ...
```

1. **Read the `error` line first** — it's `pg_dump`'s/`pg_restore`'s own stderr, wrapped, not a
   pgmanager-invented message. Common causes: the target database's role was rotated mid-dump
   (rare — `runBackup` uses the metadata store's current password), the bucket credentials expired
   or were revoked, or the bucket/network was briefly unreachable.
2. **Check the client/server version gate.** At startup the Server runs `pg_dump --version` against
   the binary baked into its own image (`postgresql17-client`, from `Dockerfile`) and refuses to
   enable backups at all if it's older than the Postgres server's major version — that failure shows
   up as three `ERROR` lines in `docker compose logs pgmanager` right after start, not as a per-backup
   failure. If backups never ran a single time, check there, not the per-database list.
3. **A single failed row never blocks the next attempt** — the scheduler and `db backup` both just
   try again on their own schedule/on demand. Failed rows are retained (trimmed to `retention`,
   independently from succeeded ones) so the history survives.
4. **The failed dump left no S3 object.** `runBackup` deletes the (possibly partial) object on any
   failure before recording the row as `failed`, so there's nothing to clean up in the bucket by hand.
5. **If backups are 503ing entirely** (`backups are not configured on this server`), the Server
   disabled itself at startup — `docker compose logs pgmanager` names the step (`config`/`init`/
   `version check`/`probe`) and the reason, but never the secret. Fix the config, then
   `docker compose restart pgmanager`; it re-probes on every start, there is no separate re-enable
   command.

## Use Case 8 — Restore a backup

Restore always creates a **brand-new** database — the source is never opened or written to, so
there's no "are you sure" moment where the wrong choice destroys data:

```bash
docker compose exec pgmanager pgmanager db backups myapp prod            # find a succeeded id
docker compose exec pgmanager pgmanager db restore myapp prod 9 --json > restored.json
jq -r .env restored.json          # e.g. "prod_restore_20260823T101500" — copy this verbatim
```

The new database (`myapp_prod_restore_20260823T101500`, its own role and password) never expires and
is invisible to `pgmanager cleanup` — delete it explicitly with
`pgmanager db delete myapp "$(jq -r .env restored.json)"` once you're done with it, or it sits there
indefinitely.

## Use Case 9 — Inspect or repair the metadata schema

The metadata lives in a `pgmanager` schema in the same Postgres server. Useful read-only queries:

```bash
sudo docker exec postgres psql -U postgres <<'SQL'
\dt pgmanager.*
SELECT name, scopes, revoked_at, expires_at FROM pgmanager.tokens ORDER BY created_at DESC;
SELECT project, env, pr_number, created_at, expires_at FROM pgmanager.databases ORDER BY created_at DESC LIMIT 20;
-- Snapshot history. object_key is the S3 key; error is only set on failed rows.
-- restored_from on pgmanager.databases (above) points back at the backup id a
-- restored database came from — there is no reverse index the other way.
SELECT database_id, status, size_bytes, started_at, finished_at, error
  FROM pgmanager.backups ORDER BY started_at DESC LIMIT 20;
-- In-flight `pgmanager login` attempts. Rows expire after 10 minutes and are
-- purged at startup and on `pgmanager cleanup`.
SELECT user_code, client_name, client_ip, status, approved_by, expires_at
  FROM pgmanager.device_requests ORDER BY created_at DESC LIMIT 20;
-- Admin UI accounts and live browser sessions. password_hash is argon2id and
-- is never reversible; sessions store only a hash of the cookie value.
SELECT email, created_by, last_login_at, disabled_at FROM pgmanager.users ORDER BY email;
SELECT u.email, s.created_ip, s.last_seen_at, s.expires_at
  FROM pgmanager.sessions s JOIN pgmanager.users u ON u.id = s.user_id;
SQL
```

Don't hand-edit rows unless you know what you're doing — the Server treats this table as the source of truth.

---

## Server config reference

### `pgmanager.yaml`

Auto-discovered in this order: `cwd → ~ → ~/.config/pgmanager/ → /etc/pgmanager/`. Override with `--config`. Used only by `pgmanager serve`.

```yaml
postgres:
  host: localhost
  port: 5432
  user: postgres
  password: ""
  database: postgres
  ssl_mode: require       # 'disable' only for local dev
  # public_host / public_port — what clients see in `db create` / `db info`
  # responses. Falls back to inbound Host header, then host/port.
  public_host: ""
  public_port: 0
api:
  listen: 127.0.0.1:8080  # bind address; put Caddy in front for TLS
  require_token: true     # refuse to start without auth
  allowed_origins: []     # CORS list; usually empty
  socket: ""              # local admin socket; opening it grants admin (mode 0660)
  socket_group: ""        # optional group to own the socket
  session_ttl: 0          # admin-UI session lifetime; 0 = 14 days
crypto:
  key: ""                 # 32-byte base64; or use key_file, or env
  # key_file: /run/secrets/pgmanager_key
data_dir: /var/lib/pgmanager   # bootstrap-token.txt lives here
cleanup:
  default_ttl: 168h
backup:                   # per-database backups to S3-compatible storage; opt-in, off by default
  enabled: false
  endpoint: ""              # empty = AWS S3; set for R2/B2/MinIO/etc.
  region: ""
  bucket: ""
  prefix: "pgmanager/"       # always normalized to end in "/"
  access_key_id: ""
  secret_access_key: ""      # or secret_access_key_file — never logged, never returned by the API
  schedule: 24h                # how often the in-process scheduler sweeps due databases
  retention: 7                  # succeeded snapshots kept per database (failed ones trimmed separately)
```

### Environment variables

| Variable | Purpose |
|----------|---------|
| `POSTGRES_HOST` / `_PORT` / `_USER` / `_PASSWORD` / `_DATABASE` | Override `postgres.*` |
| `POSTGRES_SSLMODE` | disable / require / verify-ca / verify-full |
| `POSTGRES_PUBLIC_HOST` / `POSTGRES_PUBLIC_PORT` | Client-reachable Postgres endpoint advertised in `db create` / `db info` responses |
| `PGMANAGER_LISTEN` | Bind address (default `127.0.0.1:8080`) |
| `PGMANAGER_API_PORT` | Legacy; only used if `PGMANAGER_LISTEN` unset |
| `PGMANAGER_REQUIRE_TOKEN` | `true` (default) to require auth |
| `PGMANAGER_ALLOWED_ORIGINS` | Comma-separated CORS list |
| `PGMANAGER_SOCKET` | Local admin socket path; callers are admin (client-side: where to look for one, `-` disables) |
| `PGMANAGER_SOCKET_GROUP` | Group that owns the admin socket |
| `PGMANAGER_SESSION_TTL` | How long an admin-UI sign-in lasts (default `336h`) |
| `PGMANAGER_ENCRYPTION_KEY` | base64 32-byte at-rest encryption key |
| `PGMANAGER_DATA_DIR` | Where `bootstrap-token.txt` is written |
| `PGMANAGER_BOOTSTRAP_TOKEN` | Operator-supplied initial admin token (skip auto-generation) |
| `PGMANAGER_BACKUP_ENABLED` | `true` to turn on per-database backups (default false) |
| `PGMANAGER_BACKUP_ENDPOINT` / `_REGION` / `_BUCKET` / `_PREFIX` | Where backups go; empty endpoint means AWS S3 |
| `PGMANAGER_BACKUP_ACCESS_KEY_ID` / `_SECRET_ACCESS_KEY` / `_SECRET_ACCESS_KEY_FILE` | Bucket credentials; the secret is never logged and never returned by the API |
| `PGMANAGER_BACKUP_SCHEDULE` | How often the scheduler sweeps for due databases (Go duration, e.g. `24h`; bad values are silently ignored) |
| `PGMANAGER_BACKUP_RETENTION` | Succeeded snapshots kept per database (default `7`) |

The bundled `examples/deploy/docker-compose.yml` reads all of these from `.env`.

---

## Troubleshooting

| Symptom | Likely cause | Fix |
|---------|--------------|-----|
| Server won't start: `encryption key required` | Legacy plaintext rows present, key not set | `export PGMANAGER_ENCRYPTION_KEY=$(pgmanager keygen)`; persist it, then restart. **Don't lose the key.** |
| `docker compose pull` returns `denied` for ghcr image | Package is still private | Flip to Public via `https://github.com/users/<owner>/packages/container/pgmanager/settings` |
| `db create -x foo` returns `extension "foo" is not available` | Image's Postgres doesn't ship that `.so` | `SELECT name FROM pg_available_extensions WHERE name='foo';` → if empty, swap the image (Route A/B above) |
| `CREATE EXTENSION` says `must be loaded via shared_preload_libraries` | Extension needs preloading at server start | Add to `shared_preload_libraries` in postgres command/config and restart the container |
| `db create` succeeds but client connects to wrong host | `POSTGRES_PUBLIC_HOST` mismatch | Set it explicitly in `.env`; falls back to the inbound `Host` header otherwise |
| Caddy can't fetch a cert | DNS not pointing at the VPS, or port 80/443 blocked | `curl -I http://pgm.example.com` from outside; `docker compose logs caddy` |
| pgmanager container is healthy but `/api/health` 502s through Caddy | Caddy → pgmanager network mismatch | `docker compose ps`; ensure both are on `postgres-net`; check Caddyfile target hostname matches the service name |
| Upgrade-then-rollback: old container won't come back | New release migrated the schema | The Server only adds columns; rollback usually works. Worst case, restore from the `pg_dump` backup from before the upgrade. |
| Backups all 503 with `backups are not configured on this server` | `backup.enabled` is false, or `Validate()`/bucket probe failed at startup | `docker compose logs pgmanager \| grep -i backup` — the ERROR lines name which startup step failed and why (never the secret); fix `backup:` and restart |
| A single backup shows `status: failed` | `pg_dump`/upload error for that run only — see Use Case 7's runbook | `pgmanager db backups <project> <env>` for the `error` column; the scheduler/`db backup` will simply try again |
| `db restore` returns `backup has not completed successfully` | Chosen backup is `running` or `failed`, not `succeeded` | Pick a `succeeded` id from `db backups` |

`pgmanager doctor` (run on the client side from your laptop) reports active profile, mode, server reachability, and whoami — useful as a smoke test after any Server change.

---

## Decision tree for agents

1. **Task is "deploy pgmanager to a VPS"** → Use Case 1.
2. **Task is "upgrade the Server" / "update to latest version"** → Use Case 2 (`./upgrade.sh` on the VPS, possibly `--tag` for a major bump).
3. **Task is "this DB needs extension X but `db create -x X` says it's not available"** → Use Case "Adding extensions". Check `pg_available_extensions`; if empty, pick the image-swap or custom-Dockerfile path. Don't try `CREATE EXTENSION` until the `.so` is on disk.
4. **Task is "I can't get in / no admin token works"** → Use Case 5.
5. **Task is "who's been calling the API?"** → Use Case 4 (audit log tail).
6. **Task is "switch Postgres to add pgvector/postgis/etc."** → Image-swap procedure. Back up first, swap the image:, recreate just postgres, verify `pg_available_extensions`.
7. **Task is "set up / debug backups to S3"** → Use Case 7 (config + enable per-database), or its runbook if a backup already failed.
8. **Task is "restore a database from a backup"** → Use Case 8. Always creates a new database; the source is untouched.
9. **Task is anything client-side** (create a DB, mint a token, set up CI, run `db backup`/`db restore` day-to-day) → wrong skill; use `pgmanager`.
