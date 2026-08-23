package meta

import (
	"context"
	"testing"
)

// seedProjectAndDatabase creates a project and one database in it, returning
// both IDs for tests that need to attach backups to a real database row.
func seedProjectAndDatabase(t *testing.T, s *MockStore) (projectID, databaseID int64) {
	t.Helper()
	ctx := context.Background()

	p, err := s.CreateProject(ctx, "acme")
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	db, err := s.CreateDatabase(ctx, p.ID, "acme_dev", "acme_dev_user", "pw", "dev", nil, nil)
	if err != nil {
		t.Fatalf("create database: %v", err)
	}
	return p.ID, db.ID
}

func TestBackupLifecycle(t *testing.T) {
	tests := []struct {
		name      string
		status    string
		size      int64
		errMsg    string
		wantError string
	}{
		{name: "succeeded", status: BackupStatusSucceeded, size: 1024, errMsg: ""},
		{name: "failed", status: BackupStatusFailed, size: 0, errMsg: "pg_dump: connection refused", wantError: "pg_dump: connection refused"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := NewMockStore()
			ctx := context.Background()
			_, dbID := seedProjectAndDatabase(t, s)

			b, err := s.CreateBackup(ctx, dbID, "pgmanager/acme_dev/2026-08-23T00-00-00.dump")
			if err != nil {
				t.Fatalf("create backup: %v", err)
			}
			if b.Status != BackupStatusRunning {
				t.Fatalf("status = %q, want %q", b.Status, BackupStatusRunning)
			}
			if b.FinishedAt != nil {
				t.Fatalf("expected FinishedAt nil for a running backup, got %v", b.FinishedAt)
			}

			if err := s.FinishBackup(ctx, b.ID, tt.size, tt.status, tt.errMsg); err != nil {
				t.Fatalf("finish backup: %v", err)
			}

			got, err := s.GetBackup(ctx, b.ID)
			if err != nil {
				t.Fatalf("get backup: %v", err)
			}
			if got == nil {
				t.Fatal("get backup: not found")
			}
			if got.Status != tt.status {
				t.Errorf("status = %q, want %q", got.Status, tt.status)
			}
			if got.SizeBytes != tt.size {
				t.Errorf("size = %d, want %d", got.SizeBytes, tt.size)
			}
			if got.Error != tt.wantError {
				t.Errorf("error = %q, want %q", got.Error, tt.wantError)
			}
			if got.FinishedAt == nil {
				t.Error("expected FinishedAt to be set after FinishBackup")
			}
		})
	}
}

func TestFinishBackupUnknownID(t *testing.T) {
	s := NewMockStore()
	if err := s.FinishBackup(context.Background(), 999, 0, BackupStatusSucceeded, ""); err == nil {
		t.Fatal("expected error finishing an unknown backup")
	}
}

func TestGetBackupNotFound(t *testing.T) {
	s := NewMockStore()
	b, err := s.GetBackup(context.Background(), 999)
	if err != nil {
		t.Fatalf("get backup: %v", err)
	}
	if b != nil {
		t.Fatalf("expected nil backup, got %+v", b)
	}
}

func TestListBackupsNewestFirst(t *testing.T) {
	s := NewMockStore()
	ctx := context.Background()
	_, dbID := seedProjectAndDatabase(t, s)

	var created []*Backup
	for i := 0; i < 3; i++ {
		b, err := s.CreateBackup(ctx, dbID, "key")
		if err != nil {
			t.Fatalf("create backup %d: %v", i, err)
		}
		created = append(created, b)
	}

	got, err := s.ListBackups(ctx, dbID)
	if err != nil {
		t.Fatalf("list backups: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d backups, want 3", len(got))
	}
	// StartedAt may tie within the same instant, so id descending is the
	// tiebreaker the interface promises: newest first.
	for i := 0; i < len(got); i++ {
		want := created[len(created)-1-i]
		if got[i].ID != want.ID {
			t.Errorf("position %d: got id %d, want %d (newest-first order)", i, got[i].ID, want.ID)
		}
	}
}

func TestListBackupsScopedToDatabase(t *testing.T) {
	s := NewMockStore()
	ctx := context.Background()
	p, err := s.CreateProject(ctx, "acme")
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	db1, err := s.CreateDatabase(ctx, p.ID, "acme_dev", "u1", "pw", "dev", nil, nil)
	if err != nil {
		t.Fatalf("create database 1: %v", err)
	}
	db2, err := s.CreateDatabase(ctx, p.ID, "acme_staging", "u2", "pw", "staging", nil, nil)
	if err != nil {
		t.Fatalf("create database 2: %v", err)
	}
	if _, err := s.CreateBackup(ctx, db1.ID, "key1"); err != nil {
		t.Fatalf("create backup for db1: %v", err)
	}
	if _, err := s.CreateBackup(ctx, db2.ID, "key2"); err != nil {
		t.Fatalf("create backup for db2: %v", err)
	}

	got, err := s.ListBackups(ctx, db1.ID)
	if err != nil {
		t.Fatalf("list backups: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d backups for db1, want 1", len(got))
	}
	if got[0].DatabaseID != db1.ID {
		t.Errorf("backup database_id = %d, want %d", got[0].DatabaseID, db1.ID)
	}
}

