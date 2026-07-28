package config

import (
	"errors"
	"fmt"
	"os"
	"runtime"

	"github.com/zalando/go-keyring"
)

// KeyringService is the service name every pgmanager secret is filed under.
// In Keychain Access this is what you search for to see them all; the account
// on each item is what keyringAccount builds.
const KeyringService = "pgmanager"

// keyringAccount is the Keychain account name for a profile.
//
// The profile name alone is not unique on a machine: $PGMANAGER_CONFIG_DIR and
// $XDG_CONFIG_HOME let one user keep several credentials files, and a profile
// called "prod" in each of them is a different server with a different token.
// Sharing one (service, account) pair between them means a login clobbers the
// other's token, a command can send one server's token to another, and a
// logout deletes both. Qualifying the account with the credentials file it
// belongs to keeps them apart, and keeps the item readable in Keychain Access:
// "prod (/Users/me/.config/pgmanager/credentials.yaml)".
func keyringAccount(profile string) (string, error) {
	path, err := ClientConfigPath()
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s (%s)", profile, path), nil
}

// TokenSourceKeyring marks a profile whose bearer token lives in the OS
// keychain rather than in credentials.yaml. Any other value (including the
// empty string, which is every profile written before this existed) means the
// token is in the file.
const TokenSourceKeyring = "keyring"

// keyringSupported reports whether tokens should go to the OS keychain by
// default.
//
// macOS only, deliberately. The Keychain is always present there, unlocks with
// the login session, and stores the token somewhere a stray `cat` of a dotfile
// won't find it. The Linux equivalent (DBus Secret Service) is frequently
// absent — headless boxes, bare SSH sessions, containers — and where it does
// exist it is readable by any process running as the same user anyway, so it
// would add a failure mode without adding a boundary. Those hosts keep using
// the 0600 file, and CI keeps using $PGMANAGER_API_TOKEN, which touches
// neither.
//
// $PGMANAGER_NO_KEYRING opts out, for anyone on a Mac who would rather have a
// greppable file.
// It is a variable so tests can exercise both storage paths on whatever
// platform they happen to run on — CI is Linux, the users are on macOS, and
// both branches need coverage.
var keyringSupported = func() bool {
	if os.Getenv("PGMANAGER_NO_KEYRING") != "" {
		return false
	}
	return runtime.GOOS == "darwin"
}

// KeyringAvailable reports whether new logins will store their token in the
// OS keychain.
func KeyringAvailable() bool { return keyringSupported() }

// keyringGet reads a profile's token. A profile that says its token is in the
// keychain but has no entry there returns an empty string and no error — the
// caller reports that as "no token" and tells the operator to log in again,
// which is the honest fix for a secret someone deleted out from under us.
func keyringGet(profile string) (string, error) {
	account, err := keyringAccount(profile)
	if err != nil {
		return "", err
	}
	secret, err := keyring.Get(KeyringService, account)
	if errors.Is(err, keyring.ErrNotFound) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read token for profile %q from the keychain: %w", profile, err)
	}
	return secret, nil
}

// keyringSet stores a profile's token. Callers decide whether the keychain is
// the right destination; this just writes there.
func keyringSet(profile, token string) error {
	account, err := keyringAccount(profile)
	if err != nil {
		return err
	}
	if err := keyring.Set(KeyringService, account, token); err != nil {
		return fmt.Errorf("store token for profile %q in the keychain: %w", profile, err)
	}
	return nil
}

// keyringDelete removes a profile's token. Missing is success: the goal is
// "the secret is not there afterwards", and it already isn't.
func keyringDelete(profile string) error {
	account, err := keyringAccount(profile)
	if err != nil {
		return err
	}
	// Earlier builds of this (unreleased) feature filed items under the bare
	// profile name. Those entries are no longer read, so remove them here too
	// rather than leave a live bearer token in the keychain that nothing
	// references and no command can reach. Best-effort: it is a cleanup, and
	// failing it would turn a successful logout into an error.
	if legacy := profile; legacy != account {
		_ = keyring.Delete(KeyringService, legacy)
	}
	err = keyring.Delete(KeyringService, account)
	if err == nil || errors.Is(err, keyring.ErrNotFound) {
		return nil
	}
	return fmt.Errorf("remove token for profile %q from the keychain: %w", profile, err)
}

// KeyringAccount is the Keychain account a profile's token is filed under,
// for telling an operator where to look. Errors resolving the credentials path
// are reported as the bare profile name — this is display text, not a lookup.
func KeyringAccount(profile string) string {
	account, err := keyringAccount(profile)
	if err != nil {
		return profile
	}
	return account
}
