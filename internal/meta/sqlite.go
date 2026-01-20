package meta

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// Store handles SQLite metadata operations
type Store struct {
	db *sql.DB
}

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
	Env       string      // prod, dev, staging, pr
	PRNumber  *int        // Only set for PR databases
	CreatedAt time.Time
	ExpiresAt *time.Time  // TTL for PR databases
}

// NewStore creates a new SQLite metadata store
func NewStore(dbPath string) (*Store, error) {
	// Create directory if it doesn't exist with restricted permissions
	dir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("failed to create directory: %w", err)
	}

	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	// Enable foreign key constraints
	if _, err := db.Exec("PRAGMA foreign_keys = ON"); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to enable foreign keys: %w", err)
	}

	store := &Store{db: db}
	if err := store.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to migrate: %w", err)
	}

	return store, nil
}

// migrate creates the necessary tables
func (s *Store) migrate() error {
	schema := `
	CREATE TABLE IF NOT EXISTS projects (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT UNIQUE NOT NULL,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS databases (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		project_id INTEGER NOT NULL,
		name TEXT UNIQUE NOT NULL,
		user_name TEXT NOT NULL,
		password TEXT NOT NULL,
		env TEXT NOT NULL,
		pr_number INTEGER,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		expires_at DATETIME,
		FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE CASCADE
	);

	CREATE INDEX IF NOT EXISTS idx_databases_project_id ON databases(project_id);
	CREATE INDEX IF NOT EXISTS idx_databases_env ON databases(env);
	CREATE INDEX IF NOT EXISTS idx_databases_expires_at ON databases(expires_at);
	`

	_, err := s.db.Exec(schema)
	return err
}

// Close closes the database connection
func (s *Store) Close() error {
	return s.db.Close()
}

