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

### CI Usage

```yaml
# GitHub Actions example
- name: Install pgmanager
  run: curl -sSL https://raw.githubusercontent.com/subhanmahmood/pgmanager/master/install.sh | bash

- name: Create PR database
  run: pgmanager --config config.yaml db create myproject pr ${{ github.event.pull_request.number }}
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
# CLI commands (always specify config)
./pgmanager --config config.yaml project list
./pgmanager --config config.yaml db create myproject dev

# Start REST API server
./pgmanager --config config.yaml serve -p 8080

# Start terminal UI
./pgmanager --config config.yaml tui
```

### Running via Docker

```bash
# Run any command via Docker
docker run --rm -v "$(pwd)/config.yaml:/etc/pgmanager/config.yaml" \
  pgmanager:latest pgmanager --config /etc/pgmanager/config.yaml project list

# Start API server with port mapping
docker run --rm -p 8080:8080 -v "$(pwd)/config.yaml:/etc/pgmanager/config.yaml" \
  pgmanager:latest pgmanager --config /etc/pgmanager/config.yaml serve -p 8080
```

## Architecture

pgmanager is a PostgreSQL database management tool with project-based organization. It uses dual storage: PostgreSQL for actual databases/users and SQLite for metadata tracking.

### Layer Structure

- **cmd/pgmanager/main.go** - CLI entry point with all Cobra commands defined
- **internal/project/project.go** - Core business logic (Manager struct orchestrates all operations)
- **internal/db/postgres.go** - PostgreSQL operations (create/drop databases and users via pgx)
- **internal/meta/sqlite.go** - Metadata persistence (projects, database records, TTL tracking)
- **internal/api/** - REST API server using chi router with Bearer token auth
- **internal/tui/app.go** - Terminal UI using Bubble Tea
- **internal/config/config.go** - YAML config with environment variable overrides

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
- `POSTGRES_SSLMODE` - SSL mode for PostgreSQL (disable, require, verify-ca, verify-full). Defaults to `require`
- `PGMANAGER_API_PORT`, `PGMANAGER_API_TOKEN` - API server settings
- `PGMANAGER_REQUIRE_TOKEN` - Set to `true` to require API authentication (default: true)
- `PGMANAGER_ALLOWED_ORIGINS` - Comma-separated list of allowed CORS origins
- `PGMANAGER_SQLITE_PATH` - SQLite database location

## Testing Patterns

Tests use table-driven pattern. When adding tests, follow the existing style in `*_test.go` files. Tests for validation logic are in `internal/project/project_test.go`, HTTP handlers in `internal/api/handlers_test.go`.
