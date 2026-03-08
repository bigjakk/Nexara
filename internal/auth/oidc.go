package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/redis/go-redis/v9"
	"golang.org/x/oauth2"
)

var (
	ErrOIDCProviderInit     = errors.New("oidc: provider initialization failed")
	ErrOIDCInvalidState     = errors.New("oidc: invalid or expired state")
	ErrOIDCTokenExchange    = errors.New("oidc: token exchange failed")
	ErrOIDCTokenVerify      = errors.New("oidc: token verification failed")
	ErrOIDCClaimExtract     = errors.New("oidc: claim extraction failed")
	ErrOIDCDomainNotAllowed = errors.New("oidc: email domain not allowed")
)

// OIDCConfig holds the configuration for connecting to an OIDC provider.
type OIDCConfig struct {
	IssuerURL        string
	ClientID         string
	ClientSecret     string
	RedirectURI      string
	Scopes           []string
	EmailClaim       string
	DisplayNameClaim string
	GroupsClaim      string
	AllowedDomains   []string
}

// OIDCUserInfo represents a user extracted from an OIDC ID token.
type OIDCUserInfo struct {
	Email       string
	DisplayName string
	Groups      []string
	Subject     string
}

// OIDCProvider wraps the go-oidc provider with oauth2 config and Redis for state.
type OIDCProvider struct {
	provider    *oidc.Provider
	oauth2Cfg   oauth2.Config
	verifier    *oidc.IDTokenVerifier
	rdb         *redis.Client
	cfg         OIDCConfig
}

// NewOIDCProvider discovers OIDC metadata and creates a provider.
func NewOIDCProvider(ctx context.Context, cfg OIDCConfig, rdb *redis.Client) (*OIDCProvider, error) {
	provider, err := oidc.NewProvider(ctx, cfg.IssuerURL)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrOIDCProviderInit, err)
	}

	scopes := cfg.Scopes
	if len(scopes) == 0 {
		scopes = []string{oidc.ScopeOpenID, "email", "profile"}
	}

	oauth2Cfg := oauth2.Config{
		ClientID:     cfg.ClientID,
		ClientSecret: cfg.ClientSecret,
		RedirectURL:  cfg.RedirectURI,
		Endpoint:     provider.Endpoint(),
		Scopes:       scopes,
	}

	verifier := provider.Verifier(&oidc.Config{ClientID: cfg.ClientID})

	return &OIDCProvider{
		provider:  provider,
		oauth2Cfg: oauth2Cfg,
		verifier:  verifier,
		rdb:       rdb,
		cfg:       cfg,
	}, nil
}

// GenerateAuthURL generates state+nonce+PKCE, stores in Redis, returns the auth URL.
func (p *OIDCProvider) GenerateAuthURL(ctx context.Context) (string, error) {
	state, err := generateRandomString(32)
	if err != nil {
		return "", fmt.Errorf("generate state: %w", err)
	}

	nonce, err := generateRandomString(32)
	if err != nil {
		return "", fmt.Errorf("generate nonce: %w", err)
	}

	codeVerifier, err := generateRandomString(64)
	if err != nil {
		return "", fmt.Errorf("generate PKCE verifier: %w", err)
	}

	// Store state data in Redis with 10 minute TTL
	stateData := nonce + "|" + codeVerifier
	if err := p.rdb.Set(ctx, "oidc:state:"+state, stateData, 10*time.Minute).Err(); err != nil {
		return "", fmt.Errorf("store state: %w", err)
	}

	// Generate PKCE S256 challenge
	codeChallenge := generateCodeChallenge(codeVerifier)

	url := p.oauth2Cfg.AuthCodeURL(state,
		oidc.Nonce(nonce),
		oauth2.SetAuthURLParam("code_challenge", codeChallenge),
		oauth2.SetAuthURLParam("code_challenge_method", "S256"),
	)

	return url, nil
}

// ExchangeAndVerify validates state, exchanges the code with PKCE, verifies the ID token,
// and extracts user claims.
func (p *OIDCProvider) ExchangeAndVerify(ctx context.Context, code, state string) (*OIDCUserInfo, error) {
	// Validate state — atomic GetDel ensures single-use
	stateData, err := p.rdb.GetDel(ctx, "oidc:state:"+state).Result()
	if err != nil {
		return nil, fmt.Errorf("%w: state not found or expired", ErrOIDCInvalidState)
	}

	nonce, codeVerifier, err := splitStateData(stateData)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrOIDCInvalidState, err)
	}

	// Exchange code with PKCE verifier
	token, err := p.oauth2Cfg.Exchange(ctx, code,
		oauth2.SetAuthURLParam("code_verifier", codeVerifier),
	)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrOIDCTokenExchange, err)
	}

	// Extract and verify ID token
	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok {
		return nil, fmt.Errorf("%w: no id_token in response", ErrOIDCTokenVerify)
	}

	idToken, err := p.verifier.Verify(ctx, rawIDToken)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrOIDCTokenVerify, err)
	}

	// Verify nonce
	if idToken.Nonce != nonce {
		return nil, fmt.Errorf("%w: nonce mismatch", ErrOIDCTokenVerify)
	}

	// Extract claims
	var claims map[string]interface{}
	if err := idToken.Claims(&claims); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrOIDCClaimExtract, err)
	}

	email := extractStringClaim(claims, p.cfg.EmailClaim)
	if email == "" {
		return nil, fmt.Errorf("%w: missing email claim %q", ErrOIDCClaimExtract, p.cfg.EmailClaim)
	}

	// Check allowed domains
	if len(p.cfg.AllowedDomains) > 0 {
		parts := strings.SplitN(email, "@", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("%w: invalid email format", ErrOIDCDomainNotAllowed)
		}
		domain := strings.ToLower(parts[1])
		allowed := false
		for _, d := range p.cfg.AllowedDomains {
			if strings.ToLower(d) == domain {
				allowed = true
				break
			}
		}
		if !allowed {
			return nil, fmt.Errorf("%w: domain %q is not in the allowed list", ErrOIDCDomainNotAllowed, domain)
		}
	}

	displayName := extractStringClaim(claims, p.cfg.DisplayNameClaim)
	if displayName == "" {
		displayName = email
	}

	groups := extractStringSliceClaim(claims, p.cfg.GroupsClaim)

	return &OIDCUserInfo{
		Email:       email,
		DisplayName: displayName,
		Groups:      groups,
		Subject:     idToken.Subject,
	}, nil
}

// GenerateRandomString generates a cryptographically random URL-safe string.
func GenerateRandomString(length int) (string, error) {
	return generateRandomString(length)
}

func generateRandomString(length int) (string, error) {
	b := make([]byte, length)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func generateCodeChallenge(verifier string) string {
	h := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(h[:])
}

func splitStateData(data string) (nonce, codeVerifier string, err error) {
	parts := strings.SplitN(data, "|", 2)
	if len(parts) != 2 {
		return "", "", fmt.Errorf("malformed state data")
	}
	return parts[0], parts[1], nil
}

func extractStringClaim(claims map[string]interface{}, key string) string {
	v, ok := claims[key]
	if !ok {
		return ""
	}
	s, ok := v.(string)
	if !ok {
		return ""
	}
	return s
}

func extractStringSliceClaim(claims map[string]interface{}, key string) []string {
	v, ok := claims[key]
	if !ok {
		return nil
	}
	switch val := v.(type) {
	case []interface{}:
		result := make([]string, 0, len(val))
		for _, item := range val {
			if s, ok := item.(string); ok {
				result = append(result, s)
			}
		}
		return result
	case []string:
		return val
	default:
		return nil
	}
}
