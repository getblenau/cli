package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/text/unicode/norm"
)

// envAPIURL allows overriding the API base URL.
const envAPIURL = "BLENAU_API_URL"

// envAPIToken allows overriding the API token from env.
const envAPIToken = "BLENAU_API_TOKEN"

// envAgentToken is a synonym for the service token (parity with @blenau/mcp).
const envAgentToken = "BLENAU_AGENT_TOKEN"

// authInfo is the resolved auth for a call, plus which lane it is.
type authInfo struct {
	apiURL       string
	token        string
	identityLane bool // true = device-flow identity (workspace selector allowed)
}

// serviceToken returns a pinned service credential if present (env or config).
func serviceToken() string {
	if t := os.Getenv(envAPIToken); t != "" {
		return t
	}
	if t := os.Getenv(envAgentToken); t != "" {
		return t
	}
	if cfg, _ := LoadConfig(); cfg != nil {
		return cfg.Token
	}
	return ""
}

func resolveAPIURL() string {
	if u := os.Getenv(envAPIURL); u != "" {
		return u
	}
	if cfg, _ := LoadConfig(); cfg != nil && cfg.APIURL != "" {
		return cfg.APIURL
	}
	return DefaultAPIURL
}

// resolveAuth picks the lane. The SERVICE lane wins whenever a service token is
// present (config is sticky and shared) — its workspace subsystem is OFF, so we
// never inject a selector (a pinned token would 409 on every call, SPEC 3 §1).
// Otherwise the IDENTITY lane uses the device-flow access token.
func resolveAuth() (*authInfo, error) {
	apiURL := resolveAPIURL()
	if st := serviceToken(); st != "" {
		return &authInfo{apiURL: apiURL, token: st, identityLane: false}, nil
	}
	if ok, err := identityConfigured(); err != nil {
		return nil, err
	} else if ok {
		tok, err := resolveIdentityAccessToken()
		if err != nil {
			return nil, err
		}
		return &authInfo{apiURL: apiURL, token: tok, identityLane: true}, nil
	}
	return nil, errNotLoggedIn
}

// isWorkspaceExempt: discovery paths carry NO X-Blenau-Workspace header
// (SPEC 1 §1.2) and never trigger workspace resolution (avoids recursion).
func isWorkspaceExempt(path string) bool {
	return path == "/workspaces" || path == "/health"
}

