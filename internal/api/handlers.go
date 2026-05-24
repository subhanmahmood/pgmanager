package api

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"pgmanager/internal/auth"
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
}

type CreateProjectRequest struct {
	Name string `json:"name"`
}

type CreateDatabaseRequest struct {
	Env        string   `json:"env"`
	PRNumber   *int     `json:"pr_number,omitempty"`
	Extensions []string `json:"extensions,omitempty"`
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

	response := make([]DatabaseInfoResponse, len(databases))
	for i, db := range databases {
		var expiresAt *string
		if db.ExpiresAt != nil {
			t := db.ExpiresAt.Format(time.RFC3339)
			expiresAt = &t
		}
		response[i] = DatabaseInfoResponse{
			Project:      db.Project,
			Env:          db.Env,
			PRNumber:     db.PRNumber,
			DatabaseName: db.DatabaseName,
			UserName:     db.UserName,
			Host:         db.Host,
			Port:         db.Port,
			CreatedAt:    db.CreatedAt.Format(time.RFC3339),
			ExpiresAt:    expiresAt,
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
	writeJSON(w, http.StatusCreated, DatabaseResponse{
		Project:      info.Project,
		Env:          info.Env,
		PRNumber:     info.PRNumber,
		DatabaseName: info.DatabaseName,
		UserName:     info.UserName,
		Password:     info.Password,
		Host:         info.Host,
		Port:         info.Port,
		ConnString:   info.ConnString,
		CreatedAt:    info.CreatedAt.Format(time.RFC3339),
		ExpiresAt:    expiresAt,
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
	scopeReq := auth.ScopeRequest{Resource: "project", Project: projectName, Env: env}
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
	writeJSON(w, http.StatusOK, DatabaseInfoResponse{
		Project:      info.Project,
		Env:          info.Env,
		PRNumber:     info.PRNumber,
		DatabaseName: info.DatabaseName,
		UserName:     info.UserName,
		Host:         info.Host,
		Port:         info.Port,
		CreatedAt:    info.CreatedAt.Format(time.RFC3339),
		ExpiresAt:    expiresAt,
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
	scopeReq := auth.ScopeRequest{Resource: "project", Project: projectName, Env: env}
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
	writeJSON(w, http.StatusOK, DatabaseResponse{
		Project:      info.Project,
		Env:          info.Env,
		PRNumber:     info.PRNumber,
		DatabaseName: info.DatabaseName,
		UserName:     info.UserName,
		Password:     info.Password,
		Host:         info.Host,
		Port:         info.Port,
		ConnString:   info.ConnString,
		CreatedAt:    info.CreatedAt.Format(time.RFC3339),
		ExpiresAt:    expiresAt,
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
	scopeReq := auth.ScopeRequest{Resource: "project", Project: projectName, Env: env}
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
	writeJSON(w, http.StatusOK, CleanupResponse{
		Deleted: deleted,
		Count:   len(deleted),
	})
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
