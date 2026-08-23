package api

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"pgmanager/internal/auth"
	"pgmanager/internal/db"
)

// MaxPRNumber is the maximum allowed PR number.
const MaxPRNumber = 1000000

// Response types
type ErrorResponse struct {
	Error string `json:"error"`
}

type HealthResponse struct {
	Status string `json:"status"`
	Time   string `json:"time"`
}

type ProjectResponse struct {
	Name      string `json:"name"`
	CreatedAt string `json:"created_at"`
}

// DatabaseResponse is returned when creating a database (includes sensitive info).
type DatabaseResponse struct {
	Project      string  `json:"project"`
	Env          string  `json:"env"`
	PRNumber     *int    `json:"pr_number,omitempty"`
	DatabaseName string  `json:"database_name"`
	UserName     string  `json:"user_name"`
	Password     string  `json:"password"`
	Host         string  `json:"host"`
	Port         int     `json:"port"`
	ConnString   string  `json:"connection_string"`
	CreatedAt    string  `json:"created_at"`
	ExpiresAt    *string `json:"expires_at,omitempty"`
	// BackupsEnabled is always serialized, never omitted: the admin UI's
	// toggle and the CLI both read it as the authoritative state, and an
	// absent field would be indistinguishable from "off".
	BackupsEnabled bool `json:"backups_enabled"`
}

// DatabaseInfoResponse is returned when listing/getting databases (no sensitive info).
type DatabaseInfoResponse struct {
	Project      string  `json:"project"`
	Env          string  `json:"env"`
	PRNumber     *int    `json:"pr_number,omitempty"`
	DatabaseName string  `json:"database_name"`
	UserName     string  `json:"user_name"`
	Host         string  `json:"host"`
	Port         int     `json:"port"`
	CreatedAt    string  `json:"created_at"`
	ExpiresAt    *string `json:"expires_at,omitempty"`
	// BackupsEnabled is always serialized, never omitted — see
	// DatabaseResponse.BackupsEnabled.
	BackupsEnabled bool `json:"backups_enabled"`
	// RestoredFrom holds the source backup's ID when this database was
	// created by a restore, and is omitted otherwise.
	RestoredFrom *int64 `json:"restored_from,omitempty"`
}

type CreateProjectRequest struct {
	Name string `json:"name"`
}

type CreateDatabaseRequest struct {
	Env        string   `json:"env"`
	PRNumber   *int     `json:"pr_number,omitempty"`
	Extensions []string `json:"extensions,omitempty"`
}

// RotatePasswordRequest is the (optional) body of a password rotation.
type RotatePasswordRequest struct {
	// Terminate kills existing connections to the database after the
	// rotation, so clients holding the old password must reconnect.
	Terminate bool `json:"terminate,omitempty"`
}

type CleanupRequest struct {
	OlderThan string `json:"older_than"`
}

type CleanupResponse struct {
	Deleted []string `json:"deleted"`
	Count   int      `json:"count"`
}

// Helper functions
func writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, ErrorResponse{Error: message})
}

// writeInternalError logs the full error and returns a generic message to the client.
func writeInternalError(w http.ResponseWriter, context string, err error) {
	log.Printf("ERROR [%s]: %v", context, err)
	writeError(w, http.StatusInternalServerError, "internal server error")
}

// Handlers
func (s *Server) healthHandler(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, HealthResponse{
		Status: "ok",
		Time:   time.Now().UTC().Format(time.RFC3339),
	})
}

func (s *Server) listProjects(w http.ResponseWriter, r *http.Request) {
	// Listing projects requires at least one scope that touches projects.
	// We allow it for any holder of project:* or project:<name> by filtering
	// the response down to projects whose access they have. Admins see all.
	info := AuthFromContext(r.Context())
	if info == nil {
		writeError(w, http.StatusUnauthorized, "missing authentication")
		return
	}

	projects, err := s.mgr.ListProjects(r.Context())
	if err != nil {
		writeInternalError(w, "listProjects", err)
		return
	}

	out := make([]ProjectResponse, 0, len(projects))
	for _, p := range projects {
		if auth.Authorize(info.Scopes, auth.ScopeRequest{Resource: "project", Project: p.Name}) != nil {
			continue
		}
		out = append(out, ProjectResponse{
			Name:      p.Name,
			CreatedAt: p.CreatedAt.Format(time.RFC3339),
		})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) createProject(w http.ResponseWriter, r *http.Request) {
	var req CreateProjectRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	// Creating a project requires either admin or project:* scope.
	if !requireScope(w, r, auth.ScopeRequest{Resource: "project", Project: req.Name}) {
		return
	}

	if err := s.mgr.CreateProject(r.Context(), req.Name); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, ProjectResponse{
		Name:      req.Name,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	})
}

