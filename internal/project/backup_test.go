package project

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"pgmanager/internal/backup"
	"pgmanager/internal/config"
	"pgmanager/internal/db"
	"pgmanager/internal/meta"
)

// newBackupTestManager builds a Manager backed by a MockStore and a real
// backup.MemoryStore (no network, no bucket), with one seeded project and
// database ready to attach backups to.
func newBackupTestManager(t *testing.T, retention int) (mgr *Manager, store *meta.MockStore, objects *backup.MemoryStore, dbID int64) {
	t.Helper()

	cfg := &config.Config{
		Postgres: config.PostgresConfig{
			Host: "localhost", Port: 5432, User: "postgres", Password: "x", Database: "postgres",
		},
		Backup: config.BackupConfig{
			Enabled:   true,
			Bucket:    "test-bucket",
			Prefix:    "pgmanager/",
			Schedule:  time.Hour,
			Retention: retention,
		},
	}
	store = meta.NewMockStore()
	mgr = NewManager(cfg, store)

	objects = backup.NewMemoryStore()
	mgr.EnableBackups(objects, backup.NewDumper())

	ctx := context.Background()
	p, err := store.CreateProject(ctx, "acme")
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	dbRecord, err := store.CreateDatabase(ctx, p.ID, "acme_dev", "acme_dev_user", "pw", "dev", nil, nil)
	if err != nil {
		t.Fatalf("create database: %v", err)
	}

	return mgr, store, objects, dbRecord.ID
}

// seedBackup creates one backup row and its matching object, so
// applyRetention has something real to compare its deletion decisions
// against — both the metadata row and the object must be gone (or both must
// survive) for a given snapshot.
func seedBackup(t *testing.T, store *meta.MockStore, objects *backup.MemoryStore, dbID int64, key, status string) {
	t.Helper()
	ctx := context.Background()

	b, err := store.CreateBackup(ctx, dbID, key)
	if err != nil {
		t.Fatalf("create backup: %v", err)
	}
	if _, err := objects.Put(ctx, key, bytes.NewReader([]byte("dump-bytes"))); err != nil {
		t.Fatalf("put object %s: %v", key, err)
	}
	errMsg := ""
	if status == meta.BackupStatusFailed {
		errMsg = "pg_dump: boom"
	}
	if err := store.FinishBackup(ctx, b.ID, 10, status, errMsg); err != nil {
		t.Fatalf("finish backup: %v", err)
	}
}

// TestApplyRetentionDeletesOnlyThePastRetentionTail is the test the plan
// calls out by name: the exact deleted-key set at 0, 1, exactly-retention,
// and retention+3 succeeded snapshots. A wrong comparison here would delete
// the only copy of a database, so this asserts precisely which keys survive
// and which don't rather than just a count.
func TestApplyRetentionDeletesOnlyThePastRetentionTail(t *testing.T) {
	const retention = 3

	tests := []struct {
		name        string
		succeeded   int
		wantDeleted int // oldest N of the succeeded snapshots
	}{
		{"zero snapshots", 0, 0},
		{"one snapshot", 1, 0},
		{"exactly retention", retention, 0},
		{"retention plus three", retention + 3, 3},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mgr, store, objects, dbID := newBackupTestManager(t, retention)
			ctx := context.Background()

			var keys []string // oldest first
			for i := 0; i < tt.succeeded; i++ {
				key := fmt.Sprintf("pgmanager/acme_dev/ok-%02d.dump", i)
				seedBackup(t, store, objects, dbID, key, meta.BackupStatusSucceeded)
				keys = append(keys, key)
				// MockStore stamps StartedAt with time.Now(); space creation
				// out so "newest first" is driven by the timestamp, not just
				// the id tiebreaker, matching a real deployment more closely.
				time.Sleep(time.Millisecond)
			}

			if err := mgr.applyRetention(ctx, dbID); err != nil {
				t.Fatalf("applyRetention: %v", err)
			}

			remaining, err := store.ListBackups(ctx, dbID)
			if err != nil {
				t.Fatalf("list backups: %v", err)
			}
			wantRemaining := tt.succeeded - tt.wantDeleted
			if len(remaining) != wantRemaining {
				t.Fatalf("remaining rows = %d, want %d", len(remaining), wantRemaining)
			}

			deleted := keys[:tt.wantDeleted]
			kept := keys[tt.wantDeleted:]
			for _, key := range kept {
				if !objects.Has(key) {
					t.Errorf("expected object %s to survive retention, it did not", key)
				}
			}
			for _, key := range deleted {
				if objects.Has(key) {
					t.Errorf("expected object %s to be deleted by retention, it still exists", key)
				}
			}
		})
	}
}

