package api

import (
	"context"
	"errors"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// RateLimiter implements a per-IP rate limiter.
type RateLimiter struct {
	visitors map[string]*visitor
	mu       sync.RWMutex
	rate     rate.Limit
	burst    int
}

type visitor struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

// NewRateLimiter creates a new rate limiter with the specified requests per
// second and burst.
func NewRateLimiter(rps float64, burst int) *RateLimiter {
	rl := &RateLimiter{
		visitors: make(map[string]*visitor),
		rate:     rate.Limit(rps),
		burst:    burst,
	}
	go rl.cleanupVisitors()
	return rl
}

func (rl *RateLimiter) getVisitor(ip string) *rate.Limiter {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	v, exists := rl.visitors[ip]
	if !exists {
		limiter := rate.NewLimiter(rl.rate, rl.burst)
		rl.visitors[ip] = &visitor{limiter: limiter, lastSeen: time.Now()}
		return limiter
	}
	v.lastSeen = time.Now()
	return v.limiter
}

func (rl *RateLimiter) cleanupVisitors() {
	for {
		time.Sleep(time.Minute)
		rl.mu.Lock()
		for ip, v := range rl.visitors {
			if time.Since(v.lastSeen) > 3*time.Minute {
				delete(rl.visitors, ip)
			}
		}
		rl.mu.Unlock()
	}
}

// Middleware returns a rate limiting middleware.
func (rl *RateLimiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := getClientIP(r)
		limiter := rl.getVisitor(ip)
		if !limiter.Allow() {
			writeError(w, http.StatusTooManyRequests, "rate limit exceeded")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// getClientIP extracts the client IP from the request.
// LoginLimiter throttles password guessing. The general RateLimiter above is
// sized for API traffic (100 rps) and is no obstacle at all to an offline-speed
// guessing loop, so sign-in gets its own much stricter budget, counted per
// client IP and per submitted email — one attacker shouldn't be able to lock
// everyone out, and one address shouldn't be attackable from many IPs.
type LoginLimiter struct {
	mu       sync.Mutex
	attempts map[string]*loginAttempts
	max      int
	window   time.Duration
}

type loginAttempts struct {
	count int
	first time.Time
}

// NewLoginLimiter allows max attempts per key per window.
func NewLoginLimiter(max int, window time.Duration) *LoginLimiter {
	l := &LoginLimiter{
		attempts: make(map[string]*loginAttempts),
		max:      max,
		window:   window,
	}
	go l.cleanup()
	return l
}

// Allow records an attempt against every supplied key and reports whether it
// may proceed. Keys are checked before any is incremented, so a rejected
// attempt doesn't deepen the hole for an unrelated key.
func (l *LoginLimiter) Allow(keys ...string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()

	for _, k := range keys {
		if a, ok := l.attempts[k]; ok && now.Sub(a.first) < l.window && a.count >= l.max {
			return false
		}
	}
	for _, k := range keys {
		a, ok := l.attempts[k]
		if !ok || now.Sub(a.first) >= l.window {
			l.attempts[k] = &loginAttempts{count: 1, first: now}
			continue
		}
		a.count++
	}
	return true
}

// Reset clears the counters for a key. Called on a successful sign-in so a
// few typos don't leave someone locked out afterwards.
func (l *LoginLimiter) Reset(keys ...string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, k := range keys {
		delete(l.attempts, k)
	}
}

func (l *LoginLimiter) cleanup() {
	for {
		time.Sleep(l.window)
		l.mu.Lock()
		now := time.Now()
		for k, a := range l.attempts {
			if now.Sub(a.first) >= l.window {
				delete(l.attempts, k)
			}
		}
		l.mu.Unlock()
	}
}

func getClientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if idx := strings.Index(xff, ","); idx != -1 {
			return strings.TrimSpace(xff[:idx])
		}
		return strings.TrimSpace(xff)
	}
	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		return xri
	}
	ip := r.RemoteAddr
	if idx := strings.LastIndex(ip, ":"); idx != -1 {
		ip = ip[:idx]
	}
	return ip
}

// securityHeadersMiddleware adds security headers to all responses.
func securityHeadersMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("X-XSS-Protection", "1; mode=block")
		// Strict CSP: this is a JSON API plus an optional static SPA we serve
		// from ./web/dist. No inline scripts — script-src stays 'self', which is
		// the boundary that actually stops injected markup from executing.
		//
		// style-src allows 'unsafe-inline' because the admin UI's dialog
		// primitives (Radix, via react-remove-scroll-bar) inject a <style>
		// element to lock background scrolling. Without it that fails silently:
		// no exception, the page just scrolls behind every modal. The residual
		// risk is CSS-based exfiltration, which already requires the ability to
		// inject markup that script-src forbids.
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; object-src 'none'; base-uri 'self'; frame-ancestors 'none'")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		w.Header().Set("Permissions-Policy", "geolocation=(), microphone=(), camera=()")
		next.ServeHTTP(w, r)
	})
}

