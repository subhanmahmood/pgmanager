package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"pgmanager/internal/config"
	"pgmanager/internal/meta"
	"pgmanager/internal/project"
)

// newStaticServer builds a server whose admin UI is served from a temp dir
// containing an index.html and one asset.
func newStaticServer(t *testing.T, webDir string) *Server {
	t.Helper()

	cfg := &config.Config{
		Postgres: config.PostgresConfig{Host: "localhost", Port: 5432, User: "postgres", Database: "postgres"},
		API:      config.APIConfig{Listen: "127.0.0.1:0", RequireToken: true, WebDir: webDir},
	}
	store := meta.NewMockStore()
	t.Cleanup(func() { _ = store.Close() })
	return NewServer(cfg, project.NewManager(cfg, store), store, cfg.API.BindAddress())
}

func writeWebDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range map[string]string{
		"index.html": "<!DOCTYPE html><title>pgmanager</title>",
		"app.js":     "// app",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	return dir
}

func TestStaticUI(t *testing.T) {
	srv := newStaticServer(t, writeWebDir(t))

	tests := []struct {
		name       string
		method     string
		path       string
		wantStatus int
		wantBody   string
	}{
		{"root serves index", http.MethodGet, "/", http.StatusOK, "<!DOCTYPE html><title>pgmanager</title>"},
		{"asset served verbatim", http.MethodGet, "/app.js", http.StatusOK, "// app"},
		{"unknown path falls back to index", http.MethodGet, "/projects", http.StatusOK, "<!DOCTYPE html><title>pgmanager</title>"},
		// /api/* is owned by the authenticated subrouter, so an unmatched API
		// path is rejected there and never reaches the SPA fallback — clients
		// get JSON, not HTML they would fail to parse.
		{"unknown api path stays on the api router", http.MethodGet, "/api/nope", http.StatusUnauthorized, ""},
		{"api prefix stays on the api router", http.MethodGet, "/api", http.StatusUnauthorized, ""},
		// chi rejects traversal before routing; the handler's own guard is
		// defence in depth behind this.
		{"traversal rejected", http.MethodGet, "/../../etc/passwd", http.StatusBadRequest, ""},
		{"non-GET 404s", http.MethodPost, "/app.js", http.StatusNotFound, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, "http://example.com"+tt.path, nil)
			// httptest.NewRequest cleans the URL; set the raw path directly so
			// the traversal case reaches the handler unmodified.
			req.URL.Path = tt.path

			rec := httptest.NewRecorder()
			srv.Router().ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d (body %q)", rec.Code, tt.wantStatus, rec.Body.String())
			}
			if tt.wantBody != "" && rec.Body.String() != tt.wantBody {
				t.Errorf("body = %q, want %q", rec.Body.String(), tt.wantBody)
			}
		})
	}
}

// The API must keep working when no UI is present, and must not 500 or panic
// when web_dir points somewhere that does not exist.
func TestStaticUIDisabled(t *testing.T) {
	for _, webDir := range []string{"-", filepath.Join(t.TempDir(), "missing")} {
		srv := newStaticServer(t, webDir)

		rec := httptest.NewRecorder()
		srv.Router().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://example.com/", nil))
		if rec.Code != http.StatusNotFound {
			t.Errorf("web_dir=%q: GET / = %d, want 404", webDir, rec.Code)
		}

		rec = httptest.NewRecorder()
		srv.Router().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://example.com/api/health", nil))
		if rec.Code != http.StatusOK {
			t.Errorf("web_dir=%q: health = %d, want 200", webDir, rec.Code)
		}
	}
}
