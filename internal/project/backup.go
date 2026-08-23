package project

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"time"

	"pgmanager/internal/backup"
	"pgmanager/internal/meta"
)

// ErrBackupsDisabled is returned by every backup.go method when the server
// has no working object store — either backup.enabled is false, or startup
// wiring (bucket validation, S3 client construction, or the pg_dump/pg_dump
// compatibility probe) failed and called DisableBackups. It is always
// checked with errors.Is, since the manager wraps it with the recorded
// reason.
var ErrBackupsDisabled = errors.New("backups are not configured on this server")

// ErrBackupsNotForPR is returned when a backup route targets a PR database.
// PR databases are short-lived and disposable by design; the issue that
// introduced backups explicitly keeps them out of scope.
var ErrBackupsNotForPR = errors.New("backups are not available for PR databases")

// EnableBackups wires a working object store and dumper into the manager,
// clearing any previously recorded failure. Called once at startup after
// config validation, S3 client construction and the pg_dump compatibility
// probe all succeed.
func (m *Manager) EnableBackups(objects backup.ObjectStore, d *backup.Dumper) {
	m.objects = objects
	m.dumper = d
	m.backupCfg = m.cfg.Backup
	m.backupErr = nil
}

// DisableBackups turns backups off and records why, so a caller asking for
// one gets a reason instead of a bare "disabled". Safe to call whether or
// not EnableBackups ever ran — used both when backups were never configured
// to begin with and when startup wiring failed after being configured.
func (m *Manager) DisableBackups(reason error) {
	m.objects = nil
	m.dumper = nil
	m.backupErr = reason
}

// checkBackupsEnabled is the first thing every backup.go method calls after
// validating its input shape. It never touches Postgres or the store, so a
// caller with backups off never reaches either.
func (m *Manager) checkBackupsEnabled() error {
	if m.objects != nil && m.dumper != nil {
		return nil
	}
	if m.backupErr != nil {
		return fmt.Errorf("%w: %v", ErrBackupsDisabled, m.backupErr)
	}
	return ErrBackupsDisabled
}

// backupTarget resolves the project and database record a backup route
// targets. It duplicates the project/database lookup in GetDatabase,
// RotatePassword and DeleteDatabase rather than reusing DatabaseInfo,
// because backup bookkeeping needs the database's numeric ID, which
// DatabaseInfo does not carry.
func (m *Manager) backupTarget(ctx context.Context, projectName, env string, prNumber *int) (*meta.Project, *meta.Database, error) {
	if err := ValidateEnv(env); err != nil {
		return nil, nil, err
	}

	project, err := m.store.GetProject(ctx, projectName)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get project: %w", err)
	}
	if project == nil {
		return nil, nil, fmt.Errorf("project '%s' not found", projectName)
	}

	dbRecord, err := m.store.GetDatabase(ctx, project.ID, env, prNumber)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get database: %w", err)
	}
	if dbRecord == nil {
		envStr := env
		if prNumber != nil {
			envStr = fmt.Sprintf("pr_%d", *prNumber)
		}
		return nil, nil, fmt.Errorf("database not found for %s/%s", projectName, envStr)
	}

	return project, dbRecord, nil
}

// SetBackupsEnabled turns the scheduled backup flag on or off for one
// database.
func (m *Manager) SetBackupsEnabled(ctx context.Context, projectName, env string, prNumber *int, enabled bool) error {
	if env == "pr" {
		return ErrBackupsNotForPR
	}
	if err := m.checkBackupsEnabled(); err != nil {
		return err
	}

	_, dbRecord, err := m.backupTarget(ctx, projectName, env, prNumber)
	if err != nil {
		return err
	}

	return m.store.SetBackupsEnabled(ctx, dbRecord.Name, enabled)
}

// ListBackups returns every snapshot recorded for one database, newest
// first.
func (m *Manager) ListBackups(ctx context.Context, projectName, env string, prNumber *int) ([]meta.Backup, error) {
	if env == "pr" {
		return nil, ErrBackupsNotForPR
	}
	if err := m.checkBackupsEnabled(); err != nil {
		return nil, err
	}

	_, dbRecord, err := m.backupTarget(ctx, projectName, env, prNumber)
	if err != nil {
		return nil, err
	}

	backups, err := m.store.ListBackups(ctx, dbRecord.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to list backups: %w", err)
	}
	return backups, nil
}

