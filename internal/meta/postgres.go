package meta

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PostgresStore handles PostgreSQL metadata operations
type PostgresStore struct {
	pool *pgxpool.Pool
}

// NewPostgresStore creates a new PostgreSQL metadata store
func NewPostgresStore(ctx context.Context, connString string) (*PostgresStore, error) {
	pool, err := pgxpool.New(ctx, connString)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to database: %w", err)
	}

	// Test connection
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	store := &PostgresStore{pool: pool}
	if err := store.migrate(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("failed to migrate: %w", err)
	}

	return store, nil
}

// migrate creates the necessary schema and tables
func (s *PostgresStore) migrate(ctx context.Context) error {
	schema := `
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
		password TEXT NOT NULL,
		env TEXT NOT NULL,
		pr_number INTEGER,
		created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
		expires_at TIMESTAMPTZ
	);

	CREATE INDEX IF NOT EXISTS idx_databases_project_id ON pgmanager.databases(project_id);
	CREATE INDEX IF NOT EXISTS idx_databases_env ON pgmanager.databases(env);
	CREATE INDEX IF NOT EXISTS idx_databases_expires_at ON pgmanager.databases(expires_at);
	`

	_, err := s.pool.Exec(ctx, schema)
	return err
}

// Close closes the database connection pool
func (s *PostgresStore) Close() error {
	s.pool.Close()
	return nil
}

// CreateProject creates a new project
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

	return &Project{
		ID:        id,
		Name:      name,
		CreatedAt: createdAt,
	}, nil
}

// GetProject retrieves a project by name
func (s *PostgresStore) GetProject(ctx context.Context, name string) (*Project, error) {
	var p Project
	err := s.pool.QueryRow(ctx,
		"SELECT id, name, created_at FROM pgmanager.projects WHERE name = $1",
		name,
	).Scan(&p.ID, &p.Name, &p.CreatedAt)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get project: %w", err)
	}
	return &p, nil
}

