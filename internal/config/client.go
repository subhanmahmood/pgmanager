package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// CredentialsFileName is the canonical name of the client credentials file.
const CredentialsFileName = "credentials.yaml"

// ClientConfig holds the named connection profiles a CLI/TUI client uses to
// reach pgmanager. It is stored separately from the server `pgmanager.yaml`
// so server hosts and tokens don't end up committed together.
type ClientConfig struct {
	Current  string              `yaml:"current"`
	Profiles map[string]*Profile `yaml:"profiles"`
}

// Profile describes one connection target. Exactly one of APIURL (talk to a
// remote `pgmanager serve`) or Postgres (talk directly to Postgres) should be
// set. APIURL takes precedence if both happen to be present.
type Profile struct {
	APIURL   string          `yaml:"api_url,omitempty"`
	Token    string          `yaml:"token,omitempty"`
	Postgres *PostgresConfig `yaml:"postgres,omitempty"`
	Crypto   *CryptoConfig   `yaml:"crypto,omitempty"`
}

// Mode returns "api" or "local" depending on how the profile is wired.
func (p *Profile) Mode() string {
	if p.APIURL != "" {
		return "api"
	}
	if p.Postgres != nil {
		return "local"
	}
	return ""
}

// ClientConfigPath returns the canonical location of the client credentials
// file: $XDG_CONFIG_HOME/pgmanager/credentials.yaml or
// $HOME/.config/pgmanager/credentials.yaml.
func ClientConfigPath() (string, error) {
	if dir := os.Getenv("PGMANAGER_CONFIG_DIR"); dir != "" {
		return filepath.Join(dir, CredentialsFileName), nil
	}
	if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" {
		return filepath.Join(dir, "pgmanager", CredentialsFileName), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("user home: %w", err)
	}
	return filepath.Join(home, ".config", "pgmanager", CredentialsFileName), nil
}

// LoadClient reads the client credentials file. Returns an empty (but valid)
// ClientConfig when the file doesn't exist yet — callers should treat that as
// "no profiles configured".
func LoadClient() (*ClientConfig, string, error) {
	path, err := ClientConfigPath()
	if err != nil {
		return nil, "", err
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return &ClientConfig{Profiles: map[string]*Profile{}}, path, nil
	}
	if err != nil {
		return nil, path, fmt.Errorf("read %s: %w", path, err)
	}
	cfg := &ClientConfig{Profiles: map[string]*Profile{}}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, path, fmt.Errorf("parse %s: %w", path, err)
	}
	if cfg.Profiles == nil {
		cfg.Profiles = map[string]*Profile{}
	}
	return cfg, path, nil
}

// SaveClient writes the client config back to disk with mode 0600 (file) and
// 0700 (parent directory) — these files contain bearer tokens.
func SaveClient(cfg *ClientConfig) (string, error) {
	path, err := ClientConfigPath()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return path, fmt.Errorf("mkdir %s: %w", filepath.Dir(path), err)
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return path, fmt.Errorf("marshal: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return path, fmt.Errorf("write %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return path, fmt.Errorf("rename %s: %w", path, err)
	}
	return path, nil
}

// ResolveProfile picks the profile to use for this invocation. Resolution
// order: explicit name -> $PGMANAGER_PROFILE -> cfg.Current. Returns the
// profile name and the profile itself. Env-only credentials (no file at all)
// produce a synthetic "env" profile if $PGMANAGER_API_URL is set.
func ResolveProfile(cfg *ClientConfig, explicit string) (string, *Profile, error) {
	// Env-only mode: PGMANAGER_API_URL bypasses the file entirely. This is
	// the canonical CI path — no profile file required.
	if envURL := os.Getenv("PGMANAGER_API_URL"); envURL != "" && explicit == "" && os.Getenv("PGMANAGER_PROFILE") == "" {
		return "env", &Profile{
			APIURL: envURL,
			Token:  os.Getenv("PGMANAGER_API_TOKEN"),
		}, nil
	}

	name := explicit
	if name == "" {
		name = os.Getenv("PGMANAGER_PROFILE")
	}
	if name == "" {
		name = cfg.Current
	}
	if name == "" {
		return "", nil, fmt.Errorf("no profile configured; run: pgmanager login <api-url>")
	}
	p, ok := cfg.Profiles[name]
	if !ok {
		return "", nil, fmt.Errorf("profile %q not found; available: %s", name, strings.Join(profileNames(cfg), ", "))
	}
	return name, p, nil
}

func profileNames(cfg *ClientConfig) []string {
	names := make([]string, 0, len(cfg.Profiles))
	for n := range cfg.Profiles {
		names = append(names, n)
	}
	return names
}
