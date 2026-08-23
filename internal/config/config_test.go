package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestPublicHostEnvOverrides locks in POSTGRES_PUBLIC_HOST / POSTGRES_PUBLIC_PORT
// behaviour: they overlay onto whatever was loaded from YAML, and an
// unparseable port is silently ignored (matches the existing POSTGRES_PORT
// handling — strconv.Atoi returns an error and we keep the YAML value).
func TestPublicHostEnvOverrides(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pgmanager.yaml")
	yaml := `postgres:
  host: postgres
  port: 5432
  public_host: yaml-host.example.com
  public_port: 5432
`
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	t.Run("YAML values picked up", func(t *testing.T) {
		t.Setenv("POSTGRES_PUBLIC_HOST", "")
		t.Setenv("POSTGRES_PUBLIC_PORT", "")
		cfg, err := Load(path)
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if got := cfg.Postgres.PublicHost; got != "yaml-host.example.com" {
			t.Errorf("PublicHost = %q, want yaml-host.example.com", got)
		}
		if got := cfg.Postgres.PublicPort; got != 5432 {
			t.Errorf("PublicPort = %d, want 5432", got)
		}
	})

	t.Run("Env overrides YAML", func(t *testing.T) {
		t.Setenv("POSTGRES_PUBLIC_HOST", "env-host.example.com")
		t.Setenv("POSTGRES_PUBLIC_PORT", "6543")
		cfg, err := Load(path)
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if got := cfg.Postgres.PublicHost; got != "env-host.example.com" {
			t.Errorf("PublicHost = %q, want env-host.example.com", got)
		}
		if got := cfg.Postgres.PublicPort; got != 6543 {
			t.Errorf("PublicPort = %d, want 6543", got)
		}
	})

	t.Run("Unparseable PUBLIC_PORT leaves YAML value intact", func(t *testing.T) {
		t.Setenv("POSTGRES_PUBLIC_HOST", "")
		t.Setenv("POSTGRES_PUBLIC_PORT", "not-a-number")
		cfg, err := Load(path)
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if got := cfg.Postgres.PublicPort; got != 5432 {
			t.Errorf("PublicPort = %d, want 5432 (YAML value retained)", got)
		}
	})
}

func TestEffectiveHostPort(t *testing.T) {
	tests := []struct {
		name     string
		cfg      PostgresConfig
		wantHost string
		wantPort int
	}{
		{
			name:     "PublicHost wins over Host",
			cfg:      PostgresConfig{Host: "postgres", Port: 5432, PublicHost: "pgm.example.com", PublicPort: 6543},
			wantHost: "pgm.example.com",
			wantPort: 6543,
		},
		{
			name:     "Falls back to Host when PublicHost unset",
			cfg:      PostgresConfig{Host: "postgres", Port: 5432},
			wantHost: "postgres",
			wantPort: 5432,
		},
		{
			name:     "PublicPort=0 means use Port",
			cfg:      PostgresConfig{Host: "postgres", Port: 5432, PublicHost: "pgm.example.com"},
			wantHost: "pgm.example.com",
			wantPort: 5432,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.cfg.EffectiveHost(); got != tt.wantHost {
				t.Errorf("EffectiveHost() = %q, want %q", got, tt.wantHost)
			}
			if got := tt.cfg.EffectivePort(); got != tt.wantPort {
				t.Errorf("EffectivePort() = %d, want %d", got, tt.wantPort)
			}
		})
	}
}

