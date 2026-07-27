# Deploying pgmanager on a fresh VPS

Five steps to a working remote pgmanager that your laptop and CI talk to over HTTPS.

## Prerequisites
- A VPS with Docker + Docker Compose installed.
- DNS A/AAAA records pointing two hostnames at the VPS:
  - `pgm.example.com` — the API, for the CLI and CI.
  - `admin.pgm.example.com` — the browser admin UI.
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

# Domains Caddy should serve
$EDITOR .env  # set PGMANAGER_API_DOMAIN=pgm.example.com
              # optionally PGMANAGER_ADMIN_DOMAIN (defaults to admin.<api domain>)
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

The bootstrap token is only needed to sign into the browser admin UI:

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

Open `https://admin.pgm.example.com` and paste an API token to sign in. The UI
covers projects, databases, credentials and token management — the same
operations as the CLI, backed by the same `/api` endpoints.

The token is held in that browser's `localStorage` and sent as a bearer token
to the same origin; nothing else is stored client-side. Because it is just an
API token, scope it to what the person needs — hand out `project:<name>` rather
than `admin` where you can, and revoke it from the Tokens view when done.

To run without the UI (API only), set `PGMANAGER_WEB_DIR=-` on the `pgmanager`
service and drop the admin site block from the `Caddyfile`.

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
