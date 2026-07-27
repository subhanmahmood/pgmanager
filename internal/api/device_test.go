package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"pgmanager/internal/auth"
	"pgmanager/internal/meta"
)

// startDevice runs the unauthenticated first leg of the flow.
func startDevice(t *testing.T, fx *testFixture, body interface{}) StartDeviceResponse {
	t.Helper()
	var reader *bytes.Reader
	if body == nil {
		reader = bytes.NewReader([]byte(`{}`))
	} else {
		b, _ := json.Marshal(body)
		reader = bytes.NewReader(b)
	}
	req := httptest.NewRequest("POST", "/api/auth/device", reader)
	w := httptest.NewRecorder()
	fx.server.Router().ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("start device: status %d, body %s", w.Code, w.Body.String())
	}
	var out StartDeviceResponse
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode start response: %v", err)
	}
	return out
}

// pollDevice runs one unauthenticated poll and returns the recorder so tests
// can inspect both the success and the error shapes.
func pollDevice(t *testing.T, fx *testFixture, deviceCode string) *httptest.ResponseRecorder {
	t.Helper()
	b, _ := json.Marshal(map[string]string{"device_code": deviceCode})
	req := httptest.NewRequest("POST", "/api/auth/device/token", bytes.NewReader(b))
	w := httptest.NewRecorder()
	fx.server.Router().ServeHTTP(w, req)
	return w
}

// clearPollThrottle backdates last_polled_at so a test can poll again without
// tripping the interval check.
func clearPollThrottle(t *testing.T, fx *testFixture, userCode string) {
	t.Helper()
	dr, err := fx.store.GetDeviceRequestByUserCode(context.Background(), auth.NormalizeUserCode(userCode))
	if err != nil || dr == nil {
		t.Fatalf("lookup device request: %v", err)
	}
	if err := fx.store.TouchDeviceRequest(context.Background(), dr.ID, time.Now().Add(-time.Hour)); err != nil {
		t.Fatalf("touch device request: %v", err)
	}
}

func approveDevice(t *testing.T, fx *testFixture, userCode string, body ApproveDeviceRequestBody, token string) *httptest.ResponseRecorder {
	t.Helper()
	b, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", "/api/auth/device/"+userCode+"/approve", bytes.NewReader(b))
	w := httptest.NewRecorder()
	fx.server.Router().ServeHTTP(w, authed(req, token))
	return w
}

func errorMessage(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var er struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &er); err != nil {
		t.Fatalf("decode error body %q: %v", w.Body.String(), err)
	}
	return er.Error
}

