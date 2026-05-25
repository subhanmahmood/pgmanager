# Deploying pgmanager on a fresh VPS

Five steps to a working remote pgmanager that your laptop and CI talk to over HTTPS.

## Prerequisites
- A VPS with Docker + Docker Compose installed.
- A DNS A/AAAA record pointing a hostname (e.g. `pgm.example.com`) at the VPS.
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

# Domain Caddy should serve
$EDITOR .env  # set PGMANAGER_API_DOMAIN=pgm.example.com
```

> Tip: if you already have the `pgmanager` binary, `pgmanager keygen` generates the encryption key in the right format.

## 3. Boot the stack

```bash
docker compose up -d
```

Caddy fetches a TLS certificate automatically on first request. Postgres and pgmanager start; pgmanager runs its DB migration and writes a bootstrap admin token.

## 4. Grab the bootstrap admin token

```bash
docker compose exec pgmanager cat /var/lib/pgmanager/bootstrap-token.txt
```

Copy the value. On the same machine, immediately delete the file to keep the secret out of disk-level snapshots:

```bash
docker compose exec pgmanager rm /var/lib/pgmanager/bootstrap-token.txt
```

## 5. Log in from your laptop

```bash
pgmanager login https://pgm.example.com
# paste the bootstrap token

pgmanager auth whoami
# token: pgm_live_xxxxxxxx
# scopes: admin
```

You're done. To create a project-scoped token for CI:

```bash
pgmanager auth create-token \
  --name "github-ci-myapp" \
  --scope "project:myapp:pr:*" \
  --expires 90d
```

Drop the printed token into your CI secret store as `PGMANAGER_CI_TOKEN`.

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

```bash
git pull
docker compose pull
docker compose up -d
```

The metadata migration is idempotent. Existing application databases and Postgres users are untouched.

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
