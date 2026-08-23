package api

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"pgmanager/internal/config"
)

// Backups reach a real Postgres and a real bucket, so — like the explorer
// and rotate tests beside this one — these stop at the layer that runs
// before either: scope, auth, request-shape validation, and the
// backups-disabled config gate.

func backupRoutes() []struct {
	method string
	path   string
	body   string
} {
	return []struct {
		method string
		path   string
		body   string
	}{
		{"PUT", "/api/projects/other/databases/dev/backup", `{"enabled":true}`},
		{"POST", "/api/projects/other/databases/dev/backups", ""},
		{"GET", "/api/projects/other/databases/dev/backups", ""},
		{"DELETE", "/api/projects/other/databases/dev/backups/1", ""},
	}
}

func TestBackupRequiresScope(t *testing.T) {
	fx := setupTestServer(t)
	defer fx.cleanup()

	// Scoped to a different project than the routes below target.
	token := seedScopedToken(t, fx.store, "project:myapp")

	for _, rt := range backupRoutes() {
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

// A token scoped to one project's PR databases must be refused the prod
// database of the same project.
func TestBackupRespectsEnvScope(t *testing.T) {
	fx := setupTestServer(t)
	defer fx.cleanup()

	token := seedScopedToken(t, fx.store, "project:myapp:pr:*")

	req := httptest.NewRequest("GET", "/api/projects/myapp/databases/prod/backups", nil)
	w := httptest.NewRecorder()
	fx.server.Router().ServeHTTP(w, authed(req, token))

	if w.Code != http.StatusForbidden {
		t.Errorf("prod status = %d, want %d", w.Code, http.StatusForbidden)
	}
}

func TestBackupRequiresAuth(t *testing.T) {
	fx := setupTestServer(t)
	defer fx.cleanup()

	for _, rt := range backupRoutes() {
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

// Backups are out of scope for PR databases entirely — the manager rejects
// env=pr before it ever looks anything up.
func TestBackupRejectsPREnv(t *testing.T) {
	fx := setupTestServer(t)
	defer fx.cleanup()

	for _, rt := range []struct{ method, path, body string }{
		{"PUT", "/api/projects/myapp/databases/pr_1/backup", `{"enabled":true}`},
		{"POST", "/api/projects/myapp/databases/pr_1/backups", ""},
		{"GET", "/api/projects/myapp/databases/pr_1/backups", ""},
		{"DELETE", "/api/projects/myapp/databases/pr_1/backups/1", ""},
	} {
		t.Run(rt.method+" "+rt.path, func(t *testing.T) {
			req := httptest.NewRequest(rt.method, rt.path, bytes.NewBufferString(rt.body))
			w := httptest.NewRecorder()
			fx.server.Router().ServeHTTP(w, authed(req, fx.adminToken))

			if w.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want %d (body %s)", w.Code, http.StatusBadRequest, w.Body.String())
			}
		})
	}
}

// Malformed pr_ segments are rejected before anything tries to resolve a
// database, the same as on the existing database routes.
func TestBackupRejectsBadEnvSegment(t *testing.T) {
	fx := setupTestServer(t)
	defer fx.cleanup()

	req := httptest.NewRequest("GET", "/api/projects/myapp/databases/pr_abc/backups", nil)
	w := httptest.NewRecorder()
	fx.server.Router().ServeHTTP(w, authed(req, fx.adminToken))

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

// setupTestServer never calls EnableBackups, so every backup route must
// answer 503 until an operator configures a bucket — this is the default,
// safe state.
func TestBackupDisabledByDefault(t *testing.T) {
	fx := setupTestServer(t)
	defer fx.cleanup()

	for _, rt := range []struct{ method, path, body string }{
		{"PUT", "/api/projects/myapp/databases/dev/backup", `{"enabled":true}`},
		{"POST", "/api/projects/myapp/databases/dev/backups", ""},
		{"GET", "/api/projects/myapp/databases/dev/backups", ""},
		{"DELETE", "/api/projects/myapp/databases/dev/backups/1", ""},
	} {
		t.Run(rt.method+" "+rt.path, func(t *testing.T) {
			req := httptest.NewRequest(rt.method, rt.path, bytes.NewBufferString(rt.body))
			w := httptest.NewRecorder()
			fx.server.Router().ServeHTTP(w, authed(req, fx.adminToken))

			if w.Code != http.StatusServiceUnavailable {
				t.Errorf("status = %d, want %d (body %s)", w.Code, http.StatusServiceUnavailable, w.Body.String())
			}
		})
	}
}

// The secret configured for the bucket must never reach a client, on any
// route, in any response — including the disabled-503 path, where a bug
// might have folded the raw config into the recorded failure reason.
func TestBackupResponsesNeverLeakConfiguredSecret(t *testing.T) {
	fx := setupTestServer(t)
	defer fx.cleanup()

	const secret = "s3-super-secret-value-should-never-leak-zzy9k"
	fx.server.cfg.Backup = config.BackupConfig{
		Enabled:         true,
		Bucket:          "test-bucket",
		AccessKeyID:     "AKIAEXAMPLE",
		SecretAccessKey: secret,
		Region:          "us-east-1",
		Schedule:        time.Hour,
		Retention:       3,
	}
	// Simulate what Start()'s initBackups does on a startup failure: record
	// a reason and leave backups disabled. The reason text below stands in
	// for what a real S3/config error might say (bucket name, endpoint) —
	// never the secret itself.
	fx.server.mgr.DisableBackups(fmt.Errorf("s3 dial failed for bucket %q", fx.server.cfg.Backup.Bucket))

	token := seedScopedToken(t, fx.store, "admin")
	for _, rt := range []struct{ method, path, body string }{
		{"PUT", "/api/projects/myapp/databases/dev/backup", `{"enabled":true}`},
		{"POST", "/api/projects/myapp/databases/dev/backups", ""},
		{"GET", "/api/projects/myapp/databases/dev/backups", ""},
		{"DELETE", "/api/projects/myapp/databases/dev/backups/1", ""},
	} {
		t.Run(rt.method+" "+rt.path, func(t *testing.T) {
			req := httptest.NewRequest(rt.method, rt.path, bytes.NewBufferString(rt.body))
			w := httptest.NewRecorder()
			fx.server.Router().ServeHTTP(w, authed(req, token))

			if strings.Contains(w.Body.String(), secret) {
				t.Fatalf("response body leaked the configured secret (status %d): %s", w.Code, w.Body.String())
			}
		})
	}
}
