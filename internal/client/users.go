package client

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// ErrUsersNotLocal is returned when user management is attempted against a
// remote server. Those routes only exist on the local admin socket, so the
// server answers 404 — the allowlist of humans is deliberately only editable
// from the box itself.
var ErrUsersNotLocal = errors.New(
	"user management is only available on the server itself " +
		"(run it there, or point at the local socket with --socket)")

// User is the public view of an allowlisted human.
type User struct {
	Email       string  `json:"email"`
	CreatedAt   string  `json:"created_at"`
	CreatedBy   string  `json:"created_by,omitempty"`
	LastLoginAt *string `json:"last_login_at,omitempty"`
	Disabled    bool    `json:"disabled"`
}

// UserCreated carries the generated password, when the server made one. It is
// never retrievable afterwards.
type UserCreated struct {
	User     User   `json:"user"`
	Password string `json:"password,omitempty"`
}

func (c *HTTPClient) ListUsers(ctx context.Context) ([]User, error) {
	var out []User
	if err := c.do(ctx, http.MethodGet, "/users", nil, &out); err != nil {
		return nil, localOnly(err)
	}
	return out, nil
}

// CreateUser adds an email to the allowlist. An empty password asks the
// server to generate one and return it once.
func (c *HTTPClient) CreateUser(ctx context.Context, email, password string) (*UserCreated, error) {
	body := map[string]string{"email": email}
	if password != "" {
		body["password"] = password
	}
	var out UserCreated
	if err := c.do(ctx, http.MethodPost, "/users", body, &out); err != nil {
		return nil, localOnly(err)
	}
	return &out, nil
}

// SetUserPassword resets someone's password. This is the "forgot password"
// path: recovery is an operator action on the server, which is why pgmanager
// needs no outbound email.
func (c *HTTPClient) SetUserPassword(ctx context.Context, email, password string) (*UserCreated, error) {
	body := map[string]string{}
	if password != "" {
		body["password"] = password
	}
	var out UserCreated
	path := fmt.Sprintf("/users/%s/password", url.PathEscape(email))
	if err := c.do(ctx, http.MethodPost, path, body, &out); err != nil {
		return nil, localOnly(err)
	}
	return &out, nil
}

func (c *HTTPClient) DeleteUser(ctx context.Context, email string) error {
	return localOnly(c.do(ctx, http.MethodDelete, "/users/"+url.PathEscape(email), nil, nil))
}

// localOnly turns the 404 a remote server gives for these routes into an
// explanation, rather than a bare "not found" that looks like a bug.
//
// A missing *route* and a missing *user* are both 404s, so they have to be
// told apart: our handlers answer with a JSON error body, which `do` surfaces
// as the message, while an unrouted path falls through to chi's plain-text
// handler and leaves the message as the bare HTTP status.
func localOnly(err error) error {
	var apiErr *APIError
	if errors.As(err, &apiErr) && apiErr.Status == http.StatusNotFound &&
		(apiErr.Message == "" || strings.Contains(apiErr.Message, "404")) {
		return ErrUsersNotLocal
	}
	return err
}
