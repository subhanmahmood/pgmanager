package meta

import (
	"context"
	"time"
)

// Project represents a project in the metadata store
type Project struct {
	ID        int64
	Name      string
	CreatedAt time.Time
}

// Database represents a database in the metadata store
type Database struct {
	ID        int64
	ProjectID int64
	Name      string
	UserName  string
	Password  string
	Env       string     // prod, dev, staging, pr
	PRNumber  *int       // Only set for PR databases
	CreatedAt time.Time
	ExpiresAt *time.Time // TTL for PR databases
}

// Store defines the interface for metadata storage
type Store interface {
	Close() error

	// Project operations
	CreateProject(ctx context.Context, name string) (*Project, error)
	GetProject(ctx context.Context, name string) (*Project, error)
	ListProjects(ctx context.Context) ([]Project, error)
	DeleteProject(ctx context.Context, name string) ([]Database, error)

	// Database operations
	CreateDatabase(ctx context.Context, projectID int64, name, userName, password, env string, prNumber *int, expiresAt *time.Time) (*Database, error)
	GetDatabase(ctx context.Context, projectID int64, env string, prNumber *int) (*Database, error)
	GetDatabaseByName(ctx context.Context, name string) (*Database, error)
	ListDatabases(ctx context.Context, projectID int64) ([]Database, error)
	ListAllDatabases(ctx context.Context) ([]Database, error)
	DeleteDatabase(ctx context.Context, name string) error

	// Cleanup operations
	GetExpiredDatabases(ctx context.Context) ([]Database, error)
	GetDatabasesOlderThan(ctx context.Context, env string, olderThan time.Duration) ([]Database, error)
}