func TestDeviceFlowHappyPath(t *testing.T) {
	fx := setupTestServer(t)
	defer fx.cleanup()

	start := startDevice(t, fx, StartDeviceRequest{
		ClientName:      "my-laptop",
		RequestedScopes: []string{"project:myapp"},
	})
	if start.DeviceCode == "" || start.UserCode == "" {
		t.Fatal("start response is missing codes")
	}
	if start.Interval < 1 || start.ExpiresIn < 1 {
		t.Fatalf("start response has unusable interval/expiry: %+v", start)
	}
	if !auth.ValidUserCode(start.UserCode) {
		t.Fatalf("user code %q is malformed", start.UserCode)
	}

	// Before approval the CLI is told to keep waiting.
	if w := pollDevice(t, fx, start.DeviceCode); errorMessage(t, w) != errAuthorizationPending {
		t.Fatalf("first poll: got %q, want %q", errorMessage(t, w), errAuthorizationPending)
	}

	// The approver sees the request, including what it asked for.
	req := httptest.NewRequest("GET", "/api/auth/device/"+start.UserCode, nil)
	w := httptest.NewRecorder()
	fx.server.Router().ServeHTTP(w, authed(req, fx.adminToken))
	if w.Code != http.StatusOK {
		t.Fatalf("get device request: status %d, body %s", w.Code, w.Body.String())
	}
	var view DeviceRequestResponse
	if err := json.Unmarshal(w.Body.Bytes(), &view); err != nil {
		t.Fatalf("decode device view: %v", err)
	}
	if view.ClientName != "my-laptop" || len(view.RequestedScopes) != 1 {
		t.Fatalf("unexpected device view: %+v", view)
	}

	// Approve with narrower scopes than were requested.
	aw := approveDevice(t, fx, start.UserCode, ApproveDeviceRequestBody{
		Name:   "my-laptop",
		Scopes: []string{"project:myapp:env:dev"},
	}, fx.adminToken)
	if aw.Code != http.StatusOK {
		t.Fatalf("approve: status %d, body %s", aw.Code, aw.Body.String())
	}

	clearPollThrottle(t, fx, start.UserCode)
	pw := pollDevice(t, fx, start.DeviceCode)
	if pw.Code != http.StatusOK {
		t.Fatalf("poll after approval: status %d, body %s", pw.Code, pw.Body.String())
	}
	var got PollDeviceResponse
	if err := json.Unmarshal(pw.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode poll response: %v", err)
	}
	if got.Token == "" {
		t.Fatal("poll returned no token")
	}
	if len(got.Info.Scopes) != 1 || got.Info.Scopes[0] != "project:myapp:env:dev" {
		t.Fatalf("token has scopes %v, want the approver's choice", got.Info.Scopes)
	}

	// The issued token must actually work.
	wr := httptest.NewRequest("GET", "/api/auth/whoami", nil)
	ww := httptest.NewRecorder()
	fx.server.Router().ServeHTTP(ww, authed(wr, got.Token))
	if ww.Code != http.StatusOK {
		t.Fatalf("whoami with issued token: status %d, body %s", ww.Code, ww.Body.String())
	}

	// ...and it must not be collectable twice.
	clearPollThrottle(t, fx, start.UserCode)
	second := pollDevice(t, fx, start.DeviceCode)
	if second.Code == http.StatusOK {
		t.Fatalf("replayed poll handed out the token again: %s", second.Body.String())
	}
	if msg := errorMessage(t, second); msg != errExpiredToken {
		t.Fatalf("replayed poll: got %q, want %q", msg, errExpiredToken)
	}
}

func TestDeviceFlowDenied(t *testing.T) {
	fx := setupTestServer(t)
	defer fx.cleanup()

	start := startDevice(t, fx, nil)

	req := httptest.NewRequest("POST", "/api/auth/device/"+start.UserCode+"/deny", nil)
	w := httptest.NewRecorder()
	fx.server.Router().ServeHTTP(w, authed(req, fx.adminToken))
	if w.Code != http.StatusNoContent {
		t.Fatalf("deny: status %d, body %s", w.Code, w.Body.String())
	}

	clearPollThrottle(t, fx, start.UserCode)
	if msg := errorMessage(t, pollDevice(t, fx, start.DeviceCode)); msg != errAccessDenied {
		t.Fatalf("poll after deny: got %q, want %q", msg, errAccessDenied)
	}
}

func TestDeviceFlowRejectsRapidPolling(t *testing.T) {
	fx := setupTestServer(t)
	defer fx.cleanup()

	start := startDevice(t, fx, nil)
	if msg := errorMessage(t, pollDevice(t, fx, start.DeviceCode)); msg != errAuthorizationPending {
		t.Fatalf("first poll: got %q", msg)
	}
	// Immediately again — the server should push back rather than answer.
	if msg := errorMessage(t, pollDevice(t, fx, start.DeviceCode)); msg != errSlowDown {
		t.Fatalf("second poll: got %q, want %q", msg, errSlowDown)
	}
}

