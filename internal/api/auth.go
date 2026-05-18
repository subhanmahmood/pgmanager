package api

import (
	"context"
	"crypto/subtle"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"pgmanager/internal/auth"
)

// authInfoKey is the context key for the authenticated principal.
type authInfoKey struct{}

// AuthInfo describes the authenticated principal for a request.
type AuthInfo struct {
	TokenID int64  // 0 for legacy/anonymous
	Display string // token prefix or "legacy"
	Scopes  []string
}

func contextWithAuth(ctx context.Context, info *AuthInfo) context.Context {
	return context.WithValue(ctx, authInfoKey{}, info)
}

// AuthFromContext returns the AuthInfo attached by authMiddleware, or nil if
// the request was not authenticated (health checks, anonymous calls).
func AuthFromContext(ctx context.Context) *AuthInfo {
	if v, ok := ctx.Value(authInfoKey{}).(*AuthInfo); ok {
		return v
	}
	return nil
}

// authMiddleware validates the Bearer token and attaches AuthInfo to the
// request context. Health checks pass through unauthenticated.
func (s *Server) authMiddleware(next http.Handler) http.Handler {
	var legacyWarnOnce sync.Once
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/health" {
			next.ServeHTTP(w, r)
			return
		}

		h := r.Header.Get("Authorization")
		if h == "" {
			writeError(w, http.StatusUnauthorized, "missing authorization header")
			return
		}
		if !strings.HasPrefix(h, "Bearer ") {
			writeError(w, http.StatusUnauthorized, "invalid authorization header format")
			return
		}
		plain := strings.TrimPrefix(h, "Bearer ")

		// Legacy single-token fallback (PGMANAGER_API_TOKEN). Granted full
		// admin scope; will be removed in a future release.
		if s.cfg.API.Token != "" && subtle.ConstantTimeCompare([]byte(plain), []byte(s.cfg.API.Token)) == 1 {
			legacyWarnOnce.Do(func() {
				log.Printf("DEPRECATED: PGMANAGER_API_TOKEN is in use; migrate to scoped tokens via `pgmanager auth create-token`")
			})
			ctx := contextWithAuth(r.Context(), &AuthInfo{Display: "legacy", Scopes: []string{auth.ScopeAdmin}})
			next.ServeHTTP(w, r.WithContext(ctx))
			return
		}

		hash := auth.HashToken(plain)
		tok, err := s.store.GetTokenByHash(r.Context(), hash)
		if err != nil {
			writeInternalError(w, "token lookup", err)
			return
		}
		if tok == nil || !tok.Active(time.Now()) {
			writeError(w, http.StatusUnauthorized, "invalid token")
			return
		}
		// Update last_used_at without blocking the request.
		go func(id int64) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = s.store.TouchToken(ctx, id, time.Now())
		}(tok.ID)

		ctx := contextWithAuth(r.Context(), &AuthInfo{
			TokenID: tok.ID,
			Display: tok.TokenPrefix,
			Scopes:  tok.Scopes,
		})
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// requireScope is a per-handler helper that authorizes the request or writes
// a 403 and returns false.
func requireScope(w http.ResponseWriter, r *http.Request, req auth.ScopeRequest) bool {
	info := AuthFromContext(r.Context())
	if info == nil {
		writeError(w, http.StatusUnauthorized, "missing authentication")
		return false
	}
	if err := auth.Authorize(info.Scopes, req); err != nil {
		writeError(w, http.StatusForbidden, "insufficient scope")
		return false
	}
	return true
}
