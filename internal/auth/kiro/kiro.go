// Package kiro provides authentication and token management for Kiro (Amazon Q Developer CLI).
// It implements the AWS SSO OIDC RFC 8628 Device Authorization Grant flow for Builder ID.
package kiro

import (
	"bytes"
	"context"
	"crypto/sha1"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	_ "modernc.org/sqlite" // register the "sqlite" driver
)

const (
	// oidcRegion is the default AWS region used for the OIDC endpoint when no region is specified.
	oidcRegion = "us-east-1"

	// oidcBaseURL is the AWS SSO OIDC endpoint for Amazon Builder ID (us-east-1 default).
	oidcBaseURL = "https://oidc.us-east-1.amazonaws.com"

	// oidcURLTemplate is the region-parameterized AWS SSO OIDC token endpoint.
	// The SSO region (from the token's region field) is substituted at runtime.
	oidcURLTemplate = "https://oidc.%s.amazonaws.com/token"

	// kiroDesktopRefreshURLTemplate is the Kiro Desktop authentication endpoint for token refresh.
	// Used when the token was obtained via social login (Google/GitHub/Microsoft) and no OIDC
	// client_id/client_secret is available. The region is substituted at runtime.
	kiroDesktopRefreshURLTemplate = "https://prod.%s.auth.desktop.kiro.dev/refreshToken"

	// kiroStartURL is the Amazon Builder ID start URL (public users).
	kiroStartURL = "https://view.awsapps.com/start"

	// kiroClientName registers this client with the OIDC service.
	kiroClientName = "Kiro"

	// kiroClientType is the OAuth2 client type for public clients.
	kiroClientType = "public"

	// KiroAPIBaseURL is the CodeWhisperer streaming API endpoint.
	KiroAPIBaseURL = "https://q.us-east-1.amazonaws.com"

	// deviceGrantType is the RFC 8628 device_code grant type URI.
	deviceGrantType = "urn:ietf:params:oauth:grant-type:device_code"

	// refreshGrantType is the standard OAuth2 refresh_token grant type.
	refreshGrantType = "refresh_token"

	// defaultPollInterval is the default polling interval for token requests.
	defaultPollInterval = 5 * time.Second

	// maxPollDuration is the maximum time to wait for device authorization.
	maxPollDuration = 15 * time.Minute
)

// kiroScopes are the OAuth2 scopes required for Kiro / CodeWhisperer API access.
var kiroScopes = []string{
	"codewhisperer:completions",
	"codewhisperer:analysis",
	"codewhisperer:conversations",
}

// KiroAuth handles Kiro authentication via AWS Builder ID OIDC.
type KiroAuth struct {
	cfg *config.Config
}

// NewKiroAuth creates a new KiroAuth service instance.
func NewKiroAuth(cfg *config.Config) *KiroAuth {
	return &KiroAuth{cfg: cfg}
}

// ClientRegistrationResponse is returned by the OIDC client/register endpoint.
type ClientRegistrationResponse struct {
	ClientID              string `json:"clientId"`
	ClientSecret          string `json:"clientSecret"`
	ClientIDIssuedAt      int64  `json:"clientIdIssuedAt"`
	ClientSecretExpiresAt int64  `json:"clientSecretExpiresAt"`
}

// DeviceAuthorizationResponse is returned by the OIDC device_authorization endpoint.
type DeviceAuthorizationResponse struct {
	DeviceCode              string `json:"deviceCode"`
	UserCode                string `json:"userCode"`
	VerificationURI         string `json:"verificationUri"`
	VerificationURIComplete string `json:"verificationUriComplete"`
	ExpiresIn               int    `json:"expiresIn"`
	Interval                int    `json:"interval"`
}

// TokenResponse is returned by the OIDC token endpoint on success.
type TokenResponse struct {
	AccessToken  string `json:"accessToken"`
	ExpiresIn    int    `json:"expiresIn"`
	RefreshToken string `json:"refreshToken"`
	TokenType    string `json:"tokenType"`
}

// oidcError represents an OIDC error response body.
type oidcError struct {
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
}

