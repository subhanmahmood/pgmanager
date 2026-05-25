// Package selfupdate implements `pgmanager update`: it resolves the right
// release binary for the host from GitHub Releases, verifies it against the
// published checksums.txt, and atomically swaps the running binary in place.
package selfupdate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// DefaultRepo is the GitHub repo releases are pulled from.
const DefaultRepo = "subhanmahmood/pgmanager"

const checksumsAsset = "checksums.txt"

// supportedTargets are the GOOS/GOARCH combinations we publish binaries for.
var supportedTargets = map[string]bool{
	"linux/amd64":  true,
	"linux/arm64":  true,
	"darwin/amd64": true,
	"darwin/arm64": true,
}

// assetName returns the release asset name for a platform, e.g.
// "pgmanager-linux-amd64". It errors for platforms we don't publish.
func assetName(goos, goarch string) (string, error) {
	if !supportedTargets[goos+"/"+goarch] {
		return "", fmt.Errorf("no prebuilt binary for %s/%s — download manually from https://github.com/%s/releases",
			goos, goarch, DefaultRepo)
	}
	return fmt.Sprintf("pgmanager-%s-%s", goos, goarch), nil
}

// Options configures Run. Only the exported fields are set by callers; the
// unexported ones are test seams.
type Options struct {
	CurrentVersion string // main.Version; "dev"/"" means an untagged build
	Repo           string // defaults to DefaultRepo
	Check          bool
	Force          bool
	Version        string // pin to a specific release tag
	Prerelease     bool
	DryRun         bool
	CacheDir       string // where to keep the ETag cache; empty disables it
	Out            io.Writer

	apiBase    string
	httpClient *http.Client
	execPath   string
}

// Result reports the outcome of Run. UpdateAvailable lets `--check` set its
// exit code; Updated reports whether the binary was actually swapped.
type Result struct {
	CurrentVersion  string
	LatestVersion   string
	UpdateAvailable bool
	Updated         bool
}

// Run performs the update (or check / dry-run) described by opts.
func Run(ctx context.Context, opts Options) (*Result, error) {
	out := opts.Out
	if out == nil {
		out = io.Discard
	}
	repo := opts.Repo
	if repo == "" {
		repo = DefaultRepo
	}
	apiBase := opts.apiBase
	if apiBase == "" {
		apiBase = defaultAPIBase
	}
	hc := opts.httpClient
	if hc == nil {
		hc = &http.Client{Timeout: 60 * time.Second}
	}

	name, err := assetName(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return nil, err
	}

	// Refuse a dev build before touching the network — a check still reports.
	if !opts.Check && isDevVersion(opts.CurrentVersion) && !opts.Force {
		return nil, fmt.Errorf("refusing to update a %q build (likely run via `go run` or a dev build); re-run with --force to override",
			displayVersion(opts.CurrentVersion))
	}

	gh := &githubClient{repo: repo, apiBase: apiBase, http: hc, cacheDir: opts.CacheDir}

	var rel *Release
	switch {
	case opts.Version != "":
		rel, err = gh.byTag(ctx, opts.Version)
	case opts.Prerelease:
		rel, err = gh.latestIncludingPrerelease(ctx)
	default:
		rel, err = gh.latestStable(ctx)
	}
	if err != nil {
		return nil, err
	}

	latestV, err := parseVersion(rel.TagName)
	if err != nil {
		return nil, fmt.Errorf("release tag %q is not a valid version: %w", rel.TagName, err)
	}

	res := &Result{CurrentVersion: opts.CurrentVersion, LatestVersion: rel.TagName}
	res.UpdateAvailable = updateAvailable(opts.CurrentVersion, latestV, opts.Version != "")

	if opts.Check {
		if res.UpdateAvailable {
			fmt.Fprintf(out, "update available: %s → %s\n", displayVersion(opts.CurrentVersion), rel.TagName)
		} else {
			fmt.Fprintf(out, "pgmanager %s is up to date\n", displayVersion(opts.CurrentVersion))
		}
		return res, nil
	}

	if !res.UpdateAvailable && !opts.Force {
		fmt.Fprintf(out, "pgmanager %s is up to date\n", displayVersion(opts.CurrentVersion))
		return res, nil
	}

	execPath, err := resolveExecPath(opts.execPath)
	if err != nil {
		return nil, err
	}

	if isPackageManagerPath(execPath) && !opts.Force {
		return nil, fmt.Errorf("%s looks like it's managed by a package manager; update it there, or re-run with --force to overwrite anyway", execPath)
	}

	if opts.DryRun {
		fmt.Fprintf(out, "would update %s: %s → %s\n", execPath, displayVersion(opts.CurrentVersion), rel.TagName)
		fmt.Fprintf(out, "would download %q and verify it against %s\n", name, checksumsAsset)
		return res, nil
	}

	if err := checkWritable(execPath); err != nil {
		return nil, err
	}

	binAsset, err := selectAsset(rel, name)
	if err != nil {
		return nil, err
	}
	sumAsset, err := selectAsset(rel, checksumsAsset)
	if err != nil {
		return nil, fmt.Errorf("release %s has no %s to verify against: %w", rel.TagName, checksumsAsset, err)
	}

	sums, err := fetchChecksums(ctx, hc, sumAsset.BrowserDownloadURL)
	if err != nil {
		return nil, err
	}
	wantHex, ok := sums[name]
	if !ok {
		return nil, fmt.Errorf("%s is not listed in %s", name, checksumsAsset)
	}

	if err := downloadVerifyAndSwap(ctx, hc, binAsset.BrowserDownloadURL, execPath, wantHex); err != nil {
		return nil, err
	}

	res.Updated = true
	fmt.Fprintf(out, "updated %s → %s\n", displayVersion(opts.CurrentVersion), rel.TagName)
	return res, nil
}

