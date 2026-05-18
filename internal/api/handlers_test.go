package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"pgmanager/internal/auth"
	"pgmanager/internal/config"
	"pgmanager/internal/meta"
	"pgmanager/internal/project"
)

type testFixture struct {
	server     *Server
	store      *meta.MockStore
	adminToken string
	cleanup    func()
}

func setupTestServer(t *testing.T) *testFixture {
	t.Helper()

	cfg := &config.Config{
		Postgres: config.PostgresConfig{
			Host:     "localhost",
			Port:     5432,
			User:     "postgres",
			Password: "test",
			Database: "postgres",
		},
		API: config.APIConfig{
			Listen:       "127.0.0.1:0",
			RequireToken: true,
		},
	}

	store := meta.NewMockStore()
	mgr := project.NewManager(cfg, store)
	server := NewServer(cfg, mgr, store, cfg.API.BindAddress())

	// Seed an admin token so authenticated handlers can be exercised.
	plain, hash, prefix, err := auth.GenerateToken()
	if err != nil {
		t.Fatalf("generate token: %v", err)
	}
	if err := store.CreateToken(context.Background(), &meta.Token{
		Name:        "test-admin",
		TokenHash:   hash,
		TokenPrefix: prefix,
		Scopes:      []string{auth.ScopeAdmin},
	}); err != nil {
		t.Fatalf("create token: %v", err)
	}

	return &testFixture{
		server:     server,
		store:      store,
		adminToken: plain,
		cleanup:    func() { _ = store.Close() },
	}
}

func authed(req *http.Request, token string) *http.Request {
	req.Header.Set("Authorization", "Bearer "+token)
	return req
}

func TestHealthEndpoint(t *testing.T) {
	fx := setupTestServer(t)
	defer fx.cleanup()

	req := httptest.NewRequest("GET", "/api/health", nil)
	w := httptest.NewRecorder()
	fx.server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("health check status = %d, want %d", w.Code, http.StatusOK)
	}
	var resp HealthResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Status != "ok" {
		t.Errorf("status = %q, want ok", resp.Status)
	}
}

func TestProjectEndpoints(t *testing.T) {
	fx := setupTestServer(t)
	defer fx.cleanup()

	t.Run("create project", func(t *testing.T) {
		body := bytes.NewBufferString(`{"name": "testapp"}`)
		req := httptest.NewRequest("POST", "/api/projects", body)
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		fx.server.Router().ServeHTTP(w, authed(req, fx.adminToken))

		if w.Code != http.StatusCreated {
			t.Errorf("status = %d, want %d, body: %s", w.Code, http.StatusCreated, w.Body.String())
		}
		var resp ProjectResponse
		if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if resp.Name != "testapp" {
			t.Errorf("name = %q, want testapp", resp.Name)
		}
	})

	t.Run("list projects", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/projects", nil)
		w := httptest.NewRecorder()
		fx.server.Router().ServeHTTP(w, authed(req, fx.adminToken))

		if w.Code != http.StatusOK {
			t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
		}
		var resp []ProjectResponse
		if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if len(resp) != 1 {
			t.Errorf("count = %d, want 1", len(resp))
		}
	})

	t.Run("create duplicate project", func(t *testing.T) {
		body := bytes.NewBufferString(`{"name": "testapp"}`)
		req := httptest.NewRequest("POST", "/api/projects", body)
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		fx.server.Router().ServeHTTP(w, authed(req, fx.adminToken))

		if w.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
		}
	})

	t.Run("delete project", func(t *testing.T) {
		req := httptest.NewRequest("DELETE", "/api/projects/testapp", nil)
		w := httptest.NewRecorder()
		fx.server.Router().ServeHTTP(w, authed(req, fx.adminToken))

		if w.Code != http.StatusNoContent {
			t.Errorf("status = %d, want %d", w.Code, http.StatusNoContent)
		}
	})

	t.Run("delete non-existent project", func(t *testing.T) {
		req := httptest.NewRequest("DELETE", "/api/projects/nonexistent", nil)
		w := httptest.NewRecorder()
		fx.server.Router().ServeHTTP(w, authed(req, fx.adminToken))

		if w.Code != http.StatusNotFound {
			t.Errorf("status = %d, want %d", w.Code, http.StatusNotFound)
		}
	})
}