// RegisterClient registers a new OIDC public client with the Amazon Builder ID service.
func (k *KiroAuth) RegisterClient(ctx context.Context) (*ClientRegistrationResponse, error) {
	body := map[string]any{
		"clientName": kiroClientName,
		"clientType": kiroClientType,
		"scopes":     kiroScopes,
	}
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("kiro: marshal register client body: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		oidcBaseURL+"/client/register", strings.NewReader(string(bodyBytes)))
	if err != nil {
		return nil, fmt.Errorf("kiro: create register client request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := k.httpClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("kiro: register client: %w", err)
	}
	defer func() {
		if errClose := resp.Body.Close(); errClose != nil {
			_ = errClose
		}
	}()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("kiro: read register client response: %w", err)
	}
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("kiro: register client status %d: %s", resp.StatusCode, respBody)
	}

	var result ClientRegistrationResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("kiro: parse register client response: %w", err)
	}
	return &result, nil
}

// StartDeviceAuthorization initiates the RFC 8628 device authorization flow.
func (k *KiroAuth) StartDeviceAuthorization(ctx context.Context, reg *ClientRegistrationResponse) (*DeviceAuthorizationResponse, error) {
	params := url.Values{}
	params.Set("client_id", reg.ClientID)
	params.Set("client_secret", reg.ClientSecret)
	params.Set("startUrl", kiroStartURL)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		oidcBaseURL+"/device_authorization", strings.NewReader(params.Encode()))
	if err != nil {
		return nil, fmt.Errorf("kiro: create device authorization request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := k.httpClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("kiro: device authorization: %w", err)
	}
	defer func() {
		if errClose := resp.Body.Close(); errClose != nil {
			_ = errClose
		}
	}()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("kiro: read device authorization response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("kiro: device authorization status %d: %s", resp.StatusCode, respBody)
	}

	var result DeviceAuthorizationResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("kiro: parse device authorization response: %w", err)
	}
	return &result, nil
}

// PollForToken polls the token endpoint until authorization completes or times out.
func (k *KiroAuth) PollForToken(ctx context.Context, reg *ClientRegistrationResponse, deviceAuth *DeviceAuthorizationResponse) (*TokenResponse, error) {
	pollInterval := defaultPollInterval
	if deviceAuth.Interval > 0 {
		pollInterval = time.Duration(deviceAuth.Interval) * time.Second
	}

	deadline := time.Now().Add(maxPollDuration)
	if deviceAuth.ExpiresIn > 0 {
		expiryAt := time.Now().Add(time.Duration(deviceAuth.ExpiresIn) * time.Second)
		if expiryAt.Before(deadline) {
			deadline = expiryAt
		}
	}

	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(pollInterval):
		}

		token, pending, slowDown, err := k.fetchToken(ctx, reg, deviceAuth.DeviceCode)
		if err != nil {
			return nil, err
		}
		if token != nil {
			return token, nil
		}
		if slowDown {
			pollInterval = pollInterval * 2
		}
		_ = pending
	}
	return nil, fmt.Errorf("kiro: device authorization timed out")
}

