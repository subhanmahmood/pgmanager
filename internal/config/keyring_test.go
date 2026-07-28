package config

import (
	"testing"

	"github.com/zalando/go-keyring"
)

// useKeyring points the package at an in-memory keychain and makes the
// keychain the preferred store, so the macOS path can be exercised on the
// Linux machines that actually run these tests.
func useKeyring(t *testing.T) {
	t.Helper()
	keyring.MockInit()
	orig := keyringSupported
	keyringSupported = func() bool { return true }
	t.Cleanup(func() { keyringSupported = orig })
}

// useFile forces the credentials-file path regardless of platform.
func useFile(t *testing.T) {
	t.Helper()
	orig := keyringSupported
	keyringSupported = func() bool { return false }
	t.Cleanup(func() { keyringSupported = orig })
}

func TestSetTokenUsesKeychainWhenAvailable(t *testing.T) {
	useKeyring(t)

	p := &Profile{APIURL: "https://pgm.example.com"}
	if err := p.SetToken("prod", "pgm_live_secret"); err != nil {
		t.Fatalf("SetToken: %v", err)
	}

	// The whole point: the secret is not in the struct that gets serialized.
	if p.TokenValue != "" {
		t.Errorf("token left in the profile (would be written to credentials.yaml): %q", p.TokenValue)
	}
	if p.TokenSource != TokenSourceKeyring {
		t.Errorf("TokenSource = %q, want %q", p.TokenSource, TokenSourceKeyring)
	}

	got, err := p.Token("prod")
	if err != nil || got != "pgm_live_secret" {
		t.Fatalf("Token: got %q err=%v, want the stored secret", got, err)
	}
}

func TestSetTokenUsesFileWithoutKeychain(t *testing.T) {
	useFile(t)

	p := &Profile{APIURL: "https://pgm.example.com"}
	if err := p.SetToken("prod", "pgm_live_secret"); err != nil {
		t.Fatalf("SetToken: %v", err)
	}
	if p.TokenValue != "pgm_live_secret" || p.TokenSource != "" {
		t.Fatalf("got TokenValue=%q TokenSource=%q, want the token in the file", p.TokenValue, p.TokenSource)
	}
	got, err := p.Token("prod")
	if err != nil || got != "pgm_live_secret" {
		t.Fatalf("Token: got %q err=%v", got, err)
	}
}

// Profiles written before the keychain existed have a plaintext token and no
// token_source. They must keep working untouched — an upgrade that silently
// logged everyone out would be worse than the problem being solved.
func TestLegacyPlaintextProfileStillResolves(t *testing.T) {
	useKeyring(t)

	p := &Profile{APIURL: "https://pgm.example.com", TokenValue: "pgm_live_old"}
	got, err := p.Token("prod")
	if err != nil || got != "pgm_live_old" {
		t.Fatalf("Token: got %q err=%v, want the file token", got, err)
	}
	if !p.HasToken("prod") {
		t.Error("HasToken false for a legacy profile with a token")
	}
}

// A keychain entry deleted behind our back (Keychain Access, a wiped login
// keychain) must read as "no token", not as an error — the profile is still
// valid, it just needs a fresh login.
func TestMissingKeychainEntryReadsAsNoToken(t *testing.T) {
	useKeyring(t)

	p := &Profile{APIURL: "https://pgm.example.com", TokenSource: TokenSourceKeyring}
	got, err := p.Token("prod")
	if err != nil {
		t.Fatalf("Token: unexpected error for a missing entry: %v", err)
	}
	if got != "" {
		t.Fatalf("Token: got %q, want empty", got)
	}
	if p.HasToken("prod") {
		t.Error("HasToken true with no keychain entry")
	}
}

func TestClearTokenRemovesKeychainEntry(t *testing.T) {
	useKeyring(t)

	p := &Profile{APIURL: "https://pgm.example.com"}
	if err := p.SetToken("prod", "pgm_live_secret"); err != nil {
		t.Fatalf("SetToken: %v", err)
	}
	if err := p.ClearToken("prod"); err != nil {
		t.Fatalf("ClearToken: %v", err)
	}

	// Read through a fresh keychain-backed profile: the entry itself is gone,
	// not merely unreferenced.
	orphan := &Profile{TokenSource: TokenSourceKeyring}
	got, err := orphan.Token("prod")
	if err != nil || got != "" {
		t.Fatalf("keychain entry survived logout: got %q err=%v", got, err)
	}
}

// Clearing a profile that never used the keychain must not fail.
func TestClearTokenOnFileProfile(t *testing.T) {
	useFile(t)

	p := &Profile{APIURL: "https://x", TokenValue: "pgm_live_secret"}
	if err := p.ClearToken("prod"); err != nil {
		t.Fatalf("ClearToken: %v", err)
	}
	if p.TokenValue != "" {
		t.Errorf("TokenValue = %q, want empty", p.TokenValue)
	}
}

