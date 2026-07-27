package api

import (
	"context"
	"crypto/subtle"
	"log"
	"net"
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

// auditSlot is a mutable box the audit middleware puts in the request context
// before authentication runs. Auth middleware sits *inside* audit middleware,
// so the context it derives is invisible to the outer frame — without this
// hand-back, every audit line would say the request was anonymous.
type auditSlot struct{ info *AuthInfo }

type auditSlotKey struct{}

func contextWithAuditSlot(ctx context.Context, slot *auditSlot) context.Context {
	return context.WithValue(ctx, auditSlotKey{}, slot)
}

func contextWithAuth(ctx context.Context, info *AuthInfo) context.Context {
	if slot, ok := ctx.Value(auditSlotKey{}).(*auditSlot); ok {
		slot.info = info
	}
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

// peerCredKey is the context key carrying the unix-socket peer identity.
type peerCredKey struct{}

// withPeerCred is the http.Server ConnContext hook for the socket listener.
// It stashes the peer's identity so localAuthMiddleware can name it in the
// audit log.
func withPeerCred(ctx context.Context, c net.Conn) context.Context {
	return context.WithValue(ctx, peerCredKey{}, peerIdentity(c))
}

// isSocketRequest reports whether the request arrived over the local admin
// socket. Only the socket listener installs a peer credential.
func isSocketRequest(ctx context.Context) bool {
	_, ok := ctx.Value(peerCredKey{}).(string)
	return ok
}

func peerCredFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(peerCredKey{}).(string); ok && v != "" {
		return v
	}
	return "local"
}

// anonymousPaths are reachable without any credential. The device-flow start
// and poll endpoints are here because their whole purpose is to serve callers
// who do not have a token yet; the device code itself is the secret.
var anonymousPaths = map[string]bool{
	"/api/health":            true,
	"/api/auth/device":       true,
	"/api/auth/device/token": true,
}

// authMiddleware validates the Bearer token and attaches AuthInfo to the
// request context. Health checks and the device-flow entry points pass
// through unauthenticated.
func (s *Server) authMiddleware(next http.Handler) http.Handler {
	var legacyWarnOnce sync.Once
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if anonymousPaths[r.URL.Path] {
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

// localAuthMiddleware authenticates requests arriving over the local unix
// socket. Reaching this listener at all means the caller already satisfied
// the socket's file permissions, which is the authorization decision — so
// they are granted admin without presenting a token. The peer's uid/pid is
// recorded so the audit log still says who did what.
func (s *Server) localAuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := contextWithAuth(r.Context(), &AuthInfo{
			Display: peerCredFromContext(r.Context()),
			Scopes:  []string{auth.ScopeAdmin},
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
