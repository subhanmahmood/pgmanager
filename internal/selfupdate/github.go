package selfupdate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

const defaultAPIBase = "https://api.github.com"

// Asset is a single downloadable file attached to a GitHub release.
type Asset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

// Release is the subset of the GitHub release payload we care about.
type Release struct {
	TagName    string  `json:"tag_name"`
	Prerelease bool    `json:"prerelease"`
	Draft      bool    `json:"draft"`
	Assets     []Asset `json:"assets"`
}

type githubClient struct {
	repo     string
	apiBase  string
	http     *http.Client
	cacheDir string // empty disables ETag caching
}

// latestStable resolves the repo's "latest" stable release, using an ETag
// cache so repeated --check calls don't burn rate limit.
func (g *githubClient) latestStable(ctx context.Context) (*Release, error) {
	url := fmt.Sprintf("%s/repos/%s/releases/latest", g.apiBase, g.repo)
	var rel Release
	if err := g.getCached(ctx, url, &rel); err != nil {
		return nil, err
	}
	return &rel, nil
}

// byTag resolves a specific release by its tag.
func (g *githubClient) byTag(ctx context.Context, tag string) (*Release, error) {
	url := fmt.Sprintf("%s/repos/%s/releases/tags/%s", g.apiBase, g.repo, tag)
	var rel Release
	if err := g.get(ctx, url, &rel); err != nil {
		if isNotFound(err) {
			return nil, fmt.Errorf("release %q not found for %s", tag, g.repo)
		}
		return nil, err
	}
	return &rel, nil
}

// latestIncludingPrerelease returns the highest-versioned non-draft release,
// prereleases included.
func (g *githubClient) latestIncludingPrerelease(ctx context.Context) (*Release, error) {
	url := fmt.Sprintf("%s/repos/%s/releases?per_page=30", g.apiBase, g.repo)
	var rels []Release
	if err := g.get(ctx, url, &rels); err != nil {
		return nil, err
	}
	var best *Release
	var bestV version
	for i := range rels {
		r := &rels[i]
		if r.Draft {
			continue
		}
		v, err := parseVersion(r.TagName)
		if err != nil {
			continue
		}
		if best == nil || v.compare(bestV) > 0 {
			best = r
			bestV = v
		}
	}
	if best == nil {
		return nil, fmt.Errorf("no releases found for %s", g.repo)
	}
	return best, nil
}

type notFoundError struct{ url string }

func (e *notFoundError) Error() string { return "not found: " + e.url }

func isNotFound(err error) bool {
	_, ok := err.(*notFoundError)
	return ok
}

func (g *githubClient) newRequest(ctx context.Context, url string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	return req, nil
}

func (g *githubClient) get(ctx context.Context, url string, into interface{}) error {
	req, err := g.newRequest(ctx, url)
	if err != nil {
		return err
	}
	resp, err := g.http.Do(req)
	if err != nil {
		return fmt.Errorf("github request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return &notFoundError{url: url}
	}
	if resp.StatusCode != http.StatusOK {
		return apiError(resp)
	}
	return json.NewDecoder(resp.Body).Decode(into)
}

func (g *githubClient) getCached(ctx context.Context, url string, into interface{}) error {
	cachePath := g.cachePath(url)
	entry := g.readCache(cachePath)

	req, err := g.newRequest(ctx, url)
	if err != nil {
		return err
	}
	if entry != nil && entry.ETag != "" {
		req.Header.Set("If-None-Match", entry.ETag)
	}
	resp, err := g.http.Do(req)
	if err != nil {
		return fmt.Errorf("github request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotModified && entry != nil {
		return json.Unmarshal(entry.Body, into)
	}
	if resp.StatusCode == http.StatusNotFound {
		return &notFoundError{url: url}
	}
	if resp.StatusCode != http.StatusOK {
		return apiError(resp)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if etag := resp.Header.Get("ETag"); etag != "" {
		g.writeCache(cachePath, &cacheEntry{ETag: etag, Body: body})
	}
	return json.Unmarshal(body, into)
}

func apiError(resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	msg := strings.TrimSpace(string(body))
	if msg == "" {
		msg = resp.Status
	}
	return fmt.Errorf("github api %d: %s", resp.StatusCode, msg)
}

// --- ETag cache (best-effort; all failures are silent) ----------------------

type cacheEntry struct {
	ETag string          `json:"etag"`
	Body json.RawMessage `json:"body"`
}

func (g *githubClient) cachePath(url string) string {
	if g.cacheDir == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(url))
	return filepath.Join(g.cacheDir, "update-cache-"+hex.EncodeToString(sum[:8])+".json")
}

func (g *githubClient) readCache(path string) *cacheEntry {
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var e cacheEntry
	if json.Unmarshal(data, &e) != nil {
		return nil
	}
	return &e
}

func (g *githubClient) writeCache(path string, e *cacheEntry) {
	if path == "" {
		return
	}
	data, err := json.Marshal(e)
	if err != nil {
		return
	}
	_ = os.MkdirAll(filepath.Dir(path), 0o700)
	_ = os.WriteFile(path, data, 0o600)
}
