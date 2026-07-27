package auth

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"time"
)

const (
	// SessionCookieName is the cookie the admin UI authenticates with.
	SessionCookieName = "pgmanager_session"

	// DefaultSessionTTL is how long a browser session lasts before the human
	// has to sign in again.
	DefaultSessionTTL = 14 * 24 * time.Hour
)

// GenerateSessionToken returns the secret stored in the browser's cookie
// along with its SHA-256 hash. Only the hash is persisted, so a dump of the
// sessions table cannot be replayed as a login.
func GenerateSessionToken() (plaintext string, hash []byte, err error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", nil, fmt.Errorf("session entropy: %w", err)
	}
	plaintext = base64.RawURLEncoding.EncodeToString(b)
	return plaintext, HashToken(plaintext), nil
}
