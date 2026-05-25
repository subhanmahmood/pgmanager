package selfupdate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestAssetName(t *testing.T) {
	cases := []struct {
		goos, goarch string
		want         string
		wantErr      bool
	}{
		{"linux", "amd64", "pgmanager-linux-amd64", false},
		{"linux", "arm64", "pgmanager-linux-arm64", false},
		{"darwin", "amd64", "pgmanager-darwin-amd64", false},
		{"darwin", "arm64", "pgmanager-darwin-arm64", false},
		{"windows", "amd64", "", true},
		{"linux", "386", "", true},
		{"freebsd", "amd64", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.goos+"/"+tc.goarch, func(t *testing.T) {
			got, err := assetName(tc.goos, tc.goarch)
			if (err != nil) != tc.wantErr {
				t.Fatalf("assetName(%q,%q): err=%v wantErr=%v", tc.goos, tc.goarch, err, tc.wantErr)
			}
			if got != tc.want {
				t.Errorf("assetName(%q,%q) = %q, want %q", tc.goos, tc.goarch, got, tc.want)
			}
		})
	}
}

func TestSelectAsset(t *testing.T) {
	rel := &Release{
		TagName: "v1.0.0",
		Assets: []Asset{
			{Name: "pgmanager-linux-amd64", BrowserDownloadURL: "http://x/linux"},
			{Name: "checksums.txt", BrowserDownloadURL: "http://x/sums"},
		},
	}
	a, err := selectAsset(rel, "pgmanager-linux-amd64")
	if err != nil {
		t.Fatalf("selectAsset: %v", err)
	}
	if a.BrowserDownloadURL != "http://x/linux" {
		t.Errorf("wrong asset: %+v", a)
	}
	if _, err := selectAsset(rel, "pgmanager-windows-amd64"); err == nil {
		t.Error("expected error for missing asset")
	}
}

func TestParseChecksums(t *testing.T) {
	in := "deadbeef  pgmanager-linux-amd64\n" +
		"cafef00d  pgmanager-darwin-arm64\n" +
		"\n" +
		"abc123 *dist/pgmanager-linux-arm64\n" // single space + '*' marker + path
	got := parseChecksums([]byte(in))
	want := map[string]string{
		"pgmanager-linux-amd64":  "deadbeef",
		"pgmanager-darwin-arm64": "cafef00d",
		"pgmanager-linux-arm64":  "abc123",
	}
	if len(got) != len(want) {
		t.Fatalf("parseChecksums got %d entries, want %d: %v", len(got), len(want), got)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("checksum[%q] = %q, want %q", k, got[k], v)
		}
	}
}

