# pgmanager

A PostgreSQL database management tool with project-based organization. Supports CLI, REST API, and Terminal UI interfaces.

## Features

- **Project-based organization** - Group databases by project with environment separation
- **Multi-environment support** - `prod`, `dev`, `staging`, and ephemeral `pr` databases
- **Automatic naming** - Consistent `{project}_{env}` naming with auto-generated credentials
- **TTL management** - PR databases auto-expire with configurable TTL (default 7 days)
- **Multiple interfaces** - CLI, REST API, Terminal UI, and Web UI
- **Dual storage** - PostgreSQL for databases, SQLite for metadata tracking

## Installation

### From Source

```bash
go build -o pgmanager ./cmd/pgmanager
```

### Docker

```bash
docker build -t pgmanager:latest .
```

## Quick Start

1. Create a configuration file:

```yaml
# config.yaml
postgres:
  host: localhost
  port: 5432
  user: postgres
  password: your_password
  database: postgres

sqlite:
  path: ./pgmanager.db

api:
  port: 8080
  token: ""  # Optional Bearer token

cleanup:
  default_ttl: 168h  # 7 days
```

2. Create a project and database:

```bash
./pgmanager --config config.yaml project create myapp
./pgmanager --config config.yaml db create myapp dev
./pgmanager --config config.yaml db info myapp dev
```

## CLI Commands

### Projects

```bash
pgmanager project create <name>     # Create new project
pgmanager project list              # List all projects
pgmanager project delete <name>     # Delete project and all its databases
```

### Databases

```bash
pgmanager db create <project> <env> [pr-number]  # Create database
pgmanager db delete <project> <env> [pr-number]  # Delete database
pgmanager db list [project]                      # List databases
pgmanager db info <project> <env> [pr-number]    # Get connection info
```

### Server & UI

```bash
pgmanager serve [-p 8080]    # Start REST API server
pgmanager tui                # Launch Terminal UI
pgmanager cleanup            # Clean up expired PR databases
```

## REST API

Start the server with `pgmanager serve`. Authentication via Bearer token is optional (configure `api.token`).

### Endpoints

| Method | Endpoint | Description |
|--------|----------|-------------|
| GET | `/api/projects` | List all projects |
| POST | `/api/projects` | Create project |
| DELETE | `/api/projects/{name}` | Delete project |
| GET | `/api/projects/{name}/databases` | List project databases |
| POST | `/api/projects/{name}/databases` | Create database |
| GET | `/api/projects/{name}/databases/{env}` | Get database info |
| DELETE | `/api/projects/{name}/databases/{env}` | Delete database |
| POST | `/api/cleanup` | Clean up expired databases |
| GET | `/health` | Health check (no auth) |

### Example

```bash
# Create a project
curl -X POST http://localhost:8080/api/projects \
  -H "Content-Type: application/json" \
  -d '{"name": "myapp"}'

# Create a dev database
curl -X POST http://localhost:8080/api/projects/myapp/databases \
  -H "Content-Type: application/json" \
  -d '{"env": "dev"}'

# Get connection info
curl http://localhost:8080/api/projects/myapp/databases/dev
```

## Configuration

### Environment Variables

| Variable | Description |
|----------|-------------|
| `POSTGRES_HOST` | PostgreSQL host |
| `POSTGRES_PASSWORD` | PostgreSQL password |
| `PGMANAGER_API_TOKEN` | Bearer token for API auth |
| `PGMANAGER_SQLITE_PATH` | SQLite database location |

## Docker Usage

### Build

```bash
docker build -t pgmanager:latest .
```

### Run Commands via Docker

```bash
# List projects
docker run --rm -v "$(pwd)/config.yaml:/etc/pgmanager/config.yaml" \
  pgmanager:latest pgmanager --config /etc/pgmanager/config.yaml project list

# Create a database
docker run --rm -v "$(pwd)/config.yaml:/etc/pgmanager/config.yaml" \
  pgmanager:latest pgmanager --config /etc/pgmanager/config.yaml db create myproject dev

# Start API server
docker run --rm -p 8080:8080 -v "$(pwd)/config.yaml:/etc/pgmanager/config.yaml" \
  pgmanager:latest pgmanager --config /etc/pgmanager/config.yaml serve -p 8080
```

### Shell Alias

Add to `~/.zshrc` or `~/.bashrc` for convenience:

```bash
alias pgmanager='docker run --rm -it -p 8080:8080 -v "/path/to/config.yaml:/etc/pgmanager/config.yaml" -v "/path/to/data:/data" pgmanager:latest pgmanager --config /etc/pgmanager/config.yaml'
```

Then use from anywhere:

```bash
pgmanager project list
pgmanager db create myapp dev
pgmanager serve -p 8080
```

### Docker Deployment (daemon)

```bash
docker run -d \
  -p 8080:8080 \
  -v ./config.yaml:/etc/pgmanager/config.yaml \
  -v pgmanager-data:/data \
  pgmanager:latest pgmanager --config /etc/pgmanager/config.yaml serve
```

## Naming Conventions

- **Project names**: 2-32 characters, lowercase alphanumeric and underscores, must start with a letter
- **Reserved names**: `postgres`, `template0`, `template1`, `admin`, `root`, `system`
- **Database naming**: `{project}_{env}` or `{project}_pr_{number}`
- **User naming**: `{database_name}_user`

## Development

```bash
# Run tests
go test ./...

# Run tests with verbose output
go test -v ./internal/...

# Run a specific test
go test -run TestValidateName ./internal/project
```

## License

MIT
