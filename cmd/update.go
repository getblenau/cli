package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// NewUpdateCmd builds `blenau update`.
//
// Design note (deliberate): this does NOT replace the running binary in place.
// The CLI ships via two authenticated-enough channels — `go install` (the Go
// toolchain verifies module checksums via sum.golang.org) and signed-by-nothing
// GitHub release binaries (only checksums.txt, which lives in the same release,
// so it proves transit integrity, NOT authenticity). Self-replacing a running
// binary over that trust chain — plus the Windows in-place-swap brick risk — is
// not worth it until keyless cosign signing exists. So:
//   - go-install channel  -> run `go install …@latest` (toolchain does it safely).
//   - release-binary channel -> print the exact asset URL (freshly resolved) +
//     the go-install alternative, and let the user pull it.
func NewUpdateCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "update",
		Short: "Update the CLI to the latest release (or show exactly how).",
		Long: `Check for a newer blenau release and update — or, when this binary can't safely
self-replace, tell you the one command to run.

  --check   only report current-vs-latest (force a fresh check, bypassing the
            24h cache); never changes anything.

How it behaves:
  • Installed via 'go install' → runs 'go install github.com/getblenau/cli@latest'
    (the Go toolchain performs the update and verifies module checksums).
  • Installed from a GitHub release binary → prints the exact download URL for
    your OS/arch (resolved from a fresh check) plus the go-install alternative.
    It does NOT overwrite the running binary — self-replacing an unsigned binary
    isn't safe yet, and a botched in-place swap can brick the install.

Opt out of the passive "new version" notice with BLENAU_NO_UPDATE_CHECK=1.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			current := cmd.Root().Version
			checkOnly, _ := cmd.Flags().GetBool("check")
			w := cmd.OutOrStdout()

			rel, err := fetchLatestRelease()
			if err != nil || rel.Tag == "" {
				return fmt.Errorf("could not reach GitHub to check the latest release: %v", err)
			}
			// Refresh the advisory cache so the passive notice stays accurate.
			writeUpdateCache(rel.Tag)

			if !isNewer(rel.Tag, current) {
				fmt.Fprintf(w, "blenau is up to date (v%s).\n", strings.TrimPrefix(current, "v"))
				return nil
			}

			if checkOnly {
				fmt.Fprintf(w, "A newer version is available: %s (you have v%s).\nRun 'blenau update' to update.\n",
					rel.Tag, strings.TrimPrefix(current, "v"))
				return nil
			}

			exe, err := resolveExecutable()
			if err == nil && isGoInstall(exe) {
				fmt.Fprintf(w, "Updating via 'go install' (%s → %s)…\n", "v"+strings.TrimPrefix(current, "v"), rel.Tag)
				gi := exec.Command("go", "install", "github.com/getblenau/cli@latest")
				gi.Stdout = w
				gi.Stderr = cmd.ErrOrStderr()
				if err := gi.Run(); err != nil {
					return fmt.Errorf("go install failed: %w", err)
				}
				fmt.Fprintf(w, "Updated to %s. Re-run 'blenau --version' to confirm.\n", rel.Tag)
				return nil
			}

			// Release-binary channel: resolve the asset for this OS/arch and print it.
			url := rel.assetURLFor(runtime.GOOS, runtime.GOARCH)
			fmt.Fprintf(w, "A newer version is available: %s (you have v%s).\n\n",
				rel.Tag, strings.TrimPrefix(current, "v"))
			if url != "" {
				fmt.Fprintf(w, "Download for %s/%s:\n  %s\n\n", runtime.GOOS, runtime.GOARCH, url)
			} else {
				fmt.Fprintf(w, "Releases:\n  %s\n\n", releasesURL)
			}
			fmt.Fprintf(w, "Or update via Go:\n  go install github.com/getblenau/cli@latest\n")
			return nil
		},
	}
	c.Flags().Bool("check", false, "Only check for a newer version; do not update.")
	return c
}

// ghRelease is a trimmed GitHub "latest release" payload.
type ghRelease struct {
	Tag    string
	Assets []struct {
		Name string `json:"name"`
		URL  string `json:"browser_download_url"`
	}
}

// assetURLFor returns the download URL of the release asset matching os/arch,
// by the goreleaser naming convention (blenau_<ver>_<os>_<arch>.<ext>).
func (r ghRelease) assetURLFor(goos, goarch string) string {
	needle := fmt.Sprintf("_%s_%s.", goos, goarch)
	for _, a := range r.Assets {
		if strings.Contains(a.Name, needle) {
			return a.URL
		}
	}
	return ""
}

// fetchLatestRelease reads the GitHub latest-release (tag + assets), FRESH — it
// never consults the 24h advisory cache. Unauthenticated GitHub call.
func fetchLatestRelease() (ghRelease, error) {
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(latestReleaseAPI)
	if err != nil {
		return ghRelease{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return ghRelease{}, fmt.Errorf("GitHub returned %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return ghRelease{}, err
	}
	var raw struct {
		TagName string `json:"tag_name"`
		Assets  []struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return ghRelease{}, err
	}
	return ghRelease{Tag: strings.TrimSpace(raw.TagName), Assets: raw.Assets}, nil
}

// writeUpdateCache refreshes the advisory cache used by the passive notice.
func writeUpdateCache(tag string) {
	p, err := updateCachePath()
	if err != nil {
		return
	}
	if data, err := json.Marshal(updateCache{CheckedAt: time.Now().Unix(), LatestVersion: tag}); err == nil {
		_ = os.MkdirAll(filepath.Dir(p), 0o700)
		_ = os.WriteFile(p, data, 0o600)
	}
}

// resolveExecutable returns the real path of the running binary (symlinks resolved).
func resolveExecutable() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	if real, err := filepath.EvalSymlinks(exe); err == nil {
		return real, nil
	}
	return exe, nil
}

// isGoInstall reports whether exePath looks like a `go install` binary — i.e. it
// lives in $GOBIN or $GOPATH/bin. Falls back to a ".../go/bin/..." heuristic
// when the go toolchain isn't queryable.
func isGoInstall(exePath string) bool {
	dir := filepath.Clean(filepath.Dir(exePath))
	if gobin := goEnv("GOBIN"); gobin != "" && filepath.Clean(gobin) == dir {
		return true
	}
	if gopath := goEnv("GOPATH"); gopath != "" {
		if filepath.Clean(filepath.Join(gopath, "bin")) == dir {
			return true
		}
	}
	return strings.Contains(filepath.ToSlash(exePath), "/go/bin/")
}

// goEnv returns `go env NAME`, or "" if the go toolchain isn't available.
func goEnv(name string) string {
	out, err := exec.Command("go", "env", name).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
