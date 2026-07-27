package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"pgmanager/internal/auth"
	"pgmanager/internal/meta"
)

// WhoamiResponse describes the authenticated principal back to the caller.
type WhoamiResponse struct {
	TokenPrefix string   `json:"token_prefix"`
	Scopes      []string `json:"scopes"`
}

// TokenResponse is the public view of a token (no secret material).
type TokenResponse struct {
	Name        string   `json:"name"`
	TokenPrefix string   `json:"token_prefix"`
	Scopes      []string `json:"scopes"`
	CreatedAt   string   `json:"created_at"`
	ExpiresAt   *string  `json:"expires_at,omitempty"`
	LastUsedAt  *string  `json:"last_used_at,omitempty"`
	CreatedBy   string   `json:"created_by,omitempty"`
	RevokedAt   *string  `json:"revoked_at,omitempty"`
}

// CreateTokenRequest is the body for POST /auth/tokens.
type CreateTokenRequest struct {
	Name    string   `json:"name"`
	Scopes  []string `json:"scopes"`
	Expires string   `json:"expires,omitempty"` // duration like "90d", or empty for no expiry
}

// CreateTokenResponse includes the plaintext token (only ever returned here).
type CreateTokenResponse struct {
	Token       string        `json:"token"`
	TokenPrefix string        `json:"token_prefix"`
	Info        TokenResponse `json:"info"`
}

func (s *Server) whoami(w http.ResponseWriter, r *http.Request) {
	info := AuthFromContext(r.Context())
	if info == nil {
		writeError(w, http.StatusUnauthorized, "missing authentication")
		return
	}
	writeJSON(w, http.StatusOK, WhoamiResponse{
		TokenPrefix: info.Display,
		Scopes:      info.Scopes,
	})
}

func (s *Server) listTokens(w http.ResponseWriter, r *http.Request) {
	// Listing tokens is privileged. Allow admin or a dedicated "tokens" scope.
	if !requireScope(w, r, auth.ScopeRequest{Resource: "token"}) {
		return
	}
	toks, err := s.store.ListTokens(r.Context())
	if err != nil {
		writeInternalError(w, "listTokens", err)
		return
	}
	out := make([]TokenResponse, 0, len(toks))
	for _, t := range toks {
		out = append(out, tokenView(t))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) createToken(w http.ResponseWriter, r *http.Request) {
	if !requireScope(w, r, auth.ScopeRequest{Resource: "token"}) {
		return
	}

	var req CreateTokenRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	if err := auth.ValidateScopes(req.Scopes); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	expiresAt, err := parseExpires(req.Expires)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	plain, tok, err := s.issueToken(r.Context(), req.Name, req.Scopes, expiresAt, creatorOf(r))
	if err != nil {
		writeInternalError(w, "issue token", err)
		return
	}
	writeJSON(w, http.StatusCreated, CreateTokenResponse{
		Token:       plain,
		TokenPrefix: tok.TokenPrefix,
		Info:        tokenView(*tok),
	})
}

// parseExpires turns an optional duration string ("90d") into an absolute
// expiry. An empty string means "never expires".
func parseExpires(expires string) (*time.Time, error) {
	if expires == "" {
		return nil, nil
	}
	d, err := parseDuration(expires)
	if err != nil {
		return nil, fmt.Errorf("invalid expires duration: %v", err)
	}
	t := time.Now().Add(d)
	return &t, nil
}

// creatorOf names the principal a token should be attributed to.
func creatorOf(r *http.Request) string {
	if info := AuthFromContext(r.Context()); info != nil {
		return info.Display
	}
	return "unknown"
}

// issueToken generates, stores and returns a token. Callers are responsible
// for validating name and scopes first. Shared by POST /auth/tokens and
// device-flow approval so both mint tokens identically.
func (s *Server) issueToken(ctx context.Context, name string, scopes []string, expiresAt *time.Time, creator string) (string, *meta.Token, error) {
	plain, hash, prefix, err := auth.GenerateToken()
	if err != nil {
		return "", nil, fmt.Errorf("generate token: %w", err)
	}
	tok := &meta.Token{
		Name:        name,
		TokenHash:   hash,
		TokenPrefix: prefix,
		Scopes:      scopes,
		ExpiresAt:   expiresAt,
		CreatedBy:   creator,
	}
	if err := s.store.CreateToken(ctx, tok); err != nil {
		return "", nil, fmt.Errorf("store token: %w", err)
	}
	return plain, tok, nil
}

func (s *Server) revokeToken(w http.ResponseWriter, r *http.Request) {
	if !requireScope(w, r, auth.ScopeRequest{Resource: "token"}) {
		return
	}
	prefix := chi.URLParam(r, "prefix")
	if err := s.store.RevokeToken(r.Context(), prefix); err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func tokenView(t meta.Token) TokenResponse {
	var expires, lastUsed, revoked *string
	if t.ExpiresAt != nil {
		s := t.ExpiresAt.Format(time.RFC3339)
		expires = &s
	}
	if t.LastUsedAt != nil {
		s := t.LastUsedAt.Format(time.RFC3339)
		lastUsed = &s
	}
	if t.RevokedAt != nil {
		s := t.RevokedAt.Format(time.RFC3339)
		revoked = &s
	}
	return TokenResponse{
		Name:        t.Name,
		TokenPrefix: t.TokenPrefix,
		Scopes:      t.Scopes,
		CreatedAt:   t.CreatedAt.Format(time.RFC3339),
		ExpiresAt:   expires,
		LastUsedAt:  lastUsed,
		CreatedBy:   t.CreatedBy,
		RevokedAt:   revoked,
	}
}
