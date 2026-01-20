package meta

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func setupTestStore(t *testing.T) (*Store, func()) {
	t.Helper()

	tmpDir, err := os.MkdirTemp("", "pgmanager-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}

	dbPath := filepath.Join(tmpDir, "test.db")
	store, err := NewStore(dbPath)
	if err != nil {
		os.RemoveAll(tmpDir)
		t.Fatalf("failed to create store: %v", err)
	}

	cleanup := func() {
		store.Close()
		os.RemoveAll(tmpDir)
	}

	return store, cleanup
}

func TestProjectCRUD(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	// Test Create
	project, err := store.CreateProject("testproject")
	if err != nil {
		t.Fatalf("failed to create project: %v", err)
	}
	if project.Name != "testproject" {
		t.Errorf("project name = %q, want %q", project.Name, "testproject")
	}
	if project.ID == 0 {
		t.Error("project ID should not be 0")
	}

	// Test Get
	retrieved, err := store.GetProject("testproject")
	if err != nil {
		t.Fatalf("failed to get project: %v", err)
	}
	if retrieved == nil {
		t.Fatal("expected project, got nil")
	}
	if retrieved.Name != "testproject" {
		t.Errorf("retrieved project name = %q, want %q", retrieved.Name, "testproject")
	}

	// Test Get non-existent
	notFound, err := store.GetProject("nonexistent")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if notFound != nil {
		t.Errorf("expected nil for non-existent project, got %v", notFound)
	}

	// Test List
	_, err = store.CreateProject("anotherproject")
	if err != nil {
		t.Fatalf("failed to create second project: %v", err)
	}

	projects, err := store.ListProjects()
	if err != nil {
		t.Fatalf("failed to list projects: %v", err)
	}
	if len(projects) != 2 {
		t.Errorf("expected 2 projects, got %d", len(projects))
	}

	// Test Delete
	databases, err := store.DeleteProject("testproject")
	if err != nil {
		t.Fatalf("failed to delete project: %v", err)
	}
	if len(databases) != 0 {
		t.Errorf("expected 0 databases, got %d", len(databases))
	}

	// Verify deletion
	deleted, err := store.GetProject("testproject")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if deleted != nil {
		t.Error("project should be deleted")
	}
}

func TestDuplicateProject(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	_, err := store.CreateProject("testproject")
	if err != nil {
		t.Fatalf("failed to create project: %v", err)
	}

	// Try to create duplicate
	_, err = store.CreateProject("testproject")
	if err == nil {
		t.Error("expected error for duplicate project name")
	}
}

func TestDatabaseCRUD(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	// Create project first
	project, err := store.CreateProject("testproject")
	if err != nil {
		t.Fatalf("failed to create project: %v", err)
	}

	// Test Create Database
	db, err := store.CreateDatabase(project.ID, "testproject_prod", "testproject_prod_user", "password123", "prod", nil, nil)
	if err != nil {
		t.Fatalf("failed to create database: %v", err)
	}
	if db.Name != "testproject_prod" {
		t.Errorf("database name = %q, want %q", db.Name, "testproject_prod")
	}

	// Test Get Database
	retrieved, err := store.GetDatabase(project.ID, "prod", nil)
	if err != nil {
		t.Fatalf("failed to get database: %v", err)
	}
	if retrieved == nil {
		t.Fatal("expected database, got nil")
	}
	if retrieved.Password != "password123" {
		t.Errorf("password = %q, want %q", retrieved.Password, "password123")
	}

	// Test Get by Name
	byName, err := store.GetDatabaseByName("testproject_prod")
	if err != nil {
		t.Fatalf("failed to get database by name: %v", err)
	}
	if byName == nil {
		t.Fatal("expected database, got nil")
	}

	// Test List Databases
	databases, err := store.ListDatabases(project.ID)
	if err != nil {
		t.Fatalf("failed to list databases: %v", err)
	}
	if len(databases) != 1 {
		t.Errorf("expected 1 database, got %d", len(databases))
	}

	// Test Delete Database
	err = store.DeleteDatabase("testproject_prod")
	if err != nil {
		t.Fatalf("failed to delete database: %v", err)
	}

	// Verify deletion
	deleted, err := store.GetDatabaseByName("testproject_prod")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if deleted != nil {
		t.Error("database should be deleted")
	}
}

func TestPRDatabase(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	project, err := store.CreateProject("testproject")
	if err != nil {
		t.Fatalf("failed to create project: %v", err)
	}

	prNumber := 123
	expiresAt := time.Now().Add(7 * 24 * time.Hour)

	db, err := store.CreateDatabase(project.ID, "testproject_pr_123", "testproject_pr_123_user", "password", "pr", &prNumber, &expiresAt)
	if err != nil {
		t.Fatalf("failed to create PR database: %v", err)
	}

	if db.PRNumber == nil || *db.PRNumber != 123 {
		t.Errorf("PR number = %v, want 123", db.PRNumber)
	}
	if db.ExpiresAt == nil {
		t.Error("ExpiresAt should not be nil for PR database")
	}

	// Test Get with PR number
	retrieved, err := store.GetDatabase(project.ID, "pr", &prNumber)
	if err != nil {
		t.Fatalf("failed to get PR database: %v", err)
	}
	if retrieved == nil {
		t.Fatal("expected database, got nil")
	}
}

func TestExpiredDatabases(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	project, err := store.CreateProject("testproject")
	if err != nil {
		t.Fatalf("failed to create project: %v", err)
	}

	// Create expired database
	prNumber := 1
	expiredTime := time.Now().Add(-1 * time.Hour)
	_, err = store.CreateDatabase(project.ID, "testproject_pr_1", "testproject_pr_1_user", "password", "pr", &prNumber, &expiredTime)
	if err != nil {
		t.Fatalf("failed to create expired database: %v", err)
	}

	// Create non-expired database
	prNumber2 := 2
	futureTime := time.Now().Add(7 * 24 * time.Hour)
	_, err = store.CreateDatabase(project.ID, "testproject_pr_2", "testproject_pr_2_user", "password", "pr", &prNumber2, &futureTime)
	if err != nil {
		t.Fatalf("failed to create future database: %v", err)
	}

	expired, err := store.GetExpiredDatabases()
	if err != nil {
		t.Fatalf("failed to get expired databases: %v", err)
	}

	if len(expired) != 1 {
		t.Errorf("expected 1 expired database, got %d", len(expired))
	}
	if len(expired) > 0 && expired[0].Name != "testproject_pr_1" {
		t.Errorf("expected testproject_pr_1, got %s", expired[0].Name)
	}
}

func TestGetDatabasesOlderThan(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	project, err := store.CreateProject("testproject")
	if err != nil {
		t.Fatalf("failed to create project: %v", err)
	}

	// Create a PR database (it will have current timestamp)
	prNumber := 1
	_, err = store.CreateDatabase(project.ID, "testproject_pr_1", "testproject_pr_1_user", "password", "pr", &prNumber, nil)
	if err != nil {
		t.Fatalf("failed to create PR database: %v", err)
	}

	// Get databases older than 1 hour - should be empty since we just created it
	oldDBs, err := store.GetDatabasesOlderThan("pr", 1*time.Hour)
	if err != nil {
		t.Fatalf("failed to get old databases: %v", err)
	}
	if len(oldDBs) != 0 {
		t.Errorf("expected 0 old databases, got %d", len(oldDBs))
	}
}

func TestCascadeDelete(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	project, err := store.CreateProject("testproject")
	if err != nil {
		t.Fatalf("failed to create project: %v", err)
	}

	// Create databases
	_, err = store.CreateDatabase(project.ID, "testproject_prod", "testproject_prod_user", "password", "prod", nil, nil)
	if err != nil {
		t.Fatalf("failed to create prod database: %v", err)
	}
	_, err = store.CreateDatabase(project.ID, "testproject_dev", "testproject_dev_user", "password", "dev", nil, nil)
	if err != nil {
		t.Fatalf("failed to create dev database: %v", err)
	}

	// Delete project
	databases, err := store.DeleteProject("testproject")
	if err != nil {
		t.Fatalf("failed to delete project: %v", err)
	}

	if len(databases) != 2 {
		t.Errorf("expected 2 databases returned, got %d", len(databases))
	}

	// Verify databases are deleted
	allDBs, err := store.ListAllDatabases()
	if err != nil {
		t.Fatalf("failed to list all databases: %v", err)
	}
	if len(allDBs) != 0 {
		t.Errorf("expected 0 databases after cascade delete, got %d", len(allDBs))
	}
}
