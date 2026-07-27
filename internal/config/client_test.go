package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestClientConfigRoundTrip(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PGMANAGER_CONFIG_DIR", dir)

	cfg := &ClientConfig{
		Current: "prod",
		Profiles: map[string]*Profile{
			"prod": {
				APIURL:     "https://pgm.example.com",
				TokenValue: "pgm_live_abc",
			},
			"server": {
				Socket: "/run/pgmanager/pgmanager.sock",
			},
		},
	}
	path, err := SaveClient(cfg)
	if err != nil {
		t.Fatalf("SaveClient: %v", err)
	}
	if got, want := filepath.Dir(path), dir; got != want {
		t.Errorf("dir: got %q, want %q", got, want)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("file perm: got %o, want 0600", perm)
	}

	loaded, _, err := LoadClient()
	if err != nil {
		t.Fatalf("LoadClient: %v", err)
	}
	if loaded.Current != "prod" {
		t.Errorf("Current: got %q, want prod", loaded.Current)
	}
	if loaded.Profiles["prod"].APIURL != "https://pgm.example.com" {
		t.Errorf("prod APIURL mismatch")
	}
	if loaded.Profiles["server"].Socket != "/run/pgmanager/pgmanager.sock" {
		t.Errorf("socket profile mismatch")
	}
}

func TestResolveProfile(t *testing.T) {
	cfg := &ClientConfig{
		Current: "a",
		Profiles: map[string]*Profile{
			"a": {APIURL: "https://a"},
			"b": {APIURL: "https://b"},
		},
	}

	t.Run("explicit wins", func(t *testing.T) {
		name, p, err := ResolveProfile(cfg, "b")
		if err != nil || name != "b" || p.APIURL != "https://b" {
			t.Fatalf("got name=%q profile=%v err=%v", name, p, err)
		}
	})
	t.Run("env over current", func(t *testing.T) {
		t.Setenv("PGMANAGER_PROFILE", "b")
		name, _, err := ResolveProfile(cfg, "")
		if err != nil || name != "b" {
			t.Fatalf("got name=%q err=%v", name, err)
		}
	})
	t.Run("PGMANAGER_API_URL synthesizes env profile", func(t *testing.T) {
		t.Setenv("PGMANAGER_API_URL", "https://ci")
		t.Setenv("PGMANAGER_API_TOKEN", "tok")
		t.Setenv("PGMANAGER_PROFILE", "")
		name, p, err := ResolveProfile(cfg, "")
		if err != nil || name != "env" || p.APIURL != "https://ci" {
			t.Fatalf("got name=%q profile=%+v err=%v", name, p, err)
		}
		// The env profile must never be keychain-backed — CI has no keychain.
		tok, err := p.Token(name)
		if err != nil || tok != "tok" {
			t.Fatalf("env token: got %q err=%v, want \"tok\"", tok, err)
		}
	})
	t.Run("missing", func(t *testing.T) {
		empty := &ClientConfig{Profiles: map[string]*Profile{}}
		if _, _, err := ResolveProfile(empty, ""); err == nil {
			t.Fatal("expected error for empty config")
		}
	})
}

func TestProfileMode(t *testing.T) {
	if (&Profile{APIURL: "x"}).Mode() != "api" {
		t.Error("APIURL should be api mode")
	}
	if (&Profile{Socket: "/run/pgmanager/pgmanager.sock"}).Mode() != "socket" {
		t.Error("Socket should be socket mode")
	}
	// api_url wins when both are somehow set — getClient relies on this.
	if (&Profile{APIURL: "x", Socket: "/s.sock"}).Mode() != "api" {
		t.Error("APIURL should win over Socket")
	}
	if (&Profile{}).Mode() != "" {
		t.Error("empty should be no mode")
	}
}
