---
name: pgmanager
description: Manage PostgreSQL databases with project-based organization using pgmanager. Use when creating, listing, or deleting PostgreSQL databases, managing projects, setting up PR review databases, or configuring CI/CD database workflows.
argument-hint: "[command] [args]"
allowed-tools: Bash(pgmanager *), Bash(curl *), Bash(~/bin/pgmanager *)
---

# pgmanager

pgmanager is a CLI and API tool for managing PostgreSQL databases organized by projects. It creates isolated databases with dedicated users and supports environment-based organization (prod, dev, staging, pr).

All metadata is stored in PostgreSQL itself (in a `pgmanager` schema), making it stateless and shareable across machines and CI environments.

## Installation

```bash
# Quick install (Linux/macOS)
curl -sSL https://raw.githubusercontent.com/subhanmahmood/pgmanager/master/install.sh | bash

# Or download directly to ~/bin
curl -sSL https://github.com/subhanmahmood/pgmanager/releases/latest/download/pgmanager-darwin-arm64 -o ~/bin/pgmanager
chmod +x ~/bin/pgmanager
```

## Quick Start

```bash
# Initialize config in your project directory
pgmanager init

# Edit pgmanager.yaml with your PostgreSQL connection details
# Then create a project and database
pgmanager project create myproject
pgmanager db create myproject dev
```

## CLI Commands

pgmanager auto-discovers `pgmanager.yaml` in the current directory. Use `--config` or `-c` to specify a different path.

### Project Management

```bash
# Create a new project
pgmanager project create <name>

# List all projects
pgmanager project list

# Delete a project and all its databases
pgmanager project delete <name>
```

**Project name rules:**
- Must match regex `^[a-z][a-z0-9_]*$`
- Length: 2-32 characters
- Reserved names: postgres, template0, template1, admin, root, system

### Database Management

```bash
# Create a database (env: prod, dev, staging)
pgmanager db create <project> <env>

# Create a PR database (requires PR number)
pgmanager db create <project> pr <pr-number>

# List all databases (optionally filter by project)
pgmanager db list [project]

# Get database info (no password shown)
pgmanager db info <project> <env> [pr-number]

# Delete a database
pgmanager db delete <project> <env> [pr-number]
```

**Database naming convention:**
- Standard envs: `{project}_{env}` (e.g., `myapp_dev`)
- PR databases: `{project}_pr_{number}` (e.g., `myapp_pr_42`)

### Cleanup

```bash
# Clean up PR databases older than 7 days (default)
pgmanager cleanup

# Clean up PR databases older than custom duration
pgmanager cleanup --older-than 3d
```

Duration format: `7d` (days), `24h` (hours), `1w` (weeks), `30m` (minutes), `60s` (seconds)

### Server Mode

```bash
# Start REST API server
pgmanager serve -p 8080

# Start terminal UI
pgmanager tui
```

## REST API Endpoints

Base URL: `http://localhost:8080/api/v1`

Authentication: Bearer token in `Authorization` header (if PGMANAGER_REQUIRE_TOKEN=true)

### Projects

| Method | Endpoint | Description |
|--------|----------|-------------|
| GET | /projects | List all projects |
| POST | /projects | Create project (`{"name": "myapp"}`) |
| DELETE | /projects/{name} | Delete project |

### Databases

| Method | Endpoint | Description |
|--------|----------|-------------|
| GET | /projects/{name}/databases | List databases for project |
| POST | /projects/{name}/databases | Create database (`{"env": "dev"}` or `{"env": "pr", "number": 42}`) |
| GET | /projects/{name}/databases/{env} | Get database info (use `pr_42` format for PR dbs) |
| DELETE | /projects/{name}/databases/{env} | Delete database |

### Maintenance

| Method | Endpoint | Description |
|--------|----------|-------------|
| GET | /health | Health check |
| POST | /cleanup | Clean up old PR databases (`{"older_than": "7d"}`) |

## CI/CD Usage

### GitHub Actions Example

```yaml
name: PR Database
on:
  pull_request:
    types: [opened, synchronize]

jobs:
  create-db:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4

      - name: Install pgmanager
        run: curl -sSL https://raw.githubusercontent.com/subhanmahmood/pgmanager/master/install.sh | bash

      - name: Create PR database
        run: pgmanager db create myproject pr ${{ github.event.pull_request.number }}
        env:
          POSTGRES_HOST: ${{ secrets.POSTGRES_HOST }}
          POSTGRES_PASSWORD: ${{ secrets.POSTGRES_PASSWORD }}
```

### Cleanup Workflow

```yaml
name: Cleanup PR Databases
on:
  schedule:
    - cron: '0 0 * * *'  # Daily at midnight

jobs:
  cleanup:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4

      - name: Install pgmanager
        run: curl -sSL https://raw.githubusercontent.com/subhanmahmood/pgmanager/master/install.sh | bash

      - name: Cleanup old PR databases
        run: pgmanager cleanup --older-than 7d
        env:
          POSTGRES_HOST: ${{ secrets.POSTGRES_HOST }}
          POSTGRES_PASSWORD: ${{ secrets.POSTGRES_PASSWORD }}
```

## Configuration

### Config File Discovery

pgmanager searches for config files in this order:
1. Current directory: `pgmanager.yaml`, `.pgmanager.yaml`, `config.yaml`
2. Home directory: `~/pgmanager.yaml`, `~/.config/pgmanager/`
3. System: `/etc/pgmanager/`

### pgmanager.yaml Example

```yaml
# Metadata is stored in PostgreSQL itself (in pgmanager schema)
postgres:
  host: localhost
  port: 5432
  user: postgres
  password: secret
  database: postgres
  ssl_mode: disable  # disable, require, verify-ca, verify-full

api:
  port: 8080
  token: your-secret-token
  require_token: true

cleanup:
  default_ttl: 168h  # 7 days for PR databases
```

### Environment Variables

| Variable | Description |
|----------|-------------|
| POSTGRES_HOST | PostgreSQL host |
| POSTGRES_PORT | PostgreSQL port |
| POSTGRES_USER | PostgreSQL admin user |
| POSTGRES_PASSWORD | PostgreSQL admin password |
| POSTGRES_DATABASE | PostgreSQL admin database |
| POSTGRES_SSLMODE | SSL mode (disable, require, verify-ca, verify-full) |
| PGMANAGER_API_PORT | API server port |
| PGMANAGER_API_TOKEN | API bearer token |
| PGMANAGER_REQUIRE_TOKEN | Require API authentication (default: true) |
| PGMANAGER_ALLOWED_ORIGINS | Comma-separated CORS origins |

## Common Workflows

### Setting Up a New Project

```bash
# 1. Initialize config (if not already done)
pgmanager init

# 2. Create the project
pgmanager project create myapp

# 3. Create environment databases
pgmanager db create myapp dev
pgmanager db create myapp staging
pgmanager db create myapp prod
```

### Managing PR Review Databases

```bash
# Create database for PR #42
pgmanager db create myapp pr 42

# Get connection info
pgmanager db info myapp pr 42

# Delete when PR is merged
pgmanager db delete myapp pr 42
```

## Troubleshooting

### Database creation fails
- Ensure PostgreSQL is running and accessible
- Verify admin credentials in config
- Check that project exists first

### API returns 401 Unauthorized
- Include `Authorization: Bearer TOKEN` header
- Verify token matches config or PGMANAGER_API_TOKEN

### PR database not found
- Use format `pr_NUMBER` in API URLs (e.g., `/databases/pr_42`)
- Use `pr NUMBER` in CLI (e.g., `db info myapp pr 42`)
