package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"pgmanager/internal/auth"
	"pgmanager/internal/db"
)

// The data explorer lets an operator browse and edit the contents of a
// database pgmanager manages. It reuses the same scope check as every other
// database route, so a token scoped to one project's PR databases can explore
// exactly those and nothing else.

// databaseTarget resolves the project/env path params and enforces scope. It
// returns false if it has already written a response.
func databaseTarget(w http.ResponseWriter, r *http.Request) (projectName, env string, prNumber *int, ok bool) {
	projectName = chi.URLParam(r, "name")
	prNumber, env, err := parseEnvParam(chi.URLParam(r, "env"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return "", "", nil, false
	}
	scopeReq := auth.ScopeRequest{Resource: "project", Project: projectName, Env: env}
	if prNumber != nil {
		scopeReq.PR = *prNumber
	}
	if !requireScope(w, r, scopeReq) {
		return "", "", nil, false
	}
	return projectName, env, prNumber, true
}

// tableParams pulls the schema and table out of the request. Schema is a query
// parameter because it is optional; it defaults to public.
func tableParams(r *http.Request) (schema, table string) {
	schema = r.URL.Query().Get("schema")
	if schema == "" {
		schema = "public"
	}
	return schema, chi.URLParam(r, "table")
}

// writeExploreError maps the explorer's sentinel errors onto status codes.
// Anything else is a Postgres error whose text is the useful part (a failed
// constraint, a bad cast) — the caller already has full access to this
// database, so returning it leaks nothing they could not query directly.
func writeExploreError(w http.ResponseWriter, context string, err error) {
	switch {
	case errors.Is(err, db.ErrNoSuchTable):
		writeError(w, http.StatusNotFound, "table not found")
	case errors.Is(err, db.ErrNoSuchColumn), errors.Is(err, db.ErrNoPrimaryKey):
		writeError(w, http.StatusBadRequest, err.Error())
	default:
		writeError(w, http.StatusBadRequest, err.Error())
	}
}

type rowRequest struct {
	Key    map[string]any `json:"key"`
	Values map[string]any `json:"values"`
}

type tablesResponse struct {
	Tables []db.Table `json:"tables"`
}

func (s *Server) listTables(w http.ResponseWriter, r *http.Request) {
	projectName, env, prNumber, ok := databaseTarget(w, r)
	if !ok {
		return
	}
	tables, err := s.mgr.ListTables(r.Context(), projectName, env, prNumber)
	if err != nil {
		writeExploreError(w, "list tables", err)
		return
	}
	writeJSON(w, http.StatusOK, tablesResponse{Tables: tables})
}

func (s *Server) listRows(w http.ResponseWriter, r *http.Request) {
	projectName, env, prNumber, ok := databaseTarget(w, r)
	if !ok {
		return
	}
	schema, table := tableParams(r)
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))

	page, err := s.mgr.SelectRows(r.Context(), projectName, env, prNumber, schema, table, limit, offset)
	if err != nil {
		writeExploreError(w, "select rows", err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func (s *Server) createRow(w http.ResponseWriter, r *http.Request) {
	projectName, env, prNumber, ok := databaseTarget(w, r)
	if !ok {
		return
	}
	schema, table := tableParams(r)

	var req rowRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	row, err := s.mgr.InsertRow(r.Context(), projectName, env, prNumber, schema, table, req.Values)
	if err != nil {
		writeExploreError(w, "insert row", err)
		return
	}
	writeJSON(w, http.StatusCreated, row)
}

func (s *Server) updateRow(w http.ResponseWriter, r *http.Request) {
	projectName, env, prNumber, ok := databaseTarget(w, r)
	if !ok {
		return
	}
	schema, table := tableParams(r)

	var req rowRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if len(req.Key) == 0 {
		writeError(w, http.StatusBadRequest, "key is required")
		return
	}

	row, err := s.mgr.UpdateRow(r.Context(), projectName, env, prNumber, schema, table, req.Key, req.Values)
	if err != nil {
		writeExploreError(w, "update row", err)
		return
	}
	writeJSON(w, http.StatusOK, row)
}

func (s *Server) deleteRow(w http.ResponseWriter, r *http.Request) {
	projectName, env, prNumber, ok := databaseTarget(w, r)
	if !ok {
		return
	}
	schema, table := tableParams(r)

	var req rowRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if len(req.Key) == 0 {
		writeError(w, http.StatusBadRequest, "key is required")
		return
	}

	if err := s.mgr.DeleteRow(r.Context(), projectName, env, prNumber, schema, table, req.Key); err != nil {
		writeExploreError(w, "delete row", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
