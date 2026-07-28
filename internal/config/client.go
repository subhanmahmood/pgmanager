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

// Profile describes one connection target: either APIURL (a remote
// `pgmanager serve` over HTTPS) or Socket (a local one over a unix socket).
// APIURL takes precedence if both happen to be present.
//
// The bearer token is reached through Token()/SetToken() rather than read off
// the struct, because it may not be in this file at all — see TokenSource.
type Profile struct {
	APIURL string `yaml:"api_url,omitempty"`
	// TokenValue is the token when it is stored in credentials.yaml. It is
	// empty for keychain-backed profiles. Prefer Token(); this field is
	// exported only because the YAML encoder needs it to be.
	TokenValue string `yaml:"token,omitempty"`
	// TokenSource is "keyring" when the token lives in the OS keychain under
	// service "pgmanager" (see keyringAccount for the account name). Empty
	// means TokenValue.
	TokenSource string `yaml:"token_source,omitempty"`
	Socket      string `yaml:"socket,omitempty"`
}

// Token returns the profile's bearer token, reading the OS keychain when that
// is where it lives. An empty string with a nil error means the profile has no
// token — a keychain-backed profile whose entry has been deleted looks the
// same as one that never had a token, and the fix is the same: log in again.
func (p *Profile) Token(name string) (string, error) {
	if p.TokenSource == TokenSourceKeyring {
		return keyringGet(name)
	}
	return p.TokenValue, nil
}

// HasToken reports whether a token is available, for status output that would
// rather show "unknown" than fail. A keychain read can prompt on some
// configurations, which is why callers that only need to *display* status use
// this and callers that need the secret use Token().
func (p *Profile) HasToken(name string) bool {
	tok, err := p.Token(name)
	return err == nil && tok != ""
}

// SetToken stores the token wherever this machine keeps secrets, updating the
// profile to record which that was. On macOS it goes to the Keychain and the
// file records only a pointer; everywhere else it goes in the file at 0600.
//
// The caller still has to SaveClient to persist the profile itself.
func (p *Profile) SetToken(name, token string) error {
	if keyringSupported() {
		if err := keyringSet(name, token); err != nil {
			return err
		}
		p.TokenValue = ""
		p.TokenSource = TokenSourceKeyring
		return nil
	}
	// Switching a keychain-backed profile to the file (PGMANAGER_NO_KEYRING on
	// a Mac) would otherwise leave the previous, still-valid token in the
	// keychain with nothing recording that it is there — logout could never
	// remove it. Best-effort, because on a host with no keychain at all this
	// must not turn a successful login into an error.
	if p.TokenSource == TokenSourceKeyring {
		_ = keyringDelete(name)
	}
	p.TokenValue = token
	p.TokenSource = ""
	return nil
}

// ClearToken removes the profile's token from wherever it is stored. Called on
// logout, so that dropping a profile from the file doesn't strand its secret
// in the keychain forever.
func (p *Profile) ClearToken(name string) error {
	p.TokenValue = ""
	if p.TokenSource == TokenSourceKeyring {
		p.TokenSource = ""
		return keyringDelete(name)
	}
	return nil
}

// Mode returns how the profile reaches pgmanager: "api" (remote HTTP) or
// "socket" (local unix socket on the server itself).
func (p *Profile) Mode() string {
	if p.APIURL != "" {
		return "api"
	}
	if p.Socket != "" {
		return "socket"
	}
	return ""
}

// DefaultSocketPath is where `pgmanager serve` is conventionally told to put
// its local admin socket, and therefore where the CLI looks when no profile
// is configured. Matches examples/deploy.
const DefaultSocketPath = "/run/pgmanager/pgmanager.sock"

// LocalSocketPath returns the socket the CLI should try when nothing else is
// configured, and whether one is actually there. $PGMANAGER_SOCKET overrides
// the default; "-" disables the probe entirely.
func LocalSocketPath() (string, bool) {
	path := os.Getenv("PGMANAGER_SOCKET")
	if path == "-" {
		return "", false
	}
	if path == "" {
		path = DefaultSocketPath
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode()&os.ModeSocket == 0 {
		return path, false
	}
	return path, true
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
		// Never keychain-backed: the environment *is* the source, which is why
		// CI works on a machine with no keychain and no credentials file.
		return "env", &Profile{
			APIURL:     envURL,
			TokenValue: os.Getenv("PGMANAGER_API_TOKEN"),
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