// fetchToken makes one token request.
// Returns (token, false, false, nil) on success,
// (nil, true, false, nil) when authorization is pending,
// (nil, false, true, nil) for slow_down,
// or (nil, false, false, err) on a terminal error.
func (k *KiroAuth) fetchToken(ctx context.Context, reg *ClientRegistrationResponse, deviceCode string) (*TokenResponse, bool, bool, error) {
	params := url.Values{}
	params.Set("client_id", reg.ClientID)
	params.Set("client_secret", reg.ClientSecret)
	params.Set("device_code", deviceCode)
	params.Set("grant_type", deviceGrantType)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		oidcBaseURL+"/token", strings.NewReader(params.Encode()))
	if err != nil {
		return nil, false, false, fmt.Errorf("kiro: create token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := k.httpClient().Do(req)
	if err != nil {
		return nil, false, false, fmt.Errorf("kiro: token request: %w", err)
	}
	defer func() {
		if errClose := resp.Body.Close(); errClose != nil {
			_ = errClose
		}
	}()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, false, false, fmt.Errorf("kiro: read token response: %w", err)
	}

	if resp.StatusCode == http.StatusOK {
		var token TokenResponse
		if errUnmarshal := json.Unmarshal(respBody, &token); errUnmarshal != nil {
			return nil, false, false, fmt.Errorf("kiro: parse token response: %w", errUnmarshal)
		}
		return &token, false, false, nil
	}

	var errResp oidcError
	if errUnmarshal := json.Unmarshal(respBody, &errResp); errUnmarshal != nil {
		return nil, false, false, fmt.Errorf("kiro: token request status %d: %s", resp.StatusCode, respBody)
	}

	switch errResp.Error {
	case "authorization_pending":
		return nil, true, false, nil
	case "slow_down":
		return nil, true, true, nil
	case "expired_token":
		return nil, false, false, fmt.Errorf("kiro: device code expired")
	case "access_denied":
		return nil, false, false, fmt.Errorf("kiro: access denied by user")
	default:
		return nil, false, false, fmt.Errorf("kiro: token error %q: %s", errResp.Error, errResp.ErrorDescription)
	}
}

// RefreshToken exchanges a refresh token for a new access token via AWS SSO OIDC.
// ssoRegion specifies the AWS region for the OIDC endpoint; if empty, us-east-1 is used.
// Note: the CodeWhisperer API always uses us-east-1 regardless of ssoRegion.
func (k *KiroAuth) RefreshToken(ctx context.Context, clientID, clientSecret, refreshToken, ssoRegion string) (*TokenResponse, error) {
	region := strings.TrimSpace(ssoRegion)
	if region == "" {
		region = oidcRegion
	}
	tokenURL := fmt.Sprintf(oidcURLTemplate, region)

	// Attempt 1: standard OAuth2 form-encoded body (snake_case params).
	if tok, err := k.doRefreshTokenRequest(ctx, tokenURL, clientID, clientSecret, refreshToken, false, false); err == nil {
		return tok, nil
	}

	// Attempt 2: JSON body with camelCase params (some kiro OIDC backends deviate from RFC 6749).
	if tok, err := k.doRefreshTokenRequest(ctx, tokenURL, clientID, clientSecret, refreshToken, true, false); err == nil {
		return tok, nil
	}

	// Attempt 3: form-encoded without client_secret (public-client flows may reject it).
	return k.doRefreshTokenRequest(ctx, tokenURL, clientID, clientSecret, refreshToken, false, true)
}

// doRefreshTokenRequest performs a single OIDC refresh-token HTTP request.
// jsonBody selects JSON body (camelCase) vs form-encoded (snake_case).
// omitSecret omits the client_secret field (for public clients that reject it).
func (k *KiroAuth) doRefreshTokenRequest(
	ctx context.Context,
	tokenURL, clientID, clientSecret, refreshToken string,
	jsonBody, omitSecret bool,
) (*TokenResponse, error) {
	var reqBody io.Reader
	var contentType string

	if jsonBody {
		payload := map[string]string{
			"clientId":     clientID,
			"grantType":    refreshGrantType,
			"refreshToken": refreshToken,
		}
		if !omitSecret && clientSecret != "" {
			payload["clientSecret"] = clientSecret
		}
		b, err := json.Marshal(payload)
		if err != nil {
			return nil, fmt.Errorf("kiro: marshal JSON refresh body: %w", err)
		}
		reqBody = bytes.NewReader(b)
		contentType = "application/json"
	} else {
		params := url.Values{}
		params.Set("client_id", clientID)
		if !omitSecret && clientSecret != "" {
			params.Set("client_secret", clientSecret)
		}
		params.Set("refresh_token", refreshToken)
		params.Set("grant_type", refreshGrantType)
		reqBody = strings.NewReader(params.Encode())
		contentType = "application/x-www-form-urlencoded"
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, reqBody)
	if err != nil {
		return nil, fmt.Errorf("kiro: create refresh token request: %w", err)
	}
	req.Header.Set("Content-Type", contentType)

	resp, err := k.httpClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("kiro: refresh token request: %w", err)
	}
	defer func() {
		if errClose := resp.Body.Close(); errClose != nil {
			_ = errClose
		}
	}()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("kiro: read refresh token response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("kiro: refresh token status %d: %s", resp.StatusCode, respBody)
	}

	var result TokenResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("kiro: parse refresh token response: %w", err)
	}
	return &result, nil
}

