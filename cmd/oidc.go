package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// OIDC / device-flow configuration for the Blenau identity lane. Same public
// client, issuer and scope as @blenau/mcp so the two clients are interchangeable
// (a person can log in from either). Overridable by env for non-prod.
func oidcIssuer() string {
	if v := os.Getenv("BLENAU_OIDC_ISSUER"); v != "" {
		return strings.TrimRight(v, "/")
	}
	return "https://blenau.auth.prysmid.com"
}

func oidcClientID() string {
	if v := os.Getenv("BLENAU_OIDC_CLIENT_ID"); v != "" {
		return v
	}
	return "375546511070593027"
}

const oidcScope = "openid profile email offline_access"

// discovery is the subset of the OpenID Connect discovery document we use.
type discovery struct {
	Issuer                      string `json:"issuer"`
	DeviceAuthorizationEndpoint string `json:"device_authorization_endpoint"`
	TokenEndpoint               string `json:"token_endpoint"`
	RevocationEndpoint          string `json:"revocation_endpoint"`
}

// oauthError carries the OAuth 2.0 `error` code from a token-endpoint response
// so callers can branch on `authorization_pending` / `slow_down` /
// `invalid_grant` rather than on HTTP status.
type oauthError struct {
	Code        string
	Description string
}

func (e *oauthError) Error() string {
	if e.Description != "" {
		return fmt.Sprintf("%s: %s", e.Code, e.Description)
	}
	return e.Code
}

// tokenResponse is the token-endpoint success payload.
type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	IDToken      string `json:"id_token"`
	ExpiresIn    int    `json:"expires_in"`
	TokenType    string `json:"token_type"`
}

// deviceAuthResponse is the device_authorization_endpoint payload (RFC 8628).
type deviceAuthResponse struct {
	DeviceCode              string `json:"device_code"`
	UserCode                string `json:"user_code"`
	VerificationURI         string `json:"verification_uri"`
	VerificationURIComplete string `json:"verification_uri_complete"`
	ExpiresIn               int    `json:"expires_in"`
	Interval                int    `json:"interval"`
}

func oidcHTTP() *http.Client { return &http.Client{Timeout: 30 * time.Second} }

// fetchDiscovery loads <issuer>/.well-known/openid-configuration so a Prysm:ID
// endpoint change never strands the client on a hard-coded path.
func fetchDiscovery() (*discovery, error) {
	u := oidcIssuer() + "/.well-known/openid-configuration"
	resp, err := oidcHTTP().Get(u)
	if err != nil {
		return nil, fmt.Errorf("OIDC discovery: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("OIDC discovery: %s returned %d", u, resp.StatusCode)
	}
	var d discovery
	if err := json.NewDecoder(resp.Body).Decode(&d); err != nil {
		return nil, fmt.Errorf("OIDC discovery: decode: %w", err)
	}
	if d.DeviceAuthorizationEndpoint == "" || d.TokenEndpoint == "" {
		return nil, fmt.Errorf("OIDC discovery: missing device_authorization/token endpoint")
	}
	return &d, nil
}

// postForm posts application/x-www-form-urlencoded and maps an OAuth error body
// to *oauthError (so callers can branch on the code).
func postForm(endpoint string, form url.Values) (*tokenResponse, error) {
	resp, err := oidcHTTP().PostForm(endpoint, form)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		var e struct {
			Error       string `json:"error"`
			Description string `json:"error_description"`
		}
		if json.Unmarshal(raw, &e) == nil && e.Error != "" {
			return nil, &oauthError{Code: e.Error, Description: e.Description}
		}
		return nil, fmt.Errorf("token endpoint %s: %d %s", endpoint, resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	var tok tokenResponse
	if err := json.Unmarshal(raw, &tok); err != nil {
		return nil, fmt.Errorf("token endpoint: decode: %w", err)
	}
	return &tok, nil
}

// startDeviceAuthorization kicks off the device flow (RFC 8628 §3.1).
func startDeviceAuthorization(d *discovery) (*deviceAuthResponse, error) {
	form := url.Values{"client_id": {oidcClientID()}, "scope": {oidcScope}}
	resp, err := oidcHTTP().PostForm(d.DeviceAuthorizationEndpoint, form)
	if err != nil {
		return nil, fmt.Errorf("device_authorization: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		raw, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("device_authorization failed: %d %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	var da deviceAuthResponse
	if err := json.NewDecoder(resp.Body).Decode(&da); err != nil {
		return nil, fmt.Errorf("device_authorization: decode: %w", err)
	}
	if da.Interval == 0 {
		da.Interval = 5
	}
	return &da, nil
}

// pollDeviceToken polls the token endpoint until the user approves, handling
// authorization_pending / slow_down (RFC 8628 §3.4-3.5). `sleep` is injectable
// for tests.
func pollDeviceToken(d *discovery, da *deviceAuthResponse, sleep func(time.Duration)) (*tokenResponse, error) {
	interval := time.Duration(da.Interval) * time.Second
	deadline := time.Now().Add(time.Duration(da.ExpiresIn) * time.Second)
	for {
		if !deadline.IsZero() && time.Now().After(deadline) {
			return nil, fmt.Errorf("device code expired before approval — run `blenau login` again")
		}
		sleep(interval)
		tok, err := postForm(d.TokenEndpoint, url.Values{
			"grant_type":  {"urn:ietf:params:oauth:grant-type:device_code"},
			"device_code": {da.DeviceCode},
			"client_id":   {oidcClientID()},
		})
		if err != nil {
			var oe *oauthError
			if asOAuthErr(err, &oe) {
				switch oe.Code {
				case "authorization_pending":
					continue
				case "slow_down":
					interval += 5 * time.Second
					continue
				}
			}
			return nil, err
		}
		if tok.RefreshToken == "" {
			return nil, fmt.Errorf("no refresh_token returned — was offline_access granted to the client?")
		}
		return tok, nil
	}
}

// refreshAccessToken exchanges a refresh token for a new access token. On
// rotation the caller must persist the new refresh token before using the
// access token.
func refreshAccessToken(d *discovery, refreshToken string) (*tokenResponse, error) {
	return postForm(d.TokenEndpoint, url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
		"client_id":     {oidcClientID()},
	})
}

// revokeToken best-effort revokes the refresh token at the IdP (RFC 7009).
func revokeToken(d *discovery, refreshToken string) {
	if d.RevocationEndpoint == "" {
		return
	}
	_, _ = oidcHTTP().PostForm(d.RevocationEndpoint, url.Values{
		"token":           {refreshToken},
		"token_type_hint": {"refresh_token"},
		"client_id":       {oidcClientID()},
	})
}

// asOAuthErr is a small errors.As helper kept local to avoid importing errors in
// every caller.
func asOAuthErr(err error, target **oauthError) bool {
	if oe, ok := err.(*oauthError); ok {
		*target = oe
		return true
	}
	return false
}