// updateAvailable decides whether the resolved release should be installed. A
// pinned tag counts as "available" whenever it differs from the current build;
// an unpinned lookup only counts strictly-newer releases.
func updateAvailable(current string, latest version, pinned bool) bool {
	if isDevVersion(current) {
		return true
	}
	cur, err := parseVersion(current)
	if err != nil {
		return true
	}
	if pinned {
		return latest.compare(cur) != 0
	}
	return latest.compare(cur) > 0
}

func isDevVersion(v string) bool {
	return v == "" || v == "dev"
}

func displayVersion(v string) string {
	if v == "" {
		return "dev"
	}
	return v
}

func resolveExecPath(override string) (string, error) {
	if override != "" {
		return override, nil
	}
	p, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("locate current binary: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(p)
	if err != nil {
		return "", fmt.Errorf("resolve binary path %s: %w", p, err)
	}
	return resolved, nil
}

func selectAsset(rel *Release, name string) (*Asset, error) {
	for i := range rel.Assets {
		if rel.Assets[i].Name == name {
			return &rel.Assets[i], nil
		}
	}
	return nil, fmt.Errorf("asset %q not found in release %s", name, rel.TagName)
}

// checkWritable confirms we can place a new file alongside the binary (an
// atomic rename only needs write access to the containing directory).
func checkWritable(binPath string) error {
	f, err := os.CreateTemp(filepath.Dir(binPath), ".pgmanager-perm-*")
	if err != nil {
		return fmt.Errorf("cannot write to %s — re-run with sudo, or set PGMANAGER_INSTALL_DIR=~/.local/bin and reinstall there", binPath)
	}
	name := f.Name()
	f.Close()
	os.Remove(name)
	return nil
}

// isPackageManagerPath reports whether the path lives somewhere a package
// manager owns, where clobbering the binary is likely to cause grief.
func isPackageManagerPath(p string) bool {
	lp := strings.ToLower(p)
	for _, pre := range []string{"/usr/bin/", "/bin/", "/opt/homebrew/", "/home/linuxbrew/", "/snap/", "/var/lib/snapd/"} {
		if strings.HasPrefix(lp, pre) {
			return true
		}
	}
	return strings.Contains(lp, "/cellar/")
}

// downloadVerifyAndSwap streams the asset to a temp file next to the target,
// verifies its SHA-256, and atomically renames it over the target. The
// original binary is only touched once verification passes.
func downloadVerifyAndSwap(ctx context.Context, hc *http.Client, url, dest, wantHex string) error {
	tmp, err := os.CreateTemp(filepath.Dir(dest), ".pgmanager-update-*")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpName := tmp.Name()

	gotHex, err := downloadTo(ctx, hc, url, tmp)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(tmpName)
		return err
	}

	if !strings.EqualFold(gotHex, wantHex) {
		os.Remove(tmpName)
		return fmt.Errorf("checksum mismatch: got %s want %s — binary left unchanged", gotHex, wantHex)
	}

	if err := os.Chmod(tmpName, 0o755); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("chmod temp binary: %w", err)
	}
	if err := os.Rename(tmpName, dest); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("swap binary into place: %w", err)
	}
	return nil
}

func downloadTo(ctx context.Context, hc *http.Client, url string, w io.Writer) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := hc.Do(req)
	if err != nil {
		return "", fmt.Errorf("download %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download %s: http %d", url, resp.StatusCode)
	}
	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(w, h), resp.Body); err != nil {
		return "", fmt.Errorf("download %s: %w", url, err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func fetchChecksums(ctx context.Context, hc *http.Client, url string) (map[string]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download checksums: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download checksums: http %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("read checksums: %w", err)
	}
	return parseChecksums(data), nil
}

// parseChecksums reads `sha256sum`-style output ("<hex>  <name>") into a
// name→hex map. The optional '*' binary marker and any leading path are
// stripped from names.
func parseChecksums(data []byte) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		name := filepath.Base(strings.TrimPrefix(fields[1], "*"))
		out[name] = fields[0]
	}
	return out
}
