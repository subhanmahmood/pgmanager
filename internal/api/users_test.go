package api

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"pgmanager/internal/auth"
)

// The allowlist of humans who can sign in has no HTTP surface at all: it is
// edited by `pgmanager users` against the database directly. This is the
// load-bearing property of the design, and it cuts both ways.
//
// Security: no request, however authenticated, can add a user — so a leaked
// admin token cannot mint itself a persistent UI login that survives the
// token being revoked.
//
// Availability: provisioning does not depend on the API being up, the admin
// socket being enabled, or any account already existing — so there is no
// configuration in which the first account cannot be created.
//
// If a user-management route ever appears on either router, both properties
// are gone and this test should fail loudly.
func TestNoUserManagementOverHTTP(t *testing.T) {
	fx := setupTestServer(t)
	defer fx.cleanup()

	routes := []struct {
		method string
		path   string
	}{
		{"GET", "/api/users"},
		{"POST", "/api/users"},
		{"GET", "/api/users/me@example.com"},
		{"POST", "/api/users/me@example.com/password"},
		{"DELETE", "/api/users/me@example.com"},
	}

	transports := []struct {
		name    string
		handler http.Handler
		// The socket router grants admin to anything that reaches it, so it is
		// the most permissive caller there is — if the routes are absent there,
		// they are absent everywhere.
		token string
	}{
		{"tcp with admin token", fx.server.Router(), fx.adminToken},
		{"local admin socket", fx.server.socketRouter, ""},
	}

	for _, tr := range transports {
		for _, rt := range routes {
			t.Run(tr.name+" "+rt.method+" "+rt.path, func(t *testing.T) {
				body := bytes.NewReader([]byte(`{"email":"me@example.com"}`))
				req := httptest.NewRequest(rt.method, rt.path, body)
				if tr.token != "" {
					req = authed(req, tr.token)
				}
				w := httptest.NewRecorder()
				tr.handler.ServeHTTP(w, req)
				if w.Code != http.StatusNotFound {
					t.Fatalf("status %d, want 404 — user management must not be reachable over HTTP (body %s)",
						w.Code, w.Body.String())
				}
			})
		}
	}

	// Nothing was created by any of those attempts.
	n, err := fx.store.CountUsers(context.Background())
	if err != nil {
		t.Fatalf("count users: %v", err)
	}
	if n != 0 {
		t.Fatalf("%d users exist after HTTP attempts, want 0", n)
	}
}

// An operator reset exists precisely for the case where you no longer trust
// what is signed in, so it must drop live sessions.
func TestSetPasswordSignsOutExistingSessions(t *testing.T) {
	fx := setupTestServer(t)
	defer fx.cleanup()
	seedUser(t, fx, "me@example.com")
	cookie := login(t, fx, "me@example.com", testPassword)

	newHash, err := auth.HashPassword("a-different-password")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if err := fx.store.SetUserPassword(context.Background(), "me@example.com", newHash); err != nil {
		t.Fatalf("set password: %v", err)
	}

	req := httptest.NewRequest("GET", "/api/auth/whoami", nil)
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	fx.server.Router().ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("session after operator reset: status %d, want 401", w.Code)
	}
}
