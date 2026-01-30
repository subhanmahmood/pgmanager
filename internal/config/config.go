package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// ConfigFileNames are the names to search for when auto-discovering config
var ConfigFileNames = []string{
	"pgmanager.yaml",
	"pgmanager.yml",
	".pgmanager.yaml",
	".pgmanager.yml",
}

type Config struct {
	Postgres PostgresConfig `yaml:"postgres"`
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

type APIConfig struct {
	Port           int      `yaml:"port"`
	Token          string   `yaml:"token"`
	RequireToken   bool     `yaml:"require_token"`   // If true, API requires authentication even if token is empty
	AllowedOrigins []string `yaml:"allowed_origins"` // CORS allowed origins
}

type CleanupConfig struct {
	DefaultTTL time.Duration `yaml:"default_ttl"`
}

// Discover searches for a config file in standard locations
// Search order: current directory, then home directory
func Discover() (string, error) {
	// Get current working directory
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("failed to get working directory: %w", err)
	}

	// Get home directory
	home, err := os.UserHomeDir()
	if err != nil {
		home = "" // Continue without home directory
	}

	// Search paths in order
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
		// Also check for config.yaml in each directory
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

	cfg := &Config{
		Postgres: PostgresConfig{
			Host:     "localhost",
			Port:     5432,
			User:     "postgres",
			Database: "postgres",
			SSLMode:  "disable", // Default to disable for local development
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
			SSLMode:  "disable",
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

// ConnectionString returns a PostgreSQL connection string
func (c *PostgresConfig) ConnectionString() string {
	return fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=%s",
		c.Host, c.Port, c.User, c.Password, c.Database, c.SSLMode)
}