// DeleteBackup removes one snapshot: the S3 object first, then the metadata
// row. The backup must belong to the database the path names — a caller
// scoped to one database can't delete another project's snapshot by ID
// alone.
func (m *Manager) DeleteBackup(ctx context.Context, projectName, env string, prNumber *int, backupID int64) error {
	if env == "pr" {
		return ErrBackupsNotForPR
	}
	if err := m.checkBackupsEnabled(); err != nil {
		return err
	}

	_, dbRecord, err := m.backupTarget(ctx, projectName, env, prNumber)
	if err != nil {
		return err
	}

	snap, err := m.store.GetBackup(ctx, backupID)
	if err != nil {
		return fmt.Errorf("failed to get backup: %w", err)
	}
	if snap == nil || snap.DatabaseID != dbRecord.ID {
		return fmt.Errorf("backup not found")
	}

	if err := m.objects.Delete(ctx, snap.Key); err != nil {
		return fmt.Errorf("failed to delete backup object: %w", err)
	}
	if err := m.store.DeleteBackup(ctx, backupID); err != nil {
		return fmt.Errorf("failed to delete backup record: %w", err)
	}
	return nil
}

// BackupNow takes an immediate snapshot of one database, outside the
// schedule.
func (m *Manager) BackupNow(ctx context.Context, projectName, env string, prNumber *int) (*meta.Backup, error) {
	if env == "pr" {
		return nil, ErrBackupsNotForPR
	}
	if err := m.checkBackupsEnabled(); err != nil {
		return nil, err
	}

	_, dbRecord, err := m.backupTarget(ctx, projectName, env, prNumber)
	if err != nil {
		return nil, err
	}

	return m.runBackup(ctx, projectName, dbRecord)
}

// runBackup performs the actual dump/upload/finish/retention flow for an
// already-resolved (project name, database record) pair. Both BackupNow and
// RunDueBackups funnel through this, so the io.Pipe wiring — the part most
// likely to deadlock if it's wrong — exists in exactly one place.
//
// Flow: CreateBackup (status "running") -> an io.Pipe, with a goroutine
// running dumper.Dump into the write end while this goroutine hands the read
// end to objects.Put. Whichever side fails first, the pipe is always closed
// with that error so the other side's blocked Read/Write unblocks instead of
// hanging forever — that is what lets the caller ever reach FinishBackup, on
// either success or failure.
func (m *Manager) runBackup(ctx context.Context, projectName string, dbRecord *meta.Database) (*meta.Backup, error) {
	key := backup.ObjectKey(m.backupCfg.EffectivePrefix(), projectName, dbRecord.Name, time.Now())

	snap, err := m.store.CreateBackup(ctx, dbRecord.ID, key)
	if err != nil {
		return nil, fmt.Errorf("failed to create backup record: %w", err)
	}

	params := backup.ConnParams{
		Host:     m.cfg.Postgres.Host,
		Port:     m.cfg.Postgres.Port,
		DBName:   dbRecord.Name,
		User:     dbRecord.UserName,
		Password: dbRecord.Password,
		SSLMode:  m.cfg.Postgres.SSLMode,
	}

	pr, pw := io.Pipe()

	dumpDone := make(chan error, 1)
	go func() {
		dumpErr := m.dumper.Dump(ctx, params, pw)
		// Always close the write end, success or failure — this is what
		// lets objects.Put see EOF (nil) or the failure (non-nil) instead
		// of blocking forever waiting for more input.
		pw.CloseWithError(dumpErr)
		dumpDone <- dumpErr
	}()

	size, putErr := m.objects.Put(ctx, key, pr)
	if putErr != nil {
		// The upload gave up without draining the pipe. Unless the read end
		// is closed with an error, a dump goroutine still blocked on Write
		// hangs forever — this is the exact deadlock BackupNow must avoid.
		pr.CloseWithError(putErr)
	} else {
		pr.Close()
	}
	dumpErr := <-dumpDone

	failErr := putErr
	if failErr == nil {
		failErr = dumpErr
	}
	if failErr != nil {
		// Best-effort: the object may not exist (Put may have failed before
		// writing anything), but if it partially landed, don't leave a
		// broken dump sitting in the bucket.
		_ = m.objects.Delete(ctx, key)
		_ = m.store.FinishBackup(ctx, snap.ID, size, meta.BackupStatusFailed, failErr.Error())
		return nil, fmt.Errorf("backup of %s failed: %w", dbRecord.Name, failErr)
	}

	if err := m.store.FinishBackup(ctx, snap.ID, size, meta.BackupStatusSucceeded, ""); err != nil {
		return nil, fmt.Errorf("failed to record finished backup: %w", err)
	}

	if err := m.applyRetention(ctx, dbRecord.ID); err != nil {
		log.Printf("ERROR [backup retention %s]: %v", dbRecord.Name, err)
	}

	result, err := m.store.GetBackup(ctx, snap.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to reload finished backup: %w", err)
	}
	if result == nil {
		return nil, fmt.Errorf("backup %d vanished after finishing", snap.ID)
	}
	return result, nil
}

