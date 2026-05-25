package config

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// ConfigFileNames are the names to search for when auto-discovering the
// server config (pgmanager.yaml).
var ConfigFileNames = []string{
	"pgmanager.yaml",
	"pgmanager.yml",
	".pgmanager.yaml",
	".pgmanager.yml",
}

// Config is the server-side configuration loaded by `pgmanager serve`.
// Client-side configuration (profiles, API tokens) lives in ClientConfig.
type Config struct {
	Postgres PostgresConfig `yaml:"postgres"`
	API      APIConfig      `yaml:"api"`
	Cleanup  CleanupConfig  `yaml:"cleanup"`
	Crypto   CryptoConfig   `yaml:"crypto"`
	DataDir  string         `yaml:"data_dir"`
}

type PostgresConfig struct {
	Host     string `yaml:"host"`
	Port     int    `yaml:"port"`
	User     string `yaml:"user"`
	Password string `yaml:"password"`
	Database string `yaml:"database"`
	SSLMode  string `yaml:"ssl_mode"` // disable, require, verify-ca, verify-full

	// PublicHost / PublicPort are how *clients* reach Postgres. The server's
	// own connection uses Host / Port (unchanged). When unset, db responses
	// fall back to the inbound request's Host header (port stripped), then
	// finally to Host / Port. See internal/api.Server.publicHostPort.
	PublicHost string `yaml:"public_host"`
	PublicPort int    `yaml:"public_port"`
}

type APIConfig struct {
	Listen         string   `yaml:"listen"`          // bind address, e.g., "127.0.0.1:8080"
	Port           int      `yaml:"port"`            // legacy; used if Listen is empty
	Token          string   `yaml:"token"`           // deprecated; use scoped tokens
	RequireToken   bool     `yaml:"require_token"`   // if true, refuse to start without auth
	AllowedOrigins []string `yaml:"allowed_origins"` // CORS allowed origins
}

type CleanupConfig struct {
	DefaultTTL time.Duration `yaml:"default_ttl"`
}

// CryptoConfig holds at-rest encryption settings. The key may be supplied
// inline (Key) or via a file path (KeyFile). PGMANAGER_ENCRYPTION_KEY env
// always wins.
type CryptoConfig struct {
	Key     string `yaml:"key"`      // base64-encoded 32-byte key
	KeyFile string `yaml:"key_file"` // optional path to a file containing the key
}

// Discover searches for a server config file in standard locations.
func Discover() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("failed to get working directory: %w", err)
	}

	home, err := os.UserHomeDir()
	if err != nil {
		home = ""
	}

	searchDirs := []string{cwd}
	if home != "" {
		searchDirs = append(searchDirs, home, filepath.Join(home, ".config", "pgmanager"))
	}
	searchDirs = append(searchDirs, "/etc/pgmanager")

	for _, dir := range searchDirs {
		for _, name := range ConfigFileNames {
			path := filepath.Join(dir, name)
			if _, err := os.Stat(path); err == nil {
				return path, nil
			}
		}
		path := filepath.Join(dir, "config.yaml")
		if _, err := os.Stat(path); err == nil {
			return path, nil
		}
	}

	return "", fmt.Errorf("no config file found; create pgmanager.yaml in current directory or specify with --config")
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	cfg := Default()

	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, err
	}

	applyEnvOverrides(cfg)
	return cfg, nil
}

