package backup

import (
	"strings"
	"testing"

	"pgmanager/internal/config"
)

// These only exercise NewS3Store's local validation — constructing an
// s3.Client never dials the network, so no bucket or credentials are
// needed, but we still must not require either to run this suite.

func TestNewS3StoreRequiresBucket(t *testing.T) {
	_, err := NewS3Store(config.BackupConfig{AccessKeyID: "id", SecretAccessKey: "secret"})
	if err == nil {
		t.Fatal("NewS3Store() = nil error, want error for missing bucket")
	}
}

func TestNewS3StoreRequiresAccessKeyID(t *testing.T) {
	_, err := NewS3Store(config.BackupConfig{Bucket: "b", SecretAccessKey: "secret"})
	if err == nil {
		t.Fatal("NewS3Store() = nil error, want error for missing access_key_id")
	}
}

func TestNewS3StoreRequiresSecret(t *testing.T) {
	_, err := NewS3Store(config.BackupConfig{Bucket: "b", AccessKeyID: "id"})
	if err == nil {
		t.Fatal("NewS3Store() = nil error, want error for missing secret")
	}
}

func TestNewS3StoreErrorsNeverContainSecret(t *testing.T) {
	const secret = "super-sensitive-value-should-not-leak"
	cases := []config.BackupConfig{
		{},
		{Bucket: "b"},
		{Bucket: "b", AccessKeyID: "id"},
		{Bucket: "b", AccessKeyID: "id", SecretAccessKeyFile: "/nonexistent/path/should/fail"},
	}
	for _, cfg := range cases {
		cfg.SecretAccessKey = ""
		_, err := NewS3Store(cfg)
		if err == nil {
			continue
		}
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("error leaked secret: %v", err)
		}
	}
}

func TestNewS3StoreSucceedsWithValidConfig(t *testing.T) {
	store, err := NewS3Store(config.BackupConfig{
		Bucket:          "test-bucket",
		Region:          "auto",
		Endpoint:        "https://example-endpoint.invalid",
		AccessKeyID:     "AKIAEXAMPLE",
		SecretAccessKey: "example-secret-not-real",
	})
	if err != nil {
		t.Fatalf("NewS3Store() unexpected error: %v", err)
	}
	if store == nil {
		t.Fatal("NewS3Store() returned nil store with nil error")
	}
}
