package api

import (
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"time"

	"pgmanager/internal/auth"
	"pgmanager/internal/meta"
)

// LoginRequest is the body for POST /auth/login.
type LoginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// SessionResponse describes the signed-in human back to the browser.
type SessionResponse struct {
	Email     string `json:"email"`
	ExpiresAt string `json:"expires_at"`
}

// ChangePasswordRequest is the body for POST /auth/password.
type ChangePasswordRequest struct {
	Current string `json:"current"`
	New     string `json:"new"`
}

// invalidLogin is deliberately the same for a bad password, an unknown
// address and a disabled account: the sign-in page must not become a way to
// enumerate who is on the allowlist.
const invalidLogin = "invalid email or password"

func (s *Server) sessionTTL() time.Duration {
	if s.cfg.API.SessionTTL > 0 {
		return s.cfg.API.SessionTTL
	}
	return auth.DefaultSessionTTL
}

// login exchanges an allowlisted email and password for a session cookie.
// Unauthenticated by design — it is how you become authenticated.
func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var req LoginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	email := auth.NormalizeEmail(req.Email)

	// Throttle before doing any work: argon2 is deliberately expensive, so
	// unbounded attempts are a denial-of-service lever as well as a guessing one.
	ipKey, emailKey := "ip:"+getClientIP(r), "email:"+email
	if !s.loginLimiter.Allow(ipKey, emailKey) {
		writeError(w, http.StatusTooManyRequests, "too many sign-in attempts; try again later")
		return
	}

	user, err := s.store.GetUserByEmail(r.Context(), email)
	if err != nil {
		writeInternalError(w, "lookup user", err)
		return
	}

	// Always spend the same work, whether or not the address exists, so
	// response time doesn't reveal which addresses are on the list.
	hash := auth.DummyHash
	if user != nil && user.Active() {
		hash = user.PasswordHash
	}
	ok := auth.VerifyPassword(hash, req.Password)
	if user == nil || !user.Active() || !ok {
		writeError(w, http.StatusUnauthorized, invalidLogin)
		return
	}

	s.loginLimiter.Reset(ipKey, emailKey)

	sess, plain, err := s.newSession(r, user)
	if err != nil {
		writeInternalError(w, "create session", err)
		return
	}
	if err := s.store.TouchUserLogin(r.Context(), user.ID, time.Now()); err != nil {
		log.Printf("session: touch login for %s: %v", user.Email, err)
	}
	log.Printf("session: %s signed in from %s", user.Email, getClientIP(r))

	http.SetCookie(w, s.sessionCookie(r, plain, sess.ExpiresAt))
	writeJSON(w, http.StatusOK, SessionResponse{
		Email:     user.Email,
		ExpiresAt: sess.ExpiresAt.Format(time.RFC3339),
	})
}

// logout drops the session server-side and clears the cookie. Deleting the
// row is what actually ends access — clearing the cookie is a courtesy.
func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(auth.SessionCookieName); err == nil && c.Value != "" {
		if err := s.store.DeleteSession(r.Context(), auth.HashToken(c.Value)); err != nil {
			writeInternalError(w, "delete session", err)
			return
		}
	}
	clear := s.sessionCookie(r, "", time.Unix(0, 0))
	clear.MaxAge = -1
	http.SetCookie(w, clear)
	w.WriteHeader(http.StatusNoContent)
}

// changePassword lets a signed-in human rotate their own password. Requires
// the current one, so a borrowed session can't lock the owner out.
func (s *Server) changePassword(w http.ResponseWriter, r *http.Request) {
	info := AuthFromContext(r.Context())
	if info == nil || !strings.Contains(info.Display, "@") {
		writeError(w, http.StatusForbidden, "only a signed-in user can change a password")
		return
	}

	var req ChangePasswordRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	user, err := s.store.GetUserByEmail(r.Context(), info.Display)
	if err != nil {
		writeInternalError(w, "lookup user", err)
		return
	}
	if user == nil || !auth.VerifyPassword(user.PasswordHash, req.Current) {
		writeError(w, http.StatusUnauthorized, "current password is incorrect")
		return
	}

	hash, err := auth.HashPassword(req.New)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	// SetUserPassword drops every existing session for this user, including
	// this one — so the browser has to sign in again with the new password.
	if err := s.store.SetUserPassword(r.Context(), user.Email, hash); err != nil {
		writeInternalError(w, "set password", err)
		return
	}
	log.Printf("session: %s changed their password", user.Email)

	clear := s.sessionCookie(r, "", time.Unix(0, 0))
	clear.MaxAge = -1
	http.SetCookie(w, clear)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) newSession(r *http.Request, user *meta.User) (*meta.Session, string, error) {
	plain, hash, err := auth.GenerateSessionToken()
	if err != nil {
		return nil, "", err
	}
	sess := &meta.Session{
		TokenHash: hash,
		UserID:    user.ID,
		Email:     user.Email,
		ExpiresAt: time.Now().Add(s.sessionTTL()),
		CreatedIP: getClientIP(r),
	}
	if err := s.store.CreateSession(r.Context(), sess); err != nil {
		return nil, "", err
	}
	return sess, plain, nil
}

// sessionCookie builds the session cookie.
//
// SameSite=Lax is the CSRF defence: the browser withholds this cookie on
// cross-site POST/DELETE, so another origin cannot make state-changing calls
// on the user's behalf. That is why there is no separate CSRF token.
func (s *Server) sessionCookie(r *http.Request, value string, expires time.Time) *http.Cookie {
	return &http.Cookie{
		Name:     auth.SessionCookieName,
		Value:    value,
		Path:     "/",
		Expires:  expires,
		HttpOnly: true,
		Secure:   requestIsTLS(r),
		SameSite: http.SameSiteLaxMode,
	}
}

// requestIsTLS reports whether the browser reached us over HTTPS. Behind a
// reverse proxy the inbound hop is plain HTTP, so the forwarded header is
// what tells the truth.
func requestIsTLS(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	proto := r.Header.Get("X-Forwarded-Proto")
	return strings.EqualFold(strings.TrimSpace(strings.Split(proto, ",")[0]), "https")
}
