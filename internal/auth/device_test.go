package auth

import (
	"strings"
	"testing"
)

func TestGenerateUserCodeFormat(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 200; i++ {
		code, err := GenerateUserCode()
		if err != nil {
			t.Fatalf("GenerateUserCode: %v", err)
		}
		if len(code) != UserCodeLen+1 || code[UserCodeLen/2] != '-' {
			t.Fatalf("code %q is not in XXXX-XXXX form", code)
		}
		if !ValidUserCode(code) {
			t.Fatalf("generated code %q failed its own validation", code)
		}
		for _, r := range strings.ReplaceAll(code, "-", "") {
			if strings.ContainsRune("01OILU", r) {
				t.Fatalf("code %q contains ambiguous character %q", code, r)
			}
		}
		if seen[code] {
			t.Fatalf("duplicate code %q within 200 draws", code)
		}
		seen[code] = true
	}
}

func TestNormalizeAndFormatUserCode(t *testing.T) {
	tests := []struct {
		in         string
		normalized string
		formatted  string
	}{
		{"WXYZ-2468", "WXYZ2468", "WXYZ-2468"},
		{"wxyz-2468", "WXYZ2468", "WXYZ-2468"},
		{"  wxyz2468 ", "WXYZ2468", "WXYZ-2468"},
		{"WXYZ2468", "WXYZ2468", "WXYZ-2468"},
	}
	for _, tt := range tests {
		if got := NormalizeUserCode(tt.in); got != tt.normalized {
			t.Errorf("NormalizeUserCode(%q) = %q, want %q", tt.in, got, tt.normalized)
		}
		if got := FormatUserCode(tt.in); got != tt.formatted {
			t.Errorf("FormatUserCode(%q) = %q, want %q", tt.in, got, tt.formatted)
		}
	}
}

func TestValidUserCode(t *testing.T) {
	tests := []struct {
		in   string
		want bool
	}{
		{"WXYZ-2468", true},
		{"wxyz2468", true},
		{"WXYZ-246", false},   // too short
		{"WXYZ-24689", false}, // too long
		{"WXY0-2468", false},  // '0' is not in the alphabet
		{"WXYI-2468", false},  // 'I' is not in the alphabet
		{"", false},
		{"../../etc", false},
	}
	for _, tt := range tests {
		if got := ValidUserCode(tt.in); got != tt.want {
			t.Errorf("ValidUserCode(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
}

func TestGenerateDeviceCodeIsHashed(t *testing.T) {
	plain, hash, err := GenerateDeviceCode()
	if err != nil {
		t.Fatalf("GenerateDeviceCode: %v", err)
	}
	if len(plain) < 32 {
		t.Fatalf("device code %q is suspiciously short", plain)
	}
	if strings.Contains(string(hash), plain) {
		t.Fatal("hash contains the plaintext")
	}
	// The hash must be reproducible from the plaintext — that is how the
	// poll endpoint finds the request.
	if string(hash) != string(HashToken(plain)) {
		t.Fatal("hash does not match HashToken(plaintext)")
	}
}