func sha256hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// TestChecksumVerification exercises the verify-before-swap path end to end:
// the happy path swaps the binary, a tampered checksum refuses and leaves the
// original untouched.
func TestChecksumVerification(t *testing.T) {
	if _, err := assetName(runtime.GOOS, runtime.GOARCH); err != nil {
		t.Skipf("unsupported test platform: %v", err)
	}
	name, _ := assetName(runtime.GOOS, runtime.GOARCH)
	newBinary := []byte("#!/fake new pgmanager binary v0.2.0\n")

	t.Run("happy path swaps binary", func(t *testing.T) {
		srv := newReleaseServer(t, "v0.2.0", false, name, newBinary, sha256hex(newBinary))
		defer srv.Close()

		dir := t.TempDir()
		execPath := filepath.Join(dir, "pgmanager")
		if err := os.WriteFile(execPath, []byte("old binary v0.1.0"), 0o755); err != nil {
			t.Fatal(err)
		}

		res, err := Run(context.Background(), Options{
			CurrentVersion: "v0.1.0",
			Repo:           "owner/repo",
			apiBase:        srv.URL,
			httpClient:     srv.Client(),
			execPath:       execPath,
		})
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		if !res.Updated {
			t.Error("expected Updated=true")
		}
		got, _ := os.ReadFile(execPath)
		if string(got) != string(newBinary) {
			t.Errorf("binary not swapped: %q", got)
		}
		assertNoTempLeftovers(t, dir)
	})

	t.Run("tampered checksum refuses and leaves original", func(t *testing.T) {
		// Advertise a checksum that doesn't match the served bytes.
		srv := newReleaseServer(t, "v0.2.0", false, name, newBinary, sha256hex([]byte("something else")))
		defer srv.Close()

		dir := t.TempDir()
		execPath := filepath.Join(dir, "pgmanager")
		original := []byte("old binary v0.1.0")
		if err := os.WriteFile(execPath, original, 0o755); err != nil {
			t.Fatal(err)
		}

		_, err := Run(context.Background(), Options{
			CurrentVersion: "v0.1.0",
			Repo:           "owner/repo",
			apiBase:        srv.URL,
			httpClient:     srv.Client(),
			execPath:       execPath,
		})
		if err == nil {
			t.Fatal("expected checksum mismatch error")
		}
		if !strings.Contains(err.Error(), "checksum mismatch") {
			t.Errorf("unexpected error: %v", err)
		}
		got, _ := os.ReadFile(execPath)
		if string(got) != string(original) {
			t.Errorf("original binary was modified: %q", got)
		}
		assertNoTempLeftovers(t, dir)
	})
}

func TestRunDevGuard(t *testing.T) {
	if _, err := assetName(runtime.GOOS, runtime.GOARCH); err != nil {
		t.Skipf("unsupported test platform: %v", err)
	}
	name, _ := assetName(runtime.GOOS, runtime.GOARCH)
	bin := []byte("new")
	srv := newReleaseServer(t, "v0.2.0", false, name, bin, sha256hex(bin))
	defer srv.Close()

	dir := t.TempDir()
	execPath := filepath.Join(dir, "pgmanager")
	os.WriteFile(execPath, []byte("dev"), 0o755)

	// dev build, no --force: must refuse.
	_, err := Run(context.Background(), Options{
		CurrentVersion: "dev",
		Repo:           "owner/repo",
		apiBase:        srv.URL,
		httpClient:     srv.Client(),
		execPath:       execPath,
	})
	if err == nil || !strings.Contains(err.Error(), "--force") {
		t.Fatalf("dev build without --force should refuse, got: %v", err)
	}

	// dev build, --force: proceeds and swaps.
	res, err := Run(context.Background(), Options{
		CurrentVersion: "dev",
		Force:          true,
		Repo:           "owner/repo",
		apiBase:        srv.URL,
		httpClient:     srv.Client(),
		execPath:       execPath,
	})
	if err != nil {
		t.Fatalf("dev build with --force: %v", err)
	}
	if !res.Updated {
		t.Error("expected update with --force on dev build")
	}
}

func TestRunCheck(t *testing.T) {
	if _, err := assetName(runtime.GOOS, runtime.GOARCH); err != nil {
		t.Skipf("unsupported test platform: %v", err)
	}
	name, _ := assetName(runtime.GOOS, runtime.GOARCH)
	bin := []byte("new")

	t.Run("update available", func(t *testing.T) {
		srv := newReleaseServer(t, "v0.2.0", false, name, bin, sha256hex(bin))
		defer srv.Close()
		var out strings.Builder
		res, err := Run(context.Background(), Options{
			CurrentVersion: "v0.1.0",
			Check:          true,
			Repo:           "owner/repo",
			apiBase:        srv.URL,
			httpClient:     srv.Client(),
			Out:            &out,
		})
		if err != nil {
			t.Fatal(err)
		}
		if !res.UpdateAvailable {
			t.Error("expected UpdateAvailable=true")
		}
		if res.Updated {
			t.Error("--check must not write")
		}
		if !strings.Contains(out.String(), "update available") {
			t.Errorf("unexpected output: %q", out.String())
		}
	})

	t.Run("up to date", func(t *testing.T) {
		srv := newReleaseServer(t, "v0.2.0", false, name, bin, sha256hex(bin))
		defer srv.Close()
		var out strings.Builder
		res, err := Run(context.Background(), Options{
			CurrentVersion: "v0.2.0",
			Check:          true,
			Repo:           "owner/repo",
			apiBase:        srv.URL,
			httpClient:     srv.Client(),
			Out:            &out,
		})
		if err != nil {
			t.Fatal(err)
		}
		if res.UpdateAvailable {
			t.Error("expected UpdateAvailable=false")
		}
		if !strings.Contains(out.String(), "up to date") {
			t.Errorf("unexpected output: %q", out.String())
		}
	})
}

