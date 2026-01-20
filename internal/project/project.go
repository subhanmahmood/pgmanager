package project

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"pgmanager/internal/config"
	"pgmanager/internal/db"
	"pgmanager/internal/meta"
)

var (
	// validNameRegex matches valid project names (lowercase alphanumeric and underscores)
	validNameRegex = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

	// reservedNames are names that cannot be used as project names
	reservedNames = map[string]bool{
		"postgres":  true,
		"template0": true,
		"template1": true,
		"admin":     true,
		"root":      true,
		"system":    true,
	}

	// validEnvs are the allowed environment names
	validEnvs = map[string]bool{
		"prod":    true,
		"dev":     true,
		"staging": true,
		"pr":      true,
	}
)

// Manager handles project and database operations
type Manager struct {
	cfg    *config.Config
	pg     *db.PostgresClient
	store  *meta.Store
}

// DatabaseInfo contains information about a database
type DatabaseInfo struct {
	Project      string
	Env          string
	PRNumber     *int
	DatabaseName string
	UserName     string
	Password     string
	Host         string
	Port         int
	ConnString   string
	CreatedAt    time.Time
	ExpiresAt    *time.Time
}

// NewManager creates a new project manager
func NewManager(cfg *config.Config, store *meta.Store) *Manager {
	return &Manager{
		cfg:   cfg,
		pg:    db.NewPostgresClient(&cfg.Postgres),
		store: store,
	}
}

// ValidateName validates a project name
func ValidateName(name string) error {
	if len(name) < 2 {
		return fmt.Errorf("project name must be at least 2 characters")
	}
	if len(name) > 32 {
		return fmt.Errorf("project name must be at most 32 characters")
	}
	if !validNameRegex.MatchString(name) {
		return fmt.Errorf("project name must start with a letter and contain only lowercase letters, numbers, and underscores")
	}
	if reservedNames[name] {
		return fmt.Errorf("'%s' is a reserved name", name)
	}
	return nil
}

// ValidateEnv validates an environment name
func ValidateEnv(env string) error {
	if !validEnvs[env] {
		return fmt.Errorf("invalid environment '%s', must be one of: prod, dev, staging, pr", env)
	}
	return nil
}

// DatabaseName generates the database name for a project and environment
func DatabaseName(project, env string, prNumber *int) string {
	if env == "pr" && prNumber != nil {
		return fmt.Sprintf("%s_pr_%d", project, *prNumber)
	}
	return fmt.Sprintf("%s_%s", project, env)
}

// UserName generates the user name for a database
func UserName(dbName string) string {
	return dbName + "_user"
}

// CreateProject creates a new project
func (m *Manager) CreateProject(ctx context.Context, name string) error {
	if err := ValidateName(name); err != nil {
		return err
	}

	// Check if project already exists
	existing, err := m.store.GetProject(name)
	if err != nil {
		return fmt.Errorf("failed to check project: %w", err)
	}
	if existing != nil {
		return fmt.Errorf("project '%s' already exists", name)
	}

	_, err = m.store.CreateProject(name)
	if err != nil {
		return fmt.Errorf("failed to create project: %w", err)
	}

	return nil
}

// ListProjects returns all projects
func (m *Manager) ListProjects(ctx context.Context) ([]meta.Project, error) {
	return m.store.ListProjects()
}

// DeleteProject deletes a project and all its databases
func (m *Manager) DeleteProject(ctx context.Context, name string) error {
	// Get all databases for this project
	databases, err := m.store.DeleteProject(name)
	if err != nil {
		return err
	}

	// Drop all databases from PostgreSQL
	for _, db := range databases {
		if err := m.pg.DropDatabase(ctx, db.Name, db.UserName); err != nil {
			// Log but continue with other databases
			fmt.Printf("Warning: failed to drop database %s: %v\n", db.Name, err)
		}
	}

	return nil
}

