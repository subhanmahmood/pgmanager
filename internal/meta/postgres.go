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

	CREATE TABLE IF NOT EXISTS pgmanager.device_requests (
		id SERIAL PRIMARY KEY,
		device_code_hash BYTEA UNIQUE NOT NULL,
		user_code TEXT UNIQUE NOT NULL,
		client_name TEXT,
		client_ip TEXT,
		requested_scopes TEXT[],
		status TEXT NOT NULL DEFAULT 'pending',
		token_id INTEGER REFERENCES pgmanager.tokens(id) ON DELETE SET NULL,
		issued_token_ct BYTEA,
		created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
		expires_at TIMESTAMPTZ NOT NULL,
		approved_by TEXT,
		approved_at TIMESTAMPTZ,
		last_polled_at TIMESTAMPTZ
	);

	CREATE INDEX IF NOT EXISTS idx_device_requests_user_code ON pgmanager.device_requests(user_code);
	CREATE INDEX IF NOT EXISTS idx_device_requests_expires_at ON pgmanager.device_requests(expires_at);

	CREATE TABLE IF NOT EXISTS pgmanager.users (
		id SERIAL PRIMARY KEY,
		email TEXT UNIQUE NOT NULL,
		password_hash TEXT NOT NULL,
		password_changed_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
		created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
		created_by TEXT,
		last_login_at TIMESTAMPTZ,
		disabled_at TIMESTAMPTZ
	);

	CREATE TABLE IF NOT EXISTS pgmanager.sessions (
		id SERIAL PRIMARY KEY,
		token_hash BYTEA UNIQUE NOT NULL,
		user_id INTEGER NOT NULL REFERENCES pgmanager.users(id) ON DELETE CASCADE,
		created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
		expires_at TIMESTAMPTZ NOT NULL,
		last_seen_at TIMESTAMPTZ,
		created_ip TEXT
	);

	ALTER TABLE pgmanager.users ADD COLUMN IF NOT EXISTS password_changed_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP;

	CREATE INDEX IF NOT EXISTS idx_sessions_user ON pgmanager.sessions(user_id);
	CREATE INDEX IF NOT EXISTS idx_sessions_expires_at ON pgmanager.sessions(expires_at);
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

