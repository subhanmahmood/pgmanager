package api

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"time"

	"github.com/go-chi/chi/v5"
	"pgmanager/internal/auth"
	"pgmanager/internal/meta"
)

// These handlers are registered ONLY on the unix-socket router (see
// buildRouter). The allowlist of humans who can sign in is the root of trust
// for the admin UI, so it is writable only from the server itself: off-box
// these routes do not exist and return 404, which no token — leaked,
// over-scoped or admin — can change.

// UserResponse is the public view of an allowlisted human.
type UserResponse struct {
	Email       string  `json:"email"`
	CreatedAt   string  `json:"created_at"`
	CreatedBy   string  `json:"created_by,omitempty"`
	LastLoginAt *string `json:"last_login_at,omitempty"`
	Disabled    bool    `json:"disabled"`
}

// CreateUserRequest is the body for POST /users. An omitted password means
// "generate one and show it to me once".
type CreateUserRequest struct {
	Email    string `json:"email"`
	Password string `json:"password,omitempty"`
}

// CreateUserResponse carries the generated password, if we made one. It is
// never shown again.
type CreateUserResponse struct {
	User     UserResponse `json:"user"`
	Password string       `json:"password,omitempty"`
}

// SetPasswordRequest is the body for POST /users/{email}/password.
type SetPasswordRequest struct {
	Password string `json:"password,omitempty"`
}

func (s *Server) listUsers(w http.ResponseWriter, r *http.Request) {
	users, err := s.store.ListUsers(r.Context())
	if err != nil {
		writeInternalError(w, "list users", err)
		return
	}
	out := make([]UserResponse, 0, len(users))
	for _, u := range users {
		out = append(out, userView(u))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) createUser(w http.ResponseWriter, r *http.Request) {
	var req CreateUserRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	email := auth.NormalizeEmail(req.Email)
	if !auth.ValidEmail(email) {
		writeError(w, http.StatusBadRequest, "a valid email address is required")
		return
	}

	password, generated, err := resolvePassword(req.Password)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	user := &meta.User{
		Email:        email,
		PasswordHash: hash,
		CreatedBy:    creatorOf(r),
	}
	if err := s.store.CreateUser(r.Context(), user); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	log.Printf("users: %s added %s", creatorOf(r), email)

	resp := CreateUserResponse{User: userView(*user)}
	if generated {
		resp.Password = password
	}
	writeJSON(w, http.StatusCreated, resp)
}

func (s *Server) setUserPassword(w http.ResponseWriter, r *http.Request) {
	email, ok := userEmailParam(w, r)
	if !ok {
		return
	}

	var req SetPasswordRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	password, generated, err := resolvePassword(req.Password)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	// Also drops the user's existing sessions — a reset exists precisely for
	// the case where you don't trust what is currently signed in.
	if err := s.store.SetUserPassword(r.Context(), email, hash); err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	log.Printf("users: %s reset the password for %s", creatorOf(r), email)

	resp := CreateUserResponse{User: UserResponse{Email: email}}
	if generated {
		resp.Password = password
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) deleteUser(w http.ResponseWriter, r *http.Request) {
	email, ok := userEmailParam(w, r)
	if !ok {
		return
	}
	if err := s.store.DeleteUser(r.Context(), email); err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	log.Printf("users: %s removed %s", creatorOf(r), email)
	w.WriteHeader(http.StatusNoContent)
}

// resolvePassword returns the password to use and whether we generated it.
func resolvePassword(supplied string) (string, bool, error) {
	if supplied != "" {
		if len(supplied) < auth.MinPasswordLen {
			return "", false, fmt.Errorf("password must be at least %d characters", auth.MinPasswordLen)
		}
		return supplied, false, nil
	}
	generated, err := auth.GeneratePassword()
	if err != nil {
		return "", false, err
	}
	return generated, true, nil
}

func userEmailParam(w http.ResponseWriter, r *http.Request) (string, bool) {
	raw, err := url.PathUnescape(chi.URLParam(r, "email"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid email in path")
		return "", false
	}
	email := auth.NormalizeEmail(raw)
	if !auth.ValidEmail(email) {
		writeError(w, http.StatusBadRequest, "invalid email in path")
		return "", false
	}
	return email, true
}

func userView(u meta.User) UserResponse {
	var lastLogin *string
	if u.LastLoginAt != nil {
		s := u.LastLoginAt.Format(time.RFC3339)
		lastLogin = &s
	}
	return UserResponse{
		Email:       u.Email,
		CreatedAt:   u.CreatedAt.Format(time.RFC3339),
		CreatedBy:   u.CreatedBy,
		LastLoginAt: lastLogin,
		Disabled:    !u.Active(),
	}
}
