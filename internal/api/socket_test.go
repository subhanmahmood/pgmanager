package api

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// unixHTTPClient dials the given socket for every request, whatever host the
// URL names.
func unixHTTPClient(socket string) *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "unix", socket)
			},
		},
	}
}

func TestSocketGrantsAdminWithoutToken(t *testing.T) {
	fx := setupTestServer(t)
	defer fx.cleanup()

	// Socket paths are limited to ~104 bytes; t.TempDir() can be longer than
	// that on some systems, so keep the name short.
	socket := filepath.Join(t.TempDir(), "s.sock")
	fx.server.cfg.API.Socket = socket

	errs := make(chan error, 1)
	srv, cleanup, err := fx.server.startSocketServer(errs)
	if err != nil {
		t.Fatalf("start socket server: %v", err)
	}
	defer cleanup()
	defer srv.Close()

	info, err := os.Stat(socket)
	if err != nil {
		t.Fatalf("stat socket: %v", err)
	}
	if info.Mode()&os.ModeSocket == 0 {
		t.Fatal("expected a socket at the configured path")
	}
	// The socket's permissions are the whole authorization story, so an
	// accidental world-writable mode would be a real hole.
	if perm := info.Mode().Perm(); perm != 0o660 {
		t.Fatalf("socket mode is %o, want 660", perm)
	}

	resp, err := unixHTTPClient(socket).Get("http://pgmanager.local/api/auth/whoami")
	if err != nil {
		t.Fatalf("whoami over socket: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status %d, want 200 (body %s)", resp.StatusCode, body)
	}
	var who WhoamiResponse
	if err := json.NewDecoder(resp.Body).Decode(&who); err != nil {
		t.Fatalf("decode whoami: %v", err)
	}
	if len(who.Scopes) != 1 || who.Scopes[0] != "admin" {
		t.Fatalf("socket caller got scopes %v, want [admin]", who.Scopes)
	}
	if who.TokenPrefix == "" {
		t.Fatal("socket caller has no audit identity")
	}

	// The same request over TCP, with no token, must still be rejected.
	req := httptest.NewRequest("GET", "/api/auth/whoami", nil)
	w := httptest.NewRecorder()
	fx.server.Router().ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated TCP request: status %d, want 401", w.Code)
	}
}

// The unix dialer invents a Host header, so connection strings handed back
// over the socket must come from config rather than from the request.
func TestSocketDoesNotAdvertiseDialerHost(t *testing.T) {
	fx := setupTestServer(t)
	defer fx.cleanup()
	fx.server.cfg.Postgres.Host = "db.internal"
	fx.server.cfg.Postgres.Port = 5432

	req := httptest.NewRequest("GET", "/api/health", nil)
	req.Host = "pgmanager.local"
	ctx := context.WithValue(req.Context(), peerCredKey{}, "local:uid=0,pid=1")

	host, port := fx.server.publicHostPort(req.WithContext(ctx))
	if host != "db.internal" || port != 5432 {
		t.Fatalf("socket request advertised %s:%d, want db.internal:5432", host, port)
	}

	// A TCP request still prefers its Host header, as before.
	if host, _ := fx.server.publicHostPort(req); host != "pgmanager.local" {
		t.Fatalf("TCP request advertised %q, want the Host header", host)
	}
}

func TestSocketDisabledByDefault(t *testing.T) {
	fx := setupTestServer(t)
	defer fx.cleanup()

	srv, cleanup, err := fx.server.startSocketServer(make(chan error, 1))
	if err != nil {
		t.Fatalf("startSocketServer: %v", err)
	}
	defer cleanup()
	if srv != nil {
		t.Fatal("no socket configured, but a listener was started")
	}
}

func TestSocketReplacesStaleFile(t *testing.T) {
	fx := setupTestServer(t)
	defer fx.cleanup()

	dir := t.TempDir()
	socket := filepath.Join(dir, "s.sock")

	// A leftover socket from an unclean shutdown must not wedge startup.
	// Go unlinks the socket on Close, so opt out to reproduce the crash case.
	ln, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatalf("pre-create socket: %v", err)
	}
	ln.(*net.UnixListener).SetUnlinkOnClose(false)
	ln.Close()
	if _, err := os.Stat(socket); err != nil {
		t.Fatalf("stale socket did not survive close: %v", err)
	}

	fx.server.cfg.API.Socket = socket
	srv, cleanup, err := fx.server.startSocketServer(make(chan error, 1))
	if err != nil {
		t.Fatalf("start over stale socket: %v", err)
	}
	defer cleanup()
	defer srv.Close()

	// A regular file at the path is a different story — that is somebody
	// else's data, and we refuse to delete it.
	other := filepath.Join(dir, "f.sock")
	if err := os.WriteFile(other, []byte("not a socket"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
	fx.server.cfg.API.Socket = other
	if _, _, err := fx.server.startSocketServer(make(chan error, 1)); err == nil {
		t.Fatal("expected a refusal to replace a regular file")
	}
}
