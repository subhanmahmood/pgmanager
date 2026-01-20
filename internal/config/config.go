package config

import (
	"os"
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
}

type SQLiteConfig struct {
	Path string `yaml:"path"`
}

type APIConfig struct {
	Port  int    `yaml:"port"`
	Token string `yaml:"token"`
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
		},
		SQLite: SQLiteConfig{
			Path: "data/pgmanager.db",
		},
		API: APIConfig{
			Port: 8080,
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
	if password := os.Getenv("POSTGRES_PASSWORD"); password != "" {
		cfg.Postgres.Password = password
	}
	if token := os.Getenv("PGMANAGER_API_TOKEN"); token != "" {
		cfg.API.Token = token
	}
	if sqlitePath := os.Getenv("PGMANAGER_SQLITE_PATH"); sqlitePath != "" {
		cfg.SQLite.Path = sqlitePath
	}

	return cfg, nil
}

// Default returns a default configuration without loading from file
func Default() *Config {
	return &Config{
		Postgres: PostgresConfig{
			Host:     "localhost",
			Port:     5432,
			User:     "postgres",
			Database: "postgres",
		},
		SQLite: SQLiteConfig{
			Path: "data/pgmanager.db",
		},
		API: APIConfig{
			Port: 8080,
		},
		Cleanup: CleanupConfig{
			DefaultTTL: 7 * 24 * time.Hour,
		},
	}
}