// CreateProject creates a new project
func (s *Store) CreateProject(name string) (*Project, error) {
	result, err := s.db.Exec("INSERT INTO projects (name) VALUES (?)", name)
	if err != nil {
		return nil, fmt.Errorf("failed to create project: %w", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("failed to get project id: %w", err)
	}

	return &Project{
		ID:        id,
		Name:      name,
		CreatedAt: time.Now(),
	}, nil
}

// GetProject retrieves a project by name
func (s *Store) GetProject(name string) (*Project, error) {
	var p Project
	err := s.db.QueryRow(
		"SELECT id, name, created_at FROM projects WHERE name = ?",
		name,
	).Scan(&p.ID, &p.Name, &p.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get project: %w", err)
	}
	return &p, nil
}

// ListProjects returns all projects
func (s *Store) ListProjects() ([]Project, error) {
	rows, err := s.db.Query("SELECT id, name, created_at FROM projects ORDER BY name")
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
func (s *Store) DeleteProject(name string) ([]Database, error) {
	// First, get the project
	project, err := s.GetProject(name)
	if err != nil {
		return nil, err
	}
	if project == nil {
		return nil, fmt.Errorf("project not found: %s", name)
	}

	// Get all databases for this project
	databases, err := s.ListDatabases(project.ID)
	if err != nil {
		return nil, err
	}

	// Delete the project (cascades to databases due to foreign key)
	_, err = s.db.Exec("DELETE FROM projects WHERE id = ?", project.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to delete project: %w", err)
	}

	return databases, nil
}

// CreateDatabase creates a new database record
func (s *Store) CreateDatabase(projectID int64, name, userName, password, env string, prNumber *int, expiresAt *time.Time) (*Database, error) {
	result, err := s.db.Exec(
		"INSERT INTO databases (project_id, name, user_name, password, env, pr_number, expires_at) VALUES (?, ?, ?, ?, ?, ?, ?)",
		projectID, name, userName, password, env, prNumber, expiresAt,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create database: %w", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("failed to get database id: %w", err)
	}

	return &Database{
		ID:        id,
		ProjectID: projectID,
		Name:      name,
		UserName:  userName,
		Password:  password,
		Env:       env,
		PRNumber:  prNumber,
		CreatedAt: time.Now(),
		ExpiresAt: expiresAt,
	}, nil
}

// GetDatabase retrieves a database by project and environment
func (s *Store) GetDatabase(projectID int64, env string, prNumber *int) (*Database, error) {
	var d Database
	var expiresAt sql.NullTime
	var prNum sql.NullInt64

	query := "SELECT id, project_id, name, user_name, password, env, pr_number, created_at, expires_at FROM databases WHERE project_id = ? AND env = ?"
	args := []interface{}{projectID, env}

	if prNumber != nil {
		query += " AND pr_number = ?"
		args = append(args, *prNumber)
	} else {
		query += " AND pr_number IS NULL"
	}

	err := s.db.QueryRow(query, args...).Scan(
		&d.ID, &d.ProjectID, &d.Name, &d.UserName, &d.Password, &d.Env, &prNum, &d.CreatedAt, &expiresAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get database: %w", err)
	}

	if prNum.Valid {
		n := int(prNum.Int64)
		d.PRNumber = &n
	}
	if expiresAt.Valid {
		d.ExpiresAt = &expiresAt.Time
	}

	return &d, nil
}

// GetDatabaseByName retrieves a database by its full name
func (s *Store) GetDatabaseByName(name string) (*Database, error) {
	var d Database
	var expiresAt sql.NullTime
	var prNum sql.NullInt64

	err := s.db.QueryRow(
		"SELECT id, project_id, name, user_name, password, env, pr_number, created_at, expires_at FROM databases WHERE name = ?",
		name,
	).Scan(&d.ID, &d.ProjectID, &d.Name, &d.UserName, &d.Password, &d.Env, &prNum, &d.CreatedAt, &expiresAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get database: %w", err)
	}

	if prNum.Valid {
		n := int(prNum.Int64)
		d.PRNumber = &n
	}
	if expiresAt.Valid {
		d.ExpiresAt = &expiresAt.Time
	}

	return &d, nil
}

// ListDatabases returns all databases for a project
func (s *Store) ListDatabases(projectID int64) ([]Database, error) {
	rows, err := s.db.Query(
		"SELECT id, project_id, name, user_name, password, env, pr_number, created_at, expires_at FROM databases WHERE project_id = ? ORDER BY name",
		projectID,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to list databases: %w", err)
	}
	defer rows.Close()

	return scanDatabases(rows)
}

// ListAllDatabases returns all databases
func (s *Store) ListAllDatabases() ([]Database, error) {
	rows, err := s.db.Query(
		"SELECT id, project_id, name, user_name, password, env, pr_number, created_at, expires_at FROM databases ORDER BY name",
	)
	if err != nil {
		return nil, fmt.Errorf("failed to list all databases: %w", err)
	}
	defer rows.Close()

	return scanDatabases(rows)
}

// DeleteDatabase deletes a database record by name
func (s *Store) DeleteDatabase(name string) error {
	result, err := s.db.Exec("DELETE FROM databases WHERE name = ?", name)
	if err != nil {
		return fmt.Errorf("failed to delete database: %w", err)
	}

	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get affected rows: %w", err)
	}
	if affected == 0 {
		return fmt.Errorf("database not found: %s", name)
	}

	return nil
}

// GetExpiredDatabases returns databases that have expired
func (s *Store) GetExpiredDatabases() ([]Database, error) {
	rows, err := s.db.Query(
		"SELECT id, project_id, name, user_name, password, env, pr_number, created_at, expires_at FROM databases WHERE expires_at IS NOT NULL AND expires_at < datetime('now') ORDER BY expires_at",
	)
	if err != nil {
		return nil, fmt.Errorf("failed to get expired databases: %w", err)
	}
	defer rows.Close()

	return scanDatabases(rows)
}

// GetDatabasesOlderThan returns databases created before the given duration
func (s *Store) GetDatabasesOlderThan(env string, olderThan time.Duration) ([]Database, error) {
	cutoff := time.Now().Add(-olderThan)
	rows, err := s.db.Query(
		"SELECT id, project_id, name, user_name, password, env, pr_number, created_at, expires_at FROM databases WHERE env = ? AND created_at < ? ORDER BY created_at",
		env, cutoff,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to get old databases: %w", err)
	}
	defer rows.Close()

	return scanDatabases(rows)
}

func scanDatabases(rows *sql.Rows) ([]Database, error) {
	var databases []Database
	for rows.Next() {
		var d Database
		var expiresAt sql.NullTime
		var prNum sql.NullInt64

		if err := rows.Scan(&d.ID, &d.ProjectID, &d.Name, &d.UserName, &d.Password, &d.Env, &prNum, &d.CreatedAt, &expiresAt); err != nil {
			return nil, fmt.Errorf("failed to scan database: %w", err)
		}

		if prNum.Valid {
			n := int(prNum.Int64)
			d.PRNumber = &n
		}
		if expiresAt.Valid {
			d.ExpiresAt = &expiresAt.Time
		}

		databases = append(databases, d)
	}

	return databases, rows.Err()
}
