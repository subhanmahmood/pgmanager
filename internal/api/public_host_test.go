package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"pgmanager/internal/config"
	"pgmanager/internal/meta"
	"pgmanager/internal/project"
)

const testToken = "test-token"

// makeServer builds a Server whose mock store is pre-seeded with a single
// project and database. The Postgres "host" in cfg is the internal compose
// service name "postgres" — exactly the scenario from issue #12. Tests then
// flip cfg.Postgres.PublicHost / .PublicPort to assert the resolution order.
//
// authMiddleware always runs; tests use the legacy-token fallback by setting
// cfg.API.Token = testToken and passing "Bearer test-token" on requests.
func makeServer(t *testing.T, cfg *config.Config) (*Server, *meta.MockStore) {
	t.Helper()
	store := meta.NewMockStore()
	ctx := context.Background()

	p, err := store.CreateProject(ctx, "content")
	if err != nil {
		t.Fatalf("seed project: %v", err)
	}
	if _, err := store.CreateDatabase(ctx, p.ID, "content_dev", "content_dev_user", "secret", "dev", nil, nil); err != nil {
		t.Fatalf("seed database: %v", err)
	}

	cfg.API.Token = testToken
	cfg.API.RequireToken = true
	mgr := project.NewManager(cfg, store)
	srv := NewServer(cfg, mgr, store, cfg.API.BindAddress())
	return srv, store
}

func TestPublicHostPort_Resolution(t *testing.T) {
	tests := []struct {
		name       string
		cfg        config.PostgresConfig
		reqHost    string // value of r.Host
		wantHost   string
		wantPort   int
		wantConnIn string // substring expected in connection_string
	}{
		{
			name:       "PublicHost overrides everything",
			cfg:        config.PostgresConfig{Host: "postgres", Port: 5432, PublicHost: "pgm.example.com", PublicPort: 6543, SSLMode: "disable"},
			reqHost:    "internal-api:8080",
			wantHost:   "pgm.example.com",
			wantPort:   6543,
			wantConnIn: "@pgm.example.com:6543/",
		},
		{
			name:       "Falls back to inbound Host header when PublicHost unset",
			cfg:        config.PostgresConfig{Host: "postgres", Port: 5432, SSLMode: "disable"},
			reqHost:    "pgm.example.com:443",
			wantHost:   "pgm.example.com",
			wantPort:   5432,
			wantConnIn: "@pgm.example.com:5432/",
		},
		{
			name:       "Host header without port is used as-is",
			cfg:        config.PostgresConfig{Host: "postgres", Port: 5432, SSLMode: "disable"},
			reqHost:    "pgm.example.com",
			wantHost:   "pgm.example.com",
			wantPort:   5432,
			wantConnIn: "@pgm.example.com:5432/",
		},
		{
			name:       "Last-resort fallback to cfg.Postgres.Host when no PublicHost and no r.Host",
			cfg:        config.PostgresConfig{Host: "postgres", Port: 5432, SSLMode: "disable"},
			reqHost:    "",
			wantHost:   "postgres",
			wantPort:   5432,
			wantConnIn: "@postgres:5432/",
		},
		{
			name:       "PublicPort alone overrides port; host still derived from request",
			cfg:        config.PostgresConfig{Host: "postgres", Port: 5432, PublicPort: 6543, SSLMode: "disable"},
			reqHost:    "pgm.example.com",
			wantHost:   "pgm.example.com",
			wantPort:   6543,
			wantConnIn: "@pgm.example.com:6543/",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &config.Config{
				Postgres: tt.cfg,
				API:      config.APIConfig{Listen: "127.0.0.1:0"},
			}
			srv, _ := makeServer(t, cfg)

			req := httptest.NewRequest("GET", "/api/projects/content/databases/dev/credentials", nil)
			req.Header.Set("Authorization", "Bearer "+testToken)
			// httptest.NewRequest sets r.Host from the URL; override to
			// simulate the operator-facing endpoint the client actually
			// reached.
			req.Host = tt.reqHost
			w := httptest.NewRecorder()
			srv.Router().ServeHTTP(w, req)

			if w.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200, body=%s", w.Code, w.Body.String())
			}
			var resp DatabaseResponse
			if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if resp.Host != tt.wantHost {
				t.Errorf("host = %q, want %q", resp.Host, tt.wantHost)
			}
			if resp.Port != tt.wantPort {
				t.Errorf("port = %d, want %d", resp.Port, tt.wantPort)
			}
			if !strings.Contains(resp.ConnString, tt.wantConnIn) {
				t.Errorf("conn_string = %q, want substring %q", resp.ConnString, tt.wantConnIn)
			}
		})
	}
}

// TestPublicHostPort_ListDatabases verifies the same rewrite applies to the
// list endpoint, not just per-database lookups.
func TestPublicHostPort_ListDatabases(t *testing.T) {
	cfg := &config.Config{
		Postgres: config.PostgresConfig{Host: "postgres", Port: 5432, SSLMode: "disable"},
		API:      config.APIConfig{Listen: "127.0.0.1:0"},
	}
	srv, _ := makeServer(t, cfg)

	req := httptest.NewRequest("GET", "/api/projects/content/databases", nil)
	req.Header.Set("Authorization", "Bearer "+testToken)
	req.Host = "pgm.example.com:443"
	w := httptest.NewRecorder()
	srv.Router().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", w.Code, w.Body.String())
	}
	var resp []DatabaseInfoResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp) != 1 {
		t.Fatalf("got %d databases, want 1", len(resp))
	}
	if resp[0].Host != "pgm.example.com" {
		t.Errorf("host = %q, want %q", resp[0].Host, "pgm.example.com")
	}
}

// TestPublicHostPort_NoRequest covers the path with no inbound HTTP request
// to inspect: Manager itself must prefer PublicHost when set, so callers that
// never see a request (cleanup, startup work) still advertise the right
// endpoint.
func TestPublicHostPort_NoRequest(t *testing.T) {
	cfg := &config.Config{
		Postgres: config.PostgresConfig{
			Host: "postgres", Port: 5432,
			PublicHost: "pgm.example.com", PublicPort: 6543,
			SSLMode: "disable",
		},
	}
	store := meta.NewMockStore()
	ctx := context.Background()
	p, err := store.CreateProject(ctx, "content")
	if err != nil {
		t.Fatalf("seed project: %v", err)
	}
	if _, err := store.CreateDatabase(ctx, p.ID, "content_dev", "content_dev_user", "secret", "dev", nil, nil); err != nil {
		t.Fatalf("seed database: %v", err)
	}

	mgr := project.NewManager(cfg, store)
	info, err := mgr.GetDatabase(ctx, "content", "dev", nil)
	if err != nil {
		t.Fatalf("GetDatabase: %v", err)
	}
	if info.Host != "pgm.example.com" {
		t.Errorf("no-request host = %q, want pgm.example.com", info.Host)
	}
	if info.Port != 6543 {
		t.Errorf("no-request port = %d, want 6543", info.Port)
	}
	if !strings.Contains(info.ConnString, "@pgm.example.com:6543/") {
		t.Errorf("no-request conn_string = %q, want @pgm.example.com:6543/", info.ConnString)
	}
}
