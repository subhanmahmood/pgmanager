package meta

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"pgmanager/internal/crypto"
)

// ErrEncryptionKeyRequired is returned when an operation needs the at-rest
// encryption key but none is configured.
var ErrEncryptionKeyRequired = errors.New("encryption key required: set PGMANAGER_ENCRYPTION_KEY (see `pgmanager keygen`)")

// PostgresStore handles PostgreSQL metadata operations.
type PostgresStore struct {
	pool *pgxpool.Pool
	key  []byte // optional; required for any operation that touches passwords
}

// NewPostgresStore creates a new PostgreSQL metadata store. key may be nil
// for read-only or token-only operations, but any database create/get/list
// that returns a password will fail with ErrEncryptionKeyRequired.
func NewPostgresStore(ctx context.Context, connString string, key []byte) (*PostgresStore, error) {
	pool, err := pgxpool.New(ctx, connString)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to database: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	s := &PostgresStore{pool: pool, key: key}
	if err := s.migrate(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("failed to migrate: %w", err)
	}
	return s, nil
}

// migrate brings the schema up to the current version. Idempotent.
func (s *PostgresStore) migrate(ctx context.Context) error {
	base := `
	CREATE SCHEMA IF NOT EXISTS pgmanager;

	CREATE TABLE IF NOT EXISTS pgmanager.projects (
		id SERIAL PRIMARY KEY,
		name TEXT UNIQUE NOT NULL,
		created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS pgmanager.databases (
		id SERIAL PRIMARY KEY,
		project_id INTEGER NOT NULL REFERENCES pgmanager.projects(id) ON DELETE CASCADE,
		name TEXT UNIQUE NOT NULL,
		user_name TEXT NOT NULL,
		password_ct BYTEA,
		env TEXT NOT NULL,
		pr_number INTEGER,
		created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
		expires_at TIMESTAMPTZ
	);

	-- Upgrade path: older schemas had a plaintext "password" column that we
	-- migrate into password_ct in migrateLegacyPasswords below.
	ALTER TABLE pgmanager.databases ADD COLUMN IF NOT EXISTS password_ct BYTEA;

	CREATE INDEX IF NOT EXISTS idx_databases_project_id ON pgmanager.databases(project_id);
	CREATE INDEX IF NOT EXISTS idx_databases_env ON pgmanager.databases(env);
	CREATE INDEX IF NOT EXISTS idx_databases_expires_at ON pgmanager.databases(expires_at);

	CREATE TABLE IF NOT EXISTS pgmanager.tokens (
		id SERIAL PRIMARY KEY,
		name TEXT NOT NULL,
		token_hash BYTEA UNIQUE NOT NULL,
		token_prefix TEXT NOT NULL,
		scopes TEXT[] NOT NULL,
		created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
		expires_at TIMESTAMPTZ,
		last_used_at TIMESTAMPTZ,
		created_by TEXT,
		revoked_at TIMESTAMPTZ
	);

	CREATE INDEX IF NOT EXISTS idx_tokens_prefix ON pgmanager.tokens(token_prefix);
	`
	if _, err := s.pool.Exec(ctx, base); err != nil {
		return err
	}

	return s.migrateLegacyPasswords(ctx)
}

