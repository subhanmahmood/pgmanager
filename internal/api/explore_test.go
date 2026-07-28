package api

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"pgmanager/internal/auth"
	"pgmanager/internal/meta"
)

// The explorer reaches a real Postgres, so these tests cover the part that
// runs before any connection is made: that every explorer route is scope
// checked exactly like the database routes it sits beside. A token that
// cannot read a project's databases must not be able to read their contents
// either.

func seedScopedToken(t *testing.T, store *meta.MockStore, scopes ...string) string {
	t.Helper()
	plain, hash, prefix, err := auth.GenerateToken()
	if err != nil {
		t.Fatalf("generate token: %v", err)
	}
	if err := store.CreateToken(context.Background(), &meta.Token{
		Name:        "scoped",
		TokenHash:   hash,
		TokenPrefix: prefix,
		Scopes:      scopes,
	}); err != nil {
		t.Fatalf("create token: %v", err)
	}
	return plain
}

func exploreRoutes() []struct {
	method string
	path   string
	body   string
} {
	return []struct {
		method string
		path   string
		body   string
	}{
		{"GET", "/api/projects/other/databases/dev/tables", ""},
		{"GET", "/api/projects/other/databases/dev/tables/users/rows", ""},
		{"POST", "/api/projects/other/databases/dev/tables/users/rows", `{"values":{"a":"1"}}`},
		{"PATCH", "/api/projects/other/databases/dev/tables/users/rows", `{"key":{"id":"1"},"values":{"a":"1"}}`},
		{"DELETE", "/api/projects/other/databases/dev/tables/users/rows", `{"key":{"id":"1"}}`},
	}
}

func TestExploreRequiresScope(t *testing.T) {
	fx := setupTestServer(t)
	defer fx.cleanup()

	// Scoped to a different project than the routes below target.
	token := seedScopedToken(t, fx.store, "project:myapp")

	for _, rt := range exploreRoutes() {
		t.Run(rt.method+" "+rt.path, func(t *testing.T) {
			req := httptest.NewRequest(rt.method, rt.path, bytes.NewBufferString(rt.body))
			w := httptest.NewRecorder()
			fx.server.Router().ServeHTTP(w, authed(req, token))

			if w.Code != http.StatusForbidden {
				t.Errorf("status = %d, want %d (body %s)", w.Code, http.StatusForbidden, w.Body.String())
			}
		})
	}
}

func TestExploreRequiresAuth(t *testing.T) {
	fx := setupTestServer(t)
	defer fx.cleanup()

	for _, rt := range exploreRoutes() {
		t.Run(rt.method+" "+rt.path, func(t *testing.T) {
			req := httptest.NewRequest(rt.method, rt.path, bytes.NewBufferString(rt.body))
			w := httptest.NewRecorder()
			fx.server.Router().ServeHTTP(w, req)

			if w.Code != http.StatusUnauthorized {
				t.Errorf("status = %d, want %d", w.Code, http.StatusUnauthorized)
			}
		})
	}
}

// A token scoped to one project's PR databases must be refused the prod
// database of the same project — the explorer inherits the env-level part of
// the scope grammar, not just the project-level part.
func TestExploreRespectsEnvScope(t *testing.T) {
	fx := setupTestServer(t)
	defer fx.cleanup()

	token := seedScopedToken(t, fx.store, "project:myapp:pr:*")

	req := httptest.NewRequest("GET", "/api/projects/myapp/databases/prod/tables", nil)
	w := httptest.NewRecorder()
	fx.server.Router().ServeHTTP(w, authed(req, token))

	if w.Code != http.StatusForbidden {
		t.Errorf("prod status = %d, want %d", w.Code, http.StatusForbidden)
	}
}

// Malformed pr_ segments are rejected before anything tries to resolve a
// database, the same as on the existing database routes.
func TestExploreRejectsBadEnvSegment(t *testing.T) {
	fx := setupTestServer(t)
	defer fx.cleanup()

	req := httptest.NewRequest("GET", "/api/projects/myapp/databases/pr_abc/tables", nil)
	w := httptest.NewRecorder()
	fx.server.Router().ServeHTTP(w, authed(req, fx.adminToken))

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

// The row routes require a key for update and delete: without one there is no
// bound on how many rows a statement would touch.
func TestExploreRowKeyRequired(t *testing.T) {
	fx := setupTestServer(t)
	defer fx.cleanup()

	for _, method := range []string{"PATCH", "DELETE"} {
		t.Run(method, func(t *testing.T) {
			req := httptest.NewRequest(method,
				"/api/projects/myapp/databases/dev/tables/users/rows",
				bytes.NewBufferString(`{"values":{"a":"1"}}`))
			w := httptest.NewRecorder()
			fx.server.Router().ServeHTTP(w, authed(req, fx.adminToken))

			if w.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want %d (body %s)", w.Code, http.StatusBadRequest, w.Body.String())
			}
		})
	}
}
