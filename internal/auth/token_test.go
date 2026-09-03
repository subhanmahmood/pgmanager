package auth

import (
	"bytes"
	"crypto/sha256"
	"strings"
	"testing"
)

func TestGenerateAndHash(t *testing.T) {
	plain, hash, prefix, err := GenerateToken()
	if err != nil {
		t.Fatalf("GenerateToken: %v", err)
	}
	if !strings.HasPrefix(plain, TokenPrefix) {
		t.Errorf("plain should start with %q, got %q", TokenPrefix, plain)
	}
	expectedHash := sha256.Sum256([]byte(plain))
	if !bytes.Equal(hash, expectedHash[:]) {
		t.Error("hash mismatch")
	}
	if prefix != plain[:DisplayPrefixLen] {
		t.Errorf("prefix mismatch: got %q want %q", prefix, plain[:DisplayPrefixLen])
	}
}

func TestAuthorize(t *testing.T) {
	cases := []struct {
		name    string
		held    []string
		req     ScopeRequest
		wantErr bool
	}{
		{"admin allows project", []string{"admin"}, ScopeRequest{Resource: "project", Project: "x", Env: "dev"}, false},
		{"admin allows tokens", []string{"admin"}, ScopeRequest{Resource: "token"}, false},
		{"tokens only allows tokens", []string{"tokens"}, ScopeRequest{Resource: "project", Project: "x", Env: "dev"}, true},
		{"tokens scope allows token resource", []string{"tokens"}, ScopeRequest{Resource: "token"}, false},
		{"project:* allows any project", []string{"project:*"}, ScopeRequest{Resource: "project", Project: "x", Env: "dev"}, false},
		{"project:myapp allows myapp", []string{"project:myapp"}, ScopeRequest{Resource: "project", Project: "myapp", Env: "dev"}, false},
		{"project:myapp denies other", []string{"project:myapp"}, ScopeRequest{Resource: "project", Project: "other", Env: "dev"}, true},
		{"project:myapp:pr:* allows pr", []string{"project:myapp:pr:*"}, ScopeRequest{Resource: "project", Project: "myapp", Env: "pr", PR: 42}, false},
		{"project:myapp:pr:* denies dev", []string{"project:myapp:pr:*"}, ScopeRequest{Resource: "project", Project: "myapp", Env: "dev"}, true},
		{"project:myapp:env:dev allows dev", []string{"project:myapp:env:dev"}, ScopeRequest{Resource: "project", Project: "myapp", Env: "dev"}, false},
		{"project:myapp:env:dev denies prod", []string{"project:myapp:env:dev"}, ScopeRequest{Resource: "project", Project: "myapp", Env: "prod"}, true},
		{"project:myapp:env:scratch allows scratch", []string{"project:myapp:env:scratch"}, ScopeRequest{Resource: "project", Project: "myapp", Env: "scratch"}, false},
		{"project:myapp:env:scratch denies dev", []string{"project:myapp:env:scratch"}, ScopeRequest{Resource: "project", Project: "myapp", Env: "dev"}, true},
		{"project:myapp:env:scratch denies pr", []string{"project:myapp:env:scratch"}, ScopeRequest{Resource: "project", Project: "myapp", Env: "pr", PR: 42}, true},
		{"project:myapp:pr:* denies scratch", []string{"project:myapp:pr:*"}, ScopeRequest{Resource: "project", Project: "myapp", Env: "scratch"}, true},
		{"empty scopes denies", []string{}, ScopeRequest{Resource: "project", Project: "x"}, true},
		{"unknown scope denies", []string{"foo:bar"}, ScopeRequest{Resource: "project", Project: "x"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := Authorize(tc.held, tc.req)
			if (err != nil) != tc.wantErr {
				t.Fatalf("Authorize: gotErr=%v wantErr=%v", err, tc.wantErr)
			}
		})
	}
}

func TestValidateScopes(t *testing.T) {
	good := [][]string{
		{"admin"},
		{"tokens"},
		{"project:*"},
		{"project:myapp"},
		{"project:myapp:pr:*"},
		{"project:myapp:env:dev"},
		{"project:myapp:env:prod"},
		{"project:*:env:staging"},
		{"project:myapp:env:scratch"},
	}
	for _, scopes := range good {
		if err := ValidateScopes(scopes); err != nil {
			t.Errorf("expected %v valid, got: %v", scopes, err)
		}
	}

	bad := [][]string{
		nil,
		{},
		{"foo"},
		{"project:"},
		{"project:myapp:pr:42"}, // pr scope only supports *
		{"project:myapp:env:nonsense"},
		{"project:myapp:extra:dev"},
	}
	for _, scopes := range bad {
		if err := ValidateScopes(scopes); err == nil {
			t.Errorf("expected %v invalid", scopes)
		}
	}
}