// Succeeded and failed snapshots are trimmed to the same retention count,
// but independently of each other — a run of failures must not evict
// succeeded backups (or vice versa).
func TestApplyRetentionTrimsFailedIndependentlyOfSucceeded(t *testing.T) {
	const retention = 2
	mgr, store, objects, dbID := newBackupTestManager(t, retention)
	ctx := context.Background()

	var succeededKeys, failedKeys []string
	for i := 0; i < 4; i++ {
		key := fmt.Sprintf("pgmanager/acme_dev/ok-%02d.dump", i)
		seedBackup(t, store, objects, dbID, key, meta.BackupStatusSucceeded)
		succeededKeys = append(succeededKeys, key)
		time.Sleep(time.Millisecond)
	}
	for i := 0; i < 4; i++ {
		key := fmt.Sprintf("pgmanager/acme_dev/bad-%02d.dump", i)
		seedBackup(t, store, objects, dbID, key, meta.BackupStatusFailed)
		failedKeys = append(failedKeys, key)
		time.Sleep(time.Millisecond)
	}

	if err := mgr.applyRetention(ctx, dbID); err != nil {
		t.Fatalf("applyRetention: %v", err)
	}

	checkSurvivors := func(label string, keys []string) {
		for _, key := range keys[2:] {
			if !objects.Has(key) {
				t.Errorf("expected %s object %s to survive, it did not", label, key)
			}
		}
		for _, key := range keys[:2] {
			if objects.Has(key) {
				t.Errorf("expected %s object %s to be deleted, it still exists", label, key)
			}
		}
	}
	checkSurvivors("succeeded", succeededKeys)
	checkSurvivors("failed", failedKeys)

	remaining, err := store.ListBackups(ctx, dbID)
	if err != nil {
		t.Fatalf("list backups: %v", err)
	}
	if len(remaining) != 4 {
		t.Fatalf("remaining rows = %d, want 4 (2 succeeded + 2 failed)", len(remaining))
	}
}

// applyRetention must never run when backups are disabled (m.objects ==
// nil) — a stale retention sweep after DisableBackups must not delete
// anything.
func TestApplyRetentionNoopWhenBackupsDisabled(t *testing.T) {
	cfg := &config.Config{
		Postgres: config.PostgresConfig{Host: "localhost", Port: 5432, User: "postgres", Password: "x", Database: "postgres"},
		Backup:   config.BackupConfig{Retention: 1},
	}
	store := meta.NewMockStore()
	mgr := NewManager(cfg, store)
	ctx := context.Background()

	p, err := store.CreateProject(ctx, "acme")
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	dbRecord, err := store.CreateDatabase(ctx, p.ID, "acme_dev", "acme_dev_user", "pw", "dev", nil, nil)
	if err != nil {
		t.Fatalf("create database: %v", err)
	}
	b, err := store.CreateBackup(ctx, dbRecord.ID, "pgmanager/acme_dev/only.dump")
	if err != nil {
		t.Fatalf("create backup: %v", err)
	}
	if err := store.FinishBackup(ctx, b.ID, 10, meta.BackupStatusSucceeded, ""); err != nil {
		t.Fatalf("finish backup: %v", err)
	}

	// mgr.objects is nil here — EnableBackups was never called.
	if err := mgr.applyRetention(ctx, dbRecord.ID); err != nil {
		t.Fatalf("applyRetention: %v", err)
	}

	remaining, err := store.ListBackups(ctx, dbRecord.ID)
	if err != nil {
		t.Fatalf("list backups: %v", err)
	}
	if len(remaining) != 1 {
		t.Fatalf("remaining rows = %d, want 1 (retention must not run with backups disabled)", len(remaining))
	}
}

// writeInfiniteWriterScript returns the path to an executable that ignores
// its arguments (pg_dump's connection flags, in production) and streams
// bytes to stdout forever until killed or its output stops being read.
//
// Dumper.run is unexported (chunk 3's seam deliberately keeps it that way,
// so tests outside internal/backup can't inject a fake), so the only way
// this package can drive Dumper.Dump's real subprocess path — which is what
// TestBackupNowDoesNotDeadlockWhenUploadFailsMidDump needs to exercise the
// actual io.Pipe wiring — is to point it at a real, if fake, "pg_dump".
func writeInfiniteWriterScript(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "fake-pg_dump.sh")
	script := "#!/bin/sh\nexec dd if=/dev/zero bs=65536\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake pg_dump script: %v", err)
	}
	return path
}

