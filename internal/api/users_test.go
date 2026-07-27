package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// userRoutes is every route that edits the allowlist. None of them may exist
// off-box.
var userRoutes = []struct {
	name   string
	method string
	path   string
}{
	{"list", "GET", "/api/users"},
	{"create", "POST", "/api/users"},
	{"set password", "POST", "/api/users/me@example.com/password"},
	{"delete", "DELETE", "/api/users/me@example.com"},
}

// This is the load-bearing test for the whole design. The allowlist of humans
// who can sign in is the root of trust for the admin UI, and the only thing
// keeping it trustworthy is that these routes are not reachable over the
// network. If this ever passes over TCP, an admin token becomes enough to add
// yourself as a user — so the check is deliberately made with a valid admin
// token attached.
func TestUserRoutesDoNotExistOverTCP(t *testing.T) {
	fx := setupTestServer(t)
	defer fx.cleanup()

	for _, rt := range userRoutes {
		t.Run(rt.name, func(t *testing.T) {
			body := bytes.NewReader([]byte(`{"email":"me@example.com"}`))
			req := httptest.NewRequest(rt.method, rt.path, body)
			w := httptest.NewRecorder()
			fx.server.Router().ServeHTTP(w, authed(req, fx.adminToken))
			if w.Code != http.StatusNotFound {
				t.Fatalf("%s %s over TCP: status %d, want 404 (body %s)",
					rt.method, rt.path, w.Code, w.Body.String())
			}
		})
	}

	// And nothing was created despite the attempts.
	n, err := fx.store.CountUsers(context.Background())
	if err != nil {
		t.Fatalf("count users: %v", err)
	}
	if n != 0 {
		t.Fatalf("%d users exist after TCP attempts, want 0", n)
	}
}

// socketRequest runs a request through the local admin socket router.
func socketRequest(t *testing.T, fx *testFixture, method, path string, body interface{}) *httptest.ResponseRecorder {
	t.Helper()
	var reader *bytes.Reader
	if body == nil {
		reader = bytes.NewReader(nil)
	} else {
		b, _ := json.Marshal(body)
		reader = bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, path, reader)
	w := httptest.NewRecorder()
	fx.server.socketRouter.ServeHTTP(w, req)
	return w
}

func TestUserLifecycleOverSocket(t *testing.T) {
	fx := setupTestServer(t)
	defer fx.cleanup()

	// Create with a generated password.
	w := socketRequest(t, fx, "POST", "/api/users", CreateUserRequest{Email: "Me@Example.com"})
	if w.Code != http.StatusCreated {
		t.Fatalf("create: status %d, body %s", w.Code, w.Body.String())
	}
	var created CreateUserResponse
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create: %v", err)
	}
	if created.User.Email != "me@example.com" {
		t.Fatalf("email not normalized: %q", created.User.Email)
	}
	if created.Password == "" {
		t.Fatal("no password returned for a generated account")
	}

	// The generated password actually works for signing in.
	if w := postJSON(t, fx, "/api/auth/login", LoginRequest{
		Email: "me@example.com", Password: created.Password,
	}); w.Code != http.StatusOK {
		t.Fatalf("login with generated password: status %d, body %s", w.Code, w.Body.String())
	}

	// The stored hash is a hash, not the password.
	user, err := fx.store.GetUserByEmail(context.Background(), "me@example.com")
	if err != nil || user == nil {
		t.Fatalf("get user: %v", err)
	}
	if user.PasswordHash == created.Password {
		t.Fatal("the password was stored in the clear")
	}

	// Duplicate address is refused.
	if w := socketRequest(t, fx, "POST", "/api/users", CreateUserRequest{Email: "me@example.com"}); w.Code != http.StatusConflict {
		t.Fatalf("duplicate create: status %d, want 409", w.Code)
	}

	// List shows them.
	w = socketRequest(t, fx, "GET", "/api/users", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("list: status %d", w.Code)
	}
	var users []UserResponse
	if err := json.Unmarshal(w.Body.Bytes(), &users); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(users) != 1 || users[0].Email != "me@example.com" {
		t.Fatalf("unexpected list: %+v", users)
	}

	// Reset, then remove.
	w = socketRequest(t, fx, "POST", "/api/users/me@example.com/password", SetPasswordRequest{})
	if w.Code != http.StatusOK {
		t.Fatalf("set-password: status %d, body %s", w.Code, w.Body.String())
	}
	var reset CreateUserResponse
	if err := json.Unmarshal(w.Body.Bytes(), &reset); err != nil {
		t.Fatalf("decode reset: %v", err)
	}
	if reset.Password == "" || reset.Password == created.Password {
		t.Fatal("reset did not produce a new password")
	}

	if w := socketRequest(t, fx, "DELETE", "/api/users/me@example.com", nil); w.Code != http.StatusNoContent {
		t.Fatalf("delete: status %d, body %s", w.Code, w.Body.String())
	}
	if w := socketRequest(t, fx, "DELETE", "/api/users/me@example.com", nil); w.Code != http.StatusNotFound {
		t.Fatalf("delete of a missing user: status %d, want 404", w.Code)
	}
}

func TestCreateUserValidation(t *testing.T) {
	fx := setupTestServer(t)
	defer fx.cleanup()

	for _, tt := range []struct {
		name string
		body CreateUserRequest
	}{
		{"no email", CreateUserRequest{}},
		{"malformed email", CreateUserRequest{Email: "not-an-email"}},
		{"short password", CreateUserRequest{Email: "me@example.com", Password: "short"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if w := socketRequest(t, fx, "POST", "/api/users", tt.body); w.Code != http.StatusBadRequest {
				t.Fatalf("status %d, want 400 (body %s)", w.Code, w.Body.String())
			}
		})
	}
}

func TestSetPasswordSignsOutExistingSessions(t *testing.T) {
	fx := setupTestServer(t)
	defer fx.cleanup()
	seedUser(t, fx, "me@example.com")
	cookie := login(t, fx, "me@example.com", testPassword)

	// An operator reset exists precisely for the case where you no longer
	// trust what is signed in, so it must drop live sessions.
	if w := socketRequest(t, fx, "POST", "/api/users/me@example.com/password", SetPasswordRequest{}); w.Code != http.StatusOK {
		t.Fatalf("set-password: status %d", w.Code)
	}

	req := httptest.NewRequest("GET", "/api/auth/whoami", nil)
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	fx.server.Router().ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("session after operator reset: status %d, want 401", w.Code)
	}
}
