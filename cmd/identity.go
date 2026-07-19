package cmd

import (
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/gofrs/flock"
	"github.com/zalando/go-keyring"
)

// keyringService is SEPARATE from @blenau/mcp's `blenau-mcp` service on purpose:
// sharing a refresh token across two clients would race on the single-use
// rotation and log both out (and the keytar↔go-keyring storage schemas differ).
const keyringService = "blenau-cli"
const keyringUser = "refresh_token"

// accessExpiryBuffer refreshes slightly before the real expiry so an in-flight
// request never races the boundary.
const accessExpiryBuffer = 60 * time.Second

// errNotLoggedIn signals the identity lane isn't configured (no refresh token).
var errNotLoggedIn = errors.New("not logged in: run `blenau login` (browser) or `blenau login --token <tk>`")

func saveRefreshToken(rt string) error {
	return keyring.Set(keyringService, keyringUser, rt)
}

func loadRefreshToken() (string, error) {
	rt, err := keyring.Get(keyringService, keyringUser)
	if errors.Is(err, keyring.ErrNotFound) {
		return "", nil
	}
	return rt, err
}

func deleteRefreshToken() {
	// Best-effort: absence is success.
	if err := keyring.Delete(keyringService, keyringUser); err != nil && !errors.Is(err, keyring.ErrNotFound) {
		// Non-fatal; local cache is cleared by the caller regardless.
		fmt.Fprintf(os.Stderr, "warning: could not clear keychain entry: %v\n", err)
	}
}

// identityConfigured reports whether a device-flow login exists (a refresh
// token in the keychain).
func identityConfigured() (bool, error) {
	rt, err := loadRefreshToken()
	if err != nil {
		return false, err
	}
	return rt != "", nil
}

// lockPath is the refresh serialization lockfile, next to config.json.
func lockPath() (string, error) {
	p, err := ConfigPath()
	if err != nil {
		return "", err
	}
	return p + ".lock", nil
}

// resolveIdentityAccessToken returns a valid access token for the identity lane,
// implementing the ephemeral-process token model (SPEC 3 §4):
//   - use the cached access token until ~60s before expiry;
//   - otherwise take a FILE LOCK, re-check the cache (another invocation may have
//     just refreshed), then refresh — serializing concurrent `blenau` processes
//     so the single-use refresh-token rotation never races;
//   - on invalid_grant, RE-READ the keychain before giving up (a concurrent
//     invocation may have rotated the RT) — never log out due to concurrency.
func resolveIdentityAccessToken() (string, error) {
	cfg, _ := LoadConfig()
	if cfg != nil && cfg.Identity != nil && cfg.Identity.AccessToken != "" {
		if time.Now().Add(accessExpiryBuffer).Unix() < cfg.Identity.ExpiresAt {
			return cfg.Identity.AccessToken, nil
		}
	}

	rt, err := loadRefreshToken()
	if err != nil {
		return "", err
	}
	if rt == "" {
		return "", errNotLoggedIn
	}

	lp, err := lockPath()
	if err != nil {
		return "", err
	}
	lock := flock.New(lp)
	if err := lock.Lock(); err != nil {
		return "", fmt.Errorf("acquire refresh lock: %w", err)
	}
	defer lock.Unlock()

	// Re-check under the lock: another process may have refreshed while we waited.
	if cfg2, _ := LoadConfig(); cfg2 != nil && cfg2.Identity != nil && cfg2.Identity.AccessToken != "" {
		if time.Now().Add(accessExpiryBuffer).Unix() < cfg2.Identity.ExpiresAt {
			return cfg2.Identity.AccessToken, nil
		}
		cfg = cfg2
	}
	// Re-read the RT under the lock too (a concurrent refresh may have rotated it).
	if freshRT, e := loadRefreshToken(); e == nil && freshRT != "" {
		rt = freshRT
	}

	d, err := fetchDiscovery()
	if err != nil {
		return "", err
	}
	tok, err := refreshAccessToken(d, rt)
	if err != nil {
		var oe *oauthError
		if asOAuthErr(err, &oe) && oe.Code == "invalid_grant" {
			// A concurrent invocation may have rotated the RT between our read and
			// now — re-read once before concluding the session is dead.
			if latest, e := loadRefreshToken(); e == nil && latest != "" && latest != rt {
				if tok2, e2 := refreshAccessToken(d, latest); e2 == nil {
					return persistRefreshed(cfg, latest, tok2)
				}
			}
			deleteRefreshToken()
			clearIdentityCache(cfg)
			return "", fmt.Errorf("session expired or revoked — run `blenau login` again")
		}
		return "", err
	}
	return persistRefreshed(cfg, rt, tok)
}

