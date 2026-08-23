package backup

import (
	"regexp"
	"testing"
	"time"
)

// nonceSuffix matches the random component ObjectKey appends after the
// timestamp. Every assertion below pins the whole key except those hex
// digits, so the layout stays exactly as tightly specified as it was before
// the nonce existed.
var nonceSuffix = regexp.MustCompile(`^-[0-9a-f]{12}\.dump$`)

// wantKey asserts that got is the fixed part of a key followed by nothing
// but the random nonce and the .dump extension.
func wantKey(t *testing.T, got, fixed string) {
	t.Helper()
	if len(got) <= len(fixed) || got[:len(fixed)] != fixed {
		t.Fatalf("ObjectKey() = %q, want prefix %q", got, fixed)
	}
	if rest := got[len(fixed):]; !nonceSuffix.MatchString(rest) {
		t.Fatalf("ObjectKey() = %q, tail %q does not match %s", got, rest, nonceSuffix)
	}
}

func TestObjectKey(t *testing.T) {
	at := time.Date(2026, 8, 23, 12, 34, 56, 0, time.UTC)
	got := ObjectKey("pgmanager/", "myapp", "myapp_dev", at)
	wantKey(t, got, "pgmanager/myapp/myapp_dev/20260823T123456Z")
}

func TestObjectKeyConvertsToUTC(t *testing.T) {
	loc := time.FixedZone("UTC-5", -5*3600)
	at := time.Date(2026, 8, 23, 7, 34, 56, 0, loc) // 12:34:56 UTC
	got := ObjectKey("pgmanager/", "myapp", "myapp_dev", at)
	wantKey(t, got, "pgmanager/myapp/myapp_dev/20260823T123456Z")
}

func TestObjectKeyPrefixIsUsedVerbatim(t *testing.T) {
	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	got := ObjectKey("custom/prefix/", "proj", "proj_prod", at)
	wantKey(t, got, "custom/prefix/proj/proj_prod/20260102T030405Z")
}

// Two backups of the same database taken in the same second — a manual one
// racing the scheduler — must not share an object key. If they did, the two
// uploads would overwrite each other and deleting either metadata row would
// destroy the object the other row still points at.
func TestObjectKeyIsUniquePerCallWithinTheSameSecond(t *testing.T) {
	at := time.Date(2026, 8, 23, 12, 34, 56, 0, time.UTC)

	const n = 1000
	seen := make(map[string]struct{}, n)
	for i := 0; i < n; i++ {
		key := ObjectKey("pgmanager/", "myapp", "myapp_dev", at)
		if _, dup := seen[key]; dup {
			t.Fatalf("ObjectKey() returned a duplicate key %q for the same timestamp", key)
		}
		seen[key] = struct{}{}
	}
}
