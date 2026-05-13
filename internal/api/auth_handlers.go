package api

import (
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

	var expiresAt *time.Time
	if req.Expires != "" {
		d, err := parseDuration(req.Expires)
		if err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid expires duration: %v", err))
			return
		}
		t := time.Now().Add(d)
		expiresAt = &t
	}

	creator := "unknown"
	if info := AuthFromContext(r.Context()); info != nil {
		creator = info.Display
	}

	plain, hash, prefix, err := auth.GenerateToken()
	if err != nil {
		writeInternalError(w, "generate token", err)
		return
	}
	tok := &meta.Token{
		Name:        req.Name,
		TokenHash:   hash,
		TokenPrefix: prefix,
		Scopes:      req.Scopes,
		ExpiresAt:   expiresAt,
		CreatedBy:   creator,
	}
	if err := s.store.CreateToken(r.Context(), tok); err != nil {
		writeInternalError(w, "store token", err)
		return
	}
	writeJSON(w, http.StatusCreated, CreateTokenResponse{
		Token:       plain,
		TokenPrefix: prefix,
		Info:        tokenView(*tok),
	})
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