func applyEnvOverrides(cfg *Config) {
	if host := os.Getenv("POSTGRES_HOST"); host != "" {
		cfg.Postgres.Host = host
	}
	if port := os.Getenv("POSTGRES_PORT"); port != "" {
		if p, err := strconv.Atoi(port); err == nil {
			cfg.Postgres.Port = p
		}
	}
	if user := os.Getenv("POSTGRES_USER"); user != "" {
		cfg.Postgres.User = user
	}
	if password := os.Getenv("POSTGRES_PASSWORD"); password != "" {
		cfg.Postgres.Password = password
	}
	if database := os.Getenv("POSTGRES_DATABASE"); database != "" {
		cfg.Postgres.Database = database
	}
	if sslMode := os.Getenv("POSTGRES_SSLMODE"); sslMode != "" {
		cfg.Postgres.SSLMode = sslMode
	}
	if host := os.Getenv("POSTGRES_PUBLIC_HOST"); host != "" {
		cfg.Postgres.PublicHost = host
	}
	if port := os.Getenv("POSTGRES_PUBLIC_PORT"); port != "" {
		if p, err := strconv.Atoi(port); err == nil {
			cfg.Postgres.PublicPort = p
		}
	}
	if listen := os.Getenv("PGMANAGER_LISTEN"); listen != "" {
		cfg.API.Listen = listen
	}
	if apiPort := os.Getenv("PGMANAGER_API_PORT"); apiPort != "" {
		if p, err := strconv.Atoi(apiPort); err == nil {
			cfg.API.Port = p
		}
	}
	if token := os.Getenv("PGMANAGER_API_TOKEN"); token != "" {
		cfg.API.Token = token
	}
	if requireToken := os.Getenv("PGMANAGER_REQUIRE_TOKEN"); requireToken != "" {
		cfg.API.RequireToken = requireToken == "true" || requireToken == "1"
	}
	if origins := os.Getenv("PGMANAGER_ALLOWED_ORIGINS"); origins != "" {
		cfg.API.AllowedOrigins = splitAndTrim(origins, ",")
	}
	if key := os.Getenv("PGMANAGER_ENCRYPTION_KEY"); key != "" {
		cfg.Crypto.Key = key
	}
	if dir := os.Getenv("PGMANAGER_DATA_DIR"); dir != "" {
		cfg.DataDir = dir
	}
}

func splitAndTrim(s, sep string) []string {
	parts := strings.Split(s, sep)
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		if trimmed := strings.TrimSpace(p); trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

// Default returns a default server configuration.
func Default() *Config {
	return &Config{
		Postgres: PostgresConfig{
			Host:     "localhost",
			Port:     5432,
			User:     "postgres",
			Database: "postgres",
			SSLMode:  "disable",
		},
		API: APIConfig{
			Listen:       "127.0.0.1:8080",
			Port:         8080,
			RequireToken: true,
		},
		Cleanup: CleanupConfig{
			DefaultTTL: 7 * 24 * time.Hour,
		},
		DataDir: "/var/lib/pgmanager",
	}
}

// BindAddress returns the address `pgmanager serve` should listen on.
// If both Listen and Port are set, Listen wins. If only Port is set,
// returns "127.0.0.1:<port>" (safe default).
func (c *APIConfig) BindAddress() string {
	if c.Listen != "" {
		return c.Listen
	}
	port := c.Port
	if port == 0 {
		port = 8080
	}
	return fmt.Sprintf("127.0.0.1:%d", port)
}

// EncryptionKey returns the configured key bytes. Order: env Key field
// (already set from PGMANAGER_ENCRYPTION_KEY) -> KeyFile contents.
// Returns nil with no error if no key is configured.
func (c *CryptoConfig) EncryptionKey() ([]byte, error) {
	if c.Key != "" {
		return parseB64Key(c.Key)
	}
	if c.KeyFile != "" {
		data, err := os.ReadFile(c.KeyFile)
		if err != nil {
			return nil, fmt.Errorf("read key file: %w", err)
		}
		return parseB64Key(strings.TrimSpace(string(data)))
	}
	return nil, nil
}

func parseB64Key(s string) ([]byte, error) {
	// Mirror of internal/crypto.ParseKey to avoid an import cycle.
	for _, dec := range []*base64.Encoding{
		base64.StdEncoding,
		base64.RawStdEncoding,
		base64.URLEncoding,
		base64.RawURLEncoding,
	} {
		key, err := dec.DecodeString(s)
		if err == nil {
			if len(key) != 32 {
				return nil, fmt.Errorf("encryption key must be 32 bytes")
			}
			return key, nil
		}
	}
	return nil, fmt.Errorf("encryption key must be base64-encoded")
}

// ConnectionString returns a PostgreSQL connection string.
func (c *PostgresConfig) ConnectionString() string {
	return fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=%s",
		c.Host, c.Port, c.User, c.Password, c.Database, c.SSLMode)
}

// EffectiveHost returns the host that should be advertised to clients —
// PublicHost if set, otherwise the server-side Host. Used by code paths that
// have no inbound HTTP request to inspect (local mode, project.Manager).
func (c *PostgresConfig) EffectiveHost() string {
	if c.PublicHost != "" {
		return c.PublicHost
	}
	return c.Host
}

// EffectivePort mirrors EffectiveHost for the port.
func (c *PostgresConfig) EffectivePort() int {
	if c.PublicPort != 0 {
		return c.PublicPort
	}
	return c.Port
}