// CreateDatabase creates a new database for a project
func (m *Manager) CreateDatabase(ctx context.Context, projectName, env string, prNumber *int) (*DatabaseInfo, error) {
	if err := ValidateEnv(env); err != nil {
		return nil, err
	}

	if env == "pr" && prNumber == nil {
		return nil, fmt.Errorf("PR number is required for PR databases")
	}

	// Get project
	project, err := m.store.GetProject(projectName)
	if err != nil {
		return nil, fmt.Errorf("failed to get project: %w", err)
	}
	if project == nil {
		return nil, fmt.Errorf("project '%s' not found", projectName)
	}

	// Check if database already exists
	existing, err := m.store.GetDatabase(project.ID, env, prNumber)
	if err != nil {
		return nil, fmt.Errorf("failed to check database: %w", err)
	}
	if existing != nil {
		return nil, fmt.Errorf("database already exists for %s/%s", projectName, env)
	}

	// Generate names and password
	dbName := DatabaseName(projectName, env, prNumber)
	userName := UserName(dbName)
	password := db.GeneratePassword()

	// Set TTL for PR databases
	var expiresAt *time.Time
	if env == "pr" {
		t := time.Now().Add(m.cfg.Cleanup.DefaultTTL)
		expiresAt = &t
	}

	// Create database in PostgreSQL
	if err := m.pg.CreateDatabase(ctx, dbName, userName, password); err != nil {
		return nil, fmt.Errorf("failed to create database: %w", err)
	}

	// Store metadata
	dbRecord, err := m.store.CreateDatabase(project.ID, dbName, userName, password, env, prNumber, expiresAt)
	if err != nil {
		// Try to clean up the PostgreSQL database
		_ = m.pg.DropDatabase(ctx, dbName, userName)
		return nil, fmt.Errorf("failed to store database metadata: %w", err)
	}

	return &DatabaseInfo{
		Project:      projectName,
		Env:          env,
		PRNumber:     prNumber,
		DatabaseName: dbName,
		UserName:     userName,
		Password:     password,
		Host:         m.cfg.Postgres.Host,
		Port:         m.cfg.Postgres.Port,
		ConnString:   db.ConnectionString(m.cfg.Postgres.Host, m.cfg.Postgres.Port, dbName, userName, password, m.cfg.Postgres.SSLMode),
		CreatedAt:    dbRecord.CreatedAt,
		ExpiresAt:    expiresAt,
	}, nil
}

// GetDatabase returns information about a database
func (m *Manager) GetDatabase(ctx context.Context, projectName, env string, prNumber *int) (*DatabaseInfo, error) {
	if err := ValidateEnv(env); err != nil {
		return nil, err
	}

	// Get project
	project, err := m.store.GetProject(projectName)
	if err != nil {
		return nil, fmt.Errorf("failed to get project: %w", err)
	}
	if project == nil {
		return nil, fmt.Errorf("project '%s' not found", projectName)
	}

	// Get database
	dbRecord, err := m.store.GetDatabase(project.ID, env, prNumber)
	if err != nil {
		return nil, fmt.Errorf("failed to get database: %w", err)
	}
	if dbRecord == nil {
		envStr := env
		if prNumber != nil {
			envStr = fmt.Sprintf("pr_%d", *prNumber)
		}
		return nil, fmt.Errorf("database not found for %s/%s", projectName, envStr)
	}

	return &DatabaseInfo{
		Project:      projectName,
		Env:          env,
		PRNumber:     dbRecord.PRNumber,
		DatabaseName: dbRecord.Name,
		UserName:     dbRecord.UserName,
		Password:     dbRecord.Password,
		Host:         m.cfg.Postgres.Host,
		Port:         m.cfg.Postgres.Port,
		ConnString:   db.ConnectionString(m.cfg.Postgres.Host, m.cfg.Postgres.Port, dbRecord.Name, dbRecord.UserName, dbRecord.Password, m.cfg.Postgres.SSLMode),
		CreatedAt:    dbRecord.CreatedAt,
		ExpiresAt:    dbRecord.ExpiresAt,
	}, nil
}

