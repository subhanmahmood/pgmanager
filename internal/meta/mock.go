package meta

import (
	"bytes"
	"context"
	"fmt"
	"sync"
	"time"
)

// MockStore is an in-memory implementation of Store for testing.
type MockStore struct {
	mu        sync.RWMutex
	projects  map[int64]*Project
	databases map[int64]*Database
	tokens    map[int64]*Token
	devices   map[int64]*DeviceRequest
	nextPID   int64
	nextDBID  int64
	nextTID   int64
	nextDevID int64
}

// NewMockStore creates a new mock store for testing.
func NewMockStore() *MockStore {
	return &MockStore{
		projects:  make(map[int64]*Project),
		databases: make(map[int64]*Database),
		tokens:    make(map[int64]*Token),
		devices:   make(map[int64]*DeviceRequest),
		nextPID:   1,
		nextDBID:  1,
		nextTID:   1,
		nextDevID: 1,
	}
}

func (s *MockStore) Close() error { return nil }

func (s *MockStore) CreateProject(ctx context.Context, name string) (*Project, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, p := range s.projects {
		if p.Name == name {
			return nil, fmt.Errorf("project already exists: %s", name)
		}
	}
	p := &Project{ID: s.nextPID, Name: name, CreatedAt: time.Now()}
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

// --- Token operations -------------------------------------------------------

func (s *MockStore) CreateToken(ctx context.Context, t *Token) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	t.ID = s.nextTID
	if t.CreatedAt.IsZero() {
		t.CreatedAt = time.Now()
	}
	stored := *t
	s.tokens[t.ID] = &stored
	s.nextTID++
	return nil
}

func (s *MockStore) GetTokenByHash(ctx context.Context, hash []byte) (*Token, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, t := range s.tokens {
		if bytes.Equal(t.TokenHash, hash) {
			out := *t
			return &out, nil
		}
	}
	return nil, nil
}

func (s *MockStore) GetTokenByPrefix(ctx context.Context, prefix string) (*Token, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var newest *Token
	for _, t := range s.tokens {
		if t.TokenPrefix == prefix && t.RevokedAt == nil {
			if newest == nil || t.ID > newest.ID {
				newest = t
			}
		}
	}
	if newest == nil {
		return nil, nil
	}
	out := *newest
	return &out, nil
}

func (s *MockStore) ListTokens(ctx context.Context) ([]Token, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Token, 0, len(s.tokens))
	for _, t := range s.tokens {
		out = append(out, *t)
	}
	return out, nil
}

func (s *MockStore) RevokeToken(ctx context.Context, prefix string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	for _, t := range s.tokens {
		if t.TokenPrefix == prefix && t.RevokedAt == nil {
			t.RevokedAt = &now
			return nil
		}
	}
	return fmt.Errorf("token not found or already revoked: %s", prefix)
}

func (s *MockStore) TouchToken(ctx context.Context, id int64, when time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if t, ok := s.tokens[id]; ok {
		t.LastUsedAt = &when
	}
	return nil
}

// --- Device authorization operations -----------------------------------------

func (s *MockStore) CreateDeviceRequest(ctx context.Context, d *DeviceRequest) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, existing := range s.devices {
		if existing.UserCode == d.UserCode {
			return fmt.Errorf("user code already in use: %s", d.UserCode)
		}
	}
	d.ID = s.nextDevID
	d.Status = DeviceStatusPending
	if d.CreatedAt.IsZero() {
		d.CreatedAt = time.Now()
	}
	stored := *d
	s.devices[d.ID] = &stored
	s.nextDevID++
	return nil
}

func (s *MockStore) GetDeviceRequestByCodeHash(ctx context.Context, hash []byte) (*DeviceRequest, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, d := range s.devices {
		if bytes.Equal(d.DeviceCodeHash, hash) {
			out := *d
			return &out, nil
		}
	}
	return nil, nil
}

func (s *MockStore) GetDeviceRequestByUserCode(ctx context.Context, userCode string) (*DeviceRequest, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, d := range s.devices {
		if d.UserCode == userCode {
			out := *d
			return &out, nil
		}
	}
	return nil, nil
}

func (s *MockStore) ListPendingDeviceRequests(ctx context.Context) ([]DeviceRequest, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	now := time.Now()
	out := make([]DeviceRequest, 0, len(s.devices))
	for _, d := range s.devices {
		if d.Status == DeviceStatusPending && !d.Expired(now) {
			copied := *d
			copied.IssuedToken = ""
			out = append(out, copied)
		}
	}
	return out, nil
}

func (s *MockStore) ApproveDeviceRequest(ctx context.Context, id, tokenID int64, plaintext, approvedBy string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.devices[id]
	if !ok || d.Status != DeviceStatusPending {
		return fmt.Errorf("device request %d is no longer pending", id)
	}
	now := time.Now()
	d.Status = DeviceStatusApproved
	d.TokenID = &tokenID
	d.IssuedToken = plaintext
	d.ApprovedBy = approvedBy
	d.ApprovedAt = &now
	return nil
}

func (s *MockStore) DenyDeviceRequest(ctx context.Context, id int64, deniedBy string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.devices[id]
	if !ok || d.Status != DeviceStatusPending {
		return fmt.Errorf("device request %d is no longer pending", id)
	}
	now := time.Now()
	d.Status = DeviceStatusDenied
	d.ApprovedBy = deniedBy
	d.ApprovedAt = &now
	return nil
}

func (s *MockStore) ConsumeDeviceToken(ctx context.Context, id int64) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.devices[id]
	if !ok || d.IssuedToken == "" {
		return "", nil
	}
	plain := d.IssuedToken
	d.IssuedToken = ""
	return plain, nil
}

func (s *MockStore) TouchDeviceRequest(ctx context.Context, id int64, when time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if d, ok := s.devices[id]; ok {
		d.LastPolledAt = &when
	}
	return nil
}

func (s *MockStore) DeleteExpiredDeviceRequests(ctx context.Context) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	n := 0
	for id, d := range s.devices {
		if d.Expired(now) {
			delete(s.devices, id)
			n++
		}
	}
	return n, nil
}

func (s *MockStore) HasActiveAdminToken(ctx context.Context) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	now := time.Now()
	for _, t := range s.tokens {
		if !t.Active(now) {
			continue
		}
		for _, sc := range t.Scopes {
			if sc == "admin" {
				return true, nil
			}
		}
	}
	return false, nil
}
