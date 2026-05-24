// Package client exposes the operations the CLI and TUI need, abstracting
// over whether they talk to a remote `pgmanager serve` (HTTPClient) or hit
// PostgreSQL directly (LocalClient).
package client

import (
	"context"
	"time"
)

// Project is the public view of a project.
type Project struct {
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
}

// Database holds the connection info a caller can use. Password is only set
// on Create and GetCredentials responses; List/Get strip it.
type Database struct {
	Project      string     `json:"project"`
	Env          string     `json:"env"`
	PRNumber     *int       `json:"pr_number,omitempty"`
	DatabaseName string     `json:"database_name"`
	UserName     string     `json:"user_name"`
	Password     string     `json:"password,omitempty"`
	Host         string     `json:"host"`
	Port         int        `json:"port"`
	ConnString   string     `json:"connection_string,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
	ExpiresAt    *time.Time `json:"expires_at,omitempty"`
}

// Whoami describes the authenticated principal.
type Whoami struct {
	TokenPrefix string   `json:"token_prefix"`
	Scopes      []string `json:"scopes"`
}

// Token is the public view of an API token (no secret material).
type Token struct {
	Name        string     `json:"name"`
	TokenPrefix string     `json:"token_prefix"`
	Scopes      []string   `json:"scopes"`
	CreatedAt   time.Time  `json:"created_at"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
	LastUsedAt  *time.Time `json:"last_used_at,omitempty"`
	CreatedBy   string     `json:"created_by,omitempty"`
	RevokedAt   *time.Time `json:"revoked_at,omitempty"`
}

// Client is the abstraction the CLI calls regardless of transport.
type Client interface {
	// Projects.
	CreateProject(ctx context.Context, name string) error
	ListProjects(ctx context.Context) ([]Project, error)
	DeleteProject(ctx context.Context, name string) error

	// Databases. extensions, if non-empty, lists Postgres extensions to
	// install in the new database (e.g., []string{"vector"}).
	CreateDatabase(ctx context.Context, project, env string, prNumber *int, extensions []string) (*Database, error)
	GetDatabase(ctx context.Context, project, env string, prNumber *int) (*Database, error)
	GetDatabaseCredentials(ctx context.Context, project, env string, prNumber *int) (*Database, error)
	ListDatabases(ctx context.Context, project string) ([]Database, error)
	DeleteDatabase(ctx context.Context, project, env string, prNumber *int) error

	// Cleanup.
	Cleanup(ctx context.Context, olderThan time.Duration) ([]string, error)

	// Auth / tokens.
	Whoami(ctx context.Context) (*Whoami, error)
	ListTokens(ctx context.Context) ([]Token, error)
	CreateToken(ctx context.Context, name string, scopes []string, expires string) (plaintext string, info *Token, err error)
	RevokeToken(ctx context.Context, prefix string) error

	Close() error
}
