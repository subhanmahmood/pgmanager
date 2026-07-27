package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"pgmanager/internal/auth"
	"pgmanager/internal/meta"
)

const testPassword = "a-perfectly-fine-password"

// seedUser adds a human to the allowlist directly in the store, standing in
// for `pgmanager users add` on the server.
func seedUser(t *testing.T, fx *testFixture, email string) *meta.User {
	t.Helper()
	hash, err := auth.HashPassword(testPassword)
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	u := &meta.User{Email: email, PasswordHash: hash, CreatedBy: "test"}
	if err := fx.store.CreateUser(context.Background(), u); err != nil {
		t.Fatalf("create user: %v", err)
	}
	return u
}

func postJSON(t *testing.T, fx *testFixture, path string, body interface{}) *httptest.ResponseRecorder {
	t.Helper()
	b, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", path, bytes.NewReader(b))
	w := httptest.NewRecorder()
	fx.server.Router().ServeHTTP(w, req)
	return w
}

// login returns the session cookie for a successful sign-in.
func login(t *testing.T, fx *testFixture, email, password string) *http.Cookie {
	t.Helper()
	w := postJSON(t, fx, "/api/auth/login", LoginRequest{Email: email, Password: password})
	if w.Code != http.StatusOK {
		t.Fatalf("login: status %d, body %s", w.Code, w.Body.String())
	}
	for _, c := range w.Result().Cookies() {
		if c.Name == auth.SessionCookieName {
			return c
		}
	}
	t.Fatal("login did not set a session cookie")
	return nil
}

func TestLoginSetsUsableSession(t *testing.T) {
	fx := setupTestServer(t)
	defer fx.cleanup()
	seedUser(t, fx, "me@example.com")

	w := postJSON(t, fx, "/api/auth/login", LoginRequest{Email: "ME@Example.com", Password: testPassword})
	if w.Code != http.StatusOK {
		t.Fatalf("login: status %d, body %s", w.Code, w.Body.String())
	}

	var cookie *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == auth.SessionCookieName {
			cookie = c
		}
	}
	if cookie == nil {
		t.Fatal("no session cookie")
	}
	// The cookie is the credential, so script must not be able to read it and
	// other origins must not be able to send it on state-changing requests.
	if !cookie.HttpOnly {
		t.Error("session cookie is not HttpOnly")
	}
	if cookie.SameSite != http.SameSiteLaxMode {
		t.Errorf("session cookie SameSite = %v, want Lax (the CSRF defence)", cookie.SameSite)
	}
	if cookie.Value == "" {
		t.Error("session cookie has no value")
	}

	// The cookie authenticates, and reports the human rather than a token.
	req := httptest.NewRequest("GET", "/api/auth/whoami", nil)
	req.AddCookie(cookie)
	ww := httptest.NewRecorder()
	fx.server.Router().ServeHTTP(ww, req)
	if ww.Code != http.StatusOK {
		t.Fatalf("whoami with cookie: status %d, body %s", ww.Code, ww.Body.String())
	}
	var who WhoamiResponse
	if err := json.Unmarshal(ww.Body.Bytes(), &who); err != nil {
		t.Fatalf("decode whoami: %v", err)
	}
	if who.Email != "me@example.com" {
		t.Fatalf("whoami email = %q, want me@example.com", who.Email)
	}
	if len(who.Scopes) != 1 || who.Scopes[0] != auth.ScopeAdmin {
		t.Fatalf("session scopes = %v, want [admin]", who.Scopes)
	}
}

// Wrong password, unknown address and disabled account must be
// indistinguishable, or the login form becomes a way to enumerate the
// allowlist.
func TestLoginFailuresAreIndistinguishable(t *testing.T) {
	fx := setupTestServer(t)
	defer fx.cleanup()
	seedUser(t, fx, "me@example.com")

	// Someone who was on the list and has since been removed.
	seedUser(t, fx, "gone@example.com")
	if err := fx.store.DeleteUser(context.Background(), "gone@example.com"); err != nil {
		t.Fatalf("delete user: %v", err)
	}

	cases := []struct {
		name     string
		email    string
		password string
	}{
		{"wrong password", "me@example.com", "not-the-password"},
		{"unknown address", "nobody@example.com", testPassword},
		{"removed account", "gone@example.com", testPassword},
	}
	var bodies []string
	for _, tc := range cases {
		w := postJSON(t, fx, "/api/auth/login", LoginRequest{Email: tc.email, Password: tc.password})
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("%s: status %d, want 401", tc.name, w.Code)
		}
		if len(w.Result().Cookies()) != 0 {
			t.Fatalf("%s: a failed login set a cookie", tc.name)
		}
		bodies = append(bodies, w.Body.String())
	}
	for i := 1; i < len(bodies); i++ {
		if bodies[i] != bodies[0] {
			t.Fatalf("failure responses differ:\n %s\n %s", bodies[0], bodies[i])
		}
	}
}

