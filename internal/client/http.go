package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// HTTPClient talks to a remote `pgmanager serve` instance over HTTPS.
type HTTPClient struct {
	baseURL string
	token   string
	http    *http.Client
}

// NewHTTP creates a new HTTPClient. baseURL should be the scheme+host
// (e.g., "https://pgm.example.com"); /api is added automatically.
func NewHTTP(baseURL, token string) *HTTPClient {
	return &HTTPClient{
		baseURL: strings.TrimRight(baseURL, "/"),
		token:   token,
		http: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

func (c *HTTPClient) Close() error { return nil }

// APIError is returned for non-2xx responses.
type APIError struct {
	Status  int
	Message string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("pgmanager api: %d %s", e.Status, e.Message)
}

func (c *HTTPClient) do(ctx context.Context, method, path string, body, into interface{}) error {
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal: %w", err)
		}
		reader = bytes.NewReader(b)
	}
	u := c.baseURL + "/api" + path
	req, err := http.NewRequestWithContext(ctx, method, u, reader)
	if err != nil {
		return err
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("request %s %s: %w", method, u, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		var er struct {
			Error string `json:"error"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&er)
		msg := er.Error
		if msg == "" {
			msg = resp.Status
		}
		return &APIError{Status: resp.StatusCode, Message: msg}
	}
	if into != nil {
		if err := json.NewDecoder(resp.Body).Decode(into); err != nil && err != io.EOF {
			return fmt.Errorf("decode: %w", err)
		}
	}
	return nil
}

func (c *HTTPClient) CreateProject(ctx context.Context, name string) error {
	return c.do(ctx, http.MethodPost, "/projects", map[string]string{"name": name}, nil)
}

func (c *HTTPClient) ListProjects(ctx context.Context) ([]Project, error) {
	var out []Project
	if err := c.do(ctx, http.MethodGet, "/projects", nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *HTTPClient) DeleteProject(ctx context.Context, name string) error {
	return c.do(ctx, http.MethodDelete, "/projects/"+url.PathEscape(name), nil, nil)
}

func (c *HTTPClient) CreateDatabase(ctx context.Context, projectName, env string, prNumber *int) (*Database, error) {
	body := map[string]interface{}{"env": env}
	if prNumber != nil {
		body["pr_number"] = *prNumber
	}
	var out Database
	if err := c.do(ctx, http.MethodPost, "/projects/"+url.PathEscape(projectName)+"/databases", body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *HTTPClient) GetDatabase(ctx context.Context, projectName, env string, prNumber *int) (*Database, error) {
	path := dbPath(projectName, env, prNumber)
	var out Database
	if err := c.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *HTTPClient) GetDatabaseCredentials(ctx context.Context, projectName, env string, prNumber *int) (*Database, error) {
	path := dbPath(projectName, env, prNumber) + "/credentials"
	var out Database
	if err := c.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *HTTPClient) ListDatabases(ctx context.Context, projectName string) ([]Database, error) {
	var out []Database
	if err := c.do(ctx, http.MethodGet, "/projects/"+url.PathEscape(projectName)+"/databases", nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *HTTPClient) DeleteDatabase(ctx context.Context, projectName, env string, prNumber *int) error {
	return c.do(ctx, http.MethodDelete, dbPath(projectName, env, prNumber), nil, nil)
}

func (c *HTTPClient) Cleanup(ctx context.Context, olderThan time.Duration) ([]string, error) {
	type resp struct {
		Deleted []string `json:"deleted"`
		Count   int      `json:"count"`
	}
	var r resp
	body := map[string]string{"older_than": formatDuration(olderThan)}
	if err := c.do(ctx, http.MethodPost, "/cleanup", body, &r); err != nil {
		return nil, err
	}
	return r.Deleted, nil
}

func (c *HTTPClient) Whoami(ctx context.Context) (*Whoami, error) {
	var out Whoami
	if err := c.do(ctx, http.MethodGet, "/auth/whoami", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *HTTPClient) ListTokens(ctx context.Context) ([]Token, error) {
	var out []Token
	if err := c.do(ctx, http.MethodGet, "/auth/tokens", nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *HTTPClient) CreateToken(ctx context.Context, name string, scopes []string, expires string) (string, *Token, error) {
	body := map[string]interface{}{"name": name, "scopes": scopes}
	if expires != "" {
		body["expires"] = expires
	}
	type resp struct {
		Token string `json:"token"`
		Info  Token  `json:"info"`
	}
	var r resp
	if err := c.do(ctx, http.MethodPost, "/auth/tokens", body, &r); err != nil {
		return "", nil, err
	}
	return r.Token, &r.Info, nil
}

func (c *HTTPClient) RevokeToken(ctx context.Context, prefix string) error {
	return c.do(ctx, http.MethodDelete, "/auth/tokens/"+url.PathEscape(prefix), nil, nil)
}

func dbPath(projectName, env string, prNumber *int) string {
	envSeg := env
	if env == "pr" && prNumber != nil {
		envSeg = fmt.Sprintf("pr_%d", *prNumber)
	}
	return "/projects/" + url.PathEscape(projectName) + "/databases/" + envSeg
}

func formatDuration(d time.Duration) string {
	if d%(24*time.Hour) == 0 {
		return fmt.Sprintf("%dd", int(d/(24*time.Hour)))
	}
	if d%time.Hour == 0 {
		return fmt.Sprintf("%dh", int(d/time.Hour))
	}
	if d%time.Minute == 0 {
		return fmt.Sprintf("%dm", int(d/time.Minute))
	}
	return fmt.Sprintf("%ds", int(d/time.Second))
}
