package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Client-side deadlines.
//
// requestTimeout covers every ordinary call: they are metadata lookups and
// short DDL statements, and 30 seconds is already generous.
//
// backupTimeout covers the two calls that wait on a whole database moving —
// CreateBackup runs pg_dump straight into the bucket, RestoreBackup streams
// an object back through pg_restore. Under the ordinary deadline
// `pgmanager db backup` aborted after 30 seconds no matter how healthy the
// server was, which no real database survives. It is deliberately a little
// longer than the server's own budget for those routes
// (backupRequestTimeout in internal/api/middleware.go) so that when a backup
// really does run too long, the client receives the server's error instead
// of the transport giving up first and reporting nothing useful.
const (
	requestTimeout = 30 * time.Second
	backupTimeout  = 65 * time.Minute
)

// HTTPClient talks to a remote `pgmanager serve` instance over HTTPS.
type HTTPClient struct {
	baseURL string
	token   string
	http    *http.Client
	// longHTTP uses the same transport as http — the default one, or the
	// unix-socket dialer — but carries backupTimeout instead of
	// requestTimeout. http.Client.Timeout is a per-client setting, so a
	// second client is the only way to give two calls a different budget
	// from the rest.
	longHTTP *http.Client
}

// NewHTTP creates a new HTTPClient. baseURL should be the scheme+host
// (e.g., "https://pgm.example.com"); /api is added automatically.
func NewHTTP(baseURL, token string) *HTTPClient {
	return &HTTPClient{
		baseURL:  strings.TrimRight(baseURL, "/"),
		token:    token,
		http:     &http.Client{Timeout: requestTimeout},
		longHTTP: &http.Client{Timeout: backupTimeout},
	}
}

// NewUnix creates a client that talks to a `pgmanager serve` listening on a
// local unix socket. There is no token: reaching the socket at all is the
// credential, and the server grants admin on that basis.
func NewUnix(socketPath string) *HTTPClient {
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", socketPath)
		},
	}
	return &HTTPClient{
		// The host in the URL is never resolved — the dialer ignores it — but
		// net/http still requires a well-formed one.
		baseURL:  "http://pgmanager.local",
		http:     &http.Client{Timeout: requestTimeout, Transport: transport},
		longHTTP: &http.Client{Timeout: backupTimeout, Transport: transport},
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
	return c.doWith(ctx, c.http, method, path, body, into)
}

// doLong is do for the two requests that wait on pg_dump or pg_restore. See
// backupTimeout.
func (c *HTTPClient) doLong(ctx context.Context, method, path string, body, into interface{}) error {
	return c.doWith(ctx, c.longHTTP, method, path, body, into)
}

func (c *HTTPClient) doWith(ctx context.Context, httpClient *http.Client, method, path string, body, into interface{}) error {
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
	resp, err := httpClient.Do(req)
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

func (c *HTTPClient) CreateDatabase(ctx context.Context, projectName, env string, prNumber *int, extensions []string) (*Database, error) {
	body := map[string]interface{}{"env": env}
	if prNumber != nil {
		body["pr_number"] = *prNumber
	}
	if len(extensions) > 0 {
		body["extensions"] = extensions
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

func (c *HTTPClient) RotatePassword(ctx context.Context, projectName, env string, prNumber *int, terminate bool) (*Database, error) {
	path := dbPath(projectName, env, prNumber) + "/rotate"
	body := map[string]bool{"terminate": terminate}
	var out Database
	if err := c.do(ctx, http.MethodPost, path, body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *HTTPClient) DeleteDatabase(ctx context.Context, projectName, env string, prNumber *int) error {
	return c.do(ctx, http.MethodDelete, dbPath(projectName, env, prNumber), nil, nil)
}

func (c *HTTPClient) SetBackupsEnabled(ctx context.Context, projectName, env string, prNumber *int, enabled bool) error {
	path := dbPath(projectName, env, prNumber) + "/backup"
	body := map[string]bool{"enabled": enabled}
	return c.do(ctx, http.MethodPut, path, body, nil)
}

func (c *HTTPClient) ListBackups(ctx context.Context, projectName, env string, prNumber *int) ([]Backup, error) {
	path := dbPath(projectName, env, prNumber) + "/backups"
	var out []Backup
	if err := c.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *HTTPClient) CreateBackup(ctx context.Context, projectName, env string, prNumber *int) (*Backup, error) {
	path := dbPath(projectName, env, prNumber) + "/backups"
	var out Backup
	if err := c.doLong(ctx, http.MethodPost, path, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *HTTPClient) DeleteBackup(ctx context.Context, projectName, env string, prNumber *int, backupID int64) error {
	path := dbPath(projectName, env, prNumber) + "/backups/" + strconv.FormatInt(backupID, 10)
	return c.do(ctx, http.MethodDelete, path, nil, nil)
}

func (c *HTTPClient) RestoreBackup(ctx context.Context, projectName, env string, prNumber *int, backupID int64) (*Database, error) {
	path := dbPath(projectName, env, prNumber) + "/backups/" + strconv.FormatInt(backupID, 10) + "/restore"
	var out Database
	if err := c.doLong(ctx, http.MethodPost, path, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
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
