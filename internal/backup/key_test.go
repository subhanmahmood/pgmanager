package backup

import (
	"testing"
	"time"
)

func TestObjectKey(t *testing.T) {
	at := time.Date(2026, 8, 23, 12, 34, 56, 0, time.UTC)
	got := ObjectKey("pgmanager/", "myapp", "myapp_dev", at)
	want := "pgmanager/myapp/myapp_dev/20260823T123456Z.dump"
	if got != want {
		t.Fatalf("ObjectKey() = %q, want %q", got, want)
	}
}

func TestObjectKeyConvertsToUTC(t *testing.T) {
	loc := time.FixedZone("UTC-5", -5*3600)
	at := time.Date(2026, 8, 23, 7, 34, 56, 0, loc) // 12:34:56 UTC
	got := ObjectKey("pgmanager/", "myapp", "myapp_dev", at)
	want := "pgmanager/myapp/myapp_dev/20260823T123456Z.dump"
	if got != want {
		t.Fatalf("ObjectKey() = %q, want %q", got, want)
	}
}

func TestObjectKeyPrefixIsUsedVerbatim(t *testing.T) {
	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	got := ObjectKey("custom/prefix/", "proj", "proj_prod", at)
	want := "custom/prefix/proj/proj_prod/20260102T030405Z.dump"
	if got != want {
		t.Fatalf("ObjectKey() = %q, want %q", got, want)
	}
}
