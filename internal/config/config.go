package config

import (
	"os"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Postgres PostgresConfig `yaml:"postgres"`
	SQLite   SQLiteConfig   `yaml:"sqlite"`
	API      APIConfig      `yaml:"api"`
	Cleanup  CleanupConfig  `yaml:"cleanup"`
}

type PostgresConfig struct {
	Host     string `yaml:"host"`
	Port     int    `yaml:"port"`
	User     string `yaml:"user"`
	Password string `yaml:"password"`
	Database string `yaml:"database"`
	SSLMode  string `yaml:"ssl_mode"` // disable, require, verify-ca, verify-full
}

type SQLiteConfig struct {
	Path string `yaml:"path"`
}

type APIConfig struct {
	Port           int      `yaml:"port"`
	Token          string   `yaml:"token"`
	RequireToken   bool     `yaml:"require_token"`   // If true, API requires authentication even if token is empty
	AllowedOrigins []string `yaml:"allowed_origins"` // CORS allowed origins
}

type CleanupConfig struct {
	DefaultTTL time.Duration `yaml:"default_ttl"`
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	cfg := &Config{
		Postgres: PostgresConfig{
			Host:     "localhost",
			Port:     5432,
			User:     "postgres",
			Database: "postgres",
			SSLMode:  "require",
		},
		SQLite: SQLiteConfig{
			Path: "data/pgmanager.db",
		},
		API: APIConfig{
			Port:         8080,
			RequireToken: true,
		},
		Cleanup: CleanupConfig{
			DefaultTTL: 7 * 24 * time.Hour,
		},
	}

	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, err
	}

	// Override with environment variables
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
	if sqlitePath := os.Getenv("PGMANAGER_SQLITE_PATH"); sqlitePath != "" {
		cfg.SQLite.Path = sqlitePath
	}
	if apiPort := os.Getenv("PGMANAGER_API_PORT"); apiPort != "" {
		if p, err := strconv.Atoi(apiPort); err == nil {
			cfg.API.Port = p
		}
	}
	if token := os.Getenv("PGMANAGER_API_TOKEN"); token != "" {
		cfg.API.Token = token
	}
	if sslMode := os.Getenv("POSTGRES_SSLMODE"); sslMode != "" {
		cfg.Postgres.SSLMode = sslMode
	}
	if requireToken := os.Getenv("PGMANAGER_REQUIRE_TOKEN"); requireToken != "" {
		cfg.API.RequireToken = requireToken == "true" || requireToken == "1"
	}
	if origins := os.Getenv("PGMANAGER_ALLOWED_ORIGINS"); origins != "" {
		cfg.API.AllowedOrigins = splitAndTrim(origins, ",")
	}

	return cfg, nil
}

// splitAndTrim splits a string by separator and trims whitespace from each part
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

// Default returns a default configuration without loading from file
func Default() *Config {
	return &Config{
		Postgres: PostgresConfig{
			Host:     "localhost",
			Port:     5432,
			User:     "postgres",
			Database: "postgres",
			SSLMode:  "require",
		},
		SQLite: SQLiteConfig{
			Path: "data/pgmanager.db",
		},
		API: APIConfig{
			Port:         8080,
			RequireToken: true,
		},
		Cleanup: CleanupConfig{
			DefaultTTL: 7 * 24 * time.Hour,
		},
	}
}
