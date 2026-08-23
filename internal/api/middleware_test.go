package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// cspDirectives splits a Content-Security-Policy header into directive name ->
// source list.
func cspDirectives(t *testing.T, header string) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, part := range strings.Split(header, ";") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		name, sources, _ := strings.Cut(part, " ")
		out[name] = strings.TrimSpace(sources)
	}
	return out
}

// The CSP is a security property of the service, not an implementation detail.
// style-src carries 'unsafe-inline' because the admin UI's dialog primitives
// inject a <style> element for scroll locking; script-src must never follow it,
// because that is the directive that stops injected markup from executing.
func TestSecurityHeadersCSP(t *testing.T) {
	rec := httptest.NewRecorder()
	securityHeadersMiddleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})).
		ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://example.com/", nil))

	csp := rec.Header().Get("Content-Security-Policy")
	if csp == "" {
		t.Fatal("no Content-Security-Policy header")
	}
	directives := cspDirectives(t, csp)

	// script-src must stay strict. If a future change needs inline scripts,
	// that is a deliberate decision and this test should be the thing that
	// forces the conversation.
	scriptSrc, ok := directives["script-src"]
	if !ok {
		t.Fatalf("script-src missing from CSP %q", csp)
	}
	if scriptSrc != "'self'" {
		t.Errorf("script-src = %q, want exactly %q", scriptSrc, "'self'")
	}
	for _, banned := range []string{"'unsafe-inline'", "'unsafe-eval'"} {
		if strings.Contains(scriptSrc, banned) {
			t.Errorf("script-src contains %s: %q", banned, scriptSrc)
		}
	}

	for name, want := range map[string]string{
		"default-src":     "'self'",
		"object-src":      "'none'",
		"base-uri":        "'self'",
		"frame-ancestors": "'none'",
	} {
		if got := directives[name]; got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}

	// Radix scroll-locking needs this; assert it so nobody "tightens" it back
	// and reintroduces the silent scroll-behind-modal bug.
	if got := directives["style-src"]; !strings.Contains(got, "'unsafe-inline'") {
		t.Errorf("style-src = %q, want it to allow 'unsafe-inline'", got)
	}
}

func TestSecurityHeaders(t *testing.T) {
	rec := httptest.NewRecorder()
	securityHeadersMiddleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})).
		ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://example.com/", nil))

	for header, want := range map[string]string{
		"X-Content-Type-Options": "nosniff",
		"X-Frame-Options":        "DENY",
		"Referrer-Policy":        "strict-origin-when-cross-origin",
	} {
		if got := rec.Header().Get(header); got != want {
			t.Errorf("%s = %q, want %q", header, got, want)
		}
	}
}

// A backup or a restore waits synchronously for pg_dump/pg_restore plus an
// S3 transfer. Applying the ordinary one-minute deadline to those two routes
// meant they could not complete for any database worth backing up, so they
// get their own, much longer budget — and nothing else must accidentally
// pick it up.
func TestIsLongRunningRequest(t *testing.T) {
	tests := []struct {
		name   string
		method string
		path   string
		want   bool
	}{
		{"create backup", "POST", "/api/projects/myapp/databases/dev/backups", true},
		{"create backup for a pr database", "POST", "/api/projects/myapp/databases/pr_12/backups", true},
		{"restore backup", "POST", "/api/projects/myapp/databases/dev/backups/7/restore", true},
		{"create backup with a trailing slash", "POST", "/api/projects/myapp/databases/dev/backups/", true},

		{"list backups", "GET", "/api/projects/myapp/databases/dev/backups", false},
		{"delete backup", "DELETE", "/api/projects/myapp/databases/dev/backups/7", false},
		{"toggle scheduled backups", "PUT", "/api/projects/myapp/databases/dev/backup", false},
		{"create database", "POST", "/api/projects/myapp/databases", false},
		{"create project", "POST", "/api/projects", false},
		{"insert an explorer row", "POST", "/api/projects/myapp/databases/dev/tables/users/rows", false},
		{"login", "POST", "/api/auth/login", false},
		{"health", "GET", "/api/health", false},
		// Nothing outside the database route family may borrow the long
		// budget just by ending in a matching word.
		{"static asset called restore", "POST", "/restore", false},
		{"project route without a database segment", "POST", "/api/projects/myapp/backups", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isLongRunningRequest(tt.method, tt.path); got != tt.want {
				t.Errorf("isLongRunningRequest(%q, %q) = %v, want %v", tt.method, tt.path, got, tt.want)
			}
		})
	}
}

// The middleware must actually hand the handler the longer deadline, not
// merely classify the request correctly.
func TestRequestTimeoutMiddlewareGivesBackupsTheLongerDeadline(t *testing.T) {
	tests := []struct {
		name   string
		method string
		path   string
		want   time.Duration
	}{
		{"ordinary request", "GET", "/api/projects", defaultRequestTimeout},
		{"backup request", "POST", "/api/projects/myapp/databases/dev/backups", backupRequestTimeout},
		{"restore request", "POST", "/api/projects/myapp/databases/dev/backups/7/restore", backupRequestTimeout},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var budget time.Duration
			var ok bool
			h := requestTimeoutMiddleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				var deadline time.Time
				deadline, ok = r.Context().Deadline()
				budget = time.Until(deadline)
			}))

			h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(tt.method, tt.path, nil))

			if !ok {
				t.Fatal("handler received a context with no deadline; every request must stay bounded")
			}
			// Allow for the handful of microseconds spent getting here.
			if budget > tt.want || budget < tt.want-time.Second {
				t.Errorf("deadline budget = %v, want ~%v", budget, tt.want)
			}
		})
	}
}