// applyRetention keeps the newest backupCfg.Retention succeeded snapshots
// and the newest backupCfg.Retention failed ones for a database, deleting
// the object and the row for everything past that. It never runs when
// m.objects is nil, and it only ever deletes a key read from the exact row
// being deleted in the same pass — an empty or short list deletes nothing.
//
// This is the highest-risk code in the whole backup feature: a wrong
// comparison here destroys the only copy of a database. See
// applyRetention's tests for the exact deleted-key set at 0, 1, N and N+3
// snapshots.
func (m *Manager) applyRetention(ctx context.Context, dbID int64) error {
	if m.objects == nil {
		return nil
	}

	retention := m.backupCfg.Retention

	backups, err := m.store.ListBackups(ctx, dbID) // newest first
	if err != nil {
		return fmt.Errorf("failed to list backups: %w", err)
	}

	var keptSucceeded, keptFailed int
	var errs []error
	for _, b := range backups {
		switch b.Status {
		case meta.BackupStatusSucceeded:
			keptSucceeded++
			if keptSucceeded <= retention {
				continue
			}
		case meta.BackupStatusFailed:
			keptFailed++
			if keptFailed <= retention {
				continue
			}
		default:
			// "running" (or anything else): never touched by retention.
			continue
		}

		if err := m.objects.Delete(ctx, b.Key); err != nil {
			errs = append(errs, fmt.Errorf("delete object %s: %w", b.Key, err))
			continue
		}
		if err := m.store.DeleteBackup(ctx, b.ID); err != nil {
			errs = append(errs, fmt.Errorf("delete backup row %d: %w", b.ID, err))
		}
	}
	return errors.Join(errs...)
}

// RunDueBackups sweeps every backup-enabled database and takes a snapshot of
// any that are due. It never runs when m.objects is nil. A failure on one
// database is logged and does not stop the sweep from continuing to the
// next.
func (m *Manager) RunDueBackups(ctx context.Context) (int, error) {
	if m.objects == nil {
		return 0, nil
	}

	dbs, err := m.store.ListBackupEnabledDatabases(ctx)
	if err != nil {
		return 0, fmt.Errorf("failed to list backup-enabled databases: %w", err)
	}
	if len(dbs) == 0 {
		return 0, nil
	}

	projects, err := m.store.ListProjects(ctx)
	if err != nil {
		return 0, fmt.Errorf("failed to list projects: %w", err)
	}
	projectNames := make(map[int64]string, len(projects))
	for _, p := range projects {
		projectNames[p.ID] = p.Name
	}

	var ran int
	for i := range dbs {
		dbRecord := dbs[i]

		due, err := m.backupDue(ctx, dbRecord.ID)
		if err != nil {
			log.Printf("ERROR [RunDueBackups %s]: %v", dbRecord.Name, err)
			continue
		}
		if !due {
			continue
		}

		projectName, ok := projectNames[dbRecord.ProjectID]
		if !ok {
			log.Printf("ERROR [RunDueBackups %s]: project id %d not found", dbRecord.Name, dbRecord.ProjectID)
			continue
		}

		if _, err := m.runBackup(ctx, projectName, &dbRecord); err != nil {
			log.Printf("ERROR [RunDueBackups %s]: %v", dbRecord.Name, err)
			continue
		}
		ran++
	}
	return ran, nil
}

// backupDue reports whether a database's next scheduled backup is due:
// either it has no succeeded snapshot yet, or the newest succeeded one
// started longer ago than backupCfg.Schedule.
func (m *Manager) backupDue(ctx context.Context, dbID int64) (bool, error) {
	backups, err := m.store.ListBackups(ctx, dbID) // newest first
	if err != nil {
		return false, fmt.Errorf("failed to list backups: %w", err)
	}
	for _, b := range backups {
		if b.Status == meta.BackupStatusSucceeded {
			return time.Since(b.StartedAt) >= m.backupCfg.Schedule, nil
		}
	}
	return true, nil
}
