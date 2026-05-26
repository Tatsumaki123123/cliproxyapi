package auth

import (
	"context"
	"fmt"
	"strings"
	"time"

	kiroauth "github.com/router-for-me/CLIProxyAPI/v6/internal/auth/kiro"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	coreauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
)

// kiroRefreshLead is the duration before token expiry when refresh should occur.
var kiroRefreshLead = 5 * time.Minute

// KiroAuthenticator imports Kiro credentials from the Kiro CLI local token cache.
type KiroAuthenticator struct{}

// NewKiroAuthenticator constructs a new Kiro authenticator.
func NewKiroAuthenticator() Authenticator {
	return &KiroAuthenticator{}
}

// Provider returns the provider key for kiro.
func (KiroAuthenticator) Provider() string {
	return "kiro"
}

// RefreshLead returns the duration before token expiry when refresh should occur.
func (KiroAuthenticator) RefreshLead() *time.Duration {
	return &kiroRefreshLead
}

// Login imports the Kiro access token from the Kiro CLI SSO cache file.
// The Kiro CLI writes the token to ~/.aws/sso/cache/kiro-auth-token.json
// after a successful `kiro login`. This method reads that file directly
// instead of running a separate device authorization flow.
func (a KiroAuthenticator) Login(ctx context.Context, cfg *config.Config, opts *LoginOptions) (*coreauth.Auth, error) {
	if cfg == nil {
		return nil, fmt.Errorf("cliproxy auth: configuration is required")
	}

	authSvc := kiroauth.NewKiroAuth(cfg)

	fmt.Println("Importing Kiro token from Kiro CLI cache...")
	token, tokenPath, err := authSvc.ImportFromKiroCLI()
	if err != nil {
		return nil, fmt.Errorf("kiro: %w\n\nTo authenticate, run 'kiro login' in your terminal first, then retry", err)
	}

	fmt.Printf("Kiro CLI token found at %s\n", tokenPath)

	ts := &kiroauth.KiroTokenStorage{
		AccessToken:  token.AccessToken,
		RefreshToken: token.RefreshToken,
		TokenType:    "Bearer",
		Type:         "kiro",
	}
	if token.ExpiresAt != "" {
		ts.ExpiresAt = token.ExpiresAt
	}

	metadata := map[string]any{
		"type":         "kiro",
		"access_token": token.AccessToken,
		"token_type":   "Bearer",
		"timestamp":    time.Now().UnixMilli(),
	}
	if strings.TrimSpace(token.RefreshToken) != "" {
		metadata["refresh_token"] = token.RefreshToken
	}
	if token.ExpiresAt != "" {
		metadata["expires_at"] = token.ExpiresAt
	}
	if token.ClientIDHash != "" {
		metadata["client_id_hash"] = token.ClientIDHash
	}
	// Store the SSO region from the token for region-specific OIDC refresh.
	if ssoRegion := strings.TrimSpace(token.Region); ssoRegion != "" {
		metadata["sso_region"] = ssoRegion
		ts.Region = ssoRegion
	}

	// Attempt to locate the OIDC client registration in ~/.aws/sso/cache/ so that
	// the token can be refreshed automatically without re-running 'kiro login'.
	if token.ClientIDHash != "" {
		if reg, regErr := kiroauth.FindClientRegistration(token.ClientIDHash); reg != nil && regErr == nil {
			metadata["client_id"] = reg.ClientID
			metadata["client_secret"] = reg.ClientSecret
			ts.ClientID = reg.ClientID
			ts.ClientSecret = reg.ClientSecret
			fmt.Println("Kiro client registration found; automatic token refresh enabled.")
		}
	}
	// auth_type: "aws_sso_oidc" when OIDC client credentials are available;
	// "kiro_desktop" for social-login tokens (no client_id/secret needed for refresh).
	if ts.ClientID != "" {
		metadata["auth_type"] = "aws_sso_oidc"
	} else {
		metadata["auth_type"] = "kiro_desktop"
	}

	fileName := fmt.Sprintf("kiro-%d.json", time.Now().UnixMilli())

	fmt.Println("\nKiro authentication successful!")

	return &coreauth.Auth{
		ID:       fileName,
		Provider: a.Provider(),
		FileName: fileName,
		Label:    "Kiro User",
		Storage:  ts,
		Metadata: metadata,
	}, nil
}