func (s *PostgresStore) SetDatabasePassword(ctx context.Context, name, password string) error {
	ct, err := s.encryptPassword(password)
	if err != nil {
		return err
	}
	result, err := s.pool.Exec(ctx,
		"UPDATE pgmanager.databases SET password_ct = $1 WHERE name = $2", ct, name)
	if err != nil {
		return fmt.Errorf("failed to update database password: %w", err)
	}
	if result.RowsAffected() == 0 {
		return fmt.Errorf("database not found: %s", name)
	}
	return nil
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

// --- Device authorization operations -----------------------------------------

func derefString(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

const deviceSelect = `SELECT id, device_code_hash, user_code, client_name, client_ip, requested_scopes,
	status, token_id, issued_token_ct, created_at, expires_at, approved_by, approved_at, last_polled_at
	FROM pgmanager.device_requests`

func (s *PostgresStore) CreateDeviceRequest(ctx context.Context, d *DeviceRequest) error {
	var id int64
	var createdAt time.Time
	err := s.pool.QueryRow(ctx,
		`INSERT INTO pgmanager.device_requests
		   (device_code_hash, user_code, client_name, client_ip, requested_scopes, status, expires_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)
		 RETURNING id, created_at`,
		d.DeviceCodeHash, d.UserCode, d.ClientName, d.ClientIP, d.RequestedScopes,
		DeviceStatusPending, d.ExpiresAt,
	).Scan(&id, &createdAt)
	if err != nil {
		return fmt.Errorf("create device request: %w", err)
	}
	d.ID = id
	d.CreatedAt = createdAt
	d.Status = DeviceStatusPending
	return nil
}

func (s *PostgresStore) GetDeviceRequestByCodeHash(ctx context.Context, hash []byte) (*DeviceRequest, error) {
	return s.scanOneDeviceRequest(ctx, deviceSelect+" WHERE device_code_hash = $1", hash)
}

func (s *PostgresStore) GetDeviceRequestByUserCode(ctx context.Context, userCode string) (*DeviceRequest, error) {
	return s.scanOneDeviceRequest(ctx, deviceSelect+" WHERE user_code = $1", userCode)
}

func (s *PostgresStore) ListPendingDeviceRequests(ctx context.Context) ([]DeviceRequest, error) {
	rows, err := s.pool.Query(ctx,
		deviceSelect+" WHERE status = $1 AND expires_at > NOW() ORDER BY id DESC",
		DeviceStatusPending)
	if err != nil {
		return nil, fmt.Errorf("list device requests: %w", err)
	}
	defer rows.Close()

	var out []DeviceRequest
	for rows.Next() {
		d, err := s.scanDeviceRow(rows)
		if err != nil {
			return nil, err
		}
		// Pending requests never carry a token; don't decrypt anything here.
		d.IssuedToken = ""
		out = append(out, *d)
	}
	return out, rows.Err()
}

func (s *PostgresStore) ApproveDeviceRequest(ctx context.Context, id, tokenID int64, plaintext, approvedBy string) error {
	if s.key == nil {
		return ErrEncryptionKeyRequired
	}
	ct, err := crypto.Encrypt(s.key, []byte(plaintext))
	if err != nil {
		return fmt.Errorf("encrypt issued token: %w", err)
	}
	// The status guard makes approval single-shot: a second approver racing
	// the first affects no rows rather than overwriting the issued token.
	result, err := s.pool.Exec(ctx,
		`UPDATE pgmanager.device_requests
		 SET status = $1, token_id = $2, issued_token_ct = $3, approved_by = $4, approved_at = NOW()
		 WHERE id = $5 AND status = $6`,
		DeviceStatusApproved, tokenID, ct, approvedBy, id, DeviceStatusPending,
	)
	if err != nil {
		return fmt.Errorf("approve device request: %w", err)
	}
	if result.RowsAffected() == 0 {
		return fmt.Errorf("device request %d is no longer pending", id)
	}
	return nil
}

func (s *PostgresStore) DenyDeviceRequest(ctx context.Context, id int64, deniedBy string) error {
	result, err := s.pool.Exec(ctx,
		`UPDATE pgmanager.device_requests
		 SET status = $1, approved_by = $2, approved_at = NOW()
		 WHERE id = $3 AND status = $4`,
		DeviceStatusDenied, deniedBy, id, DeviceStatusPending,
	)
	if err != nil {
		return fmt.Errorf("deny device request: %w", err)
	}
	if result.RowsAffected() == 0 {
		return fmt.Errorf("device request %d is no longer pending", id)
	}
	return nil
}

func (s *PostgresStore) ConsumeDeviceToken(ctx context.Context, id int64) (string, error) {
	if s.key == nil {
		return "", ErrEncryptionKeyRequired
	}
	// Read-and-clear in one statement so two concurrent polls can never both
	// walk away with the token. A plain UPDATE ... RETURNING would hand back
	// the post-update value (NULL), hence the CTE holding the old ciphertext.
	var ct []byte
	err := s.pool.QueryRow(ctx,
		`WITH claimed AS (
		     SELECT id, issued_token_ct FROM pgmanager.device_requests
		     WHERE id = $1 AND issued_token_ct IS NOT NULL
		     FOR UPDATE
		 ), cleared AS (
		     UPDATE pgmanager.device_requests d SET issued_token_ct = NULL
		     FROM claimed WHERE d.id = claimed.id
		 )
		 SELECT issued_token_ct FROM claimed`, id).Scan(&ct)
	if err == pgx.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("consume device token: %w", err)
	}
	plain, err := crypto.Decrypt(s.key, ct)
	if err != nil {
		return "", fmt.Errorf("decrypt issued token: %w", err)
	}
	return string(plain), nil
}

func (s *PostgresStore) TouchDeviceRequest(ctx context.Context, id int64, when time.Time) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE pgmanager.device_requests SET last_polled_at = $1 WHERE id = $2`, when, id)
	return err
}

func (s *PostgresStore) DeleteExpiredDeviceRequests(ctx context.Context) (int, error) {
	result, err := s.pool.Exec(ctx, `DELETE FROM pgmanager.device_requests WHERE expires_at < NOW()`)
	if err != nil {
		return 0, fmt.Errorf("delete expired device requests: %w", err)
	}
	return int(result.RowsAffected()), nil
}

func (s *PostgresStore) scanOneDeviceRequest(ctx context.Context, query string, args ...interface{}) (*DeviceRequest, error) {
	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query device request: %w", err)
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, rows.Err()
	}
	d, err := s.scanDeviceRow(rows)
	if err != nil {
		return nil, err
	}
	return d, rows.Err()
}

func (s *PostgresStore) scanDeviceRow(rows pgx.Rows) (*DeviceRequest, error) {
	var d DeviceRequest
	var ct []byte
	var clientName, clientIP, approvedBy *string
	if err := rows.Scan(&d.ID, &d.DeviceCodeHash, &d.UserCode, &clientName, &clientIP,
		&d.RequestedScopes, &d.Status, &d.TokenID, &ct, &d.CreatedAt, &d.ExpiresAt,
		&approvedBy, &d.ApprovedAt, &d.LastPolledAt); err != nil {
		return nil, fmt.Errorf("scan device request: %w", err)
	}
	d.ClientName = derefString(clientName)
	d.ClientIP = derefString(clientIP)
	d.ApprovedBy = derefString(approvedBy)
	if len(ct) > 0 {
		if s.key == nil {
			return nil, ErrEncryptionKeyRequired
		}
		plain, err := crypto.Decrypt(s.key, ct)
		if err != nil {
			return nil, fmt.Errorf("decrypt issued token: %w", err)
		}
		d.IssuedToken = string(plain)
	}
	return &d, nil
}

// --- User operations ---------------------------------------------------------

const userSelect = `SELECT id, email, password_hash, password_changed_at, created_at, created_by, last_login_at, disabled_at FROM pgmanager.users`

func (s *PostgresStore) CreateUser(ctx context.Context, u *User) error {
	var id int64
	var createdAt time.Time
	err := s.pool.QueryRow(ctx,
		`INSERT INTO pgmanager.users (email, password_hash, created_by)
		 VALUES ($1, $2, $3) RETURNING id, created_at`,
		u.Email, u.PasswordHash, u.CreatedBy,
	).Scan(&id, &createdAt)
	if err != nil {
		return fmt.Errorf("create user: %w", err)
	}
	u.ID = id
	u.CreatedAt = createdAt
	return nil
}

func (s *PostgresStore) GetUserByEmail(ctx context.Context, email string) (*User, error) {
	var u User
	var createdBy *string
	err := s.pool.QueryRow(ctx, userSelect+" WHERE email = $1", email).Scan(
		&u.ID, &u.Email, &u.PasswordHash, &u.PasswordChangedAt, &u.CreatedAt,
		&createdBy, &u.LastLoginAt, &u.DisabledAt)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get user: %w", err)
	}
	u.CreatedBy = derefString(createdBy)
	return &u, nil
}

func (s *PostgresStore) ListUsers(ctx context.Context) ([]User, error) {
	rows, err := s.pool.Query(ctx, userSelect+" ORDER BY email")
	if err != nil {
		return nil, fmt.Errorf("list users: %w", err)
	}
	defer rows.Close()

	var out []User
	for rows.Next() {
		var u User
		var createdBy *string
		if err := rows.Scan(&u.ID, &u.Email, &u.PasswordHash, &u.PasswordChangedAt,
			&u.CreatedAt, &createdBy, &u.LastLoginAt, &u.DisabledAt); err != nil {
			return nil, fmt.Errorf("scan user: %w", err)
		}
		u.CreatedBy = derefString(createdBy)
		out = append(out, u)
	}
	return out, rows.Err()
}

// SetUserPassword changes the password and signs out every existing session
// for that user. Both happen in one transaction: a partial apply would leave
// the password changed while old browsers stayed usable, which is precisely
// the situation a reset exists to end.
//
// Sessions being created concurrently are handled separately — see
// CreateSession, which refuses to insert against a password that has moved.
func (s *PostgresStore) SetUserPassword(ctx context.Context, email, passwordHash string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("set password: %w", err)
	}
	defer tx.Rollback(ctx)

	var userID int64
	err = tx.QueryRow(ctx,
		`UPDATE pgmanager.users SET password_hash = $1, password_changed_at = NOW()
		 WHERE email = $2 RETURNING id`, passwordHash, email).Scan(&userID)
	if err == pgx.ErrNoRows {
		return fmt.Errorf("user not found: %s", email)
	}
	if err != nil {
		return fmt.Errorf("set password: %w", err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM pgmanager.sessions WHERE user_id = $1`, userID); err != nil {
		return fmt.Errorf("clear sessions: %w", err)
	}
	return tx.Commit(ctx)
}

