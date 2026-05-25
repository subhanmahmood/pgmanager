package config

import (
	"os"
	"path/filepath"
	"testing"
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
