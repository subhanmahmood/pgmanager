package project

import (
	"context"
	"strings"
	"testing"
	"time"

	"pgmanager/internal/config"
	"pgmanager/internal/meta"
)

func TestValidateName(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{"valid simple name", "myapp", false},
		{"valid with numbers", "myapp123", false},
		{"valid with underscore", "my_app", false},
		{"valid long name", "my_super_cool_application", false},
		{"too short", "a", true},
		{"too long", "this_name_is_way_too_long_for_a_project_name", true},
		{"starts with number", "123app", true},
		{"contains hyphen", "my-app", true},
		{"contains uppercase", "MyApp", true},
		{"reserved name postgres", "postgres", true},
		{"reserved name admin", "admin", true},
		{"reserved name root", "root", true},
		{"reserved name template0", "template0", true},
		{"empty string", "", true},
		{"contains space", "my app", true},
		{"starts with underscore", "_myapp", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateName(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateName(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			}
		})
	}
}

func TestValidateExtensionName(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{"vector", "vector", false},
		{"pg_trgm", "pg_trgm", false},
		{"uuid-ossp (canonical hyphen)", "uuid-ossp", false},
		{"camelCase allowed", "CitusDB", false},
		{"empty", "", true},
		{"starts with digit", "1ext", true},
		{"starts with underscore", "_ext", true},
		{"starts with hyphen", "-ext", true},
		{"contains space", "bad ext", true},
		{"contains semicolon (injection)", "vector;DROP TABLE x", true},
		{"contains quote", "ext'or'1", true},
		{"too long (64 chars)", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateExtensionName(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateExtensionName(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			}
		})
	}
}

func TestValidateEnv(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{"prod environment", "prod", false},
		{"dev environment", "dev", false},
		{"staging environment", "staging", false},
		{"pr environment", "pr", false},
		{"invalid environment", "test", true},
		{"empty environment", "", true},
		{"uppercase", "PROD", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateEnv(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateEnv(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			}
		})
	}
}

func TestDatabaseName(t *testing.T) {
	tests := []struct {
		name     string
		project  string
		env      string
		prNumber *int
		want     string
	}{
		{"prod database", "myapp", "prod", nil, "myapp_prod"},
		{"dev database", "myapp", "dev", nil, "myapp_dev"},
		{"staging database", "myapp", "staging", nil, "myapp_staging"},
		{"pr database", "myapp", "pr", intPtr(123), "myapp_pr_123"},
		{"pr database with different number", "myapp", "pr", intPtr(456), "myapp_pr_456"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DatabaseName(tt.project, tt.env, tt.prNumber)
			if got != tt.want {
				t.Errorf("DatabaseName(%q, %q, %v) = %q, want %q", tt.project, tt.env, tt.prNumber, got, tt.want)
			}
		})
	}
}

func TestUserName(t *testing.T) {
	tests := []struct {
		name   string
		dbName string
		want   string
	}{
		{"prod user", "myapp_prod", "myapp_prod_user"},
		{"dev user", "myapp_dev", "myapp_dev_user"},
		{"pr user", "myapp_pr_123", "myapp_pr_123_user"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := UserName(tt.dbName)
			if got != tt.want {
				t.Errorf("UserName(%q) = %q, want %q", tt.dbName, got, tt.want)
			}
		})
	}
}

func TestParseEnv(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantEnv string
		wantPR  *int
		wantErr bool
	}{
		{"prod environment", "prod", "prod", nil, false},
		{"dev environment", "dev", "dev", nil, false},
		{"pr environment", "pr_123", "pr", intPtr(123), false},
		{"pr environment high number", "pr_9999", "pr", intPtr(9999), false},
		{"invalid pr format", "pr_abc", "", nil, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotEnv, gotPR, err := ParseEnv(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseEnv(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
				return
			}
			if gotEnv != tt.wantEnv {
				t.Errorf("ParseEnv(%q) env = %q, want %q", tt.input, gotEnv, tt.wantEnv)
			}
			if (gotPR == nil) != (tt.wantPR == nil) {
				t.Errorf("ParseEnv(%q) prNumber = %v, want %v", tt.input, gotPR, tt.wantPR)
			}
			if gotPR != nil && tt.wantPR != nil && *gotPR != *tt.wantPR {
				t.Errorf("ParseEnv(%q) prNumber = %d, want %d", tt.input, *gotPR, *tt.wantPR)
			}
		})
	}
}

func intPtr(i int) *int {
	return &i
}

// TestRestoreDatabaseNameFormat pins the exact name/user/env-segment shape a
// restore produces, since chunk 6's client and the admin UI's restore dialog
// will need to reconstruct or display these.
func TestRestoreDatabaseNameFormat(t *testing.T) {
	ts := time.Date(2026, 8, 23, 10, 15, 0, 0, time.UTC)

	dbName, userName, envSegment, err := restoreDatabaseName("myapp", "dev", ts)
	if err != nil {
		t.Fatalf("restoreDatabaseName() error = %v", err)
	}
	if want := "myapp_dev_restore_20260823T101500"; dbName != want {
		t.Errorf("dbName = %q, want %q", dbName, want)
	}
	if want := "myapp_dev_restore_20260823T101500_user"; userName != want {
		t.Errorf("userName = %q, want %q", userName, want)
	}
	if want := "dev_restore_20260823T101500"; envSegment != want {
		t.Errorf("envSegment = %q, want %q", envSegment, want)
	}
	// The env segment is exactly the database name with the project prefix
	// stripped — that's the invariant ListDatabases and RestoreBackup both
	// rely on for a restored row.
	if got := dbName; got != "myapp_"+envSegment {
		t.Errorf("dbName %q is not \"myapp_\"+envSegment (%q)", got, envSegment)
	}
}