// RefreshTokenDesktop exchanges a refresh token for a new access token via the Kiro Desktop
// authentication endpoint. This path is used when the token was obtained via social login
// (Google, GitHub, Microsoft, etc.) and no OIDC client_id/client_secret is available.
// ssoRegion specifies the AWS region; if empty, us-east-1 is used.
func (k *KiroAuth) RefreshTokenDesktop(ctx context.Context, ssoRegion, refreshToken string) (*TokenResponse, error) {
	if strings.TrimSpace(refreshToken) == "" {
		return nil, fmt.Errorf("kiro: refresh token is empty")
	}
	region := strings.TrimSpace(ssoRegion)
	if region == "" {
		region = oidcRegion
	}
	refreshURL := fmt.Sprintf(kiroDesktopRefreshURLTemplate, region)

	payload := map[string]string{"refreshToken": refreshToken}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("kiro: marshal desktop refresh body: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, refreshURL,
		strings.NewReader(string(body)))
	if err != nil {
		return nil, fmt.Errorf("kiro: create desktop refresh request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", kiroDesktopUserAgent())

	resp, err := k.httpClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("kiro: desktop refresh request: %w", err)
	}
	defer func() {
		if errClose := resp.Body.Close(); errClose != nil {
			_ = errClose
		}
	}()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("kiro: read desktop refresh response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("kiro: desktop refresh status %d: %s", resp.StatusCode, respBody)
	}

	// The Desktop endpoint returns camelCase: accessToken, refreshToken, expiresIn.
	var result TokenResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("kiro: parse desktop refresh response: %w", err)
	}
	if result.AccessToken == "" {
		return nil, fmt.Errorf("kiro: desktop refresh response missing accessToken")
	}
	return &result, nil
}

// kiroDesktopUserAgent returns a User-Agent string in the format expected by the
// Kiro Desktop Auth endpoint: "KiroIDE-0.7.45-{sha1_fingerprint}".
// The fingerprint is a SHA-1 hash of the machine hostname, matching the pattern
// used by kiro-gateway.
func kiroDesktopUserAgent() string {
	hostname, err := os.Hostname()
	if err != nil {
		hostname = "unknown"
	}
	h := sha1.Sum([]byte(hostname + "-cliproxy"))
	return "KiroIDE-0.7.45-" + hex.EncodeToString(h[:])
}

// httpClient returns an HTTP client for OIDC requests.
func (k *KiroAuth) httpClient() *http.Client {
	return &http.Client{Timeout: 30 * time.Second}
}

// StartDeviceFlow is a convenience wrapper that registers a client and starts device authorization.
func (k *KiroAuth) StartDeviceFlow(ctx context.Context) (*ClientRegistrationResponse, *DeviceAuthorizationResponse, error) {
	reg, err := k.RegisterClient(ctx)
	if err != nil {
		return nil, nil, err
	}
	deviceAuth, err := k.StartDeviceAuthorization(ctx, reg)
	if err != nil {
		return nil, nil, err
	}
	return reg, deviceAuth, nil
}

// KiroCLIToken is the token format written by the Kiro CLI to ~/.aws/sso/cache/kiro-auth-token.json.
// The Kiro CLI stores the token in camelCase JSON after a successful `kiro login`.
type KiroCLIToken struct {
	AccessToken  string `json:"accessToken"`
	RefreshToken string `json:"refreshToken,omitempty"`
	ExpiresAt    string `json:"expiresAt,omitempty"`
	// ClientIDHash is the SHA1 hex digest of the OIDC clientId used during registration.
	// It can be used to locate the client registration file in ~/.aws/sso/cache/.
	ClientIDHash string `json:"clientIdHash,omitempty"`
	Region       string `json:"region,omitempty"`
	// ClientID and ClientSecret are the OIDC client credentials from the registration file
	// (~/.aws/sso/cache/<hash>.json). They can be provided by the user when deploying on a
	// server without the Kiro IDE installed, enabling OIDC token refresh without local access
	// to ~/.aws/sso/cache/. The registration file fields are clientId/clientSecret (camelCase).
	ClientID     string `json:"clientId,omitempty"`
	ClientSecret string `json:"clientSecret,omitempty"`
}

