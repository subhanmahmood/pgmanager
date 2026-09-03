package project

import (
	"context"
	"testing"
	"time"

	"pgmanager/internal/config"
	"pgmanager/internal/meta"
)

func testManager(t *testing.T) (*Manager, *meta.MockStore) {
	t.Helper()
	store := meta.NewMockStore()
	cfg := &config.Config{}
	cfg.Cleanup.DefaultTTL = 7 * 24 * time.Hour
	return NewManager(cfg, store), store
}

// seedDatabase writes a database straight into the store, bypassing the
// manager so no Postgres is needed.
func seedDatabase(t *testing.T, store *meta.MockStore, projectName, env, key string, expiresAt *time.Time) meta.Database {
	t.Helper()
	ctx := context.Background()
	p, err := store.GetProject(ctx, projectName)
	if err != nil {
		t.Fatalf("get project: %v", err)
	}
	if p == nil {
		if p, err = store.CreateProject(ctx, projectName); err != nil {
			t.Fatalf("create project: %v", err)
		}
	}
	name := DatabaseName(projectName, env, key)
	db, err := store.CreateDatabase(ctx, p.ID, name, UserName(name), "secret", env, key, expiresAt)
	if err != nil {
		t.Fatalf("create database: %v", err)
	}
	return *db
}

func TestValidateKey(t *testing.T) {
	cases := []struct {
		env, key string
		wantErr  bool
	}{
		{"dev", "", false},
		{"prod", "", false},
		{"dev", "something", true},     // singleton envs take no key
		{"pr", "", true},               // keyed envs require one
		{"scratch", "", true},          //
		{"pr", "123", false},           //
		{"pr", "abc", true},            // a PR key is a number
		{"pr", "0", true},              //
		{"pr", "-4", true},             //
		{"scratch", "epic_231", false}, //
		{"scratch", "a", false},        //
		{"scratch", "9lives", true},    // must start with a letter
		{"scratch", "Epic", true},      // lower-case only
		{"scratch", "epic-231", true},  // no hyphens: it becomes an identifier
	}
	for _, c := range cases {
		err := ValidateKey(c.env, c.key)
		if (err != nil) != c.wantErr {
			t.Errorf("ValidateKey(%q, %q) error = %v, wantErr %v", c.env, c.key, err, c.wantErr)
		}
	}
}

func TestCreateLeasesKeyedEnvsOnly(t *testing.T) {
	// The lease is decided before any Postgres work, so assert it through the
	// store rather than by creating a real database.
	mgr, store := testManager(t)
	ctx := context.Background()

	seedDatabase(t, store, "myapp", "dev", "", nil)
	if got, err := store.GetDatabase(ctx, 1, "dev", ""); err != nil || got.ExpiresAt != nil {
		t.Errorf("dev database should be permanent, got expires=%v err=%v", got.ExpiresAt, err)
	}

	// Renewing something permanent is refused rather than quietly giving it
	// an expiry it never had.
	if _, err := mgr.RenewDatabase(ctx, "myapp", "dev", "", time.Hour); err == nil {
		t.Error("renewing a dev database should fail")
	}

	// And creating one with a lease is refused for the same reason: nothing
	// could ever extend it.
	ttl := 24 * time.Hour
	if _, err := mgr.CreateDatabase(ctx, "myapp", "dev", "", nil, &ttl); err == nil {
		t.Error("a ttl on a permanent env should be refused")
	}
	if _, err := mgr.CreateDatabase(ctx, "myapp", "scratch", "x", nil, new(time.Duration)); err == nil {
		t.Error("a zero ttl should be refused")
	}
}

func TestRenewExtendsTheLease(t *testing.T) {
	mgr, store := testManager(t)
	ctx := context.Background()

	soon := time.Now().Add(time.Hour)
	seedDatabase(t, store, "myapp", "scratch", "epic_231", &soon)

	info, err := mgr.RenewDatabase(ctx, "myapp", "scratch", "epic_231", 14*24*time.Hour)
	if err != nil {
		t.Fatalf("renew: %v", err)
	}
	if info.ExpiresAt == nil || !info.ExpiresAt.After(soon.Add(24*time.Hour)) {
		t.Errorf("lease not extended: got %v, was %v", info.ExpiresAt, soon)
	}

	stored, err := store.GetDatabase(ctx, 1, "scratch", "epic_231")
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if stored.ExpiresAt == nil || !stored.ExpiresAt.Equal(*info.ExpiresAt) {
		t.Errorf("store has %v, response had %v", stored.ExpiresAt, info.ExpiresAt)
	}

	if _, err := mgr.RenewDatabase(ctx, "myapp", "scratch", "nope", time.Hour); err == nil {
		t.Error("renewing a database that does not exist should fail")
	}
	if _, err := mgr.RenewDatabase(ctx, "myapp", "scratch", "epic_231", 0); err == nil {
		t.Error("a non-positive ttl should be refused")
	}
}

// TestReapableRespectsTheLease is the regression test for the bug this change
// fixes: cleanup used to take the union of "expired" and "any PR database older
// than N", so a database whose lease had been renewed was still dropped on the
// age rule alone — which made a longer-lived database impossible to keep.
func TestReapableRespectsTheLease(t *testing.T) {
	mgr, store := testManager(t)
	ctx := context.Background()

	future := time.Now().Add(14 * 24 * time.Hour)
	past := time.Now().Add(-time.Hour)

	renewed := seedDatabase(t, store, "myapp", "pr", "42", &future)
	lapsed := seedDatabase(t, store, "myapp", "pr", "43", &past)
	unleased := seedDatabase(t, store, "myapp", "pr", "44", nil)
	permanent := seedDatabase(t, store, "myapp", "dev", "", nil)

	// A negative olderThan puts the age cutoff in the future, so every row
	// counts as old. That isolates the lease as the only thing keeping a
	// database alive, without having to backdate created_at.
	toDelete, err := mgr.reapable(ctx, -time.Hour)
	if err != nil {
		t.Fatalf("reapable: %v", err)
	}

	if _, found := toDelete[renewed.Name]; found {
		t.Errorf("%s has a live lease and must not be reaped", renewed.Name)
	}
	if _, found := toDelete[permanent.Name]; found {
		t.Errorf("%s is permanent and must not be reaped", permanent.Name)
	}
	if _, found := toDelete[lapsed.Name]; !found {
		t.Errorf("%s has a lapsed lease and should be reaped", lapsed.Name)
	}
	if _, found := toDelete[unleased.Name]; !found {
		t.Errorf("%s carries no lease and should fall to the age sweep", unleased.Name)
	}
}
