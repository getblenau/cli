package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/text/unicode/norm"
)

// envAPIURL allows overriding the API base URL.
const envAPIURL = "BLENAU_API_URL"

// envAPIToken allows overriding the API token from env.
const envAPIToken = "BLENAU_API_TOKEN"

// envWorkspace lets a caller pin the target workspace (a tenant UUID) without a
// flag — handy for CI. The --workspace flag overrides it (see root.go).
const envWorkspace = "BLENAU_WORKSPACE"

// workspaceOverride holds the value of the global --workspace flag, set in the
// root command's PersistentPreRun. Empty = fall back to env, then the token's
// pinned workspace.
var workspaceOverride string

// resolveWorkspace returns the effective workspace selector, if any. A blenau_tk_
// token is pinned to one workspace and ignores this; an identity-lane token uses
// it to roam (the api 409s a discordant pin, so it is safe to always send).
func resolveWorkspace() string {
	if workspaceOverride != "" {
		return workspaceOverride
	}
	return os.Getenv(envWorkspace)
}

// setWorkspaceHeader adds the X-Blenau-Workspace selector to a request when one
// is configured. Shared by every request path (apiCall + multipart uploads).
func setWorkspaceHeader(req *http.Request) {
	if ws := resolveWorkspace(); ws != "" {
		req.Header.Set("X-Blenau-Workspace", ws)
	}
}

// readContentArg returns the bytes for a write body: the file at contentFile, or
// stdin when contentFile is empty or "-". This lets agents pipe content in
// (`... | blenau patch-section --content-file -`) without a temp file.
func readContentArg(contentFile string) ([]byte, error) {
	if contentFile != "" && contentFile != "-" {
		b, err := os.ReadFile(contentFile)
		if err != nil {
			return nil, fmt.Errorf("read content file: %w", err)
		}
		return b, nil
	}
	b, err := io.ReadAll(os.Stdin)
	if err != nil {
		return nil, fmt.Errorf("read stdin: %w", err)
	}
	return b, nil
}

// resolveAuth returns (apiURL, token, err).
func resolveAuth() (string, string, error) {
	apiURL := os.Getenv(envAPIURL)
	token := os.Getenv(envAPIToken)
	if apiURL != "" && token != "" {
		return apiURL, token, nil
	}
	cfg, err := LoadConfig()
	if errors.Is(err, os.ErrNotExist) {
		if token == "" {
			return "", "", fmt.Errorf("not logged in: run 'blenau login --token <tk>' first")
		}
		if apiURL == "" {
			apiURL = DefaultAPIURL
		}
		return apiURL, token, nil
	}
	if err != nil {
		return "", "", err
	}
	if apiURL == "" {
		apiURL = cfg.APIURL
	}
	if apiURL == "" {
		apiURL = DefaultAPIURL
	}
	if token == "" {
		token = cfg.Token
	}
	if token == "" {
		return "", "", fmt.Errorf("config has no token: run 'blenau login --token <tk>' first")
	}
	return apiURL, token, nil
}

// jsonFlag returns the effective --json setting (subcmd or persistent root).
func jsonFlag(cmd *cobra.Command) bool {
	if asJSON, _ := cmd.Flags().GetBool("json"); cmd.Flags().Changed("json") {
		return asJSON
	}
	if pj, _ := cmd.Root().PersistentFlags().GetBool("json"); pj {
		return true
	}
	// Default to JSON when stdout is not a TTY.
	return !isTerminal(os.Stdout)
}

// isTerminal reports whether f is a terminal (character device).
func isTerminal(f *os.File) bool {
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}

// apiCall does an HTTP request to api with auth. Body may be nil.
// Returns (response bytes, status code, error). For non-2xx, returns the body
// and a nil error so callers can format JSON-vs-human appropriately.
func apiCall(method, path string, body []byte) ([]byte, int, error) {
	apiURL, token, err := resolveAuth()
	if err != nil {
		return nil, 0, err
	}
	var rdr io.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, apiURL+path, rdr)
	if err != nil {
		return nil, 0, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Authorization", "Bearer "+token)
	setWorkspaceHeader(req)
	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("call %s: %w", req.URL, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, err
	}
	return raw, resp.StatusCode, nil
}

// emitOrFail handles the standard output/error path:
//   - 2xx: emit JSON or call humanFn for human format.
//   - non-2xx: print error to stderr, exit 1.
func emitOrFail(cmd *cobra.Command, raw []byte, status int, humanFn func(raw []byte) error) error {
	asJSON := jsonFlag(cmd)
	if status >= 400 {
		if asJSON {
			out := norm.NFC.Bytes(raw)
			cmd.OutOrStdout().Write(out)
			if len(out) == 0 || out[len(out)-1] != '\n' {
				fmt.Fprintln(cmd.OutOrStdout())
			}
			os.Exit(1)
		}
		// Try to extract "detail" from JSON.
		var m map[string]interface{}
		msg := string(raw)
		if json.Unmarshal(raw, &m) == nil {
			if d, ok := m["detail"].(string); ok && d != "" {
				msg = d
			}
		}
		fmt.Fprintf(os.Stderr, "error: %s\n", norm.NFC.String(msg))
		os.Exit(1)
		return nil
	}
	if asJSON || humanFn == nil {
		out := norm.NFC.Bytes(raw)
		cmd.OutOrStdout().Write(out)
		if len(out) == 0 || out[len(out)-1] != '\n' {
			fmt.Fprintln(cmd.OutOrStdout())
		}
		return nil
	}
	return humanFn(raw)
}