// The saved file must never contain the secret once the keychain holds it.
func TestSavedFileHasNoSecretWhenKeychainBacked(t *testing.T) {
	useKeyring(t)
	dir := t.TempDir()
	t.Setenv("PGMANAGER_CONFIG_DIR", dir)

	p := &Profile{APIURL: "https://pgm.example.com"}
	if err := p.SetToken("prod", "pgm_live_secret"); err != nil {
		t.Fatalf("SetToken: %v", err)
	}
	cfg := &ClientConfig{Current: "prod", Profiles: map[string]*Profile{"prod": p}}
	if _, err := SaveClient(cfg); err != nil {
		t.Fatalf("SaveClient: %v", err)
	}

	loaded, _, err := LoadClient()
	if err != nil {
		t.Fatalf("LoadClient: %v", err)
	}
	got := loaded.Profiles["prod"]
	if got.TokenValue != "" {
		t.Errorf("secret round-tripped through the file: %q", got.TokenValue)
	}
	if got.TokenSource != TokenSourceKeyring {
		t.Errorf("TokenSource = %q, want %q", got.TokenSource, TokenSourceKeyring)
	}
	tok, err := got.Token("prod")
	if err != nil || tok != "pgm_live_secret" {
		t.Fatalf("reloaded profile lost its token: got %q err=%v", tok, err)
	}
}

// Two credentials files on one machine can each have a profile called "prod",
// pointing at different servers. They must not share a keychain entry: a login
// to one would overwrite the other's token, and a command would then send the
// wrong server's bearer token.
func TestProfilesInDifferentConfigRootsDoNotCollide(t *testing.T) {
	useKeyring(t)

	rootA, rootB := t.TempDir(), t.TempDir()

	t.Setenv("PGMANAGER_CONFIG_DIR", rootA)
	a := &Profile{APIURL: "https://a.example.com"}
	if err := a.SetToken("prod", "pgm_live_a"); err != nil {
		t.Fatalf("SetToken(a): %v", err)
	}

	t.Setenv("PGMANAGER_CONFIG_DIR", rootB)
	b := &Profile{APIURL: "https://b.example.com"}
	if err := b.SetToken("prod", "pgm_live_b"); err != nil {
		t.Fatalf("SetToken(b): %v", err)
	}
	if got, err := b.Token("prod"); err != nil || got != "pgm_live_b" {
		t.Fatalf("Token(b): got %q err=%v", got, err)
	}

	// The first profile still has its own token, not the second one's.
	t.Setenv("PGMANAGER_CONFIG_DIR", rootA)
	if got, err := a.Token("prod"); err != nil || got != "pgm_live_a" {
		t.Fatalf("Token(a) after b logged in: got %q err=%v", got, err)
	}

	// And logging out of one leaves the other alone.
	if err := a.ClearToken("prod"); err != nil {
		t.Fatalf("ClearToken(a): %v", err)
	}
	t.Setenv("PGMANAGER_CONFIG_DIR", rootB)
	if got, err := b.Token("prod"); err != nil || got != "pgm_live_b" {
		t.Fatalf("logout of a removed b's token: got %q err=%v", got, err)
	}
}

// Re-creating a keychain-backed profile with the keychain disabled must not
// abandon the old entry: it holds a live bearer token that no later command
// would be able to find, let alone revoke.
func TestSwitchingToFileClearsKeychainEntry(t *testing.T) {
	useKeyring(t)
	t.Setenv("PGMANAGER_CONFIG_DIR", t.TempDir())

	p := &Profile{APIURL: "https://pgm.example.com"}
	if err := p.SetToken("prod", "pgm_live_old"); err != nil {
		t.Fatalf("SetToken: %v", err)
	}

	useFile(t) // as if PGMANAGER_NO_KEYRING were set on the second login
	if err := p.SetToken("prod", "pgm_live_new"); err != nil {
		t.Fatalf("SetToken (file): %v", err)
	}
	if p.TokenValue != "pgm_live_new" || p.TokenSource != "" {
		t.Fatalf("got TokenValue=%q TokenSource=%q, want the new token in the file", p.TokenValue, p.TokenSource)
	}

	orphan := &Profile{TokenSource: TokenSourceKeyring}
	if got, err := orphan.Token("prod"); err != nil || got != "" {
		t.Fatalf("old token stranded in the keychain: got %q err=%v", got, err)
	}
}

// The same switch on a host with no keychain at all must not fail — Linux and
// CI never had an entry to clean up.
func TestSwitchingToFileWithoutKeychainSucceeds(t *testing.T) {
	useFile(t)

	p := &Profile{APIURL: "https://pgm.example.com", TokenSource: TokenSourceKeyring}
	if err := p.SetToken("prod", "pgm_live_new"); err != nil {
		t.Fatalf("SetToken: %v", err)
	}
	if p.TokenValue != "pgm_live_new" {
		t.Fatalf("TokenValue = %q", p.TokenValue)
	}
}

func TestKeyringOptOutEnv(t *testing.T) {
	t.Setenv("PGMANAGER_NO_KEYRING", "1")
	if KeyringAvailable() {
		t.Error("PGMANAGER_NO_KEYRING did not disable the keychain")
	}
}