// This is acceptance criterion 4: if the upload dies partway through a
// stream, BackupNow must not hang forever. MemoryStore.FailPutAfterBytes
// simulates a bucket that goes unreachable mid-dump; the fake "pg_dump"
// above writes far more than that limit and never stops on its own, so the
// only way this test finishes is if BackupNow's pipe wiring — closing the
// read end with the upload's error — unblocks the still-writing dump
// goroutine.
func TestBackupNowDoesNotDeadlockWhenUploadFailsMidDump(t *testing.T) {
	mgr, store, objects, dbID := newBackupTestManager(t, 3)
	ctx := context.Background()

	objects.FailPutAfterBytes = 4096

	dumper := backup.NewDumper()
	dumper.DumpPath = writeInfiniteWriterScript(t)
	mgr.dumper = dumper

	done := make(chan struct{})
	var backupErr error
	go func() {
		_, backupErr = mgr.BackupNow(ctx, "acme", "dev", nil)
		close(done)
	}()

	select {
	case <-done:
		if backupErr == nil {
			t.Fatalf("expected BackupNow to report the simulated upload failure, got nil error")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("BackupNow did not return within 10s — the dump goroutine deadlocked")
	}

	backups, err := store.ListBackups(ctx, dbID)
	if err != nil {
		t.Fatalf("list backups: %v", err)
	}
	if len(backups) != 1 || backups[0].Status != meta.BackupStatusFailed {
		t.Fatalf("backups = %+v, want exactly one failed row", backups)
	}
}

// ---------------------------------------------------------------------------
// Cleanup after a cancelled request.
// ---------------------------------------------------------------------------

// Cleanup runs precisely when something went wrong, and the commonest thing
// to have gone wrong is the request context itself — the caller hung up, or
// the deadline expired mid-dump. A cleanup that inherits that dead context
// fails instantly and leaves behind exactly the debris it exists to remove.
func TestCleanupContextOutlivesACancelledRequestContext(t *testing.T) {
	type ctxKey string
	parent, cancel := context.WithCancel(context.WithValue(context.Background(), ctxKey("request-id"), "abc123"))
	cancel()

	if parent.Err() == nil {
		t.Fatal("precondition: the parent context should already be cancelled")
	}

	cleanupCtx, cleanupCancel := cleanupContext(parent)
	defer cleanupCancel()

	if err := cleanupCtx.Err(); err != nil {
		t.Fatalf("cleanup context is already dead (%v); cleanup would fail before it started", err)
	}
	deadline, ok := cleanupCtx.Deadline()
	if !ok {
		t.Fatal("cleanup context has no deadline; a wedged cleanup could hang forever")
	}
	if budget := time.Until(deadline); budget > cleanupTimeout || budget < cleanupTimeout-time.Second {
		t.Errorf("cleanup budget = %v, want ~%v", budget, cleanupTimeout)
	}
	// Request-scoped values must survive — only cancellation is dropped.
	if got := cleanupCtx.Value(ctxKey("request-id")); got != "abc123" {
		t.Errorf("request-id value = %v, want abc123", got)
	}
}

// The same property, exercised through a real call path: MemoryStore honours
// its context, so if deleteBackupObjects passed the request context straight
// through, these objects would still be sitting in the bucket with no
// metadata row left to name them.
func TestDeleteBackupObjectsRunsWithACancelledRequestContext(t *testing.T) {
	mgr, store, objects, dbID := newBackupTestManager(t, 3)

	const key = "pgmanager/acme/acme_dev/20260823T120000Z-aabbccddeeff.dump"
	seedBackup(t, store, objects, dbID, key, meta.BackupStatusSucceeded)

	ctx, cancel := context.WithCancel(context.Background())
	keys := mgr.backupObjectKeys(ctx, dbID)
	if len(keys) != 1 || keys[0] != key {
		t.Fatalf("backupObjectKeys = %v, want [%s]", keys, key)
	}
	cancel()

	mgr.deleteBackupObjects(ctx, "acme_dev", keys)

	if objects.Has(key) {
		t.Fatalf("object %s survived cleanup; it is now orphaned in the bucket", key)
	}
}

// ---------------------------------------------------------------------------
// Deleting a database or a project must take its stored objects with it.
// ---------------------------------------------------------------------------

// pgmanager.backups cascades on databases(id), so the object keys have to be
// read before the metadata goes. Reading them afterwards is not a slower
// path, it is an impossible one.
func TestBackupObjectKeysCollectsEveryStoredSnapshot(t *testing.T) {
	mgr, store, objects, dbID := newBackupTestManager(t, 3)
	ctx := context.Background()

	want := []string{
		"pgmanager/acme/acme_dev/ok.dump",
		"pgmanager/acme/acme_dev/failed.dump",
	}
	seedBackup(t, store, objects, dbID, want[0], meta.BackupStatusSucceeded)
	seedBackup(t, store, objects, dbID, want[1], meta.BackupStatusFailed)

	got := mgr.backupObjectKeys(ctx, dbID)
	if len(got) != len(want) {
		t.Fatalf("backupObjectKeys = %v, want %d keys", got, len(want))
	}
	for _, key := range want {
		if !slices.Contains(got, key) {
			t.Errorf("backupObjectKeys = %v, missing %s", got, key)
		}
	}
}

// Deleting a project cascades every database and every backup row away. If
// the objects are not deleted with them, they stay in the bucket forever
// holding the contents of databases the operator believes they deleted — and
// with the keys gone from the metadata, pgmanager can never name them again.
func TestDeleteProjectDeletesStoredBackupObjects(t *testing.T) {
	mgr, store, objects, dbID := newBackupTestManager(t, 3)
	ctx := context.Background()

	keys := []string{
		"pgmanager/acme/acme_dev/one.dump",
		"pgmanager/acme/acme_dev/two.dump",
	}
	for _, key := range keys {
		seedBackup(t, store, objects, dbID, key, meta.BackupStatusSucceeded)
	}

	// A second database in the same project, also with a snapshot, so the
	// sweep is proven to cover more than the first database it finds.
	p, err := store.GetProject(ctx, "acme")
	if err != nil || p == nil {
		t.Fatalf("get project: %v", err)
	}
	other, err := store.CreateDatabase(ctx, p.ID, "acme_prod", "acme_prod_user", "pw", "prod", nil, nil)
	if err != nil {
		t.Fatalf("create second database: %v", err)
	}
	const otherKey = "pgmanager/acme/acme_prod/one.dump"
	seedBackup(t, store, objects, other.ID, otherKey, meta.BackupStatusSucceeded)

	// DropDatabase reaches a Postgres that isn't there; DeleteProject logs
	// that and carries on by design, which is exactly the path under test.
	if err := mgr.DeleteProject(ctx, "acme"); err != nil {
		t.Fatalf("DeleteProject: %v", err)
	}

	for _, key := range append(keys, otherKey) {
		if objects.Has(key) {
			t.Errorf("object %s survived project deletion; it is now orphaned in the bucket", key)
		}
	}
}

// The safe direction to fail in: if the Postgres drop fails, the database
// still exists, so its backups must still exist too. Losing the only copy of
// a database that is still running would be far worse than leaking an
// object.
func TestDeleteDatabaseKeepsBackupObjectsWhenTheDropFails(t *testing.T) {
	mgr, store, objects, dbID := newBackupTestManager(t, 3)
	ctx := context.Background()

	// Point the manager at a port nothing can be listening on, so the drop
	// is guaranteed to fail rather than depending on the test host.
	mgr.cfg.Postgres.Port = 1
	mgr.pg = db.NewPostgresClient(&mgr.cfg.Postgres)

	const key = "pgmanager/acme/acme_dev/keep-me.dump"
	seedBackup(t, store, objects, dbID, key, meta.BackupStatusSucceeded)

	if err := mgr.DeleteDatabase(ctx, "acme", "dev", nil); err == nil {
		t.Fatal("DeleteDatabase succeeded without a reachable Postgres; the test can't prove anything")
	}

	if !objects.Has(key) {
		t.Errorf("object %s was deleted even though the database was not; its only backup is gone", key)
	}
	remaining, err := store.ListBackups(ctx, dbID)
	if err != nil {
		t.Fatalf("list backups: %v", err)
	}
	if len(remaining) != 1 {
		t.Errorf("backup rows = %d, want 1", len(remaining))
	}
}

// stallingStore is an ObjectStore whose Put never returns on its own for one
// database's keys: it waits for the context it was handed, exactly like an
// upload to an endpoint that accepts the connection and then goes quiet, or
// a pg_dump blocked forever behind a conflicting lock. Every other key is
// handled by the embedded MemoryStore.
type stallingStore struct {
	*backup.MemoryStore
	stallKeySubstring string
}

func (s *stallingStore) Put(ctx context.Context, key string, body io.Reader) (int64, error) {
	if strings.Contains(key, s.stallKeySubstring) {
		<-ctx.Done()
		return 0, ctx.Err()
	}
	return s.MemoryStore.Put(ctx, key, body)
}

// writeTinyDumpScript is a stand-in pg_dump that writes a few bytes and
// exits, so a scheduled backup can succeed without a Postgres server.
func writeTinyDumpScript(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "fake-pg_dump.sh")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nprintf 'dump-bytes'\n"), 0o755); err != nil {
		t.Fatalf("write fake pg_dump script: %v", err)
	}
	return path
}

