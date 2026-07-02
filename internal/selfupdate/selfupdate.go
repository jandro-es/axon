// Package selfupdate checks GitHub Releases for a newer axon build, downloads
// the platform asset with SHA-256 verification against the release's
// checksums.txt, and swaps the running binary atomically. Updates are always
// an explicit user action (`axon update`); nothing here runs automatically
// beyond a cached availability check (spec: operations overhaul, Component 4).
package selfupdate

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// DefaultBaseURL is the GitHub API root; tests and mirrors override it
// (also honoured from AXON_UPDATE_BASE_URL by the CLI).
const DefaultBaseURL = "https://api.github.com"

// Release is the subset of a GitHub release the updater needs.
type Release struct {
	Version string            // tag without leading v
	Assets  map[string]string // asset name → download URL
}

var httpClient = &http.Client{Timeout: 60 * time.Second}

// CheckLatest queries the latest release of owner/repo.
func CheckLatest(ctx context.Context, baseURL, owner, repo string) (Release, error) {
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	url := fmt.Sprintf("%s/repos/%s/%s/releases/latest", strings.TrimRight(baseURL, "/"), owner, repo)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return Release{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := httpClient.Do(req)
	if err != nil {
		return Release{}, fmt.Errorf("check latest release: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return Release{}, fmt.Errorf("check latest release: status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var payload struct {
		TagName string `json:"tag_name"`
		Assets  []struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return Release{}, fmt.Errorf("decode release: %w", err)
	}
	rel := Release{
		Version: strings.TrimPrefix(payload.TagName, "v"),
		Assets:  make(map[string]string, len(payload.Assets)),
	}
	for _, a := range payload.Assets {
		rel.Assets[a.Name] = a.URL
	}
	return rel, nil
}

// IsNewer reports whether latest is strictly newer than current. Non-release
// builds ("dev", empty, or anything unparsable) are never nagged.
func IsNewer(current, latest string) bool {
	cur, ok := parseSemver(current)
	if !ok {
		return false
	}
	lat, ok := parseSemver(latest)
	if !ok {
		return false
	}
	for i := 0; i < 3; i++ {
		if lat[i] != cur[i] {
			return lat[i] > cur[i]
		}
	}
	return false
}

// parseSemver reads "v1.2.3" / "1.2.3" / "2.0.0-rc.1" into [major minor patch].
func parseSemver(s string) ([3]int, bool) {
	s = strings.TrimPrefix(strings.TrimSpace(s), "v")
	if i := strings.IndexAny(s, "-+"); i >= 0 {
		s = s[:i]
	}
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return [3]int{}, false
	}
	var out [3]int
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return [3]int{}, false
		}
		out[i] = n
	}
	return out, true
}

// AssetName is the release asset for a platform, matching `make release`
// output: axon_<version>_<goos>_<goarch>.
func AssetName(version, goos, goarch string) string {
	return fmt.Sprintf("axon_%s_%s_%s", version, goos, goarch)
}

// DownloadVerified downloads asset name from rel, verifies its SHA-256 against
// the release's checksums.txt, and writes it into destDir.
func DownloadVerified(ctx context.Context, rel Release, name, destDir string) (string, error) {
	assetURL, ok := rel.Assets[name]
	if !ok {
		return "", fmt.Errorf("release %s has no asset %q (platform not published?)", rel.Version, name)
	}
	sumsURL, ok := rel.Assets["checksums.txt"]
	if !ok {
		return "", fmt.Errorf("release %s has no checksums.txt — refusing unverified update", rel.Version)
	}

	want, err := expectedChecksum(ctx, sumsURL, name)
	if err != nil {
		return "", err
	}

	data, err := fetch(ctx, assetURL)
	if err != nil {
		return "", fmt.Errorf("download %s: %w", name, err)
	}
	sum := sha256.Sum256(data)
	if got := hex.EncodeToString(sum[:]); got != want {
		return "", fmt.Errorf("checksum mismatch for %s: got %s, checksums.txt says %s — aborting", name, got, want)
	}

	path := filepath.Join(destDir, name)
	if err := os.WriteFile(path, data, 0o755); err != nil {
		return "", err
	}
	return path, nil
}

// expectedChecksum finds name's SHA-256 in the release's checksums.txt
// (`<hex>  <name>` lines, shasum format).
func expectedChecksum(ctx context.Context, sumsURL, name string) (string, error) {
	data, err := fetch(ctx, sumsURL)
	if err != nil {
		return "", fmt.Errorf("download checksums.txt: %w", err)
	}
	sc := bufio.NewScanner(strings.NewReader(string(data)))
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) == 2 && fields[1] == name {
			return strings.ToLower(fields[0]), nil
		}
	}
	return "", fmt.Errorf("checksums.txt has no entry for %q", name)
}

func fetch(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 512<<20))
}

// Swap atomically replaces target with newBin: the old binary survives as
// target.old until the caller has verified the new one and removes it.
func Swap(target, newBin string) error {
	if err := os.Chmod(newBin, 0o755); err != nil {
		return fmt.Errorf("chmod new binary: %w", err)
	}
	old := target + ".old"
	_ = os.Remove(old) // a stale .old from a previous update is fine to drop
	if err := os.Rename(target, old); err != nil {
		return fmt.Errorf("preserve current binary: %w", err)
	}
	if err := os.Rename(newBin, target); err != nil {
		// Roll back so the user still has a working binary.
		_ = os.Rename(old, target)
		return fmt.Errorf("install new binary: %w", err)
	}
	return nil
}
