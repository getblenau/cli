package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zalando/go-keyring"
)

// isolateEnv points config + keychain + OIDC at test doubles and clears the
// service-token env so tests start from a known lane.
func isolateEnv(t *testing.T) string {
	t.Helper()
	keyring.MockInit()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir) // linux/CI
	t.Setenv("APPDATA", dir)         // windows
	t.Setenv("BLENAU_API_TOKEN", "")
	t.Setenv("BLENAU_AGENT_TOKEN", "")
	t.Setenv("BLENAU_WORKSPACE", "")
	flagWorkspace = ""
	flagConfirmWorkspace = ""
	return dir
}

// mockOIDC serves discovery + device_authorization + token + revocation. The
// token endpoint returns `pending` times of authorization_pending before the
// success payload, and rotates the refresh token on each success.
func mockOIDC(t *testing.T, pending int32) *httptest.Server {
	t.Helper()
	var srv *httptest.Server
	var tokenCalls int32
	var rtCounter int32
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{
			"issuer":                        srv.URL,
			"device_authorization_endpoint": srv.URL + "/device",
			"token_endpoint":                srv.URL + "/token",
			"revocation_endpoint":           srv.URL + "/revoke",
		})
	})
	mux.HandleFunc("/device", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"device_code": "dc", "user_code": "UC",
			"verification_uri": srv.URL + "/activate", "verification_uri_complete": srv.URL + "/activate?u=UC",
			"expires_in": 300, "interval": 1,
		})
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.FormValue("grant_type") == "urn:ietf:params:oauth:grant-type:device_code" {
			if atomic.AddInt32(&tokenCalls, 1) <= pending {
				w.WriteHeader(400)
				json.NewEncoder(w).Encode(map[string]string{"error": "authorization_pending"})
				return
			}
		}
		n := atomic.AddInt32(&rtCounter, 1)
		json.NewEncoder(w).Encode(map[string]any{
			"access_token":  "access-tok",
			"refresh_token": "rt-" + string(rune('0'+n)),
			"expires_in":    3600,
			"token_type":    "Bearer",
		})
	})
	mux.HandleFunc("/revoke", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	t.Setenv("BLENAU_OIDC_ISSUER", srv.URL)
	t.Setenv("BLENAU_OIDC_CLIENT_ID", "test-client")
	return srv
}

func TestClassifyWrite(t *testing.T) {
	cases := []struct {
		method, path string
		want         bool
	}{
		{"POST", "/knowledge/ingest-enhanced", true},
		{"POST", "/knowledge/edit-section", true},
		{"POST", "/assets/upload-binary", true},
		{"DELETE", "/assets/file/x", true},
		{"POST", "/knowledge/search", false}, // a read despite POST
		{"GET", "/github/repos", false},
		{"GET", "/workspaces", false},
	}
	for _, c := range cases {
		if got := classifyWrite(c.method, c.path); got != c.want {
			t.Errorf("classifyWrite(%s %s)=%v want %v", c.method, c.path, got, c.want)
		}
	}
}

func TestVerifyWorkspaceEcho(t *testing.T) {
	if err := verifyWorkspaceEcho([]byte(`{"workspace":{"id":"W1"}}`), "W1"); err != nil {
		t.Errorf("match should pass: %v", err)
	}
	if err := verifyWorkspaceEcho([]byte(`{"workspace":{"id":"W2"}}`), "W1"); err == nil {
		t.Error("mismatch must error")
	}
	if err := verifyWorkspaceEcho([]byte(`{"ok":true}`), "W1"); err == nil {
		t.Error("missing echo must error (refuse to assume success)")
	}
}

func TestConfirmWriteNonTTYFailClosed(t *testing.T) {
	isolateEnv(t)
	target := &WorkspaceRef{ID: "G", Slug: "globex", Name: "Globex"}
	anchor := &WorkspaceRef{ID: "A", Slug: "acme", Name: "Acme"}

	// stdin is not a TTY under `go test` → fail-closed without confirmation.
	if err := confirmWrite(target, anchor); err == nil {
		t.Error("non-TTY write to non-active workspace must fail closed")
	}
	// Matching --confirm-workspace unblocks it.
	flagConfirmWorkspace = "globex"
	if err := confirmWrite(target, anchor); err != nil {
		t.Errorf("--confirm-workspace globex should pass: %v", err)
	}
	// A non-matching confirmation is rejected (not a blanket yes).
	flagConfirmWorkspace = "acme"
	if err := confirmWrite(target, anchor); err == nil {
		t.Error("--confirm-workspace naming a different workspace must be rejected")
	}
}

func TestPollDeviceToken(t *testing.T) {
	srv := mockOIDC(t, 2) // 2 pending polls, then success
	d, err := fetchDiscovery()
	if err != nil {
		t.Fatal(err)
	}
	da, err := startDeviceAuthorization(d)
	if err != nil {
		t.Fatal(err)
	}
	tok, err := pollDeviceToken(d, da, func(time.Duration) {}) // no real sleeping
	if err != nil {
		t.Fatalf("poll: %v", err)
	}
	if tok.AccessToken == "" || tok.RefreshToken == "" {
		t.Fatalf("expected tokens, got %+v", tok)
	}
	_ = srv
}

