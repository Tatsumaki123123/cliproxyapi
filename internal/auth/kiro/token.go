// Package kiro provides token persistence for Kiro/Amazon Q Builder ID credentials.
package kiro

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/misc"
)

// KiroTokenStorage holds the OIDC token data persisted to disk.
type KiroTokenStorage struct {
	// AccessToken is the Bearer token for API requests.
	AccessToken string `json:"access_token"`
	// RefreshToken is used to obtain new access tokens.
	RefreshToken string `json:"refresh_token,omitempty"`
	// TokenType is the token type, typically "Bearer".
	TokenType string `json:"token_type"`
	// ExpiresAt is an RFC3339 timestamp when the access token expires.
	ExpiresAt string `json:"expires_at,omitempty"`
	// ClientID is the registered OIDC client ID, needed for token refresh.
	ClientID string `json:"client_id,omitempty"`
	// ClientSecret is the registered OIDC client secret, needed for token refresh.
	ClientSecret string `json:"client_secret,omitempty"`
	// Region is the AWS region used for OIDC operations.
	Region string `json:"region,omitempty"`
	// Type identifies the provider; always "kiro" for this storage.
	Type string `json:"type"`

	// Metadata holds arbitrary extra key-value pairs injected via hooks.
	// It is unexported for JSON to allow flattening during serialization.
	Metadata map[string]any `json:"-"`
}

// SetMetadata allows external callers to inject metadata before saving.
func (ts *KiroTokenStorage) SetMetadata(meta map[string]any) {
	ts.Metadata = meta
}

// SaveTokenToFile serializes the token and optional metadata to a JSON file.
func (ts *KiroTokenStorage) SaveTokenToFile(path string) error {
	misc.LogSavingCredentials(path)
	ts.Type = "kiro"

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("kiro: create directory for token file: %w", err)
	}

	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("kiro: create token file: %w", err)
	}
	defer func() {
		if errClose := f.Close(); errClose != nil {
			_ = errClose
		}
	}()

	data, err := misc.MergeMetadata(ts, ts.Metadata)
	if err != nil {
		return fmt.Errorf("kiro: merge metadata: %w", err)
	}

	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	if err = enc.Encode(data); err != nil {
		return fmt.Errorf("kiro: write token file: %w", err)
	}
	return nil
}

// IsExpired returns true if the access token is expired or will expire within 1 minute.
func (ts *KiroTokenStorage) IsExpired() bool {
	if ts.ExpiresAt == "" {
		return false
	}
	t, err := time.Parse(time.RFC3339, ts.ExpiresAt)
	if err != nil {
		return true
	}
	return time.Now().After(t.Add(-time.Minute))
}

// CredentialFileName returns a unique filename for storing Kiro credentials.
func CredentialFileName() string {
	return fmt.Sprintf("kiro-%d.json", time.Now().UnixMilli())
}

// NewKiroTokenStorage constructs a KiroTokenStorage from a TokenResponse and registration.
func NewKiroTokenStorage(token *TokenResponse, reg *ClientRegistrationResponse) *KiroTokenStorage {
	ts := &KiroTokenStorage{
		AccessToken:  token.AccessToken,
		RefreshToken: token.RefreshToken,
		TokenType:    token.TokenType,
		ClientID:     reg.ClientID,
		ClientSecret: reg.ClientSecret,
		Region:       oidcRegion,
		Type:         "kiro",
	}
	if token.ExpiresIn > 0 {
		ts.ExpiresAt = time.Now().
			Add(time.Duration(token.ExpiresIn) * time.Second).
			UTC().
			Format(time.RFC3339)
	}
	return ts
}