// TestRunDueBackupsBoundsEachDatabase: the scheduler hands RunDueBackups a
// background context that nothing cancels, and the sweep is serial, so a
// backup that never returns used to strand every database behind it —
// scheduled protection silently stopped for the whole server. Each backup
// now runs under its own deadline, so the stuck one fails and the sweep
// carries on.
func TestRunDueBackupsBoundsEachDatabase(t *testing.T) {
	mgr, store, objects, stuckDBID := newBackupTestManager(t, 3)
	ctx := context.Background()

	proj, err := store.GetProject(ctx, "acme")
	if err != nil || proj == nil {
		t.Fatalf("get project: %v", err)
	}
	healthy, err := store.CreateDatabase(ctx, proj.ID, "acme_staging", "acme_staging_user", "pw", "staging", nil, nil)
	if err != nil {
		t.Fatalf("create second database: %v", err)
	}
	for _, name := range []string{"acme_dev", "acme_staging"} {
		if err := store.SetBackupsEnabled(ctx, name, true); err != nil {
			t.Fatalf("enable backups on %s: %v", name, err)
		}
	}

	dumper := backup.NewDumper()
	dumper.DumpPath = writeTinyDumpScript(t)
	mgr.EnableBackups(&stallingStore{MemoryStore: objects, stallKeySubstring: "acme_dev"}, dumper)

	restore := scheduledBackupTimeout
	scheduledBackupTimeout = 250 * time.Millisecond
	defer func() { scheduledBackupTimeout = restore }()

	type result struct {
		ran int
		err error
	}
	done := make(chan result, 1)
	go func() {
		ran, err := mgr.RunDueBackups(ctx)
		done <- result{ran, err}
	}()

	var got result
	select {
	case got = <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("RunDueBackups never returned — one stalled database stalled the whole sweep")
	}
	if got.err != nil {
		t.Fatalf("RunDueBackups error = %v, want nil (one database failing is logged, not returned)", got.err)
	}
	if got.ran != 1 {
		t.Errorf("ran = %d, want 1 (the healthy database)", got.ran)
	}

	stuck, err := store.ListBackups(ctx, stuckDBID)
	if err != nil {
		t.Fatalf("list stuck backups: %v", err)
	}
	if len(stuck) != 1 || stuck[0].Status != meta.BackupStatusFailed {
		t.Errorf("stalled database backups = %+v, want exactly one failed row", stuck)
	}

	ok, err := store.ListBackups(ctx, healthy.ID)
	if err != nil {
		t.Fatalf("list healthy backups: %v", err)
	}
	if len(ok) != 1 || ok[0].Status != meta.BackupStatusSucceeded {
		t.Fatalf("healthy database backups = %+v, want exactly one succeeded row — it must not be held hostage by the stalled one", ok)
	}
	if !objects.Has(ok[0].Key) {
		t.Errorf("object %s missing from the store", ok[0].Key)
	}
}

// A cancelled sweep stops between databases instead of starting work it
// cannot finish — that is what lets `serve` shut down without waiting out a
// backup's full deadline.
func TestRunDueBackupsStopsWhenCancelled(t *testing.T) {
	mgr, store, _, _ := newBackupTestManager(t, 3)
	ctx := context.Background()

	if err := store.SetBackupsEnabled(ctx, "acme_dev", true); err != nil {
		t.Fatalf("enable backups: %v", err)
	}

	cancelled, cancel := context.WithCancel(ctx)
	cancel()

	ran, err := mgr.RunDueBackups(cancelled)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
	if ran != 0 {
		t.Errorf("ran = %d, want 0", ran)
	}
}
