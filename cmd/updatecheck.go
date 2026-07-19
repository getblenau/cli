package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	updateCheckInterval = 24 * time.Hour
	latestReleaseAPI    = "https://api.github.com/repos/getblenau/cli/releases/latest"
	releasesURL         = "https://github.com/getblenau/cli/releases/latest"
)

type updateCache struct {
	CheckedAt     int64  `json:"checked_at"`
	LatestVersion string `json:"latest_version"`
}

func updateCachePath() (string, error) {
	p, err := ConfigPath()
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(p), "update-check.json"), nil
}

// NotifyIfUpdateAvailable prints a one-line notice to stderr when a newer
// release exists. It INFORMS, never forces or self-updates. Deliberately
// non-intrusive and agent-first:
//   - only when stderr is a TTY → scripts, CI and agents never see it;
//   - opt out entirely with BLENAU_NO_UPDATE_CHECK;
//   - the network is hit at most once per 24h (cached), with a 2s timeout, and
//     any failure (offline, rate-limited) is silent — it never blocks or errors.
func NotifyIfUpdateAvailable(current string, stderr *os.File) {
	if os.Getenv("BLENAU_NO_UPDATE_CHECK") != "" {
		return
	}
	if !isTerminal(stderr) {
		return
	}
	latest := cachedLatestRelease()
	if latest == "" || !isNewer(latest, current) {
		return
	}
	fmt.Fprintf(stderr,
		"\nA new version of blenau is available: %s (you have v%s).\n"+
			"  Update: %s\n"+
			"  or:     go install github.com/getblenau/cli@latest\n",
		latest, current, releasesURL)
}

// cachedLatestRelease returns the latest release tag, hitting GitHub at most
// once per updateCheckInterval and caching the result next to config.json.
func cachedLatestRelease() string {
	p, err := updateCachePath()
	if err != nil {
		return ""
	}
	if data, err := os.ReadFile(p); err == nil {
		var c updateCache
		if json.Unmarshal(data, &c) == nil &&
			time.Since(time.Unix(c.CheckedAt, 0)) < updateCheckInterval {
			return c.LatestVersion // still fresh — no network
		}
	}
	latest := fetchLatestReleaseTag()
	if latest == "" {
		return ""
	}
	if data, err := json.Marshal(updateCache{CheckedAt: time.Now().Unix(), LatestVersion: latest}); err == nil {
		_ = os.MkdirAll(filepath.Dir(p), 0o700)
		_ = os.WriteFile(p, data, 0o600)
	}
	return latest
}

// fetchLatestReleaseTag reads the GitHub "latest release" tag. This is an
// unauthenticated call to GitHub, unrelated to the Blenau API path (and so
// exempt from the workspace chokepoint — see the guard test's allowlist).
func fetchLatestReleaseTag() string {
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(latestReleaseAPI)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return ""
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return ""
	}
	var r struct {
		TagName string `json:"tag_name"`
	}
	if json.Unmarshal(body, &r) != nil {
		return ""
	}
	return strings.TrimSpace(r.TagName)
}

// isNewer reports whether release tag `latest` (e.g. "v0.4.1") is a higher
// version than `current` (e.g. "0.4.0").
func isNewer(latest, current string) bool {
	a, b := parseVer(latest), parseVer(current)
	for i := 0; i < 3; i++ {
		if a[i] != b[i] {
			return a[i] > b[i]
		}
	}
	return false
}

// parseVer parses a lenient "vX.Y.Z" (optional leading v, ignores any
// -prerelease/+build suffix and non-numeric junk) into [major,minor,patch].
func parseVer(s string) [3]int {
	s = strings.TrimPrefix(strings.TrimSpace(s), "v")
	if i := strings.IndexAny(s, "-+"); i >= 0 {
		s = s[:i]
	}
	var out [3]int
	for i, part := range strings.SplitN(s, ".", 3) {
		if i > 2 {
			break
		}
		n := 0
		for _, c := range part {
			if c < '0' || c > '9' {
				break
			}
			n = n*10 + int(c-'0')
		}
		out[i] = n
	}
	return out
}
