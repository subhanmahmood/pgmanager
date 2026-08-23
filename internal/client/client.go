// Package client exposes the operations the CLI and TUI need. Everything
// goes through `pgmanager serve` over HTTP — either a remote one over HTTPS
// or, on the server itself, a local unix socket. Clients never hold Postgres
// credentials, so every request is scoped and audited.
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

// Backup is one stored dump of a database. Mirrors the API's
// BackupResponse (internal/api/backup_handlers.go). Never available for pr
// databases.
type Backup struct {
	ID           int64   `json:"id"`
	DatabaseName string  `json:"database_name"`
	ObjectKey    string  `json:"object_key"`
	SizeBytes    int64   `json:"size_bytes"`
	Status       string  `json:"status"` // "running" | "succeeded" | "failed"
	Error        string  `json:"error,omitempty"`
	StartedAt    string  `json:"started_at"`            // RFC3339
	FinishedAt   *string `json:"finished_at,omitempty"` // RFC3339, nil while running
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
	// RotatePassword issues a new password for the database's role and
	// returns the updated credentials. terminate additionally kills open
	// connections, so holders of the old password must reconnect.
	RotatePassword(ctx context.Context, project, env string, prNumber *int, terminate bool) (*Database, error)
	DeleteDatabase(ctx context.Context, project, env string, prNumber *int) error

	// Backups. env identifies the source database (prod/dev/staging, or pr —
	// which the server always rejects, backups don't exist for PR databases).
	// SetBackupsEnabled toggles the scheduled/automatic backup flag; it does
	// not itself run a backup. CreateBackup runs one immediately.
	SetBackupsEnabled(ctx context.Context, project, env string, prNumber *int, enabled bool) error
	ListBackups(ctx context.Context, project, env string, prNumber *int) ([]Backup, error)
	CreateBackup(ctx context.Context, project, env string, prNumber *int) (*Backup, error)
	DeleteBackup(ctx context.Context, project, env string, prNumber *int, backupID int64) error
	// RestoreBackup creates a brand-new database from a snapshot and returns
	// its credentials, exactly like CreateDatabase. The source database is
	// never opened. The returned Database's Env is the addressable segment
	// for the new database ("{source-env}_restore_{timestamp}"), not the env
	// passed in here — pass that whole string as env to reach it afterwards.
	RestoreBackup(ctx context.Context, project, env string, prNumber *int, backupID int64) (*Database, error)

	// Cleanup.
	Cleanup(ctx context.Context, olderThan time.Duration) ([]string, error)

	// Auth / tokens.
	Whoami(ctx context.Context) (*Whoami, error)
	ListTokens(ctx context.Context) ([]Token, error)
	CreateToken(ctx context.Context, name string, scopes []string, expires string) (plaintext string, info *Token, err error)
	RevokeToken(ctx context.Context, prefix string) error

	Close() error
}