// ImportFromKiroCLI reads the access token written by the Kiro CLI after `kiro login`.
// The token is expected at ~/.aws/sso/cache/kiro-auth-token.json (all platforms).
// Returns the token, the resolved file path, and any error.
func (k *KiroAuth) ImportFromKiroCLI() (*KiroCLIToken, string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, "", fmt.Errorf("kiro: get home directory: %w", err)
	}
	tokenPath := filepath.Join(homeDir, ".aws", "sso", "cache", "kiro-auth-token.json")
	data, err := os.ReadFile(tokenPath)
	if err != nil {
		return nil, tokenPath, fmt.Errorf("kiro: read Kiro CLI token at %s: %w (run 'kiro login' to authenticate first)", tokenPath, err)
	}
	token, err := ParseKiroCLIToken(data)
	if err != nil {
		return nil, tokenPath, fmt.Errorf("kiro: parse Kiro CLI token: %w", err)
	}
	return token, tokenPath, nil
}

// KiroCLISQLiteDBPath returns the default path to the kiro-cli SQLite database.
// kiro-cli (v1+) stores its authentication data in an auth_kv table within this file.
func KiroCLISQLiteDBPath() string {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(homeDir, ".local", "share", "kiro-cli", "data.sqlite3")
}

// sqliteTokenKeys defines the auth_kv table keys searched in priority order.
// Social-login tokens are preferred because they carry a long-lived refreshToken.
var sqliteTokenKeys = []string{
	"kirocli:social:token",      // Social login (Google, GitHub, Microsoft)
	"kirocli:odic:token",        // AWS SSO OIDC (kiro-cli corporate)
	"codewhisperer:odic:token",  // Legacy AWS SSO OIDC
}

// sqliteRegistrationKeys defines the auth_kv table keys for OIDC client registration.
// Both "oidc" and "odic" spellings are included to guard against key-name variations
// across kiro-cli releases.
var sqliteRegistrationKeys = []string{
	"kirocli:oidc:device-registration",
	"kirocli:odic:device-registration",
	"codewhisperer:oidc:device-registration",
	"codewhisperer:odic:device-registration",
}

