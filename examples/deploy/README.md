# Deploying pgmanager on a fresh VPS

Five steps to a working remote pgmanager that your laptop and CI talk to over HTTPS.

## Prerequisites
- A VPS with Docker + Docker Compose installed.
- A DNS A/AAAA record pointing one hostname at the VPS — `pgm.example.com` —
  which serves both the API (for the CLI and CI) and the browser admin UI.
- Ports 80 and 443 open to the public; **port 5432 stays closed**.

## 1. Pull the deploy bundle

```bash
ssh root@vps
git clone https://github.com/subhanmahmood/pgmanager.git
cd pgmanager/examples/deploy
```

## 2. Generate secrets and configure

```bash
cp .env.example .env

# Postgres password (any strong random string)
sed -i "s/^POSTGRES_PASSWORD=.*/POSTGRES_PASSWORD=$(openssl rand -hex 24)/" .env

# At-rest encryption key for stored DB passwords
sed -i "s|^PGMANAGER_ENCRYPTION_KEY=.*|PGMANAGER_ENCRYPTION_KEY=$(openssl rand -base64 32)|" .env

# Domain Caddy should serve (API and admin UI share it)
$EDITOR .env  # set PGMANAGER_API_DOMAIN=pgm.example.com
```

> Tip: if you already have the `pgmanager` binary, `pgmanager keygen` generates the encryption key in the right format.

## 3. Boot the stack

```bash
docker compose up -d
```

Caddy fetches a TLS certificate automatically on first request. Postgres and pgmanager start; pgmanager runs its DB migration and writes a bootstrap admin token.

## 4. Check admin access from the server

The container listens on a local admin socket, so the CLI inside it is admin
without any token — being able to open the socket is the authorization:

```bash
docker compose exec pgmanager pgmanager auth whoami
# token: local:uid=0,pid=42
# scopes: admin
```

Create yourself an admin-UI account while you're here — the allowlist is only
editable from this host:

```bash
docker compose exec pgmanager pgmanager users add you@example.com
# prints a generated password once; change it from the UI after signing in
```

The bootstrap token is only needed for API clients that predate device login:

```bash
docker compose exec pgmanager cat /var/lib/pgmanager/bootstrap-token.txt
```

Copy the value, then delete the file to keep the secret out of disk-level snapshots:

```bash
docker compose exec pgmanager rm /var/lib/pgmanager/bootstrap-token.txt
```

## 5. Log in from your laptop

```bash
pgmanager login https://pgm.example.com
#   First copy your one-time code: WXYZ-2468
```

Approve it from the server (or from the Devices view in the admin UI):

```bash
docker compose exec pgmanager pgmanager auth approve WXYZ-2468 --scope admin
```

Back on the laptop, `login` unblocks and saves the profile:

```bash
pgmanager auth whoami
# token: pgm_live_xxxxxxxx
# scopes: admin
```

No secret was copy-pasted between the two machines. `pgmanager login
https://pgm.example.com --with-token` still works if you'd rather paste the
bootstrap token directly.

You're done. To create a project-scoped token for CI:

```bash
pgmanager auth create-token \
  --name "github-ci-myapp" \
  --scope "project:myapp:pr:*" \
  --expires 90d
```

Drop the printed token into your CI secret store as `PGMANAGER_CI_TOKEN`.

## 6. (Optional) Use the admin UI

Open `https://pgm.example.com` — the UI is served at `/` by the same process
that serves `/api`. It covers projects, databases, credentials, token
management and approving device logins, backed by the same endpoints as the CLI.

Sign in with an email and password. Create the first account on the VPS:

```bash
docker compose exec pgmanager pgmanager users add you@example.com
```

That prints a generated password once. No token is ever pasted into a browser:
the session is an `HttpOnly` cookie, so script on the page cannot read it, and
the account allowlist can only be edited here on the server.

To run without the UI (API only), set `PGMANAGER_WEB_DIR=-` on the `pgmanager`
service. The API keeps working on the same hostname.

---

## Sanity check

Confirm Postgres is not exposed externally:

```bash
docker compose port postgres 5432   # should print nothing
```

Confirm Caddy is forwarding:

```bash
curl https://pgm.example.com/api/health
# {"status":"ok","time":"..."}
```