// TestBackupEnvOverrides locks in the PGMANAGER_BACKUP_* env var behaviour:
// each one overlays onto whatever was loaded from YAML (or the zero value),
// and unparseable durations/ints are silently ignored, matching the existing
// PGMANAGER_SESSION_TTL / POSTGRES_PORT handling.
func TestBackupEnvOverrides(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pgmanager.yaml")
	yaml := `backup:
  enabled: false
  bucket: yaml-bucket
  schedule: 12h
  retention: 3
`
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	envVars := []string{
		"PGMANAGER_BACKUP_ENABLED",
		"PGMANAGER_BACKUP_ENDPOINT",
		"PGMANAGER_BACKUP_REGION",
		"PGMANAGER_BACKUP_BUCKET",
		"PGMANAGER_BACKUP_PREFIX",
		"PGMANAGER_BACKUP_ACCESS_KEY_ID",
		"PGMANAGER_BACKUP_SECRET_ACCESS_KEY",
		"PGMANAGER_BACKUP_SECRET_ACCESS_KEY_FILE",
		"PGMANAGER_BACKUP_SCHEDULE",
		"PGMANAGER_BACKUP_RETENTION",
	}
	clearEnv := func(t *testing.T) {
		for _, e := range envVars {
			t.Setenv(e, "")
		}
	}

	t.Run("YAML values picked up with no env set", func(t *testing.T) {
		clearEnv(t)
		cfg, err := Load(path)
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if cfg.Backup.Enabled {
			t.Errorf("Enabled = true, want false")
		}
		if got := cfg.Backup.Bucket; got != "yaml-bucket" {
			t.Errorf("Bucket = %q, want yaml-bucket", got)
		}
		if got := cfg.Backup.Schedule; got != 12*time.Hour {
			t.Errorf("Schedule = %s, want 12h", got)
		}
		if got := cfg.Backup.Retention; got != 3 {
			t.Errorf("Retention = %d, want 3", got)
		}
	})

	t.Run("env overrides every field", func(t *testing.T) {
		clearEnv(t)
		t.Setenv("PGMANAGER_BACKUP_ENABLED", "true")
		t.Setenv("PGMANAGER_BACKUP_ENDPOINT", "https://s3.example.com")
		t.Setenv("PGMANAGER_BACKUP_REGION", "us-east-1")
		t.Setenv("PGMANAGER_BACKUP_BUCKET", "env-bucket")
		t.Setenv("PGMANAGER_BACKUP_PREFIX", "custom/")
		t.Setenv("PGMANAGER_BACKUP_ACCESS_KEY_ID", "AKIAENV")
		t.Setenv("PGMANAGER_BACKUP_SECRET_ACCESS_KEY", "env-secret")
		t.Setenv("PGMANAGER_BACKUP_SCHEDULE", "6h")
		t.Setenv("PGMANAGER_BACKUP_RETENTION", "14")

		cfg, err := Load(path)
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if !cfg.Backup.Enabled {
			t.Errorf("Enabled = false, want true")
		}
		if got := cfg.Backup.Endpoint; got != "https://s3.example.com" {
			t.Errorf("Endpoint = %q", got)
		}
		if got := cfg.Backup.Region; got != "us-east-1" {
			t.Errorf("Region = %q", got)
		}
		if got := cfg.Backup.Bucket; got != "env-bucket" {
			t.Errorf("Bucket = %q, want env-bucket", got)
		}
		if got := cfg.Backup.Prefix; got != "custom/" {
			t.Errorf("Prefix = %q", got)
		}
		if got := cfg.Backup.AccessKeyID; got != "AKIAENV" {
			t.Errorf("AccessKeyID = %q", got)
		}
		if got := cfg.Backup.SecretAccessKey; got != "env-secret" {
			t.Errorf("SecretAccessKey = %q", got)
		}
		if got := cfg.Backup.Schedule; got != 6*time.Hour {
			t.Errorf("Schedule = %s, want 6h", got)
		}
		if got := cfg.Backup.Retention; got != 14 {
			t.Errorf("Retention = %d, want 14", got)
		}
	})

	t.Run("unparseable schedule and retention leave YAML values intact", func(t *testing.T) {
		clearEnv(t)
		t.Setenv("PGMANAGER_BACKUP_SCHEDULE", "not-a-duration")
		t.Setenv("PGMANAGER_BACKUP_RETENTION", "not-a-number")

		cfg, err := Load(path)
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if got := cfg.Backup.Schedule; got != 12*time.Hour {
			t.Errorf("Schedule = %s, want 12h (YAML value retained)", got)
		}
		if got := cfg.Backup.Retention; got != 3 {
			t.Errorf("Retention = %d, want 3 (YAML value retained)", got)
		}
	})

	t.Run("secret access key file env override", func(t *testing.T) {
		clearEnv(t)
		secretFile := filepath.Join(dir, "secret.txt")
		if err := os.WriteFile(secretFile, []byte("from-file-secret\n"), 0o600); err != nil {
			t.Fatalf("write secret file: %v", err)
		}
		t.Setenv("PGMANAGER_BACKUP_SECRET_ACCESS_KEY_FILE", secretFile)

		cfg, err := Load(path)
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if got := cfg.Backup.SecretAccessKeyFile; got != secretFile {
			t.Errorf("SecretAccessKeyFile = %q, want %q", got, secretFile)
		}
	})
}

