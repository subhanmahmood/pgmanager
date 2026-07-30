package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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