// ListDatabases returns all databases for a project, or all databases if project is empty
func (m *Manager) ListDatabases(ctx context.Context, projectName string) ([]DatabaseInfo, error) {
	var databases []meta.Database
	var err error

	if projectName == "" {
		databases, err = m.store.ListAllDatabases()
	} else {
		project, err := m.store.GetProject(projectName)
		if err != nil {
			return nil, fmt.Errorf("failed to get project: %w", err)
		}
		if project == nil {
			return nil, fmt.Errorf("project '%s' not found", projectName)
		}
		databases, err = m.store.ListDatabases(project.ID)
	}

	if err != nil {
		return nil, fmt.Errorf("failed to list databases: %w", err)
	}

	// Convert to DatabaseInfo and look up project names
	result := make([]DatabaseInfo, 0, len(databases))
	projectCache := make(map[int64]string)

	for _, dbItem := range databases {
		// Get project name
		projectNameStr, ok := projectCache[dbItem.ProjectID]
		if !ok {
			projects, _ := m.store.ListProjects()
			for _, p := range projects {
				projectCache[p.ID] = p.Name
			}
			projectNameStr = projectCache[dbItem.ProjectID]
		}

		result = append(result, DatabaseInfo{
			Project:      projectNameStr,
			Env:          dbItem.Env,
			PRNumber:     dbItem.PRNumber,
			DatabaseName: dbItem.Name,
			UserName:     dbItem.UserName,
			Password:     dbItem.Password,
			Host:         m.cfg.Postgres.Host,
			Port:         m.cfg.Postgres.Port,
			ConnString:   db.ConnectionString(m.cfg.Postgres.Host, m.cfg.Postgres.Port, dbItem.Name, dbItem.UserName, dbItem.Password, m.cfg.Postgres.SSLMode),
			CreatedAt:    dbItem.CreatedAt,
			ExpiresAt:    dbItem.ExpiresAt,
		})
	}

	return result, nil
}

// DeleteDatabase deletes a database
func (m *Manager) DeleteDatabase(ctx context.Context, projectName, env string, prNumber *int) error {
	if err := ValidateEnv(env); err != nil {
		return err
	}

	// Get project
	project, err := m.store.GetProject(projectName)
	if err != nil {
		return fmt.Errorf("failed to get project: %w", err)
	}
	if project == nil {
		return fmt.Errorf("project '%s' not found", projectName)
	}

	// Get database
	dbRecord, err := m.store.GetDatabase(project.ID, env, prNumber)
	if err != nil {
		return fmt.Errorf("failed to get database: %w", err)
	}
	if dbRecord == nil {
		return fmt.Errorf("database not found")
	}

	// Drop from PostgreSQL
	if err := m.pg.DropDatabase(ctx, dbRecord.Name, dbRecord.UserName); err != nil {
		return fmt.Errorf("failed to drop database: %w", err)
	}

	// Delete metadata
	if err := m.store.DeleteDatabase(dbRecord.Name); err != nil {
		return fmt.Errorf("failed to delete database metadata: %w", err)
	}

	return nil
}

// Cleanup removes expired and old PR databases
func (m *Manager) Cleanup(ctx context.Context, olderThan time.Duration) ([]string, error) {
	var deleted []string

	// Get expired databases
	expired, err := m.store.GetExpiredDatabases()
	if err != nil {
		return nil, fmt.Errorf("failed to get expired databases: %w", err)
	}

	// Get old PR databases
	oldPR, err := m.store.GetDatabasesOlderThan("pr", olderThan)
	if err != nil {
		return nil, fmt.Errorf("failed to get old PR databases: %w", err)
	}

	// Combine and deduplicate
	toDelete := make(map[string]meta.Database)
	for _, db := range expired {
		toDelete[db.Name] = db
	}
	for _, db := range oldPR {
		toDelete[db.Name] = db
	}

	// Delete each database
	for _, dbRecord := range toDelete {
		if err := m.pg.DropDatabase(ctx, dbRecord.Name, dbRecord.UserName); err != nil {
			fmt.Printf("Warning: failed to drop database %s: %v\n", dbRecord.Name, err)
			continue
		}

		if err := m.store.DeleteDatabase(dbRecord.Name); err != nil {
			fmt.Printf("Warning: failed to delete metadata for %s: %v\n", dbRecord.Name, err)
			continue
		}

		deleted = append(deleted, dbRecord.Name)
	}

	return deleted, nil
}

// ParseEnv parses an environment string which may include a PR number
func ParseEnv(envStr string) (env string, prNumber *int, err error) {
	if strings.HasPrefix(envStr, "pr_") {
		var num int
		if _, err := fmt.Sscanf(envStr, "pr_%d", &num); err != nil {
			return "", nil, fmt.Errorf("invalid PR environment format, expected pr_<number>")
		}
		return "pr", &num, nil
	}
	return envStr, nil, nil
}