// corsMiddleware adds CORS headers based on allowed origins.
func corsMiddleware(allowedOrigins []string) func(http.Handler) http.Handler {
	originsSet := make(map[string]bool)
	allowAll := false
	for _, origin := range allowedOrigins {
		if origin == "*" {
			allowAll = true
			break
		}
		originsSet[origin] = true
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			if origin != "" {
				if allowAll {
					w.Header().Set("Access-Control-Allow-Origin", "*")
				} else if originsSet[origin] {
					w.Header().Set("Access-Control-Allow-Origin", origin)
					w.Header().Set("Vary", "Origin")
				}
			}
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Accept, Authorization, Content-Type, X-Request-ID")
			w.Header().Set("Access-Control-Max-Age", "86400")
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// statusRecorder wraps http.ResponseWriter to capture the status code so the
// audit log can include it.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (sr *statusRecorder) WriteHeader(code int) {
	sr.status = code
	sr.ResponseWriter.WriteHeader(code)
}

// auditLogMiddleware emits one structured log line per request once it
// completes. Authenticated requests include the token prefix; anonymous ones
// (health check) omit it.
func auditLogMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		slot := &auditSlot{}
		next.ServeHTTP(rec, r.WithContext(contextWithAuditSlot(r.Context(), slot)))
		dur := time.Since(start)

		prefix := "-"
		scopes := "-"
		if info := slot.info; info != nil {
			if info.Display != "" {
				prefix = info.Display
			}
			if len(info.Scopes) > 0 {
				scopes = strings.Join(info.Scopes, ",")
			}
		}
		log.Printf("audit method=%s path=%s status=%d duration=%s ip=%s token=%s scopes=%s",
			r.Method, r.URL.Path, rec.status, dur, getClientIP(r), prefix, scopes)
	})
}

// Request deadlines.
//
// Almost every route here is a metadata lookup or a short DDL statement, and
// a minute is already generous for those. Two routes are not: POST
// .../backups runs pg_dump and streams its output into the bucket, and POST
// .../backups/{id}/restore streams an object back through pg_restore. Both
// wait synchronously for a whole database to move, which on a real database
// takes far longer than a minute, so applying the ordinary deadline to them
// meant `pgmanager db backup` and the admin UI's Back up / Restore buttons
// could not succeed at all beyond a trivial dataset.
//
// The remedy stays deliberately small: a longer deadline on exactly those
// two routes, matched by a longer client-side deadline in
// internal/client/http.go. Turning backup and restore into asynchronous jobs
// would be a different feature with a different CLI and UI contract.
const (
	defaultRequestTimeout = 60 * time.Second
	backupRequestTimeout  = 60 * time.Minute
)

// isLongRunningRequest reports whether a request is one of the two that wait
// on pg_dump/pg_restore.
//
// It matches the raw path rather than a chi route pattern because this
// middleware runs before the router has matched anything — chi's
// RouteContext is still empty at that point. The shapes matched are
// POST /api/projects/{name}/databases/{env}/backups and
// POST /api/projects/{name}/databases/{env}/backups/{id}/restore; nothing
// else in the route table ends in /backups or /restore.
func isLongRunningRequest(method, path string) bool {
	if method != http.MethodPost {
		return false
	}
	if !strings.HasPrefix(path, "/api/projects/") || !strings.Contains(path, "/databases/") {
		return false
	}
	path = strings.TrimSuffix(path, "/")
	return strings.HasSuffix(path, "/backups") || strings.HasSuffix(path, "/restore")
}

// requestTimeoutMiddleware bounds every request, giving the two backup
// routes a much longer budget than the rest.
//
// It replaces chi's middleware.Timeout, which can only apply one duration to
// a whole router: because a derived context can only ever shorten its
// parent's deadline, a per-route Timeout nested inside a global one could
// not have lengthened it.
func requestTimeoutMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		timeout := defaultRequestTimeout
		if isLongRunningRequest(r.Method, r.URL.Path) {
			timeout = backupRequestTimeout
			// The listener's own write deadline (http.Server.WriteTimeout in
			// Start) is set per connection and is far shorter than a dump,
			// so it would tear the connection down long before the handler
			// finished no matter what the context said. Push it out for
			// exactly these requests. A failure here is not fatal: it only
			// means the short deadline stays, which is the behaviour we
			// already had.
			// http.ErrNotSupported is not worth a line: it is what any
			// ResponseWriter that isn't the real server's returns — an
			// httptest recorder, for one — and those have no write deadline
			// to extend in the first place.
			if err := http.NewResponseController(w).SetWriteDeadline(time.Now().Add(timeout)); err != nil &&
				!errors.Is(err, http.ErrNotSupported) {
				log.Printf("WARN [%s %s]: could not extend the write deadline for a long-running request: %v",
					r.Method, r.URL.Path, err)
			}
		}

		ctx, cancel := context.WithTimeout(r.Context(), timeout)
		defer cancel()
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