// ImportFromKiroCLISQLite reads the latest token from the kiro-cli SQLite database.
// dbPath may be empty, in which case KiroCLISQLiteDBPath() is used.
// Returns the parsed token, the database file path used, and any error.
func (k *KiroAuth) ImportFromKiroCLISQLite(dbPath string) (*KiroCLIToken, string, error) {
	if dbPath == "" {
		dbPath = KiroCLISQLiteDBPath()
	}
	if dbPath == "" {
		return nil, "", fmt.Errorf("kiro: could not determine SQLite database path")
	}
	if _, err := os.Stat(dbPath); err != nil {
		return nil, dbPath, fmt.Errorf("kiro: SQLite database not found at %s: %w", dbPath, err)
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, dbPath, fmt.Errorf("kiro: open SQLite database: %w", err)
	}
	defer func() {
		if errClose := db.Close(); errClose != nil {
			_ = errClose
		}
	}()

	// Try token keys in priority order.
	var token *KiroCLIToken
	for _, key := range sqliteTokenKeys {
		var raw string
		row := db.QueryRow("SELECT value FROM auth_kv WHERE key = ?", key)
		if errScan := row.Scan(&raw); errScan != nil {
			continue
		}
		parsed, errParse := ParseKiroCLIToken([]byte(raw))
		if errParse != nil || parsed == nil {
			continue
		}
		token = parsed
		break
	}
	if token == nil {
		return nil, dbPath, fmt.Errorf("kiro: no valid token found in SQLite database at %s (run 'kiro login' to authenticate)", dbPath)
	}

	// Try to resolve OIDC client registration if not already embedded in the token.
	if token.ClientID == "" || token.ClientSecret == "" {
		// First pass: exact key matches (fast path).
		for _, key := range sqliteRegistrationKeys {
			var raw string
			row := db.QueryRow("SELECT value FROM auth_kv WHERE key = ?", key)
			if errScan := row.Scan(&raw); errScan != nil {
				continue
			}
			clientID, clientSecret := parseRegistrationValue([]byte(raw))
			if clientID != "" {
				token.ClientID = clientID
				token.ClientSecret = clientSecret
				break
			}
		}
	}

	// Second pass: wildcard scan for any row whose key contains "registration" or "client".
	// This catches key names that differ across kiro-cli versions (e.g. typos, casing, extra
	// segments) without requiring an exhaustive known-keys list.
	if token.ClientID == "" {
		rows, rowErr := db.Query(
			"SELECT key, value FROM auth_kv WHERE key LIKE '%registration%' OR key LIKE '%client%'")
		if rowErr == nil {
			defer func() {
				if errClose := rows.Close(); errClose != nil {
					_ = errClose
				}
			}()
			for rows.Next() {
				var k, v string
				if errScan := rows.Scan(&k, &v); errScan != nil {
					continue
				}
				_ = k
				clientID, clientSecret := parseRegistrationValue([]byte(v))
				if clientID != "" {
					token.ClientID = clientID
					token.ClientSecret = clientSecret
					break
				}
			}
		}

		// If still not found, dump all keys + a truncated snippet of the registration value
		// so the operator can see exactly what is stored without exposing full secrets.
		if token.ClientID == "" {
			var diagParts []string
			diagRows, diagErr := db.Query("SELECT key, value FROM auth_kv ORDER BY key")
			if diagErr == nil {
				for diagRows.Next() {
					var k, v string
					if errScan := diagRows.Scan(&k, &v); errScan == nil {
						if strings.Contains(k, "registration") {
							snippet := v
							if len(snippet) > 120 {
								snippet = snippet[:120] + "..."
							}
							diagParts = append(diagParts, k+"="+snippet)
						} else {
							diagParts = append(diagParts, k)
						}
					}
				}
				_ = diagRows.Close()
			}
			if len(diagParts) > 0 {
				token.ClientIDHash = "diagnostic:keys=" + strings.Join(diagParts, "|")
			}
		}
	}

	return token, dbPath, nil
}

// parseRegistrationValue tries multiple field-name conventions to extract clientId and
// clientSecret from a kiro-cli device registration JSON blob. kiro-cli versions have
// used both camelCase (clientId/clientSecret) and snake_case (client_id/client_secret).
func parseRegistrationValue(data []byte) (clientID, clientSecret string) {
	var camel struct {
		ClientID     string `json:"clientId"`
		ClientSecret string `json:"clientSecret"`
	}
	if err := json.Unmarshal(data, &camel); err == nil && camel.ClientID != "" {
		return camel.ClientID, camel.ClientSecret
	}
	var snake struct {
		ClientID     string `json:"client_id"`
		ClientSecret string `json:"client_secret"`
	}
	if err := json.Unmarshal(data, &snake); err == nil && snake.ClientID != "" {
		return snake.ClientID, snake.ClientSecret
	}
	return "", ""
}