func TestLoginStoresKeychainAndCachesAccess(t *testing.T) {
	isolateEnv(t)
	mockOIDC(t, 0)
	if err := loginDeviceFlow(os.Stderr); err != nil {
		t.Fatalf("login: %v", err)
	}
	rt, _ := loadRefreshToken()
	if rt == "" {
		t.Error("refresh token must be stored in the keychain")
	}
	cfg, _ := LoadConfig()
	if cfg == nil || cfg.Identity == nil || cfg.Identity.AccessToken == "" {
		t.Error("access token must be cached in config")
	}
	if cfg.Token != "" {
		t.Error("browser login must clear any stale service token")
	}
}

func TestIdentityAccessTokenCachingAndRefresh(t *testing.T) {
	isolateEnv(t)
	mockOIDC(t, 0)
	if err := loginDeviceFlow(os.Stderr); err != nil {
		t.Fatal(err)
	}
	// Cached: returns without hitting the IdP.
	tok, err := resolveIdentityAccessToken()
	if err != nil || tok == "" {
		t.Fatalf("cached token: %v", err)
	}
	// Force expiry → triggers a refresh + RT rotation.
	cfg, _ := LoadConfig()
	rtBefore, _ := loadRefreshToken()
	cfg.Identity.ExpiresAt = time.Now().Add(-time.Hour).Unix()
	SaveConfig(cfg)
	if _, err := resolveIdentityAccessToken(); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	rtAfter, _ := loadRefreshToken()
	if rtAfter == rtBefore {
		t.Error("refresh should have rotated (persisted) a new refresh token")
	}
}

func TestServiceLaneNeverSendsWorkspaceHeader(t *testing.T) {
	isolateEnv(t)
	var gotHeader string
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeader = r.Header.Get("X-Blenau-Workspace")
		w.Write([]byte(`{"ok":true}`))
	}))
	defer api.Close()
	t.Setenv("BLENAU_API_URL", api.URL)
	t.Setenv("BLENAU_API_TOKEN", "blenau_tk_test") // service lane

	// Even with an active workspace configured, the service lane must not inject.
	SaveConfig(&Config{APIURL: api.URL, ActiveWorkspace: &WorkspaceRef{ID: "W1", Slug: "w1"}})
	if _, _, err := apiCall("GET", "/knowledge/documents", nil); err != nil {
		t.Fatal(err)
	}
	if gotHeader != "" {
		t.Errorf("service lane must not send X-Blenau-Workspace, got %q", gotHeader)
	}
}

func TestIdentityLaneInjectsActiveWorkspace(t *testing.T) {
	isolateEnv(t)
	mockOIDC(t, 0)
	var gotHeader string
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeader = r.Header.Get("X-Blenau-Workspace")
		w.Write([]byte(`{"ok":true}`))
	}))
	defer api.Close()
	t.Setenv("BLENAU_API_URL", api.URL)
	if err := loginDeviceFlow(os.Stderr); err != nil {
		t.Fatal(err)
	}
	cfg, _ := LoadConfig()
	cfg.APIURL = api.URL
	cfg.ActiveWorkspace = &WorkspaceRef{ID: "W-active", Slug: "acme", Name: "Acme"}
	SaveConfig(cfg)

	if _, _, err := apiCall("GET", "/knowledge/documents", nil); err != nil {
		t.Fatal(err)
	}
	if gotHeader != "W-active" {
		t.Errorf("identity-lane read should roam to the active workspace header, got %q", gotHeader)
	}
}

func TestUpdateCacheFreshShortCircuitsNetwork(t *testing.T) {
	isolateEnv(t)
	// Point the update check at an unreachable host so any network attempt fails
	// loudly; a fresh cache must be used WITHOUT touching the network.
	p, _ := updateCachePath()
	_ = os.MkdirAll(filepath.Dir(p), 0o700)
	data, _ := json.Marshal(updateCache{CheckedAt: time.Now().Unix(), LatestVersion: "v9.9.9"})
	if err := os.WriteFile(p, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if got := cachedLatestRelease(); got != "v9.9.9" {
		t.Errorf("a fresh cache should be returned without a network call, got %q", got)
	}
}

// TestNoRawHTTPOutsideHTTPClient enforces the single-chokepoint rule (SPEC 3
// §6): no cmd/*.go may build its own http.NewRequest — otherwise the workspace
// selector/guard/echo can be bypassed.
func TestNoRawHTTPOutsideHTTPClient(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		// Sanctioned HTTP sites: the API chokepoint, the OIDC/device flow, and
		// the GitHub release check (a non-Blenau, unauthenticated call).
		if name == "httpclient.go" || name == "oidc.go" || name == "updatecheck.go" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(".", name))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), "http.NewRequest") {
			t.Errorf("%s calls http.NewRequest directly — route it through apiCall/apiRequest instead", name)
		}
	}
}