func TestDeleteBackup(t *testing.T) {
	s := NewMockStore()
	ctx := context.Background()
	_, dbID := seedProjectAndDatabase(t, s)

	b, err := s.CreateBackup(ctx, dbID, "key")
	if err != nil {
		t.Fatalf("create backup: %v", err)
	}

	if err := s.DeleteBackup(ctx, b.ID); err != nil {
		t.Fatalf("delete backup: %v", err)
	}

	got, err := s.GetBackup(ctx, b.ID)
	if err != nil {
		t.Fatalf("get backup after delete: %v", err)
	}
	if got != nil {
		t.Fatalf("expected backup to be gone, got %+v", got)
	}

	if err := s.DeleteBackup(ctx, b.ID); err == nil {
		t.Fatal("expected error deleting an already-deleted backup")
	}
}

func TestSetBackupsEnabledRoundTrip(t *testing.T) {
	s := NewMockStore()
	ctx := context.Background()
	_, dbID := seedProjectAndDatabase(t, s)

	db, err := s.GetDatabaseByName(ctx, "acme_dev")
	if err != nil {
		t.Fatalf("get database: %v", err)
	}
	if db.BackupsEnabled {
		t.Fatal("expected backups_enabled to default false")
	}

	if err := s.SetBackupsEnabled(ctx, "acme_dev", true); err != nil {
		t.Fatalf("set backups enabled: %v", err)
	}
	db, err = s.GetDatabaseByName(ctx, "acme_dev")
	if err != nil {
		t.Fatalf("get database: %v", err)
	}
	if !db.BackupsEnabled {
		t.Fatal("expected backups_enabled true after enabling")
	}

	enabled, err := s.ListBackupEnabledDatabases(ctx)
	if err != nil {
		t.Fatalf("list backup enabled databases: %v", err)
	}
	if len(enabled) != 1 || enabled[0].ID != dbID {
		t.Fatalf("list backup enabled databases = %+v, want just %d", enabled, dbID)
	}

	if err := s.SetBackupsEnabled(ctx, "acme_dev", false); err != nil {
		t.Fatalf("disable backups: %v", err)
	}
	db, err = s.GetDatabaseByName(ctx, "acme_dev")
	if err != nil {
		t.Fatalf("get database: %v", err)
	}
	if db.BackupsEnabled {
		t.Fatal("expected backups_enabled false after disabling")
	}

	enabled, err = s.ListBackupEnabledDatabases(ctx)
	if err != nil {
		t.Fatalf("list backup enabled databases: %v", err)
	}
	if len(enabled) != 0 {
		t.Fatalf("expected no backup-enabled databases, got %+v", enabled)
	}
}

func TestSetBackupsEnabledUnknownDatabase(t *testing.T) {
	s := NewMockStore()
	if err := s.SetBackupsEnabled(context.Background(), "does_not_exist", true); err == nil {
		t.Fatal("expected error enabling backups on an unknown database")
	}
}

// TestGetDatabaseSkipsRestored proves that GetDatabase (looked up by
// project/env/prNumber) never returns a database created by
// CreateRestoredDatabase, while GetDatabaseByName still finds it. Restored
// databases carry the source env, so GetDatabase would otherwise be
// ambiguous between the original and the restore.
func TestGetDatabaseSkipsRestored(t *testing.T) {
	s := NewMockStore()
	ctx := context.Background()
	projectID, dbID := seedProjectAndDatabase(t, s)

	backup, err := s.CreateBackup(ctx, dbID, "key")
	if err != nil {
		t.Fatalf("create backup: %v", err)
	}

	restored, err := s.CreateRestoredDatabase(ctx, projectID, backup.ID, "acme_dev_restore_1", "acme_dev_restore_1_user", "pw", "dev")
	if err != nil {
		t.Fatalf("create restored database: %v", err)
	}
	if restored.RestoredFrom == nil || *restored.RestoredFrom != backup.ID {
		t.Fatalf("restored.RestoredFrom = %v, want %d", restored.RestoredFrom, backup.ID)
	}

	// GetDatabase(project, "dev", nil) must still resolve to the original
	// database, not the restore, even though both share env "dev".
	got, err := s.GetDatabase(ctx, projectID, "dev", nil)
	if err != nil {
		t.Fatalf("get database: %v", err)
	}
	if got == nil {
		t.Fatal("expected the original database, got nil")
	}
	if got.ID != dbID {
		t.Fatalf("GetDatabase returned id %d, want the original database's id %d", got.ID, dbID)
	}

	// GetDatabaseByName must still find the restored row.
	byName, err := s.GetDatabaseByName(ctx, "acme_dev_restore_1")
	if err != nil {
		t.Fatalf("get database by name: %v", err)
	}
	if byName == nil {
		t.Fatal("expected to find the restored database by name")
	}
	if byName.ID != restored.ID {
		t.Fatalf("GetDatabaseByName returned id %d, want %d", byName.ID, restored.ID)
	}
}