// ListProjects returns all projects
func (s *PostgresStore) ListProjects(ctx context.Context) ([]Project, error) {
	rows, err := s.pool.Query(ctx,
		"SELECT id, name, created_at FROM pgmanager.projects ORDER BY name",
	)
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

// DeleteProject deletes a project and returns its associated databases
func (s *PostgresStore) DeleteProject(ctx context.Context, name string) ([]Database, error) {
	// First, get the project
	project, err := s.GetProject(ctx, name)
	if err != nil {
		return nil, err
	}
	if project == nil {
		return nil, fmt.Errorf("project not found: %s", name)
	}

	// Get all databases for this project
	databases, err := s.ListDatabases(ctx, project.ID)
	if err != nil {
		return nil, err
	}

	// Delete the project (cascades to databases due to foreign key)
	_, err = s.pool.Exec(ctx, "DELETE FROM pgmanager.projects WHERE id = $1", project.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to delete project: %w", err)
	}

	return databases, nil
}

// CreateDatabase creates a new database record
func (s *PostgresStore) CreateDatabase(ctx context.Context, projectID int64, name, userName, password, env string, prNumber *int, expiresAt *time.Time) (*Database, error) {
	var id int64
	var createdAt time.Time
	err := s.pool.QueryRow(ctx,
		`INSERT INTO pgmanager.databases (project_id, name, user_name, password, env, pr_number, expires_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)
		 RETURNING id, created_at`,
		projectID, name, userName, password, env, prNumber, expiresAt,
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

// GetDatabase retrieves a database by project and environment
func (s *PostgresStore) GetDatabase(ctx context.Context, projectID int64, env string, prNumber *int) (*Database, error) {
	var d Database
	var expiresAt *time.Time
	var prNum *int

	query := `SELECT id, project_id, name, user_name, password, env, pr_number, created_at, expires_at
	          FROM pgmanager.databases WHERE project_id = $1 AND env = $2`
	args := []interface{}{projectID, env}

	if prNumber != nil {
		query += " AND pr_number = $3"
		args = append(args, *prNumber)
	} else {
		query += " AND pr_number IS NULL"
	}

	err := s.pool.QueryRow(ctx, query, args...).Scan(
		&d.ID, &d.ProjectID, &d.Name, &d.UserName, &d.Password, &d.Env, &prNum, &d.CreatedAt, &expiresAt,
	)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get database: %w", err)
	}

	d.PRNumber = prNum
	d.ExpiresAt = expiresAt

	return &d, nil
}

// GetDatabaseByName retrieves a database by its full name
func (s *PostgresStore) GetDatabaseByName(ctx context.Context, name string) (*Database, error) {
	var d Database
	var expiresAt *time.Time
	var prNum *int

	err := s.pool.QueryRow(ctx,
		`SELECT id, project_id, name, user_name, password, env, pr_number, created_at, expires_at
		 FROM pgmanager.databases WHERE name = $1`,
		name,
	).Scan(&d.ID, &d.ProjectID, &d.Name, &d.UserName, &d.Password, &d.Env, &prNum, &d.CreatedAt, &expiresAt)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get database: %w", err)
	}

	d.PRNumber = prNum
	d.ExpiresAt = expiresAt

	return &d, nil
}

// ListDatabases returns all databases for a project
func (s *PostgresStore) ListDatabases(ctx context.Context, projectID int64) ([]Database, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, project_id, name, user_name, password, env, pr_number, created_at, expires_at
		 FROM pgmanager.databases WHERE project_id = $1 ORDER BY name`,
		projectID,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to list databases: %w", err)
	}
	defer rows.Close()

	return scanDatabasesPg(rows)
}

// ListAllDatabases returns all databases
func (s *PostgresStore) ListAllDatabases(ctx context.Context) ([]Database, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, project_id, name, user_name, password, env, pr_number, created_at, expires_at
		 FROM pgmanager.databases ORDER BY name`,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to list all databases: %w", err)
	}
	defer rows.Close()

	return scanDatabasesPg(rows)
}

// DeleteDatabase deletes a database record by name
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

// GetExpiredDatabases returns databases that have expired
func (s *PostgresStore) GetExpiredDatabases(ctx context.Context) ([]Database, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, project_id, name, user_name, password, env, pr_number, created_at, expires_at
		 FROM pgmanager.databases
		 WHERE expires_at IS NOT NULL AND expires_at < NOW()
		 ORDER BY expires_at`,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to get expired databases: %w", err)
	}
	defer rows.Close()

	return scanDatabasesPg(rows)
}

// GetDatabasesOlderThan returns databases created before the given duration
func (s *PostgresStore) GetDatabasesOlderThan(ctx context.Context, env string, olderThan time.Duration) ([]Database, error) {
	cutoff := time.Now().Add(-olderThan)
	rows, err := s.pool.Query(ctx,
		`SELECT id, project_id, name, user_name, password, env, pr_number, created_at, expires_at
		 FROM pgmanager.databases
		 WHERE env = $1 AND created_at < $2
		 ORDER BY created_at`,
		env, cutoff,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to get old databases: %w", err)
	}
	defer rows.Close()

	return scanDatabasesPg(rows)
}

func scanDatabasesPg(rows pgx.Rows) ([]Database, error) {
	var databases []Database
	for rows.Next() {
		var d Database
		var expiresAt *time.Time
		var prNum *int

		if err := rows.Scan(&d.ID, &d.ProjectID, &d.Name, &d.UserName, &d.Password, &d.Env, &prNum, &d.CreatedAt, &expiresAt); err != nil {
			return nil, fmt.Errorf("failed to scan database: %w", err)
		}

		d.PRNumber = prNum
		d.ExpiresAt = expiresAt

		databases = append(databases, d)
	}

	return databases, rows.Err()
}
