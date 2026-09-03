package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"pgmanager/internal/auth"
	"pgmanager/internal/meta"
	"pgmanager/internal/project"
)

func jsonBody(s string) *bytes.Reader { return bytes.NewReader([]byte(s)) }

// jsonHasKey reports whether a JSON object body carries a given key.
func jsonHasKey(t *testing.T, body, key string) bool {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(body), &m); err != nil {
		return false
	}
	_, ok := m[key]
	return ok
}

// seedScratch writes a leased scratch database straight into the store. Going
// through the create endpoint would need a real Postgres to create it in; the
// lease endpoints below only ever touch metadata.
func seedScratch(t *testing.T, fx *testFixture, projectName, key string, expiresAt *time.Time) string {
	t.Helper()
	ctx := context.Background()
	p, err := fx.store.CreateProject(ctx, projectName)
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	name := project.DatabaseName(projectName, "scratch", key)
	if _, err := fx.store.CreateDatabase(ctx, p.ID, name, name+"_user", "secret", "scratch", key, expiresAt); err != nil {
		t.Fatalf("seed database: %v", err)
	}
	return name
}

func TestRenewEndpointExtendsLease(t *testing.T) {
	fx := setupTestServer(t)
	defer fx.cleanup()

	soon := time.Now().Add(time.Hour)
	name := seedScratch(t, fx, "myapp", "epic_231", &soon)

	req := httptest.NewRequest("POST", "/api/projects/myapp/databases/scratch_epic_231/renew",
		jsonBody(`{"ttl":"14d"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	fx.server.Router().ServeHTTP(w, authed(req, fx.adminToken))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}

	var resp DatabaseInfoResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.DatabaseName != name || resp.Key != "epic_231" || resp.Env != "scratch" {
		t.Errorf("unexpected identity: %+v", resp)
	}
	if resp.ExpiresAt == nil {
		t.Fatal("response carried no expiry")
	}
	got, err := time.Parse(time.RFC3339, *resp.ExpiresAt)
	if err != nil {
		t.Fatalf("parse expiry: %v", err)
	}
	if !got.After(soon.Add(24 * time.Hour)) {
		t.Errorf("expiry %v was not pushed out from %v", got, soon)
	}
	// Renewing must not hand back a credential.
	if body := w.Body.String(); len(body) > 0 && jsonHasKey(t, body, "password") {
		t.Error("renew response leaked a password")
	}
}

func TestRenewRejectsBadInput(t *testing.T) {
	fx := setupTestServer(t)
	defer fx.cleanup()
	soon := time.Now().Add(time.Hour)
	seedScratch(t, fx, "myapp", "epic_231", &soon)

	cases := []struct {
		name, path, body string
		want             int
	}{
		{"unknown database", "/api/projects/myapp/databases/scratch_missing/renew", `{"ttl":"1d"}`, http.StatusBadRequest},
		{"bad ttl", "/api/projects/myapp/databases/scratch_epic_231/renew", `{"ttl":"soon"}`, http.StatusBadRequest},
		{"zero ttl", "/api/projects/myapp/databases/scratch_epic_231/renew", `{"ttl":"0d"}`, http.StatusBadRequest},
		{"permanent env", "/api/projects/myapp/databases/dev/renew", `{"ttl":"1d"}`, http.StatusBadRequest},
		{"malformed key", "/api/projects/myapp/databases/scratch_9lives/renew", `{"ttl":"1d"}`, http.StatusBadRequest},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := httptest.NewRequest("POST", c.path, jsonBody(c.body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			fx.server.Router().ServeHTTP(w, authed(req, fx.adminToken))
			if w.Code != c.want {
				t.Errorf("status = %d, want %d, body = %s", w.Code, c.want, w.Body.String())
			}
		})
	}
}

// TestScratchScopeIsNarrow is the reason scratch is an env rather than a flag:
// an agent token scoped to it cannot reach dev, prod, or CI's PR databases.
func TestScratchScopeIsNarrow(t *testing.T) {
	fx := setupTestServer(t)
	defer fx.cleanup()

	soon := time.Now().Add(time.Hour)
	seedScratch(t, fx, "myapp", "epic_231", &soon)

	plain, hash, prefix, err := auth.GenerateToken()
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if err := fx.store.CreateToken(context.Background(), &meta.Token{
		Name:        "agent",
		TokenHash:   hash,
		TokenPrefix: prefix,
		Scopes:      []string{"project:myapp:env:scratch"},
	}); err != nil {
		t.Fatalf("create token: %v", err)
	}

	allowed := []string{
		"/api/projects/myapp/databases/scratch_epic_231",
	}
	for _, path := range allowed {
		req := httptest.NewRequest("GET", path, nil)
		w := httptest.NewRecorder()
		fx.server.Router().ServeHTTP(w, authed(req, plain))
		if w.Code == http.StatusForbidden {
			t.Errorf("%s should be in scope, got 403", path)
		}
	}

	forbidden := []string{
		"/api/projects/myapp/databases/dev",
		"/api/projects/myapp/databases/prod",
		"/api/projects/myapp/databases/pr_42",
		"/api/projects/other/databases/scratch_epic_231",
	}
	for _, path := range forbidden {
		req := httptest.NewRequest("GET", path, nil)
		w := httptest.NewRecorder()
		fx.server.Router().ServeHTTP(w, authed(req, plain))
		if w.Code != http.StatusForbidden {
			t.Errorf("%s should be out of scope, got %d", path, w.Code)
		}
	}
}

func TestParseEnvParamKeyedEnvs(t *testing.T) {
	cases := []struct {
		segment, wantEnv, wantKey string
		wantErr                   bool
	}{
		{"dev", "dev", "", false},
		{"pr_42", "pr", "42", false},
		{"scratch_epic_231", "scratch", "epic_231", false},
		{"pr_abc", "", "", true},
		{"pr_99999999", "", "", true}, // past MaxPRNumber
	}
	for _, c := range cases {
		key, env, err := parseEnvParam(c.segment)
		if (err != nil) != c.wantErr {
			t.Errorf("parseEnvParam(%q) err = %v, wantErr %v", c.segment, err, c.wantErr)
			continue
		}
		if err != nil {
			continue
		}
		if env != c.wantEnv || key != c.wantKey {
			t.Errorf("parseEnvParam(%q) = (%q, %q), want (%q, %q)", c.segment, env, key, c.wantEnv, c.wantKey)
		}
	}
}

// TestListReturnsTheKey pins the finding that listDatabases built its response
// without the key while every single-database handler included it. Key carries
// `omitempty` and a scratch database has no pr_number to fall back on, so the
// field vanished from the list payload entirely and the admin UI could not
// address the row it had just listed.
func TestListReturnsTheKey(t *testing.T) {
	fx := setupTestServer(t)
	defer fx.cleanup()

	soon := time.Now().Add(time.Hour)
	seedScratch(t, fx, "myapp", "epic_231", &soon)

	req := httptest.NewRequest("GET", "/api/projects/myapp/databases", nil)
	w := httptest.NewRecorder()
	fx.server.Router().ServeHTTP(w, authed(req, fx.adminToken))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}

	var resp []DatabaseInfoResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp) != 1 {
		t.Fatalf("got %d databases, want 1", len(resp))
	}
	if resp[0].Key != "epic_231" {
		t.Errorf("key = %q, want epic_231", resp[0].Key)
	}
}

// TestCreateCapsPRKeySentAsKey pins the finding that the MaxPRNumber bound was
// only applied to the legacy pr_number field. A client sending the same number
// as `key` skipped the cap, and parseEnvParam — which every other endpoint uses
// — then refused the resulting pr_<n> segment, so the database existed but
// nothing could read, renew, or rotate it.
//
// The project is seeded first and the assertion is on the error text, not just
// the status: an unseeded project answers 400 with "project not found", which
// is the same status for an unrelated reason and hides a missing cap entirely.
func TestCreateCapsPRKeySentAsKey(t *testing.T) {
	fx := setupTestServer(t)
	defer fx.cleanup()
	if _, err := fx.store.CreateProject(context.Background(), "myapp"); err != nil {
		t.Fatalf("create project: %v", err)
	}

	cases := []struct {
		name, body string
	}{
		{"key past the cap", `{"env":"pr","key":"1000001"}`},
		{"pr_number past the cap", `{"env":"pr","pr_number":1000001}`},
		{"key not a number", `{"env":"pr","key":"abc"}`},
		{"key not positive", `{"env":"pr","key":"0"}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := httptest.NewRequest("POST", "/api/projects/myapp/databases", jsonBody(c.body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			fx.server.Router().ServeHTTP(w, authed(req, fx.adminToken))
			if w.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400, body = %s", w.Code, w.Body.String())
			}
			if body := w.Body.String(); !strings.Contains(body, "PR number") {
				t.Errorf("rejected for the wrong reason: %s", body)
			}
		})
	}
}
