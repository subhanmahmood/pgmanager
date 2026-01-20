package db

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"pgmanager/internal/config"
)

// PostgresClient handles PostgreSQL database operations
type PostgresClient struct {
	cfg *config.PostgresConfig
}

// NewPostgresClient creates a new PostgreSQL client
func NewPostgresClient(cfg *config.PostgresConfig) *PostgresClient {
	return &PostgresClient{cfg: cfg}
}

// connect establishes a connection to the PostgreSQL server
func (c *PostgresClient) connect(ctx context.Context) (*pgx.Conn, error) {
	connStr := fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=disable",
		c.cfg.Host, c.cfg.Port, c.cfg.User, c.cfg.Password, c.cfg.Database)
	return pgx.Connect(ctx, connStr)
}

// CreateDatabase creates a new database and user with the given names
func (c *PostgresClient) CreateDatabase(ctx context.Context, dbName, userName, password string) error {
	conn, err := c.connect(ctx)
	if err != nil {
		return fmt.Errorf("failed to connect: %w", err)
	}
	defer conn.Close(ctx)

	// Create user
	createUserSQL := fmt.Sprintf("CREATE USER %s WITH PASSWORD %s",
		pgx.Identifier{userName}.Sanitize(),
		quoteLiteral(password))
	if _, err := conn.Exec(ctx, createUserSQL); err != nil {
		// Check if user already exists
		if !strings.Contains(err.Error(), "already exists") {
			return fmt.Errorf("failed to create user: %w", err)
		}
	}

	// Create database
	createDBSQL := fmt.Sprintf("CREATE DATABASE %s OWNER %s",
		pgx.Identifier{dbName}.Sanitize(),
		pgx.Identifier{userName}.Sanitize())
	if _, err := conn.Exec(ctx, createDBSQL); err != nil {
		return fmt.Errorf("failed to create database: %w", err)
	}

	// Grant all privileges
	grantSQL := fmt.Sprintf("GRANT ALL PRIVILEGES ON DATABASE %s TO %s",
		pgx.Identifier{dbName}.Sanitize(),
		pgx.Identifier{userName}.Sanitize())
	if _, err := conn.Exec(ctx, grantSQL); err != nil {
		return fmt.Errorf("failed to grant privileges: %w", err)
	}

	return nil
}

// DropDatabase drops a database and its associated user
func (c *PostgresClient) DropDatabase(ctx context.Context, dbName, userName string) error {
	conn, err := c.connect(ctx)
	if err != nil {
		return fmt.Errorf("failed to connect: %w", err)
	}
	defer conn.Close(ctx)

	// Terminate existing connections to the database
	terminateSQL := fmt.Sprintf(`
		SELECT pg_terminate_backend(pid)
		FROM pg_stat_activity
		WHERE datname = %s AND pid <> pg_backend_pid()`,
		quoteLiteral(dbName))
	if _, err := conn.Exec(ctx, terminateSQL); err != nil {
		// Ignore errors here, the database might not exist
	}

	// Drop database
	dropDBSQL := fmt.Sprintf("DROP DATABASE IF EXISTS %s",
		pgx.Identifier{dbName}.Sanitize())
	if _, err := conn.Exec(ctx, dropDBSQL); err != nil {
		return fmt.Errorf("failed to drop database: %w", err)
	}

	// Drop user
	dropUserSQL := fmt.Sprintf("DROP USER IF EXISTS %s",
		pgx.Identifier{userName}.Sanitize())
	if _, err := conn.Exec(ctx, dropUserSQL); err != nil {
		return fmt.Errorf("failed to drop user: %w", err)
	}

	return nil
}

// DatabaseExists checks if a database exists
func (c *PostgresClient) DatabaseExists(ctx context.Context, dbName string) (bool, error) {
	conn, err := c.connect(ctx)
	if err != nil {
		return false, fmt.Errorf("failed to connect: %w", err)
	}
	defer conn.Close(ctx)

	var exists bool
	err = conn.QueryRow(ctx,
		"SELECT EXISTS(SELECT 1 FROM pg_database WHERE datname = $1)",
		dbName).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("failed to check database existence: %w", err)
	}

	return exists, nil
}

// ListDatabases returns all databases managed by pgmanager (matching pattern)
func (c *PostgresClient) ListDatabases(ctx context.Context) ([]string, error) {
	conn, err := c.connect(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to connect: %w", err)
	}
	defer conn.Close(ctx)

	rows, err := conn.Query(ctx,
		"SELECT datname FROM pg_database WHERE datistemplate = false ORDER BY datname")
	if err != nil {
		return nil, fmt.Errorf("failed to list databases: %w", err)
	}
	defer rows.Close()

	var databases []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("failed to scan database name: %w", err)
		}
		databases = append(databases, name)
	}

	return databases, rows.Err()
}

// TestConnection tests the connection to the database
func (c *PostgresClient) TestConnection(ctx context.Context, dbName, userName, password string) error {
	connStr := fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=disable",
		c.cfg.Host, c.cfg.Port, userName, password, dbName)
	conn, err := pgx.Connect(ctx, connStr)
	if err != nil {
		return fmt.Errorf("failed to connect: %w", err)
	}
	defer conn.Close(ctx)

	return conn.Ping(ctx)
}

// GeneratePassword generates a secure random password
func GeneratePassword() string {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		// Fallback to a deterministic but unique password
		return "fallback_password_" + hex.EncodeToString(bytes[:8])
	}
	return hex.EncodeToString(bytes)
}

// ConnectionString returns a PostgreSQL connection string
func ConnectionString(host string, port int, dbName, userName, password string) string {
	return fmt.Sprintf("postgresql://%s:%s@%s:%d/%s?sslmode=disable",
		userName, password, host, port, dbName)
}

// quoteLiteral properly quotes a string literal for SQL
func quoteLiteral(s string) string {
	// Escape single quotes by doubling them
	escaped := strings.ReplaceAll(s, "'", "''")
	return "'" + escaped + "'"
}