func (s *PostgresStore) DeleteUser(ctx context.Context, email string) error {
	// Sessions cascade on delete, so access dies with the row.
	result, err := s.pool.Exec(ctx, `DELETE FROM pgmanager.users WHERE email = $1`, email)
	if err != nil {
		return fmt.Errorf("delete user: %w", err)
	}
	if result.RowsAffected() == 0 {
		return fmt.Errorf("user not found: %s", email)
	}
	return nil
}

func (s *PostgresStore) TouchUserLogin(ctx context.Context, id int64, when time.Time) error {
	_, err := s.pool.Exec(ctx, `UPDATE pgmanager.users SET last_login_at = $1 WHERE id = $2`, when, id)
	return err
}

func (s *PostgresStore) CountUsers(ctx context.Context) (int, error) {
	var n int
	if err := s.pool.QueryRow(ctx, `SELECT COUNT(*) FROM pgmanager.users`).Scan(&n); err != nil {
		return 0, fmt.Errorf("count users: %w", err)
	}
	return n, nil
}

// --- Session operations ------------------------------------------------------

// CreateSession stores a session, but only if the user's password has not
// changed since the caller verified it. Without that check a login racing a
// password reset could verify the old password, then insert its session after
// the reset had already deleted the others — leaving exactly the survivor the
// reset was meant to remove. The guarded insert is a compare-and-set, so it
// needs no locking.
func (s *PostgresStore) CreateSession(ctx context.Context, sess *Session, expectPasswordChangedAt time.Time) error {
	var id int64
	var createdAt time.Time
	err := s.pool.QueryRow(ctx,
		`INSERT INTO pgmanager.sessions (token_hash, user_id, expires_at, created_ip)
		 SELECT $1, u.id, $3, $4 FROM pgmanager.users u
		 WHERE u.id = $2 AND u.password_changed_at = $5
		 RETURNING id, created_at`,
		sess.TokenHash, sess.UserID, sess.ExpiresAt, sess.CreatedIP, expectPasswordChangedAt,
	).Scan(&id, &createdAt)
	if err == pgx.ErrNoRows {
		return ErrPasswordChanged
	}
	if err != nil {
		return fmt.Errorf("create session: %w", err)
	}
	sess.ID = id
	sess.CreatedAt = createdAt
	return nil
}