Watch audit logs:

```bash
docker compose logs -f pgmanager | grep audit
```

## Updating

The Server image is published to `ghcr.io/subhanmahmood/pgmanager` on every
release. `docker-compose.yml` pins to the minor float (`:v0.1`) so patch
releases come down on `pull` but a breaking major bump won't surprise you.

The upgrade is two commands:

```bash
docker compose pull pgmanager
docker compose up -d pgmanager
```

Postgres and its data volume are untouched, application databases keep
running, and the metadata migration is idempotent.

A wrapper is shipped for convenience:

```bash
git pull                 # only if compose / Caddyfile changed
./upgrade.sh             # pull current pin and recreate
./upgrade.sh --tag v0.2  # rewrite the pin in compose first, then pull/recreate
```

### Changing the Caddyfile

`docker-compose.yml` bind-mounts `./Caddyfile` as a **single file**, and Docker
resolves a single-file mount to an inode when the container starts. `git pull`,
`sed -i` and most editors write a new file and rename it over the old one, so
the new content arrives on a new inode while the running Caddy keeps reading
the old one. The failure is quiet and convincing: `caddy validate` and `caddy
reload` inside the container both read that same stale inode and report
success, so the config looks applied while the served behaviour never changes.

Restarting the container is what re-resolves the mount:

```bash
docker compose restart caddy
```

`restart` is enough — `up -d --force-recreate` costs a longer outage for every
site Caddy fronts and buys nothing here. `upgrade.sh` does this automatically:
it diffs the host `Caddyfile` against the copy the running container sees and
restarts Caddy only when they differ, so an ordinary image upgrade leaves your
other sites alone.

(Editing *in place* — `docker compose exec caddy vi /etc/caddy/Caddyfile`, or
an editor configured not to rename — keeps the inode and does work with a plain
`reload`. Relying on that is fragile; prefer the restart.)

### Upgrading past the single-hostname change

Deployments created before that change served the admin UI on a second
hostname, `admin.<api domain>`, via its own Caddy site block. The API and the
UI come from the same process and the server does no host-based routing, so
both names were serving identical content; they are now collapsed into one.

After `git pull`:

1. Restart Caddy so the new single-block `Caddyfile` is actually loaded —
   `./upgrade.sh` detects this and does it for you, or `docker compose restart
   caddy` by hand. Without it Caddy keeps the two-host config and keeps
   renewing a certificate for a hostname nothing needs.
2. `PGMANAGER_ADMIN_DOMAIN` in `.env` is now unread and can be deleted.
3. Confirm the UI answers on the API hostname (`https://pgm.example.com`), then
   remove the `admin.<api domain>` DNS record. Do it in that order — the record
   is what let Caddy issue the old certificate, and there is no rush to delete
   it.

### Image tags

Every release publishes four tags to `ghcr.io/subhanmahmood/pgmanager`:

- `vX.Y.Z` — exact, immutable
- `vX.Y` — minor float (recommended for unattended deploys)
- `vX` — major float
- `latest` — most recent stable release

### Making the package public (one-time)

The first time the release workflow pushes to ghcr, the package is created
**private** by default. From the repo admin account:

1. Open `https://github.com/users/subhanmahmood/packages/container/pgmanager/settings`
2. Scroll to "Danger Zone" → **Change visibility** → Public

After that, `docker compose pull` works on any machine without authenticating.

## Connection strings — the public Postgres endpoint

`db create` / `db info` responses include a `connection_string` and a `host`.
The bundled `docker-compose.yml` defaults `POSTGRES_PUBLIC_HOST` to
`PGMANAGER_API_DOMAIN`, so clients receive the same hostname they used to
reach the API. No extra config required.

If you expose Postgres on a *different* hostname than the API (e.g. a separate
DNS record, a load balancer, or a Tailscale-only endpoint), set
`POSTGRES_PUBLIC_HOST` (and optionally `POSTGRES_PUBLIC_PORT`) in `.env`:

```bash
POSTGRES_PUBLIC_HOST=db.example.com
POSTGRES_PUBLIC_PORT=5432
```

Both vars are also read from `pgmanager.yaml` as `postgres.public_host` /
`postgres.public_port` if you're not using compose.