func TestInvalidProjectName(t *testing.T) {
	fx := setupTestServer(t)
	defer fx.cleanup()

	tests := []struct {
		name string
		body string
	}{
		{"empty name", `{"name": ""}`},
		{"short name", `{"name": "a"}`},
		{"reserved name", `{"name": "postgres"}`},
		{"invalid characters", `{"name": "my-app"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := bytes.NewBufferString(tt.body)
			req := httptest.NewRequest("POST", "/api/projects", body)
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			fx.server.Router().ServeHTTP(w, authed(req, fx.adminToken))

			if w.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
			}
		})
	}
}

func TestAuthMiddleware(t *testing.T) {
	fx := setupTestServer(t)
	defer fx.cleanup()

	t.Run("no token", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/projects", nil)
		w := httptest.NewRecorder()
		fx.server.Router().ServeHTTP(w, req)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("status = %d, want %d", w.Code, http.StatusUnauthorized)
		}
	})

	t.Run("wrong token", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/projects", nil)
		req.Header.Set("Authorization", "Bearer wrong-token")
		w := httptest.NewRecorder()
		fx.server.Router().ServeHTTP(w, req)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("status = %d, want %d", w.Code, http.StatusUnauthorized)
		}
	})

	t.Run("correct token", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/projects", nil)
		w := httptest.NewRecorder()
		fx.server.Router().ServeHTTP(w, authed(req, fx.adminToken))
		if w.Code != http.StatusOK {
			t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
		}
	})

	t.Run("health is anonymous", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/health", nil)
		w := httptest.NewRecorder()
		fx.server.Router().ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
		}
	})
}

func TestLegacyTokenFallback(t *testing.T) {
	cfg := &config.Config{
		API: config.APIConfig{Listen: "127.0.0.1:0", Token: "secret-token", RequireToken: true},
	}
	store := meta.NewMockStore()
	defer store.Close()
	mgr := project.NewManager(cfg, store)
	server := NewServer(cfg, mgr, store, cfg.API.BindAddress())

	req := httptest.NewRequest("GET", "/api/projects", nil)
	req.Header.Set("Authorization", "Bearer secret-token")
	w := httptest.NewRecorder()
	server.Router().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("legacy token: status = %d body=%s", w.Code, w.Body.String())
	}
}

func TestScopeEnforcement(t *testing.T) {
	fx := setupTestServer(t)
	defer fx.cleanup()

	// Seed: one project we own, one we don't.
	for _, name := range []string{"owned", "other"} {
		body := bytes.NewBufferString(`{"name": "` + name + `"}`)
		req := httptest.NewRequest("POST", "/api/projects", body)
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		fx.server.Router().ServeHTTP(w, authed(req, fx.adminToken))
		if w.Code != http.StatusCreated {
			t.Fatalf("seed %s: %d %s", name, w.Code, w.Body.String())
		}
	}

	// Create a project-scoped token.
	plain, hash, prefix, err := auth.GenerateToken()
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if err := fx.store.CreateToken(context.Background(), &meta.Token{
		Name:        "scoped",
		TokenHash:   hash,
		TokenPrefix: prefix,
		Scopes:      []string{"project:owned"},
	}); err != nil {
		t.Fatalf("create scoped token: %v", err)
	}

	t.Run("list filters to scope", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/projects", nil)
		w := httptest.NewRecorder()
		fx.server.Router().ServeHTTP(w, authed(req, plain))
		if w.Code != http.StatusOK {
			t.Fatalf("status %d", w.Code)
		}
		var resp []ProjectResponse
		if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if len(resp) != 1 || resp[0].Name != "owned" {
			t.Errorf("got %+v, want only [owned]", resp)
		}
	})

	t.Run("delete out-of-scope is forbidden", func(t *testing.T) {
		req := httptest.NewRequest("DELETE", "/api/projects/other", nil)
		w := httptest.NewRecorder()
		fx.server.Router().ServeHTTP(w, authed(req, plain))
		if w.Code != http.StatusForbidden {
			t.Errorf("status = %d, want 403, body=%s", w.Code, w.Body.String())
		}
	})
}

func TestParseDuration(t *testing.T) {
	tests := []struct {
		input    string
		expected int64
		wantErr  bool
	}{
		{"7d", 7 * 24 * 3600, false},
		{"24h", 24 * 3600, false},
		{"1w", 7 * 24 * 3600, false},
		{"30m", 30 * 60, false},
		{"60s", 60, false},
		{"", 0, false},
		{"x", 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			d, err := parseDuration(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("parseDuration(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
				return
			}
			if !tt.wantErr && int64(d.Seconds()) != tt.expected {
				t.Errorf("parseDuration(%q) = %v, want %v seconds", tt.input, d, tt.expected)
			}
		})
	}
}