// persistRefreshed stores a rotated refresh token (BEFORE returning the access
// token, so a crash never strands a valid-but-unsaved RT) and caches the access
// token + expiry.
func persistRefreshed(cfg *Config, oldRT string, tok *tokenResponse) (string, error) {
	if tok.RefreshToken != "" && tok.RefreshToken != oldRT {
		if err := saveRefreshToken(tok.RefreshToken); err != nil {
			return "", fmt.Errorf("persist rotated refresh token: %w", err)
		}
	}
	if cfg == nil {
		cfg = &Config{APIURL: DefaultAPIURL}
	}
	cfg.Identity = &IdentityCache{
		AccessToken: tok.AccessToken,
		ExpiresAt:   time.Now().Add(time.Duration(tok.ExpiresIn) * time.Second).Unix(),
	}
	if _, err := SaveConfig(cfg); err != nil {
		return "", err
	}
	return tok.AccessToken, nil
}

func clearIdentityCache(cfg *Config) {
	if cfg == nil {
		return
	}
	cfg.Identity = nil
	cfg.ActiveWorkspace = nil
	_, _ = SaveConfig(cfg)
}

// loginDeviceFlow runs the browser device flow and stores the refresh token in
// the OS keychain + caches the first access token. Progress goes to `out`.
func loginDeviceFlow(out io.Writer) error {
	d, err := fetchDiscovery()
	if err != nil {
		return err
	}
	da, err := startDeviceAuthorization(d)
	if err != nil {
		return err
	}
	target := da.VerificationURIComplete
	if target == "" {
		target = da.VerificationURI
	}
	fmt.Fprintf(out, "To sign in, open:\n\n    %s\n\n", target)
	if da.VerificationURIComplete == "" {
		fmt.Fprintf(out, "and enter the code:  %s\n\n", da.UserCode)
	} else {
		fmt.Fprintf(out, "(code: %s)\n\n", da.UserCode)
	}
	fmt.Fprintln(out, "Waiting for approval...")

	tok, err := pollDeviceToken(d, da, time.Sleep)
	if err != nil {
		return err
	}
	if err := saveRefreshToken(tok.RefreshToken); err != nil {
		return fmt.Errorf("store refresh token in keychain: %w", err)
	}
	// Merge into existing config (preserve api_url), cache the access token.
	cfg, err := LoadConfig()
	if err != nil || cfg == nil {
		cfg = &Config{APIURL: DefaultAPIURL}
	}
	if cfg.APIURL == "" {
		cfg.APIURL = DefaultAPIURL
	}
	// Signing in with a browser identity supersedes any stale service token in
	// the config file (the keychain refresh token is now the credential).
	cfg.Token = ""
	cfg.Identity = &IdentityCache{
		AccessToken: tok.AccessToken,
		ExpiresAt:   time.Now().Add(time.Duration(tok.ExpiresIn) * time.Second).Unix(),
	}
	if _, err := SaveConfig(cfg); err != nil {
		return err
	}
	fmt.Fprintln(out, "Logged in.")
	return nil
}

// identityLogout revokes the refresh token at the IdP (best-effort) and clears
// all local identity state.
func identityLogout(out io.Writer) error {
	rt, _ := loadRefreshToken()
	if rt != "" {
		if d, err := fetchDiscovery(); err == nil {
			revokeToken(d, rt)
		}
	}
	deleteRefreshToken()
	if cfg, _ := LoadConfig(); cfg != nil {
		clearIdentityCache(cfg)
	}
	fmt.Fprintln(out, "Logged out.")
	return nil
}
