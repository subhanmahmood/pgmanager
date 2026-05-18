// Package auth handles API token generation, hashing, and scope checks.
package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
)

const (
	// TokenPrefix is the literal that precedes every pgmanager API token.
	// It makes leaked tokens trivially greppable and obviously sensitive.
	TokenPrefix = "pgm_live_"

	// DisplayPrefixLen is the number of characters of the token (including
	// the literal prefix) shown in audit logs and revoke lookups.
	DisplayPrefixLen = 16
)

// ScopeAdmin grants full access.
const ScopeAdmin = "admin"

// GenerateToken returns (plaintext, hash, displayPrefix). The plaintext is
// only ever returned to the operator at creation time.
func GenerateToken() (plaintext string, hash []byte, prefix string, err error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", nil, "", fmt.Errorf("token entropy: %w", err)
	}
	suffix := base64.RawURLEncoding.EncodeToString(b)
	plaintext = TokenPrefix + suffix
	sum := sha256.Sum256([]byte(plaintext))
	return plaintext, sum[:], plaintext[:DisplayPrefixLen], nil
}

// HashToken returns the SHA-256 hash of a token's plaintext.
func HashToken(plaintext string) []byte {
	sum := sha256.Sum256([]byte(plaintext))
	return sum[:]
}

// DisplayPrefix returns the first DisplayPrefixLen characters of plaintext,
// suitable for audit logs. Falls back to the whole string for short inputs
// (mostly for tests).
func DisplayPrefix(plaintext string) string {
	if len(plaintext) < DisplayPrefixLen {
		return plaintext
	}
	return plaintext[:DisplayPrefixLen]
}

// ScopeRequest describes the action that needs authorization.
type ScopeRequest struct {
	// Resource is "project" for project-level actions and "token" for
	// token-management actions.
	Resource string
	// Project is the project name (empty for resource="token" or fleet-wide ops).
	Project string
	// Env is the database environment ("prod"/"dev"/"staging"/"pr"); empty
	// for project-level actions.
	Env string
	// PR is the PR number (only set when Env is "pr").
	PR int
}

// ErrScope is returned when no held scope satisfies the requested action.
var ErrScope = errors.New("token does not have required scope")

// Authorize reports whether any of the held scope strings satisfies the
// request. Supported scope forms:
//
//	admin                       - everything
//	tokens                      - manage tokens (no DB access)
//	project:*                   - any project, any env
//	project:<name>              - one project, any env
//	project:<name>:pr:*         - one project, only PR DBs
//	project:<name>:env:<env>    - one project, one specific env
func Authorize(held []string, req ScopeRequest) error {
	for _, s := range held {
		if scopeAllows(s, req) {
			return nil
		}
	}
	return ErrScope
}

func scopeAllows(scope string, req ScopeRequest) bool {
	if scope == ScopeAdmin {
		return true
	}
	if scope == "tokens" {
		return req.Resource == "token"
	}
	if req.Resource != "project" {
		return false
	}
	parts := strings.Split(scope, ":")
	if len(parts) < 1 || parts[0] != "project" {
		return false
	}
	switch len(parts) {
	case 1:
		// "project" alone is not a valid scope
		return false
	case 2:
		// project:<name> or project:*
		return parts[1] == "*" || parts[1] == req.Project
	case 4:
		if parts[1] != "*" && parts[1] != req.Project {
			return false
		}
		switch parts[2] {
		case "pr":
			if req.Env != "pr" {
				return false
			}
			return parts[3] == "*" // we don't currently scope per-PR-number
		case "env":
			return parts[3] == req.Env
		}
	}
	return false
}

// ValidateScopes returns an error for any malformed scope strings.
func ValidateScopes(scopes []string) error {
	if len(scopes) == 0 {
		return errors.New("at least one scope is required")
	}
	for _, s := range scopes {
		if s == ScopeAdmin || s == "tokens" {
			continue
		}
		parts := strings.Split(s, ":")
		if parts[0] != "project" {
			return fmt.Errorf("invalid scope %q", s)
		}
		switch len(parts) {
		case 2:
			if parts[1] == "" {
				return fmt.Errorf("invalid scope %q", s)
			}
		case 4:
			if parts[1] == "" || (parts[2] != "pr" && parts[2] != "env") {
				return fmt.Errorf("invalid scope %q", s)
			}
			if parts[2] == "env" {
				switch parts[3] {
				case "prod", "dev", "staging", "pr":
				default:
					return fmt.Errorf("invalid env in scope %q", s)
				}
			}
			if parts[2] == "pr" && parts[3] != "*" {
				return fmt.Errorf("invalid scope %q: pr scope only supports '*'", s)
			}
		default:
			return fmt.Errorf("invalid scope %q", s)
		}
	}
	return nil
}