func TestRunDryRun(t *testing.T) {
	if _, err := assetName(runtime.GOOS, runtime.GOARCH); err != nil {
		t.Skipf("unsupported test platform: %v", err)
	}
	name, _ := assetName(runtime.GOOS, runtime.GOARCH)
	bin := []byte("new")
	srv := newReleaseServer(t, "v0.2.0", false, name, bin, sha256hex(bin))
	defer srv.Close()

	dir := t.TempDir()
	execPath := filepath.Join(dir, "pgmanager")
	os.WriteFile(execPath, []byte("old"), 0o755)

	var out strings.Builder
	res, err := Run(context.Background(), Options{
		CurrentVersion: "v0.1.0",
		DryRun:         true,
		Repo:           "owner/repo",
		apiBase:        srv.URL,
		httpClient:     srv.Client(),
		execPath:       execPath,
		Out:            &out,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Updated {
		t.Error("--dry-run must not write")
	}
	if got, _ := os.ReadFile(execPath); string(got) != "old" {
		t.Errorf("--dry-run modified the binary: %q", got)
	}
	if !strings.Contains(out.String(), "would update") {
		t.Errorf("unexpected output: %q", out.String())
	}
}

func TestIsPackageManagerPath(t *testing.T) {
	managed := []string{"/usr/bin/pgmanager", "/opt/homebrew/bin/pgmanager", "/snap/bin/pgmanager", "/opt/homebrew/Cellar/x/pgmanager"}
	for _, p := range managed {
		if !isPackageManagerPath(p) {
			t.Errorf("expected %q to be a package-manager path", p)
		}
	}
	unmanaged := []string{"/usr/local/bin/pgmanager", "/home/user/.local/bin/pgmanager", "/tmp/pgmanager"}
	for _, p := range unmanaged {
		if isPackageManagerPath(p) {
			t.Errorf("expected %q to NOT be a package-manager path", p)
		}
	}
}

// newReleaseServer stands in for the GitHub API + release-asset CDN. It serves
// /repos/<repo>/releases/latest plus the binary and checksums downloads. The
// advertised checksum is whatever sumHex is passed, so tests can simulate
// tampering by passing a mismatched value.
func newReleaseServer(t *testing.T, tag string, prerelease bool, binName string, binBody []byte, sumHex string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	var base string
	mux.HandleFunc("/dl/"+binName, func(w http.ResponseWriter, r *http.Request) {
		w.Write(binBody)
	})
	mux.HandleFunc("/dl/checksums.txt", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "%s  %s\n", sumHex, binName)
	})
	mux.HandleFunc("/repos/owner/repo/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		rel := Release{
			TagName:    tag,
			Prerelease: prerelease,
			Assets: []Asset{
				{Name: binName, BrowserDownloadURL: base + "/dl/" + binName},
				{Name: "checksums.txt", BrowserDownloadURL: base + "/dl/checksums.txt"},
			},
		}
		json.NewEncoder(w).Encode(rel)
	})
	srv := httptest.NewServer(mux)
	base = srv.URL
	t.Cleanup(srv.Close)
	return srv
}

func assertNoTempLeftovers(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".pgmanager-") {
			t.Errorf("leftover temp file: %s", e.Name())
		}
	}
}
