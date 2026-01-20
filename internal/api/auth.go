package api

import (
	"crypto/subtle"
	"net/http"
	"strings"
)

// authMiddleware validates the Bearer token in the Authorization header
func (s *Server) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Skip auth for health check
		if r.URL.Path == "/api/health" {
			next.ServeHTTP(w, r)
			return
		}

		// Get the Authorization header
		authHeader := r.Header.Get("Authorization")
		if authHeader == "" {
			writeError(w, http.StatusUnauthorized, "missing authorization header")
			return
		}

		// Check for Bearer prefix
		if !strings.HasPrefix(authHeader, "Bearer ") {
			writeError(w, http.StatusUnauthorized, "invalid authorization header format")
			return
		}

		// Extract and validate token
		token := strings.TrimPrefix(authHeader, "Bearer ")
		valid, tokenNotConfigured := validateToken(token, s.cfg.API.Token, s.cfg.API.RequireToken)
		if tokenNotConfigured {
			writeError(w, http.StatusServiceUnavailable, "API token not configured")
			return
		}
		if !valid {
			writeError(w, http.StatusUnauthorized, "invalid token")
			return
		}

		next.ServeHTTP(w, r)
	})
}

// validateToken performs constant-time comparison of tokens to prevent timing attacks
// Returns: (valid bool, tokenRequired bool)
func validateToken(provided, expected string, requireToken bool) (bool, bool) {
	if expected == "" {
		if requireToken {
			// Token is required but not configured - reject all requests
			return false, true
		}
		// No token configured and not required - allow all requests
		return true, false
	}
	return subtle.ConstantTimeCompare([]byte(provided), []byte(expected)) == 1, false
}
