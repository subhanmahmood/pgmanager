# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Installation

### Quick install (Linux/macOS)

```bash
curl -sSL https://raw.githubusercontent.com/subhanmahmood/pgmanager/master/install.sh | bash
```

### Manual install

Download the binary for your platform from [GitHub Releases](https://github.com/subhanmahmood/pgmanager/releases):

- `pgmanager-linux-amd64` - Linux x86_64
- `pgmanager-linux-arm64` - Linux ARM64
- `pgmanager-darwin-amd64` - macOS Intel
- `pgmanager-darwin-arm64` - macOS Apple Silicon

```bash
# Example for Linux amd64
curl -sSL https://github.com/subhanmahmood/pgmanager/releases/latest/download/pgmanager-linux-amd64 -o pgmanager
chmod +x pgmanager
sudo mv pgmanager /usr/local/bin/
```

### Quick Start

```bash
# Initialize config in your project directory
pgmanager init

# Edit pgmanager.yaml with your PostgreSQL connection details
# Then create a project and database
pgmanager project create myproject
pgmanager db create myproject dev
```

### CI Usage

```yaml
# GitHub Actions example
- name: Install pgmanager
  run: curl -sSL https://raw.githubusercontent.com/subhanmahmood/pgmanager/master/install.sh | bash

- name: Create PR database
  run: pgmanager db create myproject pr ${{ github.event.pull_request.number }}
  env:
    POSTGRES_HOST: ${{ secrets.POSTGRES_HOST }}
    POSTGRES_PASSWORD: ${{ secrets.POSTGRES_PASSWORD }}
```

## Build & Test Commands

### With Go installed locally

```bash
# Run all tests
go test ./...

# Run tests with verbose output
go test -v ./internal/...

# Run a specific test
go test -run TestValidateName ./internal/project

# Build binary
go build -o pgmanager ./cmd/pgmanager
```

### With Docker (preferred if Go not installed)

```bash
# Run all tests via Docker
docker run --rm -v "$(pwd):/app" -w /app golang:1.23-alpine \
  sh -c "apk add --no-cache gcc musl-dev && go test ./..."

# Run tests with verbose output via Docker
docker run --rm -v "$(pwd):/app" -w /app golang:1.23-alpine \
  sh -c "apk add --no-cache gcc musl-dev && go test -v ./internal/..."

# Build Docker image
docker build -t pgmanager:latest .
```

## Running the Application

```bash
# Initialize config in current directory
./pgmanager init

# CLI commands (auto-discovers pgmanager.yaml in current directory)
./pgmanager project list
./pgmanager db create myproject dev

# Or specify config explicitly
./pgmanager --config /path/to/pgmanager.yaml project list

# Start REST API server
./pgmanager serve -p 8080

# Start terminal UI
./pgmanager tui
```

### Config File Discovery

pgmanager automatically searches for config files in this order:
1. Current directory: `pgmanager.yaml`, `pgmanager.yml`, `.pgmanager.yaml`, `.pgmanager.yml`, `config.yaml`
2. Home directory: `~/pgmanager.yaml`, `~/.config/pgmanager/`
3. System: `/etc/pgmanager/`

### Running via Docker

```bash
# Run any command via Docker
docker run --rm -v "$(pwd)/pgmanager.yaml:/etc/pgmanager/pgmanager.yaml" \
  pgmanager:latest pgmanager project list

# Start API server with port mapping
docker run --rm -p 8080:8080 -v "$(pwd)/pgmanager.yaml:/etc/pgmanager/pgmanager.yaml" \
  pgmanager:latest pgmanager serve -p 8080
```

## Architecture

pgmanager is a PostgreSQL database management tool with project-based organization. All metadata is stored directly in PostgreSQL (in a `pgmanager` schema), making it fully stateless and shareable across machines and CI environments.

### Layer Structure

- **cmd/pgmanager/main.go** - CLI entry point with all Cobra commands defined
- **internal/project/project.go** - Core business logic (Manager struct orchestrates all operations)
- **internal/db/postgres.go** - PostgreSQL operations (create/drop databases and users via pgx)
- **internal/meta/postgres.go** - Metadata persistence in PostgreSQL (projects, database records, TTL tracking)
- **internal/meta/store.go** - Store interface definition
- **internal/api/** - REST API server using chi router with Bearer token auth
- **internal/tui/app.go** - Terminal UI using Bubble Tea
- **internal/config/config.go** - YAML config with environment variable overrides and auto-discovery

### Key Design Patterns

- Project names validated via regex `^[a-z][a-z0-9_]*$` (2-32 chars)
- Reserved project names: `postgres`, `template0`, `template1`, `admin`, `root`, `system`
- Four environments supported: `prod`, `dev`, `staging`, `pr`
- Database naming: `{project}_{env}` or `{project}_pr_{number}`
- PR databases have TTL-based expiration (default 7 days)
- SQL injection prevention via `pgx.Identifier` sanitization

### Configuration

Config loaded from YAML with environment variable overrides:
- `POSTGRES_HOST`, `POSTGRES_PORT`, `POSTGRES_USER`, `POSTGRES_PASSWORD`, `POSTGRES_DATABASE` - PostgreSQL connection
- `POSTGRES_SSLMODE` - SSL mode for PostgreSQL (disable, require, verify-ca, verify-full). Defaults to `disable` for local development
- `PGMANAGER_API_PORT`, `PGMANAGER_API_TOKEN` - API server settings
- `PGMANAGER_REQUIRE_TOKEN` - Set to `true` to require API authentication (default: true)
- `PGMANAGER_ALLOWED_ORIGINS` - Comma-separated list of allowed CORS origins

Note: All metadata (projects, databases) is stored in the PostgreSQL server itself in a `pgmanager` schema. This means any machine with the same PostgreSQL connection can see and manage the same projects.

## Testing Patterns

Tests use table-driven pattern. When adding tests, follow the existing style in `*_test.go` files. Tests for validation logic are in `internal/project/project_test.go`, HTTP handlers in `internal/api/handlers_test.go`.