// migrateLegacyPasswords encrypts any rows that still have plaintext passwords
// in the `password` column, then drops that column. Requires a key.
func (s *PostgresStore) migrateLegacyPasswords(ctx context.Context) error {
	var hasCol bool
	err := s.pool.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM information_schema.columns
			WHERE table_schema = 'pgmanager'
			  AND table_name = 'databases'
			  AND column_name = 'password'
		)`).Scan(&hasCol)
	if err != nil {
		return fmt.Errorf("check legacy column: %w", err)
	}
	if !hasCol {
		return nil
	}

	rows, err := s.pool.Query(ctx, `SELECT id, password FROM pgmanager.databases WHERE password IS NOT NULL AND password_ct IS NULL`)
	if err != nil {
		return fmt.Errorf("scan legacy passwords: %w", err)
	}
	type legacy struct {
		id int64
		pw string
	}
	var pending []legacy
	for rows.Next() {
		var l legacy
		if err := rows.Scan(&l.id, &l.pw); err != nil {
			rows.Close()
			return fmt.Errorf("scan legacy row: %w", err)
		}
		pending = append(pending, l)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	if len(pending) > 0 {
		if s.key == nil {
			return ErrEncryptionKeyRequired
		}
		for _, l := range pending {
			ct, err := crypto.Encrypt(s.key, []byte(l.pw))
			if err != nil {
				return fmt.Errorf("encrypt legacy row %d: %w", l.id, err)
			}
			if _, err := s.pool.Exec(ctx, `UPDATE pgmanager.databases SET password_ct = $1 WHERE id = $2`, ct, l.id); err != nil {
				return fmt.Errorf("update legacy row %d: %w", l.id, err)
			}
		}
	}

	// Drop the plaintext column once we know nothing references it anymore.
	if _, err := s.pool.Exec(ctx, `ALTER TABLE pgmanager.databases DROP COLUMN IF EXISTS password`); err != nil {
		return fmt.Errorf("drop legacy column: %w", err)
	}
	return nil
}

// Close closes the database connection pool.
func (s *PostgresStore) Close() error {
	s.pool.Close()
	return nil
}

func (s *PostgresStore) encryptPassword(pw string) ([]byte, error) {
	if s.key == nil {
		return nil, ErrEncryptionKeyRequired
	}
	return crypto.Encrypt(s.key, []byte(pw))
}

func (s *PostgresStore) decryptPassword(ct []byte) (string, error) {
	if ct == nil {
		return "", nil
	}
	if s.key == nil {
		return "", ErrEncryptionKeyRequired
	}
	pt, err := crypto.Decrypt(s.key, ct)
	if err != nil {
		return "", err
	}
	return string(pt), nil
}

// --- Project operations -----------------------------------------------------

func (s *PostgresStore) CreateProject(ctx context.Context, name string) (*Project, error) {
	var id int64
	var createdAt time.Time
	err := s.pool.QueryRow(ctx,
		"INSERT INTO pgmanager.projects (name) VALUES ($1) RETURNING id, created_at",
		name,
	).Scan(&id, &createdAt)
	if err != nil {
		return nil, fmt.Errorf("failed to create project: %w", err)
	}
	return &Project{ID: id, Name: name, CreatedAt: createdAt}, nil
}

func (s *PostgresStore) GetProject(ctx context.Context, name string) (*Project, error) {
	var p Project
	err := s.pool.QueryRow(ctx,
		"SELECT id, name, created_at FROM pgmanager.projects WHERE name = $1", name,
	).Scan(&p.ID, &p.Name, &p.CreatedAt)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get project: %w", err)
	}
	return &p, nil
}

func (s *PostgresStore) ListProjects(ctx context.Context) ([]Project, error) {
	rows, err := s.pool.Query(ctx, "SELECT id, name, created_at FROM pgmanager.projects ORDER BY name")
	if err != nil {
		return nil, fmt.Errorf("failed to list projects: %w", err)
	}
	defer rows.Close()

	var projects []Project
	for rows.Next() {
		var p Project
		if err := rows.Scan(&p.ID, &p.Name, &p.CreatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan project: %w", err)
		}
		projects = append(projects, p)
	}
	return projects, rows.Err()
}

func (s *PostgresStore) DeleteProject(ctx context.Context, name string) ([]Database, error) {
	project, err := s.GetProject(ctx, name)
	if err != nil {
		return nil, err
	}
	if project == nil {
		return nil, fmt.Errorf("project not found: %s", name)
	}

	databases, err := s.ListDatabases(ctx, project.ID)
	if err != nil {
		return nil, err
	}

	if _, err := s.pool.Exec(ctx, "DELETE FROM pgmanager.projects WHERE id = $1", project.ID); err != nil {
		return nil, fmt.Errorf("failed to delete project: %w", err)
	}
	return databases, nil
}

// --- Database operations ----------------------------------------------------

func (s *PostgresStore) CreateDatabase(ctx context.Context, projectID int64, name, userName, password, env string, prNumber *int, expiresAt *time.Time) (*Database, error) {
	ct, err := s.encryptPassword(password)
	if err != nil {
		return nil, err
	}
	var id int64
	var createdAt time.Time
	err = s.pool.QueryRow(ctx,
		`INSERT INTO pgmanager.databases (project_id, name, user_name, password_ct, env, pr_number, expires_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)
		 RETURNING id, created_at`,
		projectID, name, userName, ct, env, prNumber, expiresAt,
	).Scan(&id, &createdAt)
	if err != nil {
		return nil, fmt.Errorf("failed to create database: %w", err)
	}

	return &Database{
		ID:        id,
		ProjectID: projectID,
		Name:      name,
		UserName:  userName,
		Password:  password,
		Env:       env,
		PRNumber:  prNumber,
		CreatedAt: createdAt,
		ExpiresAt: expiresAt,
	}, nil
}

const dbSelect = `SELECT id, project_id, name, user_name, password_ct, env, pr_number, created_at, expires_at FROM pgmanager.databases`

func (s *PostgresStore) GetDatabase(ctx context.Context, projectID int64, env string, prNumber *int) (*Database, error) {
	query := dbSelect + ` WHERE project_id = $1 AND env = $2`
	args := []interface{}{projectID, env}
	if prNumber != nil {
		query += " AND pr_number = $3"
		args = append(args, *prNumber)
	} else {
		query += " AND pr_number IS NULL"
	}

	d, err := s.scanOne(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	return d, nil
}

func (s *PostgresStore) GetDatabaseByName(ctx context.Context, name string) (*Database, error) {
	return s.scanOne(ctx, dbSelect+" WHERE name = $1", name)
}

func (s *PostgresStore) ListDatabases(ctx context.Context, projectID int64) ([]Database, error) {
	return s.scanMany(ctx, dbSelect+" WHERE project_id = $1 ORDER BY name", projectID)
}

func (s *PostgresStore) ListAllDatabases(ctx context.Context) ([]Database, error) {
	return s.scanMany(ctx, dbSelect+" ORDER BY name")
}

func (s *PostgresStore) DeleteDatabase(ctx context.Context, name string) error {
	result, err := s.pool.Exec(ctx, "DELETE FROM pgmanager.databases WHERE name = $1", name)
	if err != nil {
		return fmt.Errorf("failed to delete database: %w", err)
	}
	if result.RowsAffected() == 0 {
		return fmt.Errorf("database not found: %s", name)
	}
	return nil
}

func (s *PostgresStore) GetExpiredDatabases(ctx context.Context) ([]Database, error) {
	return s.scanMany(ctx, dbSelect+` WHERE expires_at IS NOT NULL AND expires_at < NOW() ORDER BY expires_at`)
}

func (s *PostgresStore) GetDatabasesOlderThan(ctx context.Context, env string, olderThan time.Duration) ([]Database, error) {
	cutoff := time.Now().Add(-olderThan)
	return s.scanMany(ctx, dbSelect+` WHERE env = $1 AND created_at < $2 ORDER BY created_at`, env, cutoff)
}

func (s *PostgresStore) scanOne(ctx context.Context, query string, args ...interface{}) (*Database, error) {
	var d Database
	var ct []byte
	var expiresAt *time.Time
	var prNum *int
	err := s.pool.QueryRow(ctx, query, args...).Scan(
		&d.ID, &d.ProjectID, &d.Name, &d.UserName, &ct, &d.Env, &prNum, &d.CreatedAt, &expiresAt,
	)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("query: %w", err)
	}
	pw, err := s.decryptPassword(ct)
	if err != nil {
		return nil, fmt.Errorf("decrypt password for %s: %w", d.Name, err)
	}
	d.Password = pw
	d.PRNumber = prNum
	d.ExpiresAt = expiresAt
	return &d, nil
}

func (s *PostgresStore) scanMany(ctx context.Context, query string, args ...interface{}) ([]Database, error) {
	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query: %w", err)
	}
	defer rows.Close()

	var out []Database
	for rows.Next() {
		var d Database
		var ct []byte
		var expiresAt *time.Time
		var prNum *int
		if err := rows.Scan(&d.ID, &d.ProjectID, &d.Name, &d.UserName, &ct, &d.Env, &prNum, &d.CreatedAt, &expiresAt); err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		pw, err := s.decryptPassword(ct)
		if err != nil {
			return nil, fmt.Errorf("decrypt password for %s: %w", d.Name, err)
		}
		d.Password = pw
		d.PRNumber = prNum
		d.ExpiresAt = expiresAt
		out = append(out, d)
	}
	return out, rows.Err()
}

// --- Token operations -------------------------------------------------------

const tokenSelect = `SELECT id, name, token_hash, token_prefix, scopes, created_at, expires_at, last_used_at, created_by, revoked_at FROM pgmanager.tokens`

func (s *PostgresStore) CreateToken(ctx context.Context, t *Token) error {
	var id int64
	var createdAt time.Time
	err := s.pool.QueryRow(ctx,
		`INSERT INTO pgmanager.tokens (name, token_hash, token_prefix, scopes, expires_at, created_by)
		 VALUES ($1, $2, $3, $4, $5, $6)
		 RETURNING id, created_at`,
		t.Name, t.TokenHash, t.TokenPrefix, t.Scopes, t.ExpiresAt, t.CreatedBy,
	).Scan(&id, &createdAt)
	if err != nil {
		return fmt.Errorf("create token: %w", err)
	}
	t.ID = id
	t.CreatedAt = createdAt
	return nil
}

func (s *PostgresStore) GetTokenByHash(ctx context.Context, hash []byte) (*Token, error) {
	t, err := s.scanOneToken(ctx, tokenSelect+" WHERE token_hash = $1", hash)
	if err != nil {
		return nil, err
	}
	return t, nil
}

func (s *PostgresStore) GetTokenByPrefix(ctx context.Context, prefix string) (*Token, error) {
	return s.scanOneToken(ctx, tokenSelect+" WHERE token_prefix = $1 AND revoked_at IS NULL ORDER BY id DESC LIMIT 1", prefix)
}

func (s *PostgresStore) ListTokens(ctx context.Context) ([]Token, error) {
	rows, err := s.pool.Query(ctx, tokenSelect+" ORDER BY id DESC")
	if err != nil {
		return nil, fmt.Errorf("list tokens: %w", err)
	}
	defer rows.Close()
	return scanTokens(rows)
}

func (s *PostgresStore) RevokeToken(ctx context.Context, prefix string) error {
	result, err := s.pool.Exec(ctx,
		`UPDATE pgmanager.tokens SET revoked_at = NOW() WHERE token_prefix = $1 AND revoked_at IS NULL`,
		prefix,
	)
	if err != nil {
		return fmt.Errorf("revoke token: %w", err)
	}
	if result.RowsAffected() == 0 {
		return fmt.Errorf("token not found or already revoked: %s", prefix)
	}
	return nil
}

func (s *PostgresStore) TouchToken(ctx context.Context, id int64, when time.Time) error {
	_, err := s.pool.Exec(ctx, `UPDATE pgmanager.tokens SET last_used_at = $1 WHERE id = $2`, when, id)
	return err
}

func (s *PostgresStore) HasActiveAdminToken(ctx context.Context) (bool, error) {
	var count int
	err := s.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM pgmanager.tokens
		 WHERE revoked_at IS NULL
		   AND (expires_at IS NULL OR expires_at > NOW())
		   AND 'admin' = ANY(scopes)`,
	).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("check admin token: %w", err)
	}
	return count > 0, nil
}

func (s *PostgresStore) scanOneToken(ctx context.Context, query string, args ...interface{}) (*Token, error) {
	var t Token
	err := s.pool.QueryRow(ctx, query, args...).Scan(
		&t.ID, &t.Name, &t.TokenHash, &t.TokenPrefix, &t.Scopes,
		&t.CreatedAt, &t.ExpiresAt, &t.LastUsedAt, &t.CreatedBy, &t.RevokedAt,
	)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("scan token: %w", err)
	}
	return &t, nil
}

func scanTokens(rows pgx.Rows) ([]Token, error) {
	var out []Token
	for rows.Next() {
		var t Token
		if err := rows.Scan(&t.ID, &t.Name, &t.TokenHash, &t.TokenPrefix, &t.Scopes,
			&t.CreatedAt, &t.ExpiresAt, &t.LastUsedAt, &t.CreatedBy, &t.RevokedAt); err != nil {
			return nil, fmt.Errorf("scan token: %w", err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
