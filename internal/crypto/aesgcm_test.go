package crypto

import (
	"bytes"
	"encoding/base64"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	keyB64, err := NewKey()
	if err != nil {
		t.Fatalf("NewKey: %v", err)
	}
	key, err := ParseKey(keyB64)
	if err != nil {
		t.Fatalf("ParseKey: %v", err)
	}

	cases := []struct {
		name      string
		plaintext []byte
	}{
		{"short", []byte("hunter2")},
		{"empty", []byte{}},
		{"binary", []byte{0, 1, 2, 0xff, 0x80}},
		{"long", bytes.Repeat([]byte("abc"), 1000)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ct, err := Encrypt(key, tc.plaintext)
			if err != nil {
				t.Fatalf("Encrypt: %v", err)
			}
			pt, err := Decrypt(key, ct)
			if err != nil {
				t.Fatalf("Decrypt: %v", err)
			}
			if !bytes.Equal(pt, tc.plaintext) {
				t.Fatalf("plaintext mismatch: got %q, want %q", pt, tc.plaintext)
			}
		})
	}
}

func TestTamperDetection(t *testing.T) {
	keyB64, _ := NewKey()
	key, _ := ParseKey(keyB64)
	ct, err := Encrypt(key, []byte("secret"))
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	// Flip a bit in the tag region.
	ct[len(ct)-1] ^= 0x01
	if _, err := Decrypt(key, ct); err == nil {
		t.Fatal("Decrypt accepted tampered ciphertext")
	}
}

func TestWrongKeyFails(t *testing.T) {
	a, _ := NewKey()
	b, _ := NewKey()
	keyA, _ := ParseKey(a)
	keyB, _ := ParseKey(b)

	ct, err := Encrypt(keyA, []byte("secret"))
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if _, err := Decrypt(keyB, ct); err == nil {
		t.Fatal("Decrypt with wrong key succeeded")
	}
}

func TestKeyLengthValidation(t *testing.T) {
	if _, err := Encrypt([]byte("tooshort"), []byte("x")); err != ErrInvalidKey {
		t.Errorf("Encrypt: want ErrInvalidKey, got %v", err)
	}
	if _, err := Decrypt([]byte("tooshort"), []byte("x")); err != ErrInvalidKey {
		t.Errorf("Decrypt: want ErrInvalidKey, got %v", err)
	}
}

func TestParseKey(t *testing.T) {
	raw := bytes.Repeat([]byte{0x42}, KeySize)
	enc := base64.StdEncoding.EncodeToString(raw)

	got, err := ParseKey(enc)
	if err != nil {
		t.Fatalf("ParseKey: %v", err)
	}
	if !bytes.Equal(got, raw) {
		t.Fatalf("round-trip mismatch")
	}

	// Wrong length.
	if _, err := ParseKey(base64.StdEncoding.EncodeToString([]byte("short"))); err != ErrInvalidKey {
		t.Errorf("want ErrInvalidKey, got %v", err)
	}

	// Not base64.
	if _, err := ParseKey("not%%base64"); err == nil {
		t.Error("expected error for non-base64 input")
	}
}

func TestNonceUniqueness(t *testing.T) {
	keyB64, _ := NewKey()
	key, _ := ParseKey(keyB64)
	// Encrypting the same plaintext twice must produce different ciphertexts
	// because the nonce is random.
	a, _ := Encrypt(key, []byte("same"))
	b, _ := Encrypt(key, []byte("same"))
	if bytes.Equal(a, b) {
		t.Fatal("two encryptions of the same plaintext produced identical ciphertext")
	}
}
