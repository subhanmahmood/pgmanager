package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// Rotation hands back a live password, so it must be gated by the same scope
// as reading one. These stop at the authorization layer — they never reach
// Postgres — which is what keeps them runnable without a live server.
func TestRotateRequiresScope(t *testing.T) {
	fx := setupTestServer(t)
	defer fx.cleanup()

	token := seedScopedToken(t, fx.store, "project:myapp")

	req := httptest.NewRequest("POST", "/api/projects/other/databases/dev/rotate", nil)
	w := httptest.NewRecorder()
	fx.server.Router().ServeHTTP(w, authed(req, token))

	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want %d (body %s)", w.Code, http.StatusForbidden, w.Body.String())
	}
}

// A PR-scoped CI token must not be able to rotate the prod password.
func TestRotateRespectsEnvScope(t *testing.T) {
	fx := setupTestServer(t)
	defer fx.cleanup()

	token := seedScopedToken(t, fx.store, "project:myapp:pr:*")

	req := httptest.NewRequest("POST", "/api/projects/myapp/databases/prod/rotate", nil)
	w := httptest.NewRecorder()
	fx.server.Router().ServeHTTP(w, authed(req, token))

	if w.Code != http.StatusForbidden {
		t.Errorf("prod status = %d, want %d", w.Code, http.StatusForbidden)
	}
}

func TestRotateRequiresAuth(t *testing.T) {
	fx := setupTestServer(t)
	defer fx.cleanup()

	req := httptest.NewRequest("POST", "/api/projects/myapp/databases/dev/rotate", nil)
	w := httptest.NewRecorder()
	fx.server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", w.Code, http.StatusUnauthorized)
	}
}

// A malformed pr_ segment is rejected before anything tries to resolve a
// database, the same as on the other database routes.
func TestRotateRejectsBadEnvSegment(t *testing.T) {
	fx := setupTestServer(t)
	defer fx.cleanup()

	token := seedScopedToken(t, fx.store, "admin")

	req := httptest.NewRequest("POST", "/api/projects/myapp/databases/pr_abc/rotate", nil)
	w := httptest.NewRecorder()
	fx.server.Router().ServeHTTP(w, authed(req, token))

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

// A restored database is addressed at "{env}_restore_{ts}" and is governed by
// the environment it was restored from, so the token that may read, browse
// and delete it must be able to rotate its password too. Rotation authorized
// the raw path segment instead of scopeEnv(segment), which failed closed —
// a 403 on a database the same token can otherwise use in every other way.
func TestRotateOnRestoredDatabaseUsesSourceEnvScope(t *testing.T) {
	fx := setupTestServer(t)
	defer fx.cleanup()

	const restoredProdRotate = "/api/projects/myapp/databases/prod_restore_20260823T101500/rotate"

	t.Run("prod-scoped token is authorized for a restored prod database", func(t *testing.T) {
		token := seedScopedToken(t, fx.store, "project:myapp:env:prod")

		req := httptest.NewRequest("POST", restoredProdRotate, nil)
		w := httptest.NewRecorder()
		fx.server.Router().ServeHTTP(w, authed(req, token))

		// The database genuinely doesn't exist in this test's store, so the
		// manager rejects it past the scope check. Anything other than 403
		// proves the scope check itself passed.
		if w.Code == http.StatusForbidden {
			t.Fatalf("status = %d, want != %d — a prod-scoped token must be able to rotate a restored prod database (body %s)", w.Code, http.StatusForbidden, w.Body.String())
		}
	})

	t.Run("dev-scoped token is still denied a restored prod database", func(t *testing.T) {
		token := seedScopedToken(t, fx.store, "project:myapp:env:dev")

		req := httptest.NewRequest("POST", restoredProdRotate, nil)
		w := httptest.NewRecorder()
		fx.server.Router().ServeHTTP(w, authed(req, token))

		if w.Code != http.StatusForbidden {
			t.Errorf("status = %d, want %d (body %s) — a dev-scoped token must not rotate a restored prod database", w.Code, http.StatusForbidden, w.Body.String())
		}
	})
}
