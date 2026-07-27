package auth

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"math/big"
	"strings"
	"time"
)

const (
	// DeviceCodeTTL is how long a device authorization request stays pending
	// before it expires and the CLI has to start over.
	DeviceCodeTTL = 10 * time.Minute

	// DevicePollInterval is the minimum gap between polls we advertise to the
	// CLI (and enforce server-side, with a little slack).
	DevicePollInterval = 5 * time.Second

	// UserCodeLen is the number of characters in a user code, excluding the
	// separating dash.
	UserCodeLen = 8
)

// userCodeAlphabet deliberately omits characters that are easy to misread
// when a human copies a code off one screen and onto another: 0/O, 1/I/L,
// and U (which is easily heard as "you" when read aloud).
const userCodeAlphabet = "ABCDEFGHJKMNPQRSTVWXYZ23456789"

// GenerateDeviceCode returns the secret the CLI holds onto while polling,
// along with its SHA-256 hash. Only the hash is stored.
func GenerateDeviceCode() (plaintext string, hash []byte, err error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", nil, fmt.Errorf("device code entropy: %w", err)
	}
	plaintext = base64.RawURLEncoding.EncodeToString(b)
	return plaintext, HashToken(plaintext), nil
}

// GenerateUserCode returns the short code a human types into the admin UI,
// formatted as "XXXX-XXXX".
func GenerateUserCode() (string, error) {
	max := big.NewInt(int64(len(userCodeAlphabet)))
	var sb strings.Builder
	for i := 0; i < UserCodeLen; i++ {
		if i == UserCodeLen/2 {
			sb.WriteByte('-')
		}
		n, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", fmt.Errorf("user code entropy: %w", err)
		}
		sb.WriteByte(userCodeAlphabet[n.Int64()])
	}
	return sb.String(), nil
}

// NormalizeUserCode canonicalizes user input for lookup: upper-cased, with
// dashes and surrounding whitespace stripped. Storage uses the same form, so
// "wxyz-2468", "WXYZ2468" and " WXYZ-2468 " all find the same request.
func NormalizeUserCode(s string) string {
	s = strings.ToUpper(strings.TrimSpace(s))
	return strings.ReplaceAll(s, "-", "")
}

// FormatUserCode renders a normalized code back into "XXXX-XXXX" for display.
func FormatUserCode(s string) string {
	s = NormalizeUserCode(s)
	if len(s) != UserCodeLen {
		return s
	}
	return s[:UserCodeLen/2] + "-" + s[UserCodeLen/2:]
}

// ValidUserCode reports whether s is a well-formed user code. Rejecting
// garbage before it reaches the database keeps the lookup surface small.
func ValidUserCode(s string) bool {
	s = NormalizeUserCode(s)
	if len(s) != UserCodeLen {
		return false
	}
	for i := 0; i < len(s); i++ {
		if !strings.ContainsRune(userCodeAlphabet, rune(s[i])) {
			return false
		}
	}
	return true
}
