package project

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"pgmanager/internal/config"
	"pgmanager/internal/db"
	"pgmanager/internal/meta"
)

var (
	// validNameRegex matches valid project names (lowercase alphanumeric and underscores)
	validNameRegex = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

	// validExtensionNameRegex matches Postgres extension names. Allows
	// hyphens because canonical extensions like "uuid-ossp" require them.
	validExtensionNameRegex = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_-]*$`)

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
		"scratch": true,
	}

	// keyedEnvs hold more than one database, so each instance needs a key to
	// tell it from its siblings: the PR number for "pr", a caller-chosen
	// label for "scratch". The other envs are singletons per project.
	keyedEnvs = map[string]bool{
		"pr":      true,
		"scratch": true,
	}

	// validKeyRegex matches a scratch key. Same shape as a project name, so
	// the composed database name stays a plain Postgres identifier.
	validKeyRegex = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
)

// MaxIdentifierLen is Postgres's identifier limit. The database's own role is
// named `<database>_user`, so a database name has to leave room for that
// suffix or the role could not be created.
const MaxIdentifierLen = 63

// IsKeyedEnv reports whether an env holds more than one database, and so
// requires a key.
func IsKeyedEnv(env string) bool { return keyedEnvs[env] }

// envLabel renders an env and key the way they appear in a database name and
// in a URL path segment: "dev", "pr_42", "scratch_epic_231".
func envLabel(env, key string) string {
	if keyedEnvs[env] && key != "" {
		return env + "_" + key
	}
	return env
}

// Manager handles project and database operations
type Manager struct {
	cfg   *config.Config
	pg    *db.PostgresClient
	store meta.Store
}

// DatabaseInfo contains information about a database
type DatabaseInfo struct {
	Project      string
	Env          string
	Key          string
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
func NewManager(cfg *config.Config, store meta.Store) *Manager {
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
		return fmt.Errorf("invalid environment '%s', must be one of: prod, dev, staging, pr, scratch", env)
	}
	return nil
}

// ValidateExtensionName validates a Postgres extension name. The actual SQL is
// still emitted through pgx.Identifier{}.Sanitize() for defense in depth, but
// rejecting bad input here gives a clearer error.
func ValidateExtensionName(name string) error {
	if name == "" {
		return fmt.Errorf("extension name must not be empty")
	}
	if len(name) > 63 {
		return fmt.Errorf("extension name %q too long (max 63 chars)", name)
	}
	if !validExtensionNameRegex.MatchString(name) {
		return fmt.Errorf("invalid extension name %q (allowed: letters, digits, underscore, hyphen; must start with a letter)", name)
	}
	return nil
}

// ValidateKey checks a database key against its env. A singleton env must have
// no key; a keyed env must have one, shaped so the composed database name and
// its role name are both valid Postgres identifiers.
func ValidateKey(env, key string) error {
	if !keyedEnvs[env] {
		if key != "" {
			return fmt.Errorf("env '%s' takes no key", env)
		}
		return nil
	}
	if key == "" {
		return fmt.Errorf("env '%s' requires a key", env)
	}
	if env == "pr" {
		n, err := strconv.Atoi(key)
		if err != nil || n <= 0 {
			return fmt.Errorf("PR number must be a positive integer, got %q", key)
		}
		return nil
	}
	if !validKeyRegex.MatchString(key) {
		return fmt.Errorf("invalid key %q (allowed: lowercase letters, numbers, underscore; must start with a letter)", key)
	}
	return nil
}

// DatabaseName generates the database name for a project, environment and key.
func DatabaseName(project, env, key string) string {
	if keyedEnvs[env] && key != "" {
		return fmt.Sprintf("%s_%s_%s", project, env, key)
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
	existing, err := m.store.GetProject(ctx, name)
	if err != nil {
		return fmt.Errorf("failed to check project: %w", err)
	}
	if existing != nil {
		return fmt.Errorf("project '%s' already exists", name)
	}

	_, err = m.store.CreateProject(ctx, name)
	if err != nil {
		return fmt.Errorf("failed to create project: %w", err)
	}

	return nil
}

// ListProjects returns all projects
func (m *Manager) ListProjects(ctx context.Context) ([]meta.Project, error) {
	return m.store.ListProjects(ctx)
}

// DeleteProject deletes a project and all its databases
func (m *Manager) DeleteProject(ctx context.Context, name string) error {
	// Get all databases for this project
	databases, err := m.store.DeleteProject(ctx, name)
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

// CreateDatabase creates a new database for a project. If extensions is
// non-empty each name is installed into the new database (extensions
// usually require superuser, so this runs as the admin connection).
func (m *Manager) CreateDatabase(ctx context.Context, projectName, env, key string, extensions []string, ttl *time.Duration) (*DatabaseInfo, error) {
	if err := ValidateEnv(env); err != nil {
		return nil, err
	}
	if err := ValidateKey(env, key); err != nil {
		return nil, err
	}
	if ttl != nil {
		if *ttl <= 0 {
			return nil, fmt.Errorf("ttl must be positive")
		}
		// Only keyed envs are leased. Accepting a ttl here would give a
		// permanent database an expiry that RenewDatabase then refuses to
		// extend — a database nothing could keep alive.
		if !keyedEnvs[env] {
			return nil, fmt.Errorf("env '%s' is permanent and takes no ttl", env)
		}
	}

	for _, ext := range extensions {
		if err := ValidateExtensionName(ext); err != nil {
			return nil, err
		}
	}

	// Get project
	project, err := m.store.GetProject(ctx, projectName)
	if err != nil {
		return nil, fmt.Errorf("failed to get project: %w", err)
	}
	if project == nil {
		return nil, fmt.Errorf("project '%s' not found", projectName)
	}

	// Check if database already exists
	existing, err := m.store.GetDatabase(ctx, project.ID, env, key)
	if err != nil {
		return nil, fmt.Errorf("failed to check database: %w", err)
	}
	if existing != nil {
		return nil, fmt.Errorf("database already exists for %s/%s", projectName, envLabel(env, key))
	}

	// Generate names and password
	dbName := DatabaseName(projectName, env, key)
	if len(UserName(dbName)) > MaxIdentifierLen {
		return nil, fmt.Errorf("database name %q is too long once the _user role suffix is added (max %d)", dbName, MaxIdentifierLen)
	}
	userName := UserName(dbName)
	password := db.GeneratePassword()

	// Keyed envs are leased: they are created for something transient and are
	// reaped when the lease lapses. An explicit --ttl overrides the default;
	// the singleton envs stay permanent.
	var expiresAt *time.Time
	switch {
	case ttl != nil:
		t := time.Now().Add(*ttl)
		expiresAt = &t
	case keyedEnvs[env]:
		t := time.Now().Add(m.cfg.Cleanup.DefaultTTL)
		expiresAt = &t
	}

	// Create database in PostgreSQL
	if err := m.pg.CreateDatabase(ctx, dbName, userName, password); err != nil {
		return nil, fmt.Errorf("failed to create database: %w", err)
	}

	// Install requested extensions. If any fails, roll back the database so
	// callers don't see a half-provisioned DB land in the metadata store.
	if err := m.pg.EnableExtensions(ctx, dbName, extensions); err != nil {
		_ = m.pg.DropDatabase(ctx, dbName, userName)
		return nil, err
	}

	// Store metadata
	dbRecord, err := m.store.CreateDatabase(ctx, project.ID, dbName, userName, password, env, key, expiresAt)
	if err != nil {
		// Try to clean up the PostgreSQL database
		_ = m.pg.DropDatabase(ctx, dbName, userName)
		return nil, fmt.Errorf("failed to store database metadata: %w", err)
	}

	host := m.cfg.Postgres.EffectiveHost()
	port := m.cfg.Postgres.EffectivePort()
	return &DatabaseInfo{
		Project:      projectName,
		Env:          env,
		Key:          key,
		PRNumber:     dbRecord.PRNumber,
		DatabaseName: dbName,
		UserName:     userName,
		Password:     password,
		Host:         host,
		Port:         port,
		ConnString:   db.ConnectionString(host, port, dbName, userName, password, m.cfg.Postgres.SSLMode),
		CreatedAt:    dbRecord.CreatedAt,
		ExpiresAt:    expiresAt,
	}, nil
}

// GetDatabase returns information about a database
func (m *Manager) GetDatabase(ctx context.Context, projectName, env, key string) (*DatabaseInfo, error) {
	if err := ValidateEnv(env); err != nil {
		return nil, err
	}
	if err := ValidateKey(env, key); err != nil {
		return nil, err
	}

	// Get project
	project, err := m.store.GetProject(ctx, projectName)
	if err != nil {
		return nil, fmt.Errorf("failed to get project: %w", err)
	}
	if project == nil {
		return nil, fmt.Errorf("project '%s' not found", projectName)
	}

	// Get database
	dbRecord, err := m.store.GetDatabase(ctx, project.ID, env, key)
	if err != nil {
		return nil, fmt.Errorf("failed to get database: %w", err)
	}
	if dbRecord == nil {
		return nil, fmt.Errorf("database not found for %s/%s", projectName, envLabel(env, key))
	}

	host := m.cfg.Postgres.EffectiveHost()
	port := m.cfg.Postgres.EffectivePort()
	return &DatabaseInfo{
		Project:      projectName,
		Env:          env,
		Key:          dbRecord.Key,
		PRNumber:     dbRecord.PRNumber,
		DatabaseName: dbRecord.Name,
		UserName:     dbRecord.UserName,
		Password:     dbRecord.Password,
		Host:         host,
		Port:         port,
		ConnString:   db.ConnectionString(host, port, dbRecord.Name, dbRecord.UserName, dbRecord.Password, m.cfg.Postgres.SSLMode),
		CreatedAt:    dbRecord.CreatedAt,
		ExpiresAt:    dbRecord.ExpiresAt,
	}, nil
}

// ListDatabases returns all databases for a project, or all databases if project is empty
func (m *Manager) ListDatabases(ctx context.Context, projectName string) ([]DatabaseInfo, error) {
	var databases []meta.Database
	var err error

	if projectName == "" {
		databases, err = m.store.ListAllDatabases(ctx)
	} else {
		project, err := m.store.GetProject(ctx, projectName)
		if err != nil {
			return nil, fmt.Errorf("failed to get project: %w", err)
		}
		if project == nil {
			return nil, fmt.Errorf("project '%s' not found", projectName)
		}
		databases, err = m.store.ListDatabases(ctx, project.ID)
	}

	if err != nil {
		return nil, fmt.Errorf("failed to list databases: %w", err)
	}

	// Convert to DatabaseInfo and look up project names
	result := make([]DatabaseInfo, 0, len(databases))
	projectCache := make(map[int64]string)
	host := m.cfg.Postgres.EffectiveHost()
	port := m.cfg.Postgres.EffectivePort()

	for _, dbItem := range databases {
		// Get project name
		projectNameStr, ok := projectCache[dbItem.ProjectID]
		if !ok {
			projects, _ := m.store.ListProjects(ctx)
			for _, p := range projects {
				projectCache[p.ID] = p.Name
			}
			projectNameStr = projectCache[dbItem.ProjectID]
		}

		result = append(result, DatabaseInfo{
			Project:      projectNameStr,
			Env:          dbItem.Env,
			Key:          dbItem.Key,
			PRNumber:     dbItem.PRNumber,
			DatabaseName: dbItem.Name,
			UserName:     dbItem.UserName,
			Password:     dbItem.Password,
			Host:         host,
			Port:         port,
			ConnString:   db.ConnectionString(host, port, dbItem.Name, dbItem.UserName, dbItem.Password, m.cfg.Postgres.SSLMode),
			CreatedAt:    dbItem.CreatedAt,
			ExpiresAt:    dbItem.ExpiresAt,
		})
	}

	return result, nil
}

// RotatePassword generates a new password for the database's own role, applies
// it in Postgres, and stores it. Returns the database info carrying the new
// password. If terminate is true, existing backends on the database are killed
// so clients still holding the old credential are forced to reconnect.
func (m *Manager) RotatePassword(ctx context.Context, projectName, env, key string, terminate bool) (*DatabaseInfo, error) {
	if err := ValidateEnv(env); err != nil {
		return nil, err
	}
	if err := ValidateKey(env, key); err != nil {
		return nil, err
	}

	project, err := m.store.GetProject(ctx, projectName)
	if err != nil {
		return nil, fmt.Errorf("failed to get project: %w", err)
	}
	if project == nil {
		return nil, fmt.Errorf("project '%s' not found", projectName)
	}

	dbRecord, err := m.store.GetDatabase(ctx, project.ID, env, key)
	if err != nil {
		return nil, fmt.Errorf("failed to get database: %w", err)
	}
	if dbRecord == nil {
		return nil, fmt.Errorf("database not found for %s/%s", projectName, envLabel(env, key))
	}

	password := db.GeneratePassword()
	if err := m.pg.SetUserPassword(ctx, dbRecord.UserName, password); err != nil {
		return nil, fmt.Errorf("failed to rotate password: %w", err)
	}

	// Postgres is now the source of the new password; if the metadata write
	// fails, put the old one back so stored credentials stay usable.
	if err := m.store.SetDatabasePassword(ctx, dbRecord.Name, password); err != nil {
		_ = m.pg.SetUserPassword(ctx, dbRecord.UserName, dbRecord.Password)
		return nil, fmt.Errorf("failed to store new password: %w", err)
	}

	if terminate {
		if err := m.pg.TerminateConnections(ctx, dbRecord.Name); err != nil {
			// The rotation itself succeeded; don't fail the call over this.
			fmt.Printf("Warning: failed to terminate connections to %s: %v\n", dbRecord.Name, err)
		}
	}

	host := m.cfg.Postgres.EffectiveHost()
	port := m.cfg.Postgres.EffectivePort()
	return &DatabaseInfo{
		Project:      projectName,
		Env:          env,
		Key:          dbRecord.Key,
		PRNumber:     dbRecord.PRNumber,
		DatabaseName: dbRecord.Name,
		UserName:     dbRecord.UserName,
		Password:     password,
		Host:         host,
		Port:         port,
		ConnString:   db.ConnectionString(host, port, dbRecord.Name, dbRecord.UserName, password, m.cfg.Postgres.SSLMode),
		CreatedAt:    dbRecord.CreatedAt,
		ExpiresAt:    dbRecord.ExpiresAt,
	}, nil
}

// DeleteDatabase deletes a database
func (m *Manager) DeleteDatabase(ctx context.Context, projectName, env, key string) error {
	if err := ValidateEnv(env); err != nil {
		return err
	}
	if err := ValidateKey(env, key); err != nil {
		return err
	}

	// Get project
	project, err := m.store.GetProject(ctx, projectName)
	if err != nil {
		return fmt.Errorf("failed to get project: %w", err)
	}
	if project == nil {
		return fmt.Errorf("project '%s' not found", projectName)
	}

	// Get database
	dbRecord, err := m.store.GetDatabase(ctx, project.ID, env, key)
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
	if err := m.store.DeleteDatabase(ctx, dbRecord.Name); err != nil {
		return fmt.Errorf("failed to delete database metadata: %w", err)
	}

	return nil
}

// reapable is the set Cleanup will drop, keyed by database name. It is split
// out from Cleanup so the selection rule can be tested without a Postgres to
// drop anything in.
func (m *Manager) reapable(ctx context.Context, olderThan time.Duration) (map[string]meta.Database, error) {
	expired, err := m.store.GetExpiredDatabases(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get expired databases: %w", err)
	}

	unleased, err := m.store.GetUnleasedDatabasesOlderThan(ctx, "pr", olderThan)
	if err != nil {
		return nil, fmt.Errorf("failed to get unleased PR databases: %w", err)
	}

	toDelete := make(map[string]meta.Database, len(expired)+len(unleased))
	for _, db := range expired {
		toDelete[db.Name] = db
	}
	for _, db := range unleased {
		toDelete[db.Name] = db
	}
	return toDelete, nil
}

// RenewDatabase pushes a database's lease out by ttl from now, so a database
// that is still in use is never reaped. Renewing a permanent database (one of
// the singleton envs) is refused rather than silently giving it an expiry.
func (m *Manager) RenewDatabase(ctx context.Context, projectName, env, key string, ttl time.Duration) (*DatabaseInfo, error) {
	if err := ValidateEnv(env); err != nil {
		return nil, err
	}
	if err := ValidateKey(env, key); err != nil {
		return nil, err
	}
	if ttl <= 0 {
		return nil, fmt.Errorf("ttl must be positive")
	}
	if !keyedEnvs[env] {
		return nil, fmt.Errorf("env '%s' has no lease to renew", env)
	}

	info, err := m.GetDatabase(ctx, projectName, env, key)
	if err != nil {
		return nil, err
	}

	expiresAt := time.Now().Add(ttl)
	if err := m.store.SetDatabaseExpiry(ctx, info.DatabaseName, &expiresAt); err != nil {
		return nil, fmt.Errorf("failed to renew lease: %w", err)
	}
	info.ExpiresAt = &expiresAt
	return info, nil
}

// Cleanup drops every database whose lease has lapsed.
//
// The lease (ExpiresAt) is the only lifetime rule. It used to be joined with a
// second one — any PR database older than olderThan, whatever its expiry —
// which silently overrode a renewed lease and made a longer-lived database
// impossible. That sweep now only reaches rows carrying no lease at all, which
// means rows written before leases were set at create time; everything created
// since expires on its own terms and can be renewed.
func (m *Manager) Cleanup(ctx context.Context, olderThan time.Duration) ([]string, error) {
	var deleted []string

	toDelete, err := m.reapable(ctx, olderThan)
	if err != nil {
		return nil, err
	}

	// Delete each database
	for _, dbRecord := range toDelete {
		if err := m.pg.DropDatabase(ctx, dbRecord.Name, dbRecord.UserName); err != nil {
			fmt.Printf("Warning: failed to drop database %s: %v\n", dbRecord.Name, err)
			continue
		}

		if err := m.store.DeleteDatabase(ctx, dbRecord.Name); err != nil {
			fmt.Printf("Warning: failed to delete metadata for %s: %v\n", dbRecord.Name, err)
			continue
		}

		deleted = append(deleted, dbRecord.Name)
	}

	return deleted, nil
}

// ParseEnv splits a database's env segment into its env and key. Keyed envs
// carry their key after an underscore — "pr_42", "scratch_epic_231" — and env
// names contain no underscore, so the first one is the separator.
func ParseEnv(segment string) (env, key string, err error) {
	env, key, found := strings.Cut(segment, "_")
	if !found || !keyedEnvs[env] {
		// Not a keyed segment at all: the whole thing is the env. Whether it
		// is a *valid* env is ValidateEnv's call, not this one's.
		return segment, "", nil
	}
	if err := ValidateKey(env, key); err != nil {
		return "", "", err
	}
	return env, key, nil
}