// TestRestoreDatabaseNameNonUTCInput proves the timestamp is normalized to
// UTC before formatting, so two restores of the same instant from different
// server-local-time inputs never produce different identifiers.
func TestRestoreDatabaseNameNonUTCInput(t *testing.T) {
	loc := time.FixedZone("UTC-5", -5*60*60)
	ts := time.Date(2026, 8, 23, 5, 15, 0, 0, loc) // 10:15 UTC

	_, _, envSegment, err := restoreDatabaseName("myapp", "dev", ts)
	if err != nil {
		t.Fatalf("restoreDatabaseName() error = %v", err)
	}
	if want := "dev_restore_20260823T101500"; envSegment != want {
		t.Errorf("envSegment = %q, want %q (non-UTC input not normalized)", envSegment, want)
	}
}

// TestRestoreDatabaseNameIdentifierLimit is the 63-byte guard the plan calls
// out by name: a project name of 26 characters always fits (even with the
// longest env, "staging"), and 27 characters always overflows. Postgres
// silently truncates identifiers past its limit, which would risk two
// different restores colliding on one role name — this must be a hard
// error, never a silent truncation.
func TestRestoreDatabaseNameIdentifierLimit(t *testing.T) {
	ts := time.Date(2026, 8, 23, 10, 15, 0, 0, time.UTC)

	fits := strings.Repeat("a", 26)
	if _, userName, _, err := restoreDatabaseName(fits, "staging", ts); err != nil {
		t.Errorf("26-char project name: unexpected error: %v", err)
	} else if len(userName) != maxIdentifierBytes {
		t.Errorf("26-char project name: userName length = %d, want exactly %d", len(userName), maxIdentifierBytes)
	}

	overflows := strings.Repeat("a", 27)
	if _, _, _, err := restoreDatabaseName(overflows, "staging", ts); err == nil {
		t.Errorf("27-char project name: expected an error, got none")
	}
}

// newRestoreTestManager builds a Manager backed by a MockStore, with one
// project and one non-restored database ready to resolve.
func newRestoreTestManager(t *testing.T) (mgr *Manager, store *meta.MockStore, projectID, dbID int64) {
	t.Helper()

	cfg := &config.Config{
		Postgres: config.PostgresConfig{
			Host: "localhost", Port: 5432, User: "postgres", Password: "x", Database: "postgres",
		},
	}
	store = meta.NewMockStore()
	mgr = NewManager(cfg, store)

	ctx := context.Background()
	p, err := store.CreateProject(ctx, "acme")
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	dbRecord, err := store.CreateDatabase(ctx, p.ID, "acme_dev", "acme_dev_user", "pw", "dev", nil, nil)
	if err != nil {
		t.Fatalf("create database: %v", err)
	}
	return mgr, store, p.ID, dbRecord.ID
}

// TestResolveDatabasePrefersNameMatch proves resolveDatabase finds a
// restored database by name — the only way it's reachable, since
// meta.Store.GetDatabase filters restored rows out — and that an ordinary
// "dev" lookup still returns the original database once a restore of it
// exists. The two paths sharing one implementation must never disagree
// about which row a segment names.
func TestResolveDatabasePrefersNameMatch(t *testing.T) {
	mgr, store, projectID, dbID := newRestoreTestManager(t)
	ctx := context.Background()

	// Plain lookup finds the original database before any restore exists.
	_, rec, err := mgr.resolveDatabase(ctx, "acme", "dev", nil)
	if err != nil {
		t.Fatalf("resolveDatabase(dev) before restore: %v", err)
	}
	if rec.Name != "acme_dev" {
		t.Fatalf("resolveDatabase(dev) before restore: got %q, want acme_dev", rec.Name)
	}

	// Create a restored row the same way RestoreBackup does: a succeeded
	// backup of acme_dev, then CreateRestoredDatabase.
	b, err := store.CreateBackup(ctx, dbID, "pgmanager/acme/acme_dev/20260823T101500Z.dump")
	if err != nil {
		t.Fatalf("create backup: %v", err)
	}
	if err := store.FinishBackup(ctx, b.ID, 1024, meta.BackupStatusSucceeded, ""); err != nil {
		t.Fatalf("finish backup: %v", err)
	}
	restoredName := "acme_dev_restore_20260823T101500"
	restored, err := store.CreateRestoredDatabase(ctx, projectID, b.ID, restoredName, restoredName+"_user", "pw2", "dev")
	if err != nil {
		t.Fatalf("create restored database: %v", err)
	}

	// The restored database is reachable only by its full name segment.
	_, rec, err = mgr.resolveDatabase(ctx, "acme", "dev_restore_20260823T101500", nil)
	if err != nil {
		t.Fatalf("resolveDatabase(dev_restore_...): %v", err)
	}
	if rec.ID != restored.ID {
		t.Errorf("resolveDatabase(dev_restore_...) = database %d (%s), want %d (%s)", rec.ID, rec.Name, restored.ID, restored.Name)
	}

	// A plain "dev" lookup still returns the original — never the restore —
	// even though a restored row with a matching source env now exists.
	_, rec, err = mgr.resolveDatabase(ctx, "acme", "dev", nil)
	if err != nil {
		t.Fatalf("resolveDatabase(dev) after restore: %v", err)
	}
	if rec.Name != "acme_dev" {
		t.Errorf("resolveDatabase(dev) after restore: got %q, want acme_dev (must not return the restored row)", rec.Name)
	}
}
