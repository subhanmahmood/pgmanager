package auth

import (
	"strings"
	"testing"
)

func TestHashAndVerifyPassword(t *testing.T) {
	const password = "correct horse battery staple"
	hash, err := HashPassword(password)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if !strings.HasPrefix(hash, "$argon2id$v=19$") {
		t.Fatalf("hash %q is not a PHC argon2id string", hash)
	}
	if strings.Contains(hash, password) {
		t.Fatal("hash contains the password")
	}
	if !VerifyPassword(hash, password) {
		t.Fatal("correct password did not verify")
	}
	if VerifyPassword(hash, password+"x") {
		t.Fatal("wrong password verified")
	}
	if VerifyPassword(hash, "") {
		t.Fatal("empty password verified")
	}
}

func TestHashPasswordUsesDistinctSalts(t *testing.T) {
	const password = "the same password twice"
	a, err := HashPassword(password)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	b, err := HashPassword(password)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if a == b {
		t.Fatal("identical passwords produced identical hashes — salt is not random")
	}
	// Both must still verify: the salt travels inside the encoded hash.
	if !VerifyPassword(a, password) || !VerifyPassword(b, password) {
		t.Fatal("a salted hash failed to verify")
	}
}

func TestHashPasswordRejectsShort(t *testing.T) {
	if _, err := HashPassword(strings.Repeat("a", MinPasswordLen-1)); err == nil {
		t.Fatal("expected an error for a short password")
	}
	if _, err := HashPassword(strings.Repeat("a", MinPasswordLen)); err != nil {
		t.Fatalf("minimum-length password rejected: %v", err)
	}
}

func TestVerifyPasswordRejectsMalformedHashes(t *testing.T) {
	valid, err := HashPassword("a valid password here")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	parts := strings.Split(valid, "$")

	for _, tt := range []struct {
		name string
		hash string
	}{
		{"empty", ""},
		{"not phc", "plaintext-password"},
		{"wrong algorithm", "$argon2i$v=19$m=65536,t=3,p=2$c2FsdA$aGFzaA"},
		{"wrong version", "$argon2id$v=13$m=65536,t=3,p=2$c2FsdA$aGFzaA"},
		{"missing fields", "$argon2id$v=19$m=65536,t=3,p=2"},
		{"bad base64 salt", "$argon2id$v=19$m=65536,t=3,p=2$!!!$" + parts[5]},
		{"empty hash segment", "$argon2id$v=19$m=65536,t=3,p=2$" + parts[4] + "$"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			// Must be false rather than panicking — these strings come from
			// the database, and a corrupt row must not take the server down.
			if VerifyPassword(tt.hash, "anything") {
				t.Fatal("a malformed hash verified")
			}
		})
	}
}

// The dummy hash keeps unknown-address logins as expensive as real ones, so
// it has to be a hash that actually verifies work.
func TestDummyHashIsUsable(t *testing.T) {
	if !strings.HasPrefix(DummyHash, "$argon2id$") {
		t.Fatalf("DummyHash is not a usable hash: %q", DummyHash)
	}
	if VerifyPassword(DummyHash, "guess") {
		t.Fatal("DummyHash verified against a guess")
	}
}

func TestGeneratePassword(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 50; i++ {
		p, err := GeneratePassword()
		if err != nil {
			t.Fatalf("GeneratePassword: %v", err)
		}
		if len(p) < MinPasswordLen {
			t.Fatalf("generated password %q is shorter than the minimum", p)
		}
		if seen[p] {
			t.Fatalf("duplicate generated password %q", p)
		}
		seen[p] = true
		// A generated password must be acceptable to the hasher.
		if _, err := HashPassword(p); err != nil {
			t.Fatalf("generated password rejected by HashPassword: %v", err)
		}
	}
}

func TestNormalizeAndValidateEmail(t *testing.T) {
	if got := NormalizeEmail("  Me@Example.COM "); got != "me@example.com" {
		t.Fatalf("NormalizeEmail = %q", got)
	}
	for _, tt := range []struct {
		in   string
		want bool
	}{
		{"me@example.com", true},
		{"Me@Example.com", true},
		{"first.last+tag@sub.example.co.uk", true},
		{"me@example", false}, // no dot in the domain
		{"me", false},
		{"@example.com", false},
		{"me@", false},
		{"a@b@example.com", false},
		{"me @example.com", false},
		{"", false},
	} {
		if got := ValidEmail(tt.in); got != tt.want {
			t.Errorf("ValidEmail(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
}
