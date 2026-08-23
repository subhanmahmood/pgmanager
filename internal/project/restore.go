package project

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"pgmanager/internal/backup"
	"pgmanager/internal/db"
	"pgmanager/internal/meta"
)

// ErrBackupNotFound is returned when a restore names a backup ID that
// doesn't exist, or that belongs to a different database than the one the
// path resolved to. The two cases are folded together deliberately: telling
// them apart would let a caller enumerate another project's backup IDs.
var ErrBackupNotFound = errors.New("backup not found")

// ErrBackupNotRestorable is returned when a restore names a backup that
// exists but never finished successfully (still running, or failed).
var ErrBackupNotRestorable = errors.New("backup has not completed successfully")

// maxIdentifierBytes is Postgres's limit on an unquoted identifier (NAMEDATALEN
// 64, minus the trailing NUL Postgres reserves internally).
const maxIdentifierBytes = 63

// restoreDatabaseName computes the name, role and addressable env segment
// for a database restored from a snapshot of project/sourceEnv taken at ts.
// The env segment is exactly the database name with the "{project}_" prefix
// stripped, e.g. "dev_restore_20260823T101500" — that's what a caller
// addresses the restored database as afterward (see scopeEnv in
// internal/api/handlers.go).
//
// It errors if the resulting role name would exceed Postgres's 63-byte
// identifier limit. Worst case is project(32) + "_"(1) + env "staging"(7) +
// "_restore_"(9) + timestamp(15) + "_user"(5) = 69 bytes; project names up to
// 26 characters always fit regardless of env. Silent truncation by Postgres
// would risk two different restores colliding on one identifier, so this is
// a hard error rather than a warning.
func restoreDatabaseName(projectName, sourceEnv string, ts time.Time) (dbName, userName, envSegment string, err error) {
	stamp := ts.UTC().Format("20060102T150405")
	envSegment = fmt.Sprintf("%s_restore_%s", sourceEnv, stamp)
	dbName = projectName + "_" + envSegment
	userName = UserName(dbName)
	if len(userName) > maxIdentifierBytes {
		return "", "", "", fmt.Errorf(
			"restored database identifier for project %q would be %d bytes, over Postgres's %d-byte limit; use a shorter project name",
			projectName, len(userName), maxIdentifierBytes,
		)
	}
	return dbName, userName, envSegment, nil
}

// RestoreBackup creates a brand-new database from a stored snapshot and
// returns its credentials. The source database is never opened — this
// method only ever reads the snapshot object from the object store and
// writes into the freshly created database, as that database's own role.
//
// On any failure after the new database is created, it is dropped before
// the error is returned, so a partial restore never lingers.
func (m *Manager) RestoreBackup(ctx context.Context, projectName, env string, prNumber *int, backupID int64) (*DatabaseInfo, error) {
	if env == "pr" {
		return nil, ErrBackupsNotForPR
	}
	if err := m.checkBackupsEnabled(); err != nil {
		return nil, err
	}

	project, source, err := m.backupTarget(ctx, projectName, env, prNumber)
	if err != nil {
		return nil, err
	}

	snap, err := m.store.GetBackup(ctx, backupID)
	if err != nil {
		return nil, fmt.Errorf("failed to get backup: %w", err)
	}
	// snap.DatabaseID must match the database the path resolved to. This is
	// an authorization check, not a sanity check — without it, a caller
	// scoped to one database could restore a snapshot belonging to another
	// database it has no access to, just by guessing/enumerating IDs.
	if snap == nil || snap.DatabaseID != source.ID {
		return nil, ErrBackupNotFound
	}
	if snap.Status != meta.BackupStatusSucceeded {
		return nil, fmt.Errorf("%w: status is %q", ErrBackupNotRestorable, snap.Status)
	}

	newName, newUser, envSegment, err := restoreDatabaseName(projectName, source.Env, time.Now())
	if err != nil {
		return nil, err
	}

	password := db.GeneratePassword()
	if err := m.pg.CreateDatabase(ctx, newName, newUser, password); err != nil {
		return nil, fmt.Errorf("failed to create restore database: %w", err)
	}

	if err := m.restoreSnapshotInto(ctx, newName, newUser, password, snap.Key); err != nil {
		m.rollbackRestore(ctx, newName, newUser, err)
		return nil, err
	}

	dbRecord, err := m.store.CreateRestoredDatabase(ctx, project.ID, backupID, newName, newUser, password, source.Env)
	if err != nil {
		m.rollbackRestore(ctx, newName, newUser, err)
		return nil, fmt.Errorf("failed to store restored database metadata: %w", err)
	}

	host := m.cfg.Postgres.EffectiveHost()
	port := m.cfg.Postgres.EffectivePort()
	return &DatabaseInfo{
		Project:      projectName,
		Env:          envSegment,
		DatabaseName: dbRecord.Name,
		UserName:     dbRecord.UserName,
		Password:     dbRecord.Password,
		Host:         host,
		Port:         port,
		ConnString:   db.ConnectionString(host, port, dbRecord.Name, dbRecord.UserName, dbRecord.Password, m.cfg.Postgres.SSLMode),
		CreatedAt:    dbRecord.CreatedAt,
		RestoredFrom: dbRecord.RestoredFrom,
	}, nil
}

// rollbackRestore drops the database and role a failed restore already
// created.
//
// It runs on a cleanupContext rather than on ctx, because the commonest
// reason to be here is that ctx itself is what failed: the caller
// disconnected, or the request deadline expired during pg_restore. Passing
// the dead context to DropDatabase would make the rollback fail immediately
// and leave a half-restored database and its role in Postgres with *no*
// pgmanager metadata row — nothing the operator could then find or delete
// through pgmanager at all, since the metadata is only written once the
// restore has succeeded.
//
// A failure to roll back is logged loudly and precisely, naming both
// identifiers, because at that point a human has to remove them by hand.
func (m *Manager) rollbackRestore(ctx context.Context, dbName, userName string, cause error) {
	cleanupCtx, cancel := cleanupContext(ctx)
	defer cancel()

	if err := m.pg.DropDatabase(cleanupCtx, dbName, userName); err != nil {
		log.Printf("ERROR [restore %s]: restore failed (%v) and rolling it back also failed: %v", dbName, cause, err)
		log.Printf("ERROR [restore %s]: database %q and role %q may still exist in Postgres with no pgmanager metadata — drop them by hand", dbName, dbName, userName)
	}
}

// restoreSnapshotInto downloads the snapshot object and streams it directly
// into pg_restore, connected as the new database's own role. Unlike
// runBackup's dump path, this needs no io.Pipe: objects.Get already hands
// back an io.Reader, and Restore already accepts one as pg_restore's stdin —
// there is no writer side to pair up.
func (m *Manager) restoreSnapshotInto(ctx context.Context, dbName, userName, password, key string) error {
	rc, err := m.objects.Get(ctx, key)
	if err != nil {
		return fmt.Errorf("failed to fetch backup object: %w", err)
	}
	defer rc.Close()

	params := backup.ConnParams{
		Host:     m.cfg.Postgres.Host,
		Port:     m.cfg.Postgres.Port,
		DBName:   dbName,
		User:     userName,
		Password: password,
		SSLMode:  m.cfg.Postgres.SSLMode,
	}
	if err := m.dumper.Restore(ctx, params, rc); err != nil {
		return fmt.Errorf("failed to restore backup into %s: %w", dbName, err)
	}
	return nil
}