func TestLoginThrottled(t *testing.T) {
	fx := setupTestServer(t)
	defer fx.cleanup()
	seedUser(t, fx, "me@example.com")

	for i := 0; i < 5; i++ {
		w := postJSON(t, fx, "/api/auth/login", LoginRequest{Email: "me@example.com", Password: "wrong"})
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d: status %d, want 401", i+1, w.Code)
		}
	}
	w := postJSON(t, fx, "/api/auth/login", LoginRequest{Email: "me@example.com", Password: "wrong"})
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("6th attempt: status %d, want 429", w.Code)
	}
}

// The per-email budget must never become an account-lockout lever: anyone who
// knows an administrator's address could otherwise keep them out forever with
// five requests a quarter hour. A correct password always gets in.
func TestThrottleCannotLockOutAValidPassword(t *testing.T) {
	fx := setupTestServer(t)
	defer fx.cleanup()
	seedUser(t, fx, "victim@example.com")

	// The attacker burns the victim's email budget from their own address.
	// getClientIP reads RemoteAddr, which httptest sets per request, so vary
	// it to model a distributed attempt rather than one exhausted IP.
	for i := 0; i < 10; i++ {
		b, _ := json.Marshal(LoginRequest{Email: "victim@example.com", Password: "wrong"})
		req := httptest.NewRequest("POST", "/api/auth/login", bytes.NewReader(b))
		req.RemoteAddr = fmt.Sprintf("203.0.113.%d:5555", i+1)
		w := httptest.NewRecorder()
		fx.server.Router().ServeHTTP(w, req)
	}

	// The victim, from their own address, signs in fine.
	b, _ := json.Marshal(LoginRequest{Email: "victim@example.com", Password: testPassword})
	req := httptest.NewRequest("POST", "/api/auth/login", bytes.NewReader(b))
	req.RemoteAddr = "198.51.100.7:5555"
	w := httptest.NewRecorder()
	fx.server.Router().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("victim locked out by an attacker's failed attempts: status %d, body %s", w.Code, w.Body.String())
	}
}

// A login that verified a password which changed before its session was
// written must not end up with a working session — that is exactly the
// survivor a reset is meant to remove.
func TestSessionLosingRaceWithPasswordChangeIsRejected(t *testing.T) {
	fx := setupTestServer(t)
	defer fx.cleanup()
	user := seedUser(t, fx, "me@example.com")
	stale := user.PasswordChangedAt

	newHash, err := auth.HashPassword("a-brand-new-password")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if err := fx.store.SetUserPassword(context.Background(), "me@example.com", newHash); err != nil {
		t.Fatalf("set password: %v", err)
	}

	// Now try to insert a session authorized by the superseded password.
	_, hash, err := auth.GenerateSessionToken()
	if err != nil {
		t.Fatalf("generate session: %v", err)
	}
	err = fx.store.CreateSession(context.Background(), &meta.Session{
		TokenHash: hash,
		UserID:    user.ID,
		ExpiresAt: time.Now().Add(time.Hour),
	}, stale)
	if !errors.Is(err, meta.ErrPasswordChanged) {
		t.Fatalf("stale session insert returned %v, want ErrPasswordChanged", err)
	}
}

func TestLogoutInvalidatesImmediately(t *testing.T) {
	fx := setupTestServer(t)
	defer fx.cleanup()
	seedUser(t, fx, "me@example.com")
	cookie := login(t, fx, "me@example.com", testPassword)

	req := httptest.NewRequest("POST", "/api/auth/logout", nil)
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	fx.server.Router().ServeHTTP(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("logout: status %d, body %s", w.Code, w.Body.String())
	}

	// Replaying the same cookie must fail: the row is gone server-side, so it
	// doesn't matter that the client kept a copy.
	req = httptest.NewRequest("GET", "/api/auth/whoami", nil)
	req.AddCookie(cookie)
	w = httptest.NewRecorder()
	fx.server.Router().ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("whoami after logout: status %d, want 401", w.Code)
	}
}