func (s *PostgresStore) GetSessionByHash(ctx context.Context, hash []byte) (*Session, error) {
	var sess Session
	var createdIP *string
	// Joined so the caller gets the identity in one round trip, and so a
	// session for a deleted user simply doesn't resolve.
	err := s.pool.QueryRow(ctx,
		`SELECT s.id, s.token_hash, s.user_id, u.email, s.created_at, s.expires_at, s.last_seen_at, s.created_ip
		 FROM pgmanager.sessions s
		 JOIN pgmanager.users u ON u.id = s.user_id
		 WHERE s.token_hash = $1 AND u.disabled_at IS NULL`, hash,
	).Scan(&sess.ID, &sess.TokenHash, &sess.UserID, &sess.Email,
		&sess.CreatedAt, &sess.ExpiresAt, &sess.LastSeenAt, &createdIP)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get session: %w", err)
	}
	sess.CreatedIP = derefString(createdIP)

	// Best-effort activity stamp; never fail a request over it.
	_, _ = s.pool.Exec(ctx, `UPDATE pgmanager.sessions SET last_seen_at = NOW() WHERE id = $1`, sess.ID)
	return &sess, nil
}

func (s *PostgresStore) DeleteSession(ctx context.Context, hash []byte) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM pgmanager.sessions WHERE token_hash = $1`, hash)
	return err
}

func (s *PostgresStore) DeleteExpiredSessions(ctx context.Context) (int, error) {
	result, err := s.pool.Exec(ctx, `DELETE FROM pgmanager.sessions WHERE expires_at < NOW()`)
	if err != nil {
		return 0, fmt.Errorf("delete expired sessions: %w", err)
	}
	return int(result.RowsAffected()), nil
}
