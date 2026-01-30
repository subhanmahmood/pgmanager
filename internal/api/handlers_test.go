package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"pgmanager/internal/config"
	"pgmanager/internal/meta"
	"pgmanager/internal/project"
)

func setupTestServer(t *testing.T) (*Server, func()) {
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
			Port:  8080,
			Token: "",
		},
	}

	store := meta.NewMockStore()
	mgr := project.NewManager(cfg, store)
	server := NewServer(cfg, mgr, cfg.API.Port)

	cleanup := func() {
		store.Close()
	}

	return server, cleanup
}

func TestHealthEndpoint(t *testing.T) {
	server, cleanup := setupTestServer(t)
	defer cleanup()

	req := httptest.NewRequest("GET", "/api/health", nil)
	w := httptest.NewRecorder()

	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("health check status = %d, want %d", w.Code, http.StatusOK)
	}

	var resp HealthResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.Status != "ok" {
		t.Errorf("health status = %q, want %q", resp.Status, "ok")
	}
}

func TestProjectEndpoints(t *testing.T) {
	server, cleanup := setupTestServer(t)
	defer cleanup()

	// Test Create Project
	t.Run("create project", func(t *testing.T) {
		body := bytes.NewBufferString(`{"name": "testapp"}`)
		req := httptest.NewRequest("POST", "/api/projects", body)
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		server.Router().ServeHTTP(w, req)

		if w.Code != http.StatusCreated {
			t.Errorf("create project status = %d, want %d, body: %s", w.Code, http.StatusCreated, w.Body.String())
		}

		var resp ProjectResponse
		if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
			t.Fatalf("failed to decode response: %v", err)
		}
		if resp.Name != "testapp" {
			t.Errorf("project name = %q, want %q", resp.Name, "testapp")
		}
	})

	// Test List Projects
	t.Run("list projects", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/projects", nil)
		w := httptest.NewRecorder()

		server.Router().ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("list projects status = %d, want %d", w.Code, http.StatusOK)
		}

		var resp []ProjectResponse
		if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
			t.Fatalf("failed to decode response: %v", err)
		}
		if len(resp) != 1 {
			t.Errorf("project count = %d, want 1", len(resp))
		}
	})

	// Test Create Duplicate Project
	t.Run("create duplicate project", func(t *testing.T) {
		body := bytes.NewBufferString(`{"name": "testapp"}`)
		req := httptest.NewRequest("POST", "/api/projects", body)
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		server.Router().ServeHTTP(w, req)

		if w.Code != http.StatusBadRequest {
			t.Errorf("duplicate project status = %d, want %d", w.Code, http.StatusBadRequest)
		}
	})

	// Test Delete Project
	t.Run("delete project", func(t *testing.T) {
		req := httptest.NewRequest("DELETE", "/api/projects/testapp", nil)
		w := httptest.NewRecorder()

		server.Router().ServeHTTP(w, req)

		if w.Code != http.StatusNoContent {
			t.Errorf("delete project status = %d, want %d", w.Code, http.StatusNoContent)
		}
	})

	// Test Delete Non-existent Project
	t.Run("delete non-existent project", func(t *testing.T) {
		req := httptest.NewRequest("DELETE", "/api/projects/nonexistent", nil)
		w := httptest.NewRecorder()

		server.Router().ServeHTTP(w, req)

		if w.Code != http.StatusNotFound {
			t.Errorf("delete non-existent project status = %d, want %d", w.Code, http.StatusNotFound)
		}
	})
}

func TestInvalidProjectName(t *testing.T) {
	server, cleanup := setupTestServer(t)
	defer cleanup()

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

			server.Router().ServeHTTP(w, req)

			if w.Code != http.StatusBadRequest {
				t.Errorf("invalid project status = %d, want %d", w.Code, http.StatusBadRequest)
			}
		})
	}
}

func TestAuthMiddleware(t *testing.T) {
	cfg := &config.Config{
		Postgres: config.PostgresConfig{
			Host:     "localhost",
			Port:     5432,
			User:     "postgres",
			Password: "test",
			Database: "postgres",
		},
		API: config.APIConfig{
			Port:  8080,
			Token: "secret-token",
		},
	}

	store := meta.NewMockStore()
	defer store.Close()

	mgr := project.NewManager(cfg, store)
	server := NewServer(cfg, mgr, cfg.API.Port)

	// Test without token
	t.Run("no token", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/projects", nil)
		w := httptest.NewRecorder()

		server.Router().ServeHTTP(w, req)

		if w.Code != http.StatusUnauthorized {
			t.Errorf("status = %d, want %d", w.Code, http.StatusUnauthorized)
		}
	})

	// Test with wrong token
	t.Run("wrong token", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/projects", nil)
		req.Header.Set("Authorization", "Bearer wrong-token")
		w := httptest.NewRecorder()

		server.Router().ServeHTTP(w, req)

		if w.Code != http.StatusUnauthorized {
			t.Errorf("status = %d, want %d", w.Code, http.StatusUnauthorized)
		}
	})

	// Test with correct token
	t.Run("correct token", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/projects", nil)
		req.Header.Set("Authorization", "Bearer secret-token")
		w := httptest.NewRecorder()

		server.Router().ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
		}
	})

	// Test health endpoint without token (should work)
	t.Run("health without token", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/health", nil)
		w := httptest.NewRecorder()

		server.Router().ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("health status = %d, want %d", w.Code, http.StatusOK)
		}
	})
}

func TestParseDuration(t *testing.T) {
	tests := []struct {
		input    string
		expected int64 // in seconds
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
