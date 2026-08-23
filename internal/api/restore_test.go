package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// Restore reaches a real Postgres, a real bucket and pg_restore, so these
// stop at the layer that runs before any of that: scope, auth, request-shape
// validation and the backups-disabled config gate — the same boundary
// rotate_test.go and backup_test.go stop at.

func TestRestoreRequiresScope(t *testing.T) {
	fx := setupTestServer(t)
	defer fx.cleanup()

	// Scoped to a different project than the route below targets.
	token := seedScopedToken(t, fx.store, "project:myapp")

	req := httptest.NewRequest("POST", "/api/projects/other/databases/dev/backups/1/restore", nil)
	w := httptest.NewRecorder()
	fx.server.Router().ServeHTTP(w, authed(req, token))

	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want %d (body %s)", w.Code, http.StatusForbidden, w.Body.String())
	}
}

// A PR-scoped CI token must not be able to restore into a prod snapshot.
func TestRestoreRespectsEnvScope(t *testing.T) {
	fx := setupTestServer(t)
	defer fx.cleanup()

	token := seedScopedToken(t, fx.store, "project:myapp:pr:*")

	req := httptest.NewRequest("POST", "/api/projects/myapp/databases/prod/backups/1/restore", nil)
	w := httptest.NewRecorder()
	fx.server.Router().ServeHTTP(w, authed(req, token))

	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want %d", w.Code, http.StatusForbidden)
	}
}

func TestRestoreRequiresAuth(t *testing.T) {
	fx := setupTestServer(t)
	defer fx.cleanup()

	req := httptest.NewRequest("POST", "/api/projects/myapp/databases/dev/backups/1/restore", nil)
	w := httptest.NewRecorder()
	fx.server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", w.Code, http.StatusUnauthorized)
	}
}

// Restoring targets the source database's own env — a snapshot of a PR
// database can't exist in the first place, since backups are never taken of
// one, but the route still rejects env=pr up front like every other backup
// route does.
func TestRestoreRejectsPREnv(t *testing.T) {
	fx := setupTestServer(t)
	defer fx.cleanup()

	req := httptest.NewRequest("POST", "/api/projects/myapp/databases/pr_1/backups/1/restore", nil)
	w := httptest.NewRecorder()
	fx.server.Router().ServeHTTP(w, authed(req, fx.adminToken))

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d (body %s)", w.Code, http.StatusBadRequest, w.Body.String())
	}
}

// A malformed pr_ segment is rejected before anything tries to resolve a
// database, the same as on the other database routes.
func TestRestoreRejectsBadEnvSegment(t *testing.T) {
	fx := setupTestServer(t)
	defer fx.cleanup()

	req := httptest.NewRequest("POST", "/api/projects/myapp/databases/pr_abc/backups/1/restore", nil)
	w := httptest.NewRecorder()
	fx.server.Router().ServeHTTP(w, authed(req, fx.adminToken))

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestRestoreRejectsNonNumericBackupID(t *testing.T) {
	fx := setupTestServer(t)
	defer fx.cleanup()

	req := httptest.NewRequest("POST", "/api/projects/myapp/databases/dev/backups/abc/restore", nil)
	w := httptest.NewRecorder()
	fx.server.Router().ServeHTTP(w, authed(req, fx.adminToken))

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

// setupTestServer never calls EnableBackups, so restore must answer 503
// until an operator configures a bucket — the same safe default as the
// other four backup routes.
func TestRestoreDisabledByDefault(t *testing.T) {
	fx := setupTestServer(t)
	defer fx.cleanup()

	req := httptest.NewRequest("POST", "/api/projects/myapp/databases/dev/backups/1/restore", nil)
	w := httptest.NewRecorder()
	fx.server.Router().ServeHTTP(w, authed(req, fx.adminToken))

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d (body %s)", w.Code, http.StatusServiceUnavailable, w.Body.String())
	}
}

// TestScopeEnvGovernsRestoredDatabaseBySourceEnv is the security-critical
// test the plan calls out by name. A restored database holds the data of
// the environment it was restored from — "prod_restore_<ts>" holds
// production data — so a token scoped only to "dev" must never be able to
// reach it, and a token scoped to "prod" must be able to (scope-wise; the
// request still 404s past that, since no such database exists in this
// test's store). If scopeEnv ever stopped mapping the restore segment back
// to its source env, a dev-scoped token would silently gain access to a
// restored production database.
func TestScopeEnvGovernsRestoredDatabaseBySourceEnv(t *testing.T) {
	fx := setupTestServer(t)
	defer fx.cleanup()

	const restoredProdPath = "/api/projects/myapp/databases/prod_restore_20260823T101500"

	t.Run("dev-scoped token is denied a restored prod database", func(t *testing.T) {
		token := seedScopedToken(t, fx.store, "project:myapp:env:dev")

		req := httptest.NewRequest("GET", restoredProdPath, nil)
		w := httptest.NewRecorder()
		fx.server.Router().ServeHTTP(w, authed(req, token))

		if w.Code != http.StatusForbidden {
			t.Errorf("status = %d, want %d (body %s) — a dev-scoped token must not reach a restored prod database", w.Code, http.StatusForbidden, w.Body.String())
		}
	})

	t.Run("prod-scoped token is authorized for a restored prod database", func(t *testing.T) {
		token := seedScopedToken(t, fx.store, "project:myapp:env:prod")

		req := httptest.NewRequest("GET", restoredProdPath, nil)
		w := httptest.NewRecorder()
		fx.server.Router().ServeHTTP(w, authed(req, token))

		// Scope must pass (never 403); the database genuinely doesn't exist
		// in this test's store, so the manager reports 404 past the scope
		// check — that 404, not a 403, is what proves scopeEnv resolved the
		// segment to "prod" correctly.
		if w.Code == http.StatusForbidden {
			t.Fatalf("status = %d, want != %d — a prod-scoped token must be authorized for a restored prod database (body %s)", w.Code, http.StatusForbidden, w.Body.String())
		}
		if w.Code != http.StatusNotFound {
			t.Errorf("status = %d, want %d (body %s)", w.Code, http.StatusNotFound, w.Body.String())
		}
	})

	t.Run("dev-scoped token still reaches a restored dev database", func(t *testing.T) {
		token := seedScopedToken(t, fx.store, "project:myapp:env:dev")

		req := httptest.NewRequest("GET", "/api/projects/myapp/databases/dev_restore_20260823T101500", nil)
		w := httptest.NewRecorder()
		fx.server.Router().ServeHTTP(w, authed(req, token))

		if w.Code == http.StatusForbidden {
			t.Fatalf("status = %d, want != %d — a dev-scoped token must be authorized for a restored dev database (body %s)", w.Code, http.StatusForbidden, w.Body.String())
		}
	})
}

// The same scopeEnv mapping guards the data explorer and every other
// database route that shares databaseTarget, not just getDatabase — prove it
// once more on a route from that family so a future change to only one call
// site doesn't go unnoticed.
func TestScopeEnvGovernsExplorerOnRestoredDatabase(t *testing.T) {
	fx := setupTestServer(t)
	defer fx.cleanup()

	token := seedScopedToken(t, fx.store, "project:myapp:env:dev")

	req := httptest.NewRequest("GET", "/api/projects/myapp/databases/prod_restore_20260823T101500/tables", nil)
	w := httptest.NewRecorder()
	fx.server.Router().ServeHTTP(w, authed(req, token))

	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want %d (body %s) — a dev-scoped token must not browse a restored prod database's tables", w.Code, http.StatusForbidden, w.Body.String())
	}
}
