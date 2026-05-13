package meta

import (
	"context"
	"time"
)

// Project represents a project in the metadata store.
type Project struct {
	ID        int64
	Name      string
	CreatedAt time.Time
}

// Database represents a database in the metadata store. Password is decrypted
// in-memory; on-disk it is stored as a ciphertext column.
type Database struct {
	ID        int64
	ProjectID int64
	Name      string
	UserName  string
	Password  string
	Env       string // prod, dev, staging, pr
	PRNumber  *int   // only set for PR databases
	CreatedAt time.Time
	ExpiresAt *time.Time
}

// Token is an API token. The plaintext is only ever shown to the operator at
// creation time; the store keeps the SHA-256 hash.
type Token struct {
	ID          int64
	Name        string
	TokenHash   []byte
	TokenPrefix string
	Scopes      []string
	CreatedAt   time.Time
	ExpiresAt   *time.Time
	LastUsedAt  *time.Time
	CreatedBy   string
	RevokedAt   *time.Time
}

// Active reports whether the token is currently valid (not revoked, not expired).
func (t *Token) Active(now time.Time) bool {
	if t.RevokedAt != nil {
		return false
	}
	if t.ExpiresAt != nil && !now.Before(*t.ExpiresAt) {
		return false
	}
	return true
}

// Store defines the interface for metadata storage.
type Store interface {
	Close() error

	// Project operations.
	CreateProject(ctx context.Context, name string) (*Project, error)
	GetProject(ctx context.Context, name string) (*Project, error)
	ListProjects(ctx context.Context) ([]Project, error)
	DeleteProject(ctx context.Context, name string) ([]Database, error)

	// Database operations.
	CreateDatabase(ctx context.Context, projectID int64, name, userName, password, env string, prNumber *int, expiresAt *time.Time) (*Database, error)
	GetDatabase(ctx context.Context, projectID int64, env string, prNumber *int) (*Database, error)
	GetDatabaseByName(ctx context.Context, name string) (*Database, error)
	ListDatabases(ctx context.Context, projectID int64) ([]Database, error)
	ListAllDatabases(ctx context.Context) ([]Database, error)
	DeleteDatabase(ctx context.Context, name string) error

	// Cleanup operations.
	GetExpiredDatabases(ctx context.Context) ([]Database, error)
	GetDatabasesOlderThan(ctx context.Context, env string, olderThan time.Duration) ([]Database, error)

	// Token operations.
	CreateToken(ctx context.Context, t *Token) error
	GetTokenByHash(ctx context.Context, hash []byte) (*Token, error)
	GetTokenByPrefix(ctx context.Context, prefix string) (*Token, error)
	ListTokens(ctx context.Context) ([]Token, error)
	RevokeToken(ctx context.Context, prefix string) error
	TouchToken(ctx context.Context, id int64, when time.Time) error
	HasActiveAdminToken(ctx context.Context) (bool, error)
}
