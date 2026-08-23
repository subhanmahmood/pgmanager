package project

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"pgmanager/internal/backup"
	"pgmanager/internal/config"
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