func TestBackupConfigEffectivePrefix(t *testing.T) {
	tests := []struct {
		name   string
		prefix string
		want   string
	}{
		{name: "empty defaults to pgmanager/", prefix: "", want: "pgmanager/"},
		{name: "trailing slash preserved", prefix: "custom/", want: "custom/"},
		{name: "missing trailing slash added", prefix: "custom", want: "custom/"},
		{name: "nested prefix gets trailing slash", prefix: "a/b", want: "a/b/"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &BackupConfig{Prefix: tt.prefix}
			if got := c.EffectivePrefix(); got != tt.want {
				t.Errorf("EffectivePrefix() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestBackupConfigSecret(t *testing.T) {
	t.Run("inline wins over file", func(t *testing.T) {
		dir := t.TempDir()
		secretFile := filepath.Join(dir, "secret.txt")
		if err := os.WriteFile(secretFile, []byte("file-secret"), 0o600); err != nil {
			t.Fatalf("write secret file: %v", err)
		}
		c := &BackupConfig{SecretAccessKey: "inline-secret", SecretAccessKeyFile: secretFile}
		got, err := c.Secret()
		if err != nil {
			t.Fatalf("Secret: %v", err)
		}
		if got != "inline-secret" {
			t.Errorf("Secret() = %q, want inline-secret", got)
		}
	})

	t.Run("falls back to file, trimmed", func(t *testing.T) {
		dir := t.TempDir()
		secretFile := filepath.Join(dir, "secret.txt")
		if err := os.WriteFile(secretFile, []byte("file-secret\n"), 0o600); err != nil {
			t.Fatalf("write secret file: %v", err)
		}
		c := &BackupConfig{SecretAccessKeyFile: secretFile}
		got, err := c.Secret()
		if err != nil {
			t.Fatalf("Secret: %v", err)
		}
		if got != "file-secret" {
			t.Errorf("Secret() = %q, want file-secret", got)
		}
	})

	t.Run("neither set returns empty, no error", func(t *testing.T) {
		c := &BackupConfig{}
		got, err := c.Secret()
		if err != nil {
			t.Fatalf("Secret: %v", err)
		}
		if got != "" {
			t.Errorf("Secret() = %q, want empty", got)
		}
	})

	t.Run("unreadable file returns error", func(t *testing.T) {
		c := &BackupConfig{SecretAccessKeyFile: filepath.Join(t.TempDir(), "does-not-exist.txt")}
		if _, err := c.Secret(); err == nil {
			t.Fatal("Secret() error = nil, want error for missing file")
		}
	})
}

func TestBackupConfigValidate(t *testing.T) {
	valid := func() BackupConfig {
		return BackupConfig{
			Enabled:         true,
			Bucket:          "my-bucket",
			AccessKeyID:     "AKIA...",
			SecretAccessKey: "shh",
			Schedule:        time.Hour,
			Retention:       7,
		}
	}

	t.Run("disabled config is always valid", func(t *testing.T) {
		c := BackupConfig{}
		if err := c.Validate(); err != nil {
			t.Errorf("Validate() = %v, want nil for disabled config", err)
		}
	})

	t.Run("valid enabled config passes", func(t *testing.T) {
		c := valid()
		if err := c.Validate(); err != nil {
			t.Errorf("Validate() = %v, want nil", err)
		}
	})

	tests := []struct {
		name    string
		mutate  func(c *BackupConfig)
		wantErr bool
	}{
		{name: "missing bucket", mutate: func(c *BackupConfig) { c.Bucket = "" }, wantErr: true},
		{name: "missing access key id", mutate: func(c *BackupConfig) { c.AccessKeyID = "" }, wantErr: true},
		{name: "missing secret", mutate: func(c *BackupConfig) { c.SecretAccessKey = "" }, wantErr: true},
		{name: "retention zero", mutate: func(c *BackupConfig) { c.Retention = 0 }, wantErr: true},
		{name: "retention negative", mutate: func(c *BackupConfig) { c.Retention = -1 }, wantErr: true},
		{name: "schedule under a minute", mutate: func(c *BackupConfig) { c.Schedule = 30 * time.Second }, wantErr: true},
		{name: "schedule exactly a minute is ok", mutate: func(c *BackupConfig) { c.Schedule = time.Minute }, wantErr: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := valid()
			tt.mutate(&c)
			err := c.Validate()
			if tt.wantErr && err == nil {
				t.Error("Validate() = nil, want error")
			}
			if !tt.wantErr && err != nil {
				t.Errorf("Validate() = %v, want nil", err)
			}
		})
	}
}