// Accepts both the camelCase format written by the Kiro CLI (accessToken, refreshToken,
// clientId, ...) and the snake_case format that some export paths produce (access_token,
// refresh_token, client_id, ...).  camelCase fields take precedence when both are present.
func ParseKiroCLIToken(data []byte) (*KiroCLIToken, error) {
	var token KiroCLIToken
	if err := json.Unmarshal(data, &token); err != nil {
		return nil, fmt.Errorf("invalid JSON: %w", err)
	}
	// If the primary camelCase fields are empty, try the snake_case aliases used by some
	// export tools (and the server's own stored auth format).
	if strings.TrimSpace(token.AccessToken) == "" {
		var snake struct {
			AccessToken  string `json:"access_token"`
			RefreshToken string `json:"refresh_token"`
			ExpiresAt    string `json:"expires_at"`
			ClientIDHash string `json:"client_id_hash"`
			Region       string `json:"region"`
			ClientID     string `json:"client_id"`
			ClientSecret string `json:"client_secret"`
		}
		if err := json.Unmarshal(data, &snake); err == nil {
			token.AccessToken = snake.AccessToken
			if token.RefreshToken == "" {
				token.RefreshToken = snake.RefreshToken
			}
			if token.ExpiresAt == "" {
				token.ExpiresAt = snake.ExpiresAt
			}
			if token.ClientIDHash == "" {
				token.ClientIDHash = snake.ClientIDHash
			}
			if token.Region == "" {
				token.Region = snake.Region
			}
			if token.ClientID == "" {
				token.ClientID = snake.ClientID
			}
			if token.ClientSecret == "" {
				token.ClientSecret = snake.ClientSecret
			}
		}
	}
	if strings.TrimSpace(token.AccessToken) == "" {
		return nil, fmt.Errorf("no access token found in the provided JSON (run 'kiro login' to authenticate first)")
	}
	return &token, nil
}

// FindAllClientRegistrations scans ~/.aws/sso/cache/ for all non-expired client registration
// JSON files. Registrations whose clientSecretExpiresAt is in the past are excluded. Results
// are returned in descending order of clientSecretExpiresAt (longest-lived first).
func FindAllClientRegistrations() ([]*ClientRegistrationResponse, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("kiro: get home directory: %w", err)
	}
	cacheDir := filepath.Join(homeDir, ".aws", "sso", "cache")
	entries, err := os.ReadDir(cacheDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("kiro: read sso cache dir: %w", err)
	}

	now := time.Now().Unix()
	var results []*ClientRegistrationResponse
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		data, readErr := os.ReadFile(filepath.Join(cacheDir, entry.Name()))
		if readErr != nil {
			continue
		}
		var reg ClientRegistrationResponse
		if jsonErr := json.Unmarshal(data, &reg); jsonErr != nil || reg.ClientID == "" || reg.ClientSecret == "" {
			continue
		}
		// Skip registrations whose client secret has expired.
		if reg.ClientSecretExpiresAt > 0 && reg.ClientSecretExpiresAt < now {
			continue
		}
		results = append(results, &reg)
	}
	// Sort by clientSecretExpiresAt descending so the longest-lived registration is tried first.
	for i := 0; i < len(results); i++ {
		for j := i + 1; j < len(results); j++ {
			if results[j].ClientSecretExpiresAt > results[i].ClientSecretExpiresAt {
				results[i], results[j] = results[j], results[i]
			}
		}
	}
	return results, nil
}

// FindClientRegistration scans ~/.aws/sso/cache/ for a client registration JSON file
// whose clientId SHA1 hex digest matches the provided clientIDHash.
// Returns nil without error if no matching file is found.
func FindClientRegistration(clientIDHash string) (*ClientRegistrationResponse, error) {
	if strings.TrimSpace(clientIDHash) == "" {
		return nil, nil
	}
	wantHash := strings.ToLower(strings.TrimSpace(clientIDHash))

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("kiro: get home directory: %w", err)
	}
	cacheDir := filepath.Join(homeDir, ".aws", "sso", "cache")
	entries, err := os.ReadDir(cacheDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("kiro: read sso cache dir: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		data, readErr := os.ReadFile(filepath.Join(cacheDir, entry.Name()))
		if readErr != nil {
			continue
		}
		var reg ClientRegistrationResponse
		if jsonErr := json.Unmarshal(data, &reg); jsonErr != nil || reg.ClientID == "" {
			continue
		}
		h := sha1.Sum([]byte(reg.ClientID)) //nolint:gosec // SHA1 used as a lookup key, not for security
		if hex.EncodeToString(h[:]) == wantHash {
			return &reg, nil
		}
	}
	return nil, nil
}
