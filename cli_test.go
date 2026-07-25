package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

// buildBin compiles the CLI into a temp binary and returns its path.
func buildBin(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "blenau")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	cmd := exec.Command("go", "build", "-o", bin, ".")
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("go build: %v", err)
	}
	return bin
}

// TestStdoutUTF8 forks the binary with a query that round-trips through the
// help text path (which contains non-ASCII em-dash) and asserts stdout is
// valid UTF-8 NFC.
func TestStdoutUTF8(t *testing.T) {
	bin := buildBin(t)
	out, err := exec.Command(bin, "--help").Output()
	if err != nil {
		t.Fatalf("run --help: %v", err)
	}
	if !utf8.Valid(out) {
		t.Fatalf("stdout is not valid UTF-8: %q", out)
	}
	if !bytes.Equal(out, norm.NFC.Bytes(out)) {
		t.Fatalf("stdout is not NFC-normalized")
	}
	// Sanity: the long help string contains an em-dash.
	if !bytes.Contains(out, []byte("—")) {
		t.Fatalf("expected em-dash in --help output, got: %s", out)
	}
}

// TestAgentManifest runs --agent-manifest and asserts shape.
func TestAgentManifest(t *testing.T) {
	bin := buildBin(t)
	out, err := exec.Command(bin, "--agent-manifest").Output()
	if err != nil {
		t.Fatalf("run --agent-manifest: %v", err)
	}
	var m struct {
		Name     string `json:"name"`
		Version  string `json:"version"`
		Commands []struct {
			Name  string `json:"name"`
			Flags []struct {
				Name     string `json:"name"`
				Required bool   `json:"required"`
			} `json:"flags"`
		} `json:"commands"`
		GlobalFlags []map[string]interface{} `json:"global_flags"`
	}
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatalf("parse manifest JSON: %v\noutput: %s", err, out)
	}
	if m.Name != "blenau" {
		t.Fatalf("manifest.name = %q, want blenau", m.Name)
	}
	if m.Version == "" {
		t.Fatalf("manifest.version is empty")
	}
	got := map[string]bool{}
	for _, c := range m.Commands {
		got[c.Name] = true
	}
	if !got["login"] {
		t.Fatalf("manifest missing 'login' command: %s", out)
	}
	if !got["search"] {
		t.Fatalf("manifest missing 'search' command: %s", out)
	}
	// login.token must be required.
	for _, c := range m.Commands {
		if c.Name == "login" {
			foundRequired := false
			for _, f := range c.Flags {
				if f.Name == "token" && f.Required {
					foundRequired = true
				}
			}
			if !foundRequired {
				t.Fatalf("login.token must be required")
			}
		}
	}
}

// TestSearchJSONOutput is a smoke test against the live API. Skipped without a
// token in BLENAU_TEST_TOKEN.
func TestSearchJSONOutput(t *testing.T) {
	tk := os.Getenv("BLENAU_TEST_TOKEN")
	if tk == "" {
		t.Skip("BLENAU_TEST_TOKEN not set; skipping live API smoke test")
	}
	bin := buildBin(t)
	// Point config at a temp dir.
	tmp := t.TempDir()
	if runtime.GOOS == "windows" {
		t.Setenv("APPDATA", tmp)
	} else {
		t.Setenv("XDG_CONFIG_HOME", tmp)
	}
	if err := exec.Command(bin, "login", "--token", tk).Run(); err != nil {
		t.Fatalf("login: %v", err)
	}
	out, err := exec.Command(bin, "search", "test", "--json").Output()
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	var resp map[string]interface{}
	if err := json.Unmarshal(out, &resp); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out)
	}
	if _, ok := resp["results"]; !ok {
		t.Fatalf("missing 'results' field: %s", out)
	}
	_ = strings.TrimSpace
}

// TestReposJSON spins up a fake API and asserts the CLI prints the JSON body.
func TestReposJSON(t *testing.T) {
	bin := buildBin(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/github/repos" {
			http.Error(w, "not found", 404)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"repos":[{"name":"api","full_name":"getblenau/api","path_prefix":"api/","private":true}]}`))
	}))
	defer srv.Close()
	tmp := t.TempDir()
	if runtime.GOOS == "windows" {
		t.Setenv("APPDATA", tmp)
	} else {
		t.Setenv("XDG_CONFIG_HOME", tmp)
	}
	t.Setenv("BLENAU_API_URL", srv.URL)
	t.Setenv("BLENAU_API_TOKEN", "test")
	out, err := exec.Command(bin, "repos", "--json").Output()
	if err != nil {
		t.Fatalf("repos: %v", err)
	}
	var resp map[string]interface{}
	if err := json.Unmarshal(out, &resp); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out)
	}
	if _, ok := resp["repos"]; !ok {
		t.Fatalf("missing 'repos' field: %s", out)
	}
}

// TestWorkspaceHeader asserts the global --workspace flag is forwarded to the
// API as the X-Blenau-Workspace header (multi-workspace roaming).
func TestWorkspaceHeader(t *testing.T) {
	bin := buildBin(t)
	gotHeader := make(chan string, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeader <- r.Header.Get("X-Blenau-Workspace")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"repos":[],"count":0}`))
	}))
	defer srv.Close()
	tmp := t.TempDir()
	if runtime.GOOS == "windows" {
		t.Setenv("APPDATA", tmp)
	} else {
		t.Setenv("XDG_CONFIG_HOME", tmp)
	}
	t.Setenv("BLENAU_API_URL", srv.URL)
	t.Setenv("BLENAU_API_TOKEN", "test")
	want := "ws-uuid-123"
	if err := exec.Command(bin, "repos", "list", "--workspace", want, "--json").Run(); err != nil {
		t.Fatalf("repos list: %v", err)
	}
	if got := <-gotHeader; got != want {
		t.Fatalf("X-Blenau-Workspace = %q, want %q", got, want)
	}
}

// TestDocsGet404 asserts non-2xx error path: stderr has detail, exit code 1.
func TestDocsGet404(t *testing.T) {
	bin := buildBin(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(404)
		_, _ = w.Write([]byte(`{"detail":"Not found"}`))
	}))
	defer srv.Close()
	tmp := t.TempDir()
	if runtime.GOOS == "windows" {
		t.Setenv("APPDATA", tmp)
	} else {
		t.Setenv("XDG_CONFIG_HOME", tmp)
	}
	t.Setenv("BLENAU_API_URL", srv.URL)
	t.Setenv("BLENAU_API_TOKEN", "test")
	cmd := exec.Command(bin, "docs", "get", "notfound")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err == nil {
		t.Fatalf("expected non-zero exit")
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		if exitErr.ExitCode() != 1 {
			t.Fatalf("expected exit code 1, got %d", exitErr.ExitCode())
		}
	} else {
		t.Fatalf("unexpected error type: %T %v", err, err)
	}
	if !bytes.Contains(stderr.Bytes(), []byte("Not found")) {
		t.Fatalf("expected 'Not found' in stderr, got: %s", stderr.String())
	}
}