func TestExpiredSessionRejected(t *testing.T) {
	fx := setupTestServer(t)
	defer fx.cleanup()
	user := seedUser(t, fx, "me@example.com")

	plain, hash, err := auth.GenerateSessionToken()
	if err != nil {
		t.Fatalf("generate session: %v", err)
	}
	if err := fx.store.CreateSession(context.Background(), &meta.Session{
		TokenHash: hash,
		UserID:    user.ID,
		ExpiresAt: time.Now().Add(-time.Minute),
	}, user.PasswordChangedAt); err != nil {
		t.Fatalf("create session: %v", err)
	}

	req := httptest.NewRequest("GET", "/api/auth/whoami", nil)
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: plain})
	w := httptest.NewRecorder()
	fx.server.Router().ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expired session: status %d, want 401", w.Code)
	}

	n, err := fx.store.DeleteExpiredSessions(context.Background())
	if err != nil || n != 1 {
		t.Fatalf("purge expired sessions: n=%d err=%v", n, err)
	}
}

func TestRemovingUserKillsSession(t *testing.T) {
	fx := setupTestServer(t)
	defer fx.cleanup()
	seedUser(t, fx, "me@example.com")
	cookie := login(t, fx, "me@example.com", testPassword)

	if err := fx.store.DeleteUser(context.Background(), "me@example.com"); err != nil {
		t.Fatalf("delete user: %v", err)
	}

	req := httptest.NewRequest("GET", "/api/auth/whoami", nil)
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	fx.server.Router().ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("session of a removed user: status %d, want 401", w.Code)
	}
}

// A bearer token must keep working exactly as before — machines are
// unaffected by humans getting sessions.
func TestBearerTokenStillWinsOverCookie(t *testing.T) {
	fx := setupTestServer(t)
	defer fx.cleanup()
	seedUser(t, fx, "me@example.com")
	cookie := login(t, fx, "me@example.com", testPassword)

	req := httptest.NewRequest("GET", "/api/auth/whoami", nil)
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	fx.server.Router().ServeHTTP(w, authed(req, fx.adminToken))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d, body %s", w.Code, w.Body.String())
	}
	var who WhoamiResponse
	if err := json.Unmarshal(w.Body.Bytes(), &who); err != nil {
		t.Fatalf("decode whoami: %v", err)
	}
	if who.Email != "" {
		t.Fatalf("bearer request resolved to the cookie's human (%q)", who.Email)
	}
}

func TestChangePasswordSignsEverythingOut(t *testing.T) {
	fx := setupTestServer(t)
	defer fx.cleanup()
	seedUser(t, fx, "me@example.com")
	cookie := login(t, fx, "me@example.com", testPassword)

	const newPassword = "an-entirely-new-password"
	b, _ := json.Marshal(ChangePasswordRequest{Current: testPassword, New: newPassword})
	req := httptest.NewRequest("POST", "/api/auth/password", bytes.NewReader(b))
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	fx.server.Router().ServeHTTP(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("change password: status %d, body %s", w.Code, w.Body.String())
	}

	// The old session is gone...
	req = httptest.NewRequest("GET", "/api/auth/whoami", nil)
	req.AddCookie(cookie)
	w = httptest.NewRecorder()
	fx.server.Router().ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("session after password change: status %d, want 401", w.Code)
	}
	// ...the old password no longer works, and the new one does.
	if w := postJSON(t, fx, "/api/auth/login", LoginRequest{Email: "me@example.com", Password: testPassword}); w.Code != http.StatusUnauthorized {
		t.Fatalf("old password still works: status %d", w.Code)
	}
	if w := postJSON(t, fx, "/api/auth/login", LoginRequest{Email: "me@example.com", Password: newPassword}); w.Code != http.StatusOK {
		t.Fatalf("new password rejected: status %d, body %s", w.Code, w.Body.String())
	}
}

func TestChangePasswordRequiresCurrent(t *testing.T) {
	fx := setupTestServer(t)
	defer fx.cleanup()
	seedUser(t, fx, "me@example.com")
	cookie := login(t, fx, "me@example.com", testPassword)

	b, _ := json.Marshal(ChangePasswordRequest{Current: "wrong", New: "an-entirely-new-password"})
	req := httptest.NewRequest("POST", "/api/auth/password", bytes.NewReader(b))
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	fx.server.Router().ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status %d, want 401", w.Code)
	}

	// A token holder is not a human and has no password to change.
	b, _ = json.Marshal(ChangePasswordRequest{Current: testPassword, New: "an-entirely-new-password"})
	req = httptest.NewRequest("POST", "/api/auth/password", bytes.NewReader(b))
	w = httptest.NewRecorder()
	fx.server.Router().ServeHTTP(w, authed(req, fx.adminToken))
	if w.Code != http.StatusForbidden {
		t.Fatalf("token holder changing a password: status %d, want 403", w.Code)
	}
}