func (s *Server) deleteProject(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	if !requireScope(w, r, auth.ScopeRequest{Resource: "project", Project: name}) {
		return
	}

	if err := s.mgr.DeleteProject(r.Context(), name); err != nil {
		if err.Error() == fmt.Sprintf("project not found: %s", name) {
			writeError(w, http.StatusNotFound, "project not found")
			return
		}
		writeInternalError(w, "deleteProject", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listDatabases(w http.ResponseWriter, r *http.Request) {
	projectName := chi.URLParam(r, "name")
	if !requireScope(w, r, auth.ScopeRequest{Resource: "project", Project: projectName}) {
		return
	}

	databases, err := s.mgr.ListDatabases(r.Context(), projectName)
	if err != nil {
		if err.Error() == fmt.Sprintf("project '%s' not found", projectName) {
			writeError(w, http.StatusNotFound, "project not found")
			return
		}
		writeInternalError(w, "listDatabases", err)
		return
	}

	host, port := s.publicHostPort(r)
	response := make([]DatabaseInfoResponse, len(databases))
	for i, info := range databases {
		var expiresAt *string
		if info.ExpiresAt != nil {
			t := info.ExpiresAt.Format(time.RFC3339)
			expiresAt = &t
		}
		response[i] = DatabaseInfoResponse{
			Project:        info.Project,
			Env:            info.Env,
			PRNumber:       info.PRNumber,
			DatabaseName:   info.DatabaseName,
			UserName:       info.UserName,
			Host:           host,
			Port:           port,
			CreatedAt:      info.CreatedAt.Format(time.RFC3339),
			ExpiresAt:      expiresAt,
			BackupsEnabled: info.BackupsEnabled,
			RestoredFrom:   info.RestoredFrom,
		}
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) createDatabase(w http.ResponseWriter, r *http.Request) {
	projectName := chi.URLParam(r, "name")

	var req CreateDatabaseRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Env == "" {
		writeError(w, http.StatusBadRequest, "env is required")
		return
	}
	if req.PRNumber != nil {
		if *req.PRNumber <= 0 {
			writeError(w, http.StatusBadRequest, "PR number must be positive")
			return
		}
		if *req.PRNumber > MaxPRNumber {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("PR number must be less than %d", MaxPRNumber))
			return
		}
	}
	scopeReq := auth.ScopeRequest{Resource: "project", Project: projectName, Env: req.Env}
	if req.PRNumber != nil {
		scopeReq.PR = *req.PRNumber
	}
	if !requireScope(w, r, scopeReq) {
		return
	}

	info, err := s.mgr.CreateDatabase(r.Context(), projectName, req.Env, req.PRNumber, req.Extensions)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	var expiresAt *string
	if info.ExpiresAt != nil {
		t := info.ExpiresAt.Format(time.RFC3339)
		expiresAt = &t
	}
	host, port, connStr := s.withPublicHost(r, info.DatabaseName, info.UserName, info.Password)
	writeJSON(w, http.StatusCreated, DatabaseResponse{
		Project:        info.Project,
		Env:            info.Env,
		PRNumber:       info.PRNumber,
		DatabaseName:   info.DatabaseName,
		UserName:       info.UserName,
		Password:       info.Password,
		Host:           host,
		Port:           port,
		ConnString:     connStr,
		CreatedAt:      info.CreatedAt.Format(time.RFC3339),
		ExpiresAt:      expiresAt,
		BackupsEnabled: info.BackupsEnabled,
	})
}

func (s *Server) getDatabase(w http.ResponseWriter, r *http.Request) {
	projectName := chi.URLParam(r, "name")
	env := chi.URLParam(r, "env")
	prNumber, env, err := parseEnvParam(env)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	scopeReq := auth.ScopeRequest{Resource: "project", Project: projectName, Env: scopeEnv(env)}
	if prNumber != nil {
		scopeReq.PR = *prNumber
	}
	if !requireScope(w, r, scopeReq) {
		return
	}

	info, err := s.mgr.GetDatabase(r.Context(), projectName, env, prNumber)
	if err != nil {
		writeError(w, http.StatusNotFound, "database not found")
		return
	}

	var expiresAt *string
	if info.ExpiresAt != nil {
		t := info.ExpiresAt.Format(time.RFC3339)
		expiresAt = &t
	}
	host, port := s.publicHostPort(r)
	writeJSON(w, http.StatusOK, DatabaseInfoResponse{
		Project:        info.Project,
		Env:            info.Env,
		PRNumber:       info.PRNumber,
		DatabaseName:   info.DatabaseName,
		UserName:       info.UserName,
		Host:           host,
		Port:           port,
		CreatedAt:      info.CreatedAt.Format(time.RFC3339),
		ExpiresAt:      expiresAt,
		BackupsEnabled: info.BackupsEnabled,
		RestoredFrom:   info.RestoredFrom,
	})
}

// getDatabaseCredentials returns the full credential set including the
// password. Requires the same scope as reading the database; intended for
// CI/agents that need to reconnect to a previously-created DB.
func (s *Server) getDatabaseCredentials(w http.ResponseWriter, r *http.Request) {
	projectName := chi.URLParam(r, "name")
	env := chi.URLParam(r, "env")
	prNumber, env, err := parseEnvParam(env)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	scopeReq := auth.ScopeRequest{Resource: "project", Project: projectName, Env: scopeEnv(env)}
	if prNumber != nil {
		scopeReq.PR = *prNumber
	}
	if !requireScope(w, r, scopeReq) {
		return
	}

	info, err := s.mgr.GetDatabase(r.Context(), projectName, env, prNumber)
	if err != nil {
		writeError(w, http.StatusNotFound, "database not found")
		return
	}
	var expiresAt *string
	if info.ExpiresAt != nil {
		t := info.ExpiresAt.Format(time.RFC3339)
		expiresAt = &t
	}
	host, port, connStr := s.withPublicHost(r, info.DatabaseName, info.UserName, info.Password)
	writeJSON(w, http.StatusOK, DatabaseResponse{
		Project:        info.Project,
		Env:            info.Env,
		PRNumber:       info.PRNumber,
		DatabaseName:   info.DatabaseName,
		UserName:       info.UserName,
		Password:       info.Password,
		Host:           host,
		Port:           port,
		ConnString:     connStr,
		CreatedAt:      info.CreatedAt.Format(time.RFC3339),
		ExpiresAt:      expiresAt,
		BackupsEnabled: info.BackupsEnabled,
	})
}

// rotateDatabasePassword issues a fresh password for the database's role and
// returns the new credentials. Requires the same scope as reading the
// database, since a caller who can read the password can already use it.
func (s *Server) rotateDatabasePassword(w http.ResponseWriter, r *http.Request) {
	projectName := chi.URLParam(r, "name")
	env := chi.URLParam(r, "env")
	prNumber, env, err := parseEnvParam(env)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	scopeReq := auth.ScopeRequest{Resource: "project", Project: projectName, Env: env}
	if prNumber != nil {
		scopeReq.PR = *prNumber
	}
	if !requireScope(w, r, scopeReq) {
		return
	}

	var req RotatePasswordRequest
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&req) // body is optional
	}

	info, err := s.mgr.RotatePassword(r.Context(), projectName, env, prNumber, req.Terminate)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	var expiresAt *string
	if info.ExpiresAt != nil {
		t := info.ExpiresAt.Format(time.RFC3339)
		expiresAt = &t
	}
	host, port, connStr := s.withPublicHost(r, info.DatabaseName, info.UserName, info.Password)
	writeJSON(w, http.StatusOK, DatabaseResponse{
		Project:        info.Project,
		Env:            info.Env,
		PRNumber:       info.PRNumber,
		DatabaseName:   info.DatabaseName,
		UserName:       info.UserName,
		Password:       info.Password,
		Host:           host,
		Port:           port,
		ConnString:     connStr,
		CreatedAt:      info.CreatedAt.Format(time.RFC3339),
		ExpiresAt:      expiresAt,
		BackupsEnabled: info.BackupsEnabled,
	})
}

func (s *Server) deleteDatabase(w http.ResponseWriter, r *http.Request) {
	projectName := chi.URLParam(r, "name")
	env := chi.URLParam(r, "env")
	prNumber, env, err := parseEnvParam(env)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	scopeReq := auth.ScopeRequest{Resource: "project", Project: projectName, Env: scopeEnv(env)}
	if prNumber != nil {
		scopeReq.PR = *prNumber
	}
	if !requireScope(w, r, scopeReq) {
		return
	}

	if err := s.mgr.DeleteDatabase(r.Context(), projectName, env, prNumber); err != nil {
		writeError(w, http.StatusNotFound, "database not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) cleanup(w http.ResponseWriter, r *http.Request) {
	// Cleanup spans projects; require admin (or any project:*).
	if !requireScope(w, r, auth.ScopeRequest{Resource: "project", Project: "*"}) {
		return
	}

	var req CleanupRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		req.OlderThan = "7d"
	}
	if req.OlderThan == "" {
		req.OlderThan = "7d"
	}
	duration, err := parseDuration(req.OlderThan)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid duration format")
		return
	}
	deleted, err := s.mgr.Cleanup(r.Context(), duration)
	if err != nil {
		writeInternalError(w, "cleanup", err)
		return
	}
	// Lapsed device authorizations are dead weight too; sweep them here so
	// the table doesn't grow forever between restarts.
	s.purgeExpired(r.Context())
	writeJSON(w, http.StatusOK, CleanupResponse{
		Deleted: deleted,
		Count:   len(deleted),
	})
}

// publicHostPort returns the host/port to advertise to clients in connection
// strings and DB-info responses. The server's own connection to Postgres
// always uses cfg.Postgres.Host / Port — this is purely about the value
// clients see.
//
// Resolution order:
//  1. cfg.Postgres.PublicHost if set (explicit operator config wins)
//  2. r.Host (port stripped) if available — covers the common case where
//     Postgres and the API live on the same host and the client already
//     reached that host. Skipped if r.Host is empty (no inbound request).
//  3. cfg.Postgres.Host (current behaviour, last resort).
//
// Port mirrors: PublicPort if set, otherwise cfg.Postgres.Port.
func (s *Server) publicHostPort(r *http.Request) (string, int) {
	host := s.cfg.Postgres.PublicHost
	// A request over the local socket has no meaningful Host header — the
	// unix dialer invents one — so fall through to the configured host
	// instead of handing the caller a hostname that resolves nowhere.
	if host == "" && r != nil && r.Host != "" && !isSocketRequest(r.Context()) {
		if h, _, err := net.SplitHostPort(r.Host); err == nil {
			host = h
		} else {
			// r.Host had no port; use it as-is.
			host = r.Host
		}
	}
	if host == "" {
		host = s.cfg.Postgres.Host
	}

	port := s.cfg.Postgres.PublicPort
	if port == 0 {
		port = s.cfg.Postgres.Port
	}
	return host, port
}

// withPublicHost rewrites the Host / Port / ConnString fields on a database
// info pulled from the manager so they reflect the client-reachable endpoint
// rather than the server-internal one. Other fields (password, names) are
// untouched.
func (s *Server) withPublicHost(r *http.Request, dbName, userName, password string) (string, int, string) {
	host, port := s.publicHostPort(r)
	return host, port, db.ConnectionString(host, port, dbName, userName, password, s.cfg.Postgres.SSLMode)
}

// parseEnvParam parses a URL path env segment which may be "pr_42" form.
// Returns (prNumber, normalizedEnv, error).
func parseEnvParam(env string) (*int, string, error) {
	if len(env) > 3 && env[:3] == "pr_" {
		num, err := strconv.Atoi(env[3:])
		if err != nil {
			return nil, env, fmt.Errorf("invalid PR number")
		}
		if num <= 0 || num > MaxPRNumber {
			return nil, env, fmt.Errorf("invalid PR number")
		}
		return &num, "pr", nil
	}
	return nil, env, nil
}

// restoreSegmentRe matches the env path segment of a restored database, e.g.
// "dev_restore_20260823T101500".
var restoreSegmentRe = regexp.MustCompile(`^(prod|dev|staging)_restore_\d{8}T\d{6}$`)

// scopeEnv returns the environment a path segment is authorized as. A
// restored database holds the data of the environment it came from, so it is
// governed by that environment's scope — a token scoped to "dev" must never
// be able to reach "prod_restore_<ts>", which holds production data. Any
// segment that isn't a restore segment is returned unchanged.
func scopeEnv(segment string) string {
	if m := restoreSegmentRe.FindStringSubmatch(segment); m != nil {
		return m[1]
	}
	return segment
}

// parseDuration parses a duration string like "7d", "24h", "1w".
func parseDuration(s string) (time.Duration, error) {
	if s == "" {
		return 0, nil
	}
	if len(s) < 2 {
		return 0, fmt.Errorf("invalid duration: %s", s)
	}
	unit := s[len(s)-1]
	value, err := strconv.Atoi(s[:len(s)-1])
	if err != nil {
		return 0, err
	}
	switch unit {
	case 's':
		return time.Duration(value) * time.Second, nil
	case 'm':
		return time.Duration(value) * time.Minute, nil
	case 'h':
		return time.Duration(value) * time.Hour, nil
	case 'd':
		return time.Duration(value) * 24 * time.Hour, nil
	case 'w':
		return time.Duration(value) * 7 * 24 * time.Hour, nil
	default:
		return 0, fmt.Errorf("invalid duration unit: %c", unit)
	}
}