// classifyWrite marks the mutating endpoints the CLI reaches (SPEC 3 §6).
// Everything else — including POST /knowledge/search — is a read.
func classifyWrite(method, path string) bool {
	switch {
	case method == "POST" && path == "/knowledge/ingest-enhanced":
		return true
	case method == "POST" && path == "/knowledge/edit-section":
		return true
	case method == "POST" && strings.HasPrefix(path, "/assets/upload-binary"):
		return true
	case method == "DELETE" && strings.HasPrefix(path, "/assets/file"):
		return true
	// Collections mutations: record writes, backfill, reconcile, grants and
	// collection deletion all change tenant data, so the cross-workspace write
	// guard must fire for them exactly as it does for knowledge writes.
	case method == "POST" && strings.HasPrefix(path, "/collections/") &&
		(strings.HasSuffix(path, "/records") || strings.HasSuffix(path, "/import") ||
			strings.HasSuffix(path, "/reconcile")):
		return true
	case (method == "PUT" || method == "DELETE") && strings.Contains(path, "/grants/"):
		return true
	// Note-list shares are access control too — the cross-workspace write
	// guard must fire for them like for collection grants.
	case (method == "PUT" || method == "DELETE") && strings.HasPrefix(path, "/notes/shares/"):
		return true
	case method == "PUT" && path == "/notes/list-prefs":
		return true
	case method == "DELETE" && strings.HasPrefix(path, "/collections/"):
		return true
	}
	return false
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

// machineOutput reports whether the caller EXPLICITLY asked for JSON. It is
// fixed once in the root's PersistentPreRun so code with no `*cobra.Command` in
// scope (apiRequest's workspace notices) can honour it.
//
// `--json` used to govern stdout ONLY, while the progress notes kept going to
// stderr — technically the Unix convention, but the flag's own help says
// "instead of human format", and a caller that merged the two streams got four
// lines of prose in front of its JSON and a parse error. With --json the prose
// is now not written at all, on either stream.
//
// Deliberately NOT `jsonFlag`: that one also returns true when stdout merely
// isn't a TTY, and there the human is still watching stderr while stdout goes to
// a file. Silencing the echo of what is about to be deleted, for someone who
// never asked for machine output, would remove a safety signal and buy nothing —
// their stdout is already clean JSON either way.
//
// Never suppressed: real errors, and `confirmWrite`'s announcement — that one is
// part of a guard that either prompts or fails closed, and silencing it would
// hide a cross-workspace write.
var machineOutput bool

// setMachineOutput fixes machine mode for this invocation.
func setMachineOutput(cmd *cobra.Command) { machineOutput = jsonFlagExplicit(cmd) }

// jsonFlagExplicit is jsonFlag minus the non-TTY inference: true only when
// --json was actually passed (on the subcommand or as the root's persistent
// flag).
func jsonFlagExplicit(cmd *cobra.Command) bool {
	if asJSON, _ := cmd.Flags().GetBool("json"); cmd.Flags().Changed("json") {
		return asJSON
	}
	pj, _ := cmd.Root().PersistentFlags().GetBool("json")
	return pj
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
//
// In the identity lane it is the ONE place that resolves and injects the
// X-Blenau-Workspace selector (SPEC 3 §6) and applies the write guard (§7):
// confirmation when writing to a workspace other than the active one, and
// verification of the workspace echo after a successful write.
func apiCall(method, path string, body []byte) ([]byte, int, error) {
	var rdr io.Reader
	ct := ""
	if body != nil {
		rdr = bytes.NewReader(body)
		ct = "application/json"
	}
	return apiRequest(method, path, rdr, ct)
}

// apiRequest is the single HTTP chokepoint (JSON and multipart both flow through
// it), so the identity-lane workspace selector, write guard and echo check are
// applied uniformly — no per-command HTTP path can bypass them.
func apiRequest(method, path string, bodyReader io.Reader, contentType string) ([]byte, int, error) {
	auth, err := resolveAuth()
	if err != nil {
		return nil, 0, err
	}

	var target *WorkspaceRef
	isWrite := false
	if auth.identityLane && !isWorkspaceExempt(path) {
		var anchor *WorkspaceRef
		target, anchor, err = resolveTargetAndAnchor()
		if err != nil {
			return nil, 0, err
		}
		isWrite = classifyWrite(method, path)
		if isWrite && target != nil {
			if anchor == nil || target.ID != anchor.ID {
				if err := confirmWrite(target, anchor); err != nil {
					return nil, 0, err
				}
			} else if !machineOutput {
				fmt.Fprintf(os.Stderr, "→ writing to %s\n", target.Name)
			}
		}
	}

	req, err := http.NewRequest(method, auth.apiURL+path, bodyReader)
	if err != nil {
		return nil, 0, err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	req.Header.Set("Authorization", "Bearer "+auth.token)
	if target != nil && !isWorkspaceExempt(path) {
		req.Header.Set("X-Blenau-Workspace", target.ID)
	}
	client := &http.Client{Timeout: 120 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("call %s: %w", req.URL, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, err
	}
	// P0: a successful write must echo the workspace it landed in.
	if isWrite && target != nil && resp.StatusCode < 400 {
		if err := verifyWorkspaceEcho(raw, target.ID); err != nil {
			return raw, resp.StatusCode, err
		}
		if !machineOutput {
			fmt.Fprintf(os.Stderr, "✓ wrote to %s\n", target.Name)
		}
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
