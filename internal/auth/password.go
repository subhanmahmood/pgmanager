package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// MinPasswordLen is the shortest password we accept. Passwords here are set
// by an operator on the server rather than chosen under time pressure at a
// signup form, so we can afford to be strict.
const MinPasswordLen = 12

// argon2id parameters. Deliberately encoded into every hash (see
// HashPassword) so these can be raised later without invalidating passwords
// that were hashed with the old values.
const (
	argonTime    = 3
	argonMemory  = 64 * 1024 // 64 MiB
	argonThreads = 2
	argonKeyLen  = 32
	argonSaltLen = 16
)

// ErrInvalidHash is returned when a stored hash isn't a PHC string we can parse.
var ErrInvalidHash = errors.New("invalid password hash format")

// HashPassword returns a PHC-format argon2id hash:
//
//	$argon2id$v=19$m=65536,t=3,p=2$<salt>$<hash>
//
// Unlike database passwords and device tokens, nothing ever needs to read a
// password back, so this is a one-way hash and the at-rest encryption key is
// not involved.
func HashPassword(plain string) (string, error) {
	if len(plain) < MinPasswordLen {
		return "", fmt.Errorf("password must be at least %d characters", MinPasswordLen)
	}
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("password salt: %w", err)
	}
	key := argon2.IDKey([]byte(plain), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemory, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key),
	), nil
}

// VerifyPassword reports whether plain matches the encoded hash. It reads the
// parameters out of the hash itself, so hashes written with older settings
// keep verifying after the constants above change.
func VerifyPassword(encoded, plain string) bool {
	salt, want, memory, time, threads, err := decodeHash(encoded)
	if err != nil {
		return false
	}
	got := argon2.IDKey([]byte(plain), salt, time, memory, threads, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1
}

// DummyHash is a valid hash of a random password, used to spend the same work
// on a login attempt for an unknown email as for a known one. Without it, the
// response time reveals which addresses are on the allowlist.
var DummyHash = func() string {
	h, err := HashPassword(strings.Repeat("x", MinPasswordLen*2))
	if err != nil {
		// Only reachable if MinPasswordLen is misconfigured; a broken dummy
		// would silently disable the timing defence, so fail loudly.
		panic("auth: cannot build dummy password hash: " + err.Error())
	}
	return h
}()

func decodeHash(encoded string) (salt, hash []byte, memory, time uint32, threads uint8, err error) {
	parts := strings.Split(encoded, "$")
	// ["", "argon2id", "v=19", "m=...,t=...,p=...", salt, hash]
	if len(parts) != 6 || parts[1] != "argon2id" {
		return nil, nil, 0, 0, 0, ErrInvalidHash
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return nil, nil, 0, 0, 0, ErrInvalidHash
	}
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &time, &threads); err != nil {
		return nil, nil, 0, 0, 0, ErrInvalidHash
	}
	if salt, err = base64.RawStdEncoding.DecodeString(parts[4]); err != nil {
		return nil, nil, 0, 0, 0, ErrInvalidHash
	}
	if hash, err = base64.RawStdEncoding.DecodeString(parts[5]); err != nil {
		return nil, nil, 0, 0, 0, ErrInvalidHash
	}
	if len(hash) == 0 {
		return nil, nil, 0, 0, 0, ErrInvalidHash
	}
	return salt, hash, memory, time, threads, nil
}

// GeneratePassword returns a random password for `pgmanager users add` to
// hand back once. Uses an unambiguous alphabet for the same reason device
// user codes do: it gets copied off one screen and onto another.
func GeneratePassword() (string, error) {
	const alphabet = "abcdefghijkmnopqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	const length = 20
	b := make([]byte, length)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("password entropy: %w", err)
	}
	out := make([]byte, length)
	for i, v := range b {
		out[i] = alphabet[int(v)%len(alphabet)]
	}
	return string(out), nil
}

// NormalizeEmail canonicalizes an address for storage and lookup. Addresses
// are the primary key of the allowlist, so "Me@Example.com" and
// "me@example.com" must not be two different people.
func NormalizeEmail(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

// ValidEmail is a deliberately loose check — enough to catch typos and to
// keep junk out of the table, without trying to out-guess RFC 5322.
func ValidEmail(s string) bool {
	s = NormalizeEmail(s)
	at := strings.Index(s, "@")
	if at <= 0 || at != strings.LastIndex(s, "@") || at == len(s)-1 {
		return false
	}
	if strings.ContainsAny(s, " \t\r\n") || len(s) > 254 {
		return false
	}
	return strings.Contains(s[at+1:], ".")
}
