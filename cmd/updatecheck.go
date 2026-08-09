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
	// Print only the VERSION and point at `blenau update` — never a URL sourced
	// from the cache. The cache (update-check.json in a user-writable dir) is
	// advisory and could be poisoned; `blenau update` re-fetches from GitHub
	// fresh and decides the actual action, so no cached value is ever actioned.
	fmt.Fprintf(stderr,
		"\nA new version of blenau is available: %s (you have v%s).\n"+
			"  Run: blenau update\n",
		latest, current)
}

// UpdateAvailable reports the latest release tag and whether `current` is
// behind it. This is the NON-TTY half of NotifyIfUpdateAvailable.
//
// That function returns early when stderr is not a terminal, so scripts, CI and
// agents never see the notice. For a human that silence is right — a human who
// runs a command that no longer exists reads the error and thinks "maybe I'm
// old". An agent does not: it reads "unknown command" and concludes the
// capability does not exist, then reports that as fact and stops. It is the one
// caller with no way to suspect its own binary, and the only one the notice was
// withheld from.
//
// So the same fact is offered through the two channels an agent actually reads
// — the `--agent-manifest` contract and the unknown-command error — while the
// interactive notice stays exactly as it was. Same opt-out
// (BLENAU_NO_UPDATE_CHECK), same 24h cache, same silent failure when offline.
func UpdateAvailable(current string) (latest string, stale bool) {
	if os.Getenv("BLENAU_NO_UPDATE_CHECK") != "" {
		return "", false
	}
	latest = cachedLatestRelease()
	if latest == "" {
		return "", false
	}
	return latest, isNewer(latest, current)
}

// OutdatedBinaryHint returns the line to append to `err` when the error is the
// kind an outdated binary produces AND this binary is in fact outdated. Returns
// "" otherwise — including when the version check is opted out of or offline,
// because a guess here would be worse than silence.
//
// Scoped to unknown command/flag errors on purpose. Every other failure (auth,
// network, a 404 from the API) has its own explanation, and appending a version
// notice to those would train the reader to ignore it.
func OutdatedBinaryHint(current string, err error) string {
	if err == nil {
		return ""
	}
	msg := strings.ToLower(err.Error())
	if !strings.Contains(msg, "unknown command") &&
		!strings.Contains(msg, "unknown flag") &&
		!strings.Contains(msg, "unknown shorthand flag") {
		return ""
	}
	latest, stale := UpdateAvailable(current)
	if !stale {
		return ""
	}
	return fmt.Sprintf(
		"\nThis binary is v%s and %s is available, so this may exist in the newer\n"+
			"release rather than not exist at all. Check before concluding otherwise:\n"+
			"  blenau update\n",
		current, latest)
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
