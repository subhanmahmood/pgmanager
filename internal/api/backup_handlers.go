package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"pgmanager/internal/meta"
	"pgmanager/internal/project"
)

// Backup routes reuse databaseTarget (internal/api/explore_handlers.go) for
// the same project/env resolution and scope check as the data explorer and
// the other database routes — a token scoped to one project's PR databases
// can back up (and only back up) exactly those.

// BackupResponse is one snapshot as returned to a client. It never carries a
// credential — the S3 bucket configuration and its secret stay server-side.
type BackupResponse struct {
	ID           int64   `json:"id"`
	DatabaseName string  `json:"database_name"`
	ObjectKey    string  `json:"object_key"`
	SizeBytes    int64   `json:"size_bytes"`
	Status       string  `json:"status"`
	Error        string  `json:"error,omitempty"`
	StartedAt    string  `json:"started_at"`
	FinishedAt   *string `json:"finished_at,omitempty"`
}

// SetBackupEnabledRequest is the body of PUT .../backup.
type SetBackupEnabledRequest struct {
	Enabled bool `json:"enabled"`
}

func backupToResponse(dbName string, b meta.Backup) BackupResponse {
	var finishedAt *string
	if b.FinishedAt != nil {
		t := b.FinishedAt.Format(time.RFC3339)
		finishedAt = &t
	}
	return BackupResponse{
		ID:           b.ID,
		DatabaseName: dbName,
		ObjectKey:    b.Key,
		SizeBytes:    b.SizeBytes,
		Status:       b.Status,
		Error:        b.Error,
		StartedAt:    b.StartedAt.Format(time.RFC3339),
		FinishedAt:   finishedAt,
	}
}

// writeBackupError maps the manager's backup sentinels onto status codes.
// Anything else means the project, database or backup named in the path
// could not be resolved.
func writeBackupError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, project.ErrBackupsDisabled):
		// err.Error() carries the recorded reason (see
		// Manager.checkBackupsEnabled) — never the secret itself, since
		// every error that can end up here (config validation, S3 client
		// construction, the pg_dump probe) is guaranteed not to contain it.
		writeError(w, http.StatusServiceUnavailable, err.Error())
	case errors.Is(err, project.ErrBackupsNotForPR):
		writeError(w, http.StatusBadRequest, err.Error())
	default:
		writeError(w, http.StatusNotFound, "not found")
	}
}

// setBackupEnabled toggles the scheduled-backup flag for one database.
func (s *Server) setBackupEnabled(w http.ResponseWriter, r *http.Request) {
	projectName, env, prNumber, ok := databaseTarget(w, r)
	if !ok {
		return
	}

	var req SetBackupEnabledRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if err := s.mgr.SetBackupsEnabled(r.Context(), projectName, env, prNumber, req.Enabled); err != nil {
		writeBackupError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// createBackup takes an immediate snapshot of one database, outside the
// schedule.
func (s *Server) createBackup(w http.ResponseWriter, r *http.Request) {
	projectName, env, prNumber, ok := databaseTarget(w, r)
	if !ok {
		return
	}

	b, err := s.mgr.BackupNow(r.Context(), projectName, env, prNumber)
	if err != nil {
		writeBackupError(w, err)
		return
	}

	dbName := project.DatabaseName(projectName, env, prNumber)
	writeJSON(w, http.StatusCreated, backupToResponse(dbName, *b))
}

// listBackups returns every snapshot recorded for one database, newest
// first.
func (s *Server) listBackups(w http.ResponseWriter, r *http.Request) {
	projectName, env, prNumber, ok := databaseTarget(w, r)
	if !ok {
		return
	}

	backups, err := s.mgr.ListBackups(r.Context(), projectName, env, prNumber)
	if err != nil {
		writeBackupError(w, err)
		return
	}

	dbName := project.DatabaseName(projectName, env, prNumber)
	out := make([]BackupResponse, len(backups))
	for i, b := range backups {
		out[i] = backupToResponse(dbName, b)
	}
	writeJSON(w, http.StatusOK, out)
}

// deleteBackup removes one snapshot: the S3 object and its metadata row.
func (s *Server) deleteBackup(w http.ResponseWriter, r *http.Request) {
	projectName, env, prNumber, ok := databaseTarget(w, r)
	if !ok {
		return
	}

	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid backup id")
		return
	}

	if err := s.mgr.DeleteBackup(r.Context(), projectName, env, prNumber, id); err != nil {
		writeBackupError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
