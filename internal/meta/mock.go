package meta

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// MockStore is an in-memory implementation of Store for testing
type MockStore struct {
	mu        sync.RWMutex
	projects  map[int64]*Project
	databases map[int64]*Database
	nextPID   int64
	nextDBID  int64
}

// NewMockStore creates a new mock store for testing
func NewMockStore() *MockStore {
	return &MockStore{
		projects:  make(map[int64]*Project),
		databases: make(map[int64]*Database),
		nextPID:   1,
		nextDBID:  1,
	}
}

func (s *MockStore) Close() error {
	return nil
}

func (s *MockStore) CreateProject(ctx context.Context, name string) (*Project, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Check for duplicate
	for _, p := range s.projects {
		if p.Name == name {
			return nil, fmt.Errorf("project already exists: %s", name)
		}
	}

	p := &Project{
		ID:        s.nextPID,
		Name:      name,
		CreatedAt: time.Now(),
	}
	s.projects[p.ID] = p
	s.nextPID++
	return p, nil
}

func (s *MockStore) GetProject(ctx context.Context, name string) (*Project, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, p := range s.projects {
		if p.Name == name {
			return p, nil
		}
	}
	return nil, nil
}

func (s *MockStore) ListProjects(ctx context.Context) ([]Project, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]Project, 0, len(s.projects))
	for _, p := range s.projects {
		result = append(result, *p)
	}
	return result, nil
}

func (s *MockStore) DeleteProject(ctx context.Context, name string) ([]Database, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var projectID int64
	for id, p := range s.projects {
		if p.Name == name {
			projectID = id
			delete(s.projects, id)
			break
		}
	}
	if projectID == 0 {
		return nil, fmt.Errorf("project not found: %s", name)
	}

	// Collect and delete associated databases
	var deleted []Database
	for id, db := range s.databases {
		if db.ProjectID == projectID {
			deleted = append(deleted, *db)
			delete(s.databases, id)
		}
	}
	return deleted, nil
}

func (s *MockStore) CreateDatabase(ctx context.Context, projectID int64, name, userName, password, env string, prNumber *int, expiresAt *time.Time) (*Database, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	db := &Database{
		ID:        s.nextDBID,
		ProjectID: projectID,
		Name:      name,
		UserName:  userName,
		Password:  password,
		Env:       env,
		PRNumber:  prNumber,
		CreatedAt: time.Now(),
		ExpiresAt: expiresAt,
	}
	s.databases[db.ID] = db
	s.nextDBID++
	return db, nil
}

func (s *MockStore) GetDatabase(ctx context.Context, projectID int64, env string, prNumber *int) (*Database, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, db := range s.databases {
		if db.ProjectID == projectID && db.Env == env {
			if prNumber == nil && db.PRNumber == nil {
				return db, nil
			}
			if prNumber != nil && db.PRNumber != nil && *prNumber == *db.PRNumber {
				return db, nil
			}
		}
	}
	return nil, nil
}

func (s *MockStore) GetDatabaseByName(ctx context.Context, name string) (*Database, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, db := range s.databases {
		if db.Name == name {
			return db, nil
		}
	}
	return nil, nil
}

func (s *MockStore) ListDatabases(ctx context.Context, projectID int64) ([]Database, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var result []Database
	for _, db := range s.databases {
		if db.ProjectID == projectID {
			result = append(result, *db)
		}
	}
	return result, nil
}

func (s *MockStore) ListAllDatabases(ctx context.Context) ([]Database, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]Database, 0, len(s.databases))
	for _, db := range s.databases {
		result = append(result, *db)
	}
	return result, nil
}

func (s *MockStore) DeleteDatabase(ctx context.Context, name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for id, db := range s.databases {
		if db.Name == name {
			delete(s.databases, id)
			return nil
		}
	}
	return fmt.Errorf("database not found: %s", name)
}

func (s *MockStore) GetExpiredDatabases(ctx context.Context) ([]Database, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var result []Database
	now := time.Now()
	for _, db := range s.databases {
		if db.ExpiresAt != nil && db.ExpiresAt.Before(now) {
			result = append(result, *db)
		}
	}
	return result, nil
}

func (s *MockStore) GetDatabasesOlderThan(ctx context.Context, env string, olderThan time.Duration) ([]Database, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	cutoff := time.Now().Add(-olderThan)
	var result []Database
	for _, db := range s.databases {
		if db.Env == env && db.CreatedAt.Before(cutoff) {
			result = append(result, *db)
		}
	}
	return result, nil
}
