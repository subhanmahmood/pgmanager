package client

import (
	"context"
	"fmt"
	"time"

	"pgmanager/internal/auth"
	"pgmanager/internal/config"
	"pgmanager/internal/meta"
	"pgmanager/internal/project"
)

// LocalClient is a Client that talks directly to Postgres via the project
// manager. Used for local dev, admin operations on the VPS itself, and as
// the implementation behind every API handler on the server side.
type LocalClient struct {
	mgr   *project.Manager
	store meta.Store
}

// NewLocal builds a LocalClient that owns the given store and manager. Close
// closes the store.
func NewLocal(mgr *project.Manager, store meta.Store) *LocalClient {
	return &LocalClient{mgr: mgr, store: store}
}

// OpenLocal opens a fresh PostgresStore from the supplied config and wraps it
// in a LocalClient.
func OpenLocal(ctx context.Context, cfg *config.Config) (*LocalClient, error) {
	key, err := cfg.Crypto.EncryptionKey()
	if err != nil {
		return nil, fmt.Errorf("encryption key: %w", err)
	}
	store, err := meta.NewPostgresStore(ctx, cfg.Postgres.ConnectionString(), key)
	if err != nil {
		return nil, err
	}
	mgr := project.NewManager(cfg, store)
	return NewLocal(mgr, store), nil
}

func (c *LocalClient) Close() error { return c.store.Close() }

func (c *LocalClient) CreateProject(ctx context.Context, name string) error {
	return c.mgr.CreateProject(ctx, name)
}

func (c *LocalClient) ListProjects(ctx context.Context) ([]Project, error) {
	projects, err := c.mgr.ListProjects(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Project, len(projects))
	for i, p := range projects {
		out[i] = Project{Name: p.Name, CreatedAt: p.CreatedAt}
	}
	return out, nil
}

func (c *LocalClient) DeleteProject(ctx context.Context, name string) error {
	return c.mgr.DeleteProject(ctx, name)
}

func (c *LocalClient) CreateDatabase(ctx context.Context, projectName, env string, prNumber *int, extensions []string) (*Database, error) {
	info, err := c.mgr.CreateDatabase(ctx, projectName, env, prNumber, extensions)
	if err != nil {
		return nil, err
	}
	return convertDB(info), nil
}

func (c *LocalClient) GetDatabase(ctx context.Context, projectName, env string, prNumber *int) (*Database, error) {
	info, err := c.mgr.GetDatabase(ctx, projectName, env, prNumber)
	if err != nil {
		return nil, err
	}
	out := convertDB(info)
	// Mirror the API split: bare Get omits credentials.
	out.Password = ""
	out.ConnString = ""
	return out, nil
}

func (c *LocalClient) GetDatabaseCredentials(ctx context.Context, projectName, env string, prNumber *int) (*Database, error) {
	info, err := c.mgr.GetDatabase(ctx, projectName, env, prNumber)
	if err != nil {
		return nil, err
	}
	return convertDB(info), nil
}

func (c *LocalClient) ListDatabases(ctx context.Context, projectName string) ([]Database, error) {
	dbs, err := c.mgr.ListDatabases(ctx, projectName)
	if err != nil {
		return nil, err
	}
	out := make([]Database, len(dbs))
	for i, d := range dbs {
		conv := convertDB(&d)
		conv.Password = ""
		conv.ConnString = ""
		out[i] = *conv
	}
	return out, nil
}

func (c *LocalClient) DeleteDatabase(ctx context.Context, projectName, env string, prNumber *int) error {
	return c.mgr.DeleteDatabase(ctx, projectName, env, prNumber)
}

func (c *LocalClient) Cleanup(ctx context.Context, olderThan time.Duration) ([]string, error) {
	return c.mgr.Cleanup(ctx, olderThan)
}

func (c *LocalClient) Whoami(ctx context.Context) (*Whoami, error) {
	return &Whoami{TokenPrefix: "local", Scopes: []string{auth.ScopeAdmin}}, nil
}

func (c *LocalClient) ListTokens(ctx context.Context) ([]Token, error) {
	toks, err := c.store.ListTokens(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Token, len(toks))
	for i, t := range toks {
		out[i] = tokenView(t)
	}
	return out, nil
}

func (c *LocalClient) CreateToken(ctx context.Context, name string, scopes []string, expires string) (string, *Token, error) {
	if err := auth.ValidateScopes(scopes); err != nil {
		return "", nil, err
	}
	var expiresAt *time.Time
	if expires != "" {
		d, err := parseDuration(expires)
		if err != nil {
			return "", nil, fmt.Errorf("expires: %w", err)
		}
		t := time.Now().Add(d)
		expiresAt = &t
	}
	plain, hash, prefix, err := auth.GenerateToken()
	if err != nil {
		return "", nil, err
	}
	tok := &meta.Token{
		Name:        name,
		TokenHash:   hash,
		TokenPrefix: prefix,
		Scopes:      scopes,
		ExpiresAt:   expiresAt,
		CreatedBy:   "local",
	}
	if err := c.store.CreateToken(ctx, tok); err != nil {
		return "", nil, err
	}
	view := tokenView(*tok)
	return plain, &view, nil
}

func (c *LocalClient) RevokeToken(ctx context.Context, prefix string) error {
	return c.store.RevokeToken(ctx, prefix)
}

func convertDB(d *project.DatabaseInfo) *Database {
	return &Database{
		Project:      d.Project,
		Env:          d.Env,
		PRNumber:     d.PRNumber,
		DatabaseName: d.DatabaseName,
		UserName:     d.UserName,
		Password:     d.Password,
		Host:         d.Host,
		Port:         d.Port,
		ConnString:   d.ConnString,
		CreatedAt:    d.CreatedAt,
		ExpiresAt:    d.ExpiresAt,
	}
}

func tokenView(t meta.Token) Token {
	return Token{
		Name:        t.Name,
		TokenPrefix: t.TokenPrefix,
		Scopes:      t.Scopes,
		CreatedAt:   t.CreatedAt,
		ExpiresAt:   t.ExpiresAt,
		LastUsedAt:  t.LastUsedAt,
		CreatedBy:   t.CreatedBy,
		RevokedAt:   t.RevokedAt,
	}
}

// parseDuration parses a duration string like "7d", "24h", "90d".
func parseDuration(s string) (time.Duration, error) {
	if len(s) < 2 {
		return 0, fmt.Errorf("invalid duration: %s", s)
	}
	unit := s[len(s)-1]
	value := s[:len(s)-1]
	n, err := atoi(value)
	if err != nil {
		return 0, err
	}
	switch unit {
	case 's':
		return time.Duration(n) * time.Second, nil
	case 'm':
		return time.Duration(n) * time.Minute, nil
	case 'h':
		return time.Duration(n) * time.Hour, nil
	case 'd':
		return time.Duration(n) * 24 * time.Hour, nil
	case 'w':
		return time.Duration(n) * 7 * 24 * time.Hour, nil
	default:
		return 0, fmt.Errorf("unknown unit %q in %s", unit, s)
	}
}

func atoi(s string) (int, error) {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("not a number: %s", s)
		}
		n = n*10 + int(c-'0')
	}
	return n, nil
}