func TestDeviceFlowExpiry(t *testing.T) {
	fx := setupTestServer(t)
	defer fx.cleanup()

	// Seed a request that already lapsed.
	if err := fx.store.CreateDeviceRequest(context.Background(), &meta.DeviceRequest{
		DeviceCodeHash: []byte("unused"),
		UserCode:       "AAAA2222",
		ExpiresAt:      time.Now().Add(-time.Minute),
	}); err != nil {
		t.Fatalf("seed expired request: %v", err)
	}

	// An expired request is invisible to the approver...
	req := httptest.NewRequest("GET", "/api/auth/device/AAAA-2222", nil)
	w := httptest.NewRecorder()
	fx.server.Router().ServeHTTP(w, authed(req, fx.adminToken))
	if w.Code != http.StatusNotFound {
		t.Fatalf("get expired request: status %d, want 404", w.Code)
	}

	// ...and gets purged on demand.
	n, err := fx.store.DeleteExpiredDeviceRequests(context.Background())
	if err != nil {
		t.Fatalf("purge: %v", err)
	}
	if n != 1 {
		t.Fatalf("purged %d requests, want 1", n)
	}
}

func TestDeviceEndpointsRequireAuth(t *testing.T) {
	fx := setupTestServer(t)
	defer fx.cleanup()

	start := startDevice(t, fx, nil)

	// Approval is privileged: no token means no approval.
	for _, tc := range []struct {
		name   string
		method string
		path   string
	}{
		{"list", "GET", "/api/auth/devices"},
		{"get", "GET", "/api/auth/device/" + start.UserCode},
		{"approve", "POST", "/api/auth/device/" + start.UserCode + "/approve"},
		{"deny", "POST", "/api/auth/device/" + start.UserCode + "/deny"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, bytes.NewReader([]byte(`{}`)))
			w := httptest.NewRecorder()
			fx.server.Router().ServeHTTP(w, req)
			if w.Code != http.StatusUnauthorized {
				t.Fatalf("status %d, want 401 (body %s)", w.Code, w.Body.String())
			}
		})
	}
}

func TestDeviceApprovalRejectsBadScopes(t *testing.T) {
	fx := setupTestServer(t)
	defer fx.cleanup()

	start := startDevice(t, fx, nil)

	w := approveDevice(t, fx, start.UserCode, ApproveDeviceRequestBody{
		Name:   "laptop",
		Scopes: []string{"not-a-scope"},
	}, fx.adminToken)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("approve with bad scope: status %d, want 400", w.Code)
	}

	// An approval that fails validation must not leave the request approved.
	dr, err := fx.store.GetDeviceRequestByUserCode(context.Background(), auth.NormalizeUserCode(start.UserCode))
	if err != nil || dr == nil {
		t.Fatalf("lookup device request: %v", err)
	}
	if dr.Status != meta.DeviceStatusPending {
		t.Fatalf("status is %q, want pending", dr.Status)
	}
}

func TestDeviceApprovalIsSingleShot(t *testing.T) {
	fx := setupTestServer(t)
	defer fx.cleanup()

	start := startDevice(t, fx, nil)
	body := ApproveDeviceRequestBody{Name: "laptop", Scopes: []string{"project:myapp"}}

	if w := approveDevice(t, fx, start.UserCode, body, fx.adminToken); w.Code != http.StatusOK {
		t.Fatalf("first approve: status %d, body %s", w.Code, w.Body.String())
	}
	w := approveDevice(t, fx, start.UserCode, body, fx.adminToken)
	if w.Code != http.StatusConflict {
		t.Fatalf("second approve: status %d, want 409 (body %s)", w.Code, w.Body.String())
	}

	// The second attempt must not have left a live orphan token behind.
	toks, err := fx.store.ListTokens(context.Background())
	if err != nil {
		t.Fatalf("list tokens: %v", err)
	}
	live := 0
	for _, tok := range toks {
		if tok.Name == "laptop" && tok.RevokedAt == nil {
			live++
		}
	}
	if live != 1 {
		t.Fatalf("found %d live tokens named laptop, want 1", live)
	}
}

func TestDeviceLookupRejectsMalformedCode(t *testing.T) {
	fx := setupTestServer(t)
	defer fx.cleanup()

	req := httptest.NewRequest("GET", "/api/auth/device/not-a-code", nil)
	w := httptest.NewRecorder()
	fx.server.Router().ServeHTTP(w, authed(req, fx.adminToken))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400 (body %s)", w.Code, w.Body.String())
	}
}
