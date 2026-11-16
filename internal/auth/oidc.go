package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/redis/go-redis/v9"
	"golang.org/x/oauth2"

	"github.com/bigjakk/nexara/internal/notifications"
)

var (
	ErrOIDCProviderInit     = errors.New("oidc: provider initialization failed")
	ErrOIDCInvalidState     = errors.New("oidc: invalid or expired state")
	ErrOIDCTokenExchange    = errors.New("oidc: token exchange failed")
	ErrOIDCTokenVerify      = errors.New("oidc: token verification failed")
	ErrOIDCClaimExtract     = errors.New("oidc: claim extraction failed")
	ErrOIDCDomainNotAllowed = errors.New("oidc: email domain not allowed")
)

// OIDCEmailNotVerifiedError refuses a sign-in whose ID token says, in
// email_verified, that the provider has not verified the address: whoever
// registered it there could otherwise sign in as the account that holds it.
// Email is the address, for the audit row; Error does not carry it.
type OIDCEmailNotVerifiedError struct {
	Email string
}

func (e *OIDCEmailNotVerifiedError) Error() string {
	return "oidc: the identity provider has not verified the email address"
}

// emailUnverified reports an email_verified claim that says false: the JSON
// boolean, or the string "false" that some providers send. A missing claim, or
// any other value, is not a refusal.
func emailUnverified(claims map[string]interface{}) bool {
	switch v := claims["email_verified"].(type) {
	case bool:
		return !v
	case string:
		return strings.EqualFold(strings.TrimSpace(v), "false")
	default:
		return false
	}
}

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
	// AllowPrivateAddress is the operator's confirmation of an issuer on a private
	// network address, which every contact with the provider then allows.
	AllowPrivateAddress bool
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
	provider  *oidc.Provider
	oauth2Cfg oauth2.Config
	verifier  *oidc.IDTokenVerifier
	rdb       *redis.Client
	cfg       OIDCConfig
	// client is the address-checked client every request to the provider goes through.
	client *http.Client
}

// NewOIDCProvider discovers OIDC metadata and creates a provider.
//
// go-oidc fetches the issuer's discovery document and signing keys, and oauth2
// posts to the token endpoint, through the HTTP client attached to the context
// (or http.DefaultClient if none). Each of them gets the provider's client: the
// notifications package's address-checked one, which checks every address it
// dials — loopback, link-local, unspecified and zoned ones always refused, a
// private one unless cfg.AllowPrivateAddress — against what the name resolves
// to at that moment, and follows no redirect, so none can carry a request to
// another host. Checking the issuer when it is saved is not enough: the name
// can resolve elsewhere by the time a sign-in contacts it.
func NewOIDCProvider(ctx context.Context, cfg OIDCConfig, rdb *redis.Client) (*OIDCProvider, error) {
	client := notifications.SafeHTTPClientAllowingPrivate(15*time.Second, cfg.AllowPrivateAddress)
	safeCtx := oidc.ClientContext(ctx, client)
	provider, err := oidc.NewProvider(safeCtx, cfg.IssuerURL)
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
		client:    client,
	}, nil
}

// OIDCStateTTL is how long a sign-in's state waits in Redis for the callback, and how
// long the browser binding of it lives (handlers' oidcStateBinding).
const OIDCStateTTL = 10 * time.Minute

// GenerateAuthURL generates state+nonce+PKCE, stores them in Redis, and returns the
// auth URL and the state, which the caller binds to the browser it hands the URL to:
// bindingDigest, the digest of the secret that browser's cookie holds, is stored with
// the state, and the callback refuses a state the browser it comes back in cannot show
// the secret of (login CSRF; see OIDCStateBindingDigest).
func (p *OIDCProvider) GenerateAuthURL(ctx context.Context, bindingDigest string) (authURL, state string, err error) {
	state, err = generateRandomString(32)
	if err != nil {
		return "", "", fmt.Errorf("generate state: %w", err)
	}

	nonce, err := generateRandomString(32)
	if err != nil {
		return "", "", fmt.Errorf("generate nonce: %w", err)
	}

	codeVerifier, err := generateRandomString(64)
	if err != nil {
		return "", "", fmt.Errorf("generate PKCE verifier: %w", err)
	}

	// Store state data in Redis for OIDCStateTTL
	stateData := nonce + "|" + codeVerifier + "|" + bindingDigest
	if err := p.rdb.Set(ctx, oidcStateKey(state), stateData, OIDCStateTTL).Err(); err != nil {
		return "", "", fmt.Errorf("store state: %w", err)
	}

	// Generate PKCE S256 challenge
	codeChallenge := generateCodeChallenge(codeVerifier)

	authURL = p.oauth2Cfg.AuthCodeURL(state,
		oidc.Nonce(nonce),
		oauth2.SetAuthURLParam("code_challenge", codeChallenge),
		oauth2.SetAuthURLParam("code_challenge_method", "S256"),
	)

	return authURL, state, nil
}

// ExchangeAndVerify validates state, exchanges the code with PKCE, verifies the ID token,
// and extracts user claims.
func (p *OIDCProvider) ExchangeAndVerify(ctx context.Context, code, state string) (*OIDCUserInfo, error) {
	// Validate state — atomic GetDel ensures single-use
	stateData, err := p.rdb.GetDel(ctx, oidcStateKey(state)).Result()
	if err != nil {
		return nil, fmt.Errorf("%w: state not found or expired", ErrOIDCInvalidState)
	}

	nonce, codeVerifier, _, err := splitStateData(stateData)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrOIDCInvalidState, err)
	}

	// Exchange code with PKCE verifier, through the provider's client: the token
	// endpoint is whatever the discovery document names.
	ctx = oidc.ClientContext(ctx, p.client)
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
	if emailUnverified(claims) {
		return nil, &OIDCEmailNotVerifiedError{Email: email}
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

// oidcStateKey is where a sign-in's state data waits for its callback.
func oidcStateKey(state string) string { return "oidc:state:" + state }

// OIDCStateBindingDigest is the browser binding's record (the digest of its secret, and
// whether its cookie is Secure; opaque here) stored with state by
// GenerateAuthURL, without spending the state; "" when there is none — an unknown or
// expired state, Redis not answering, or state data written before the binding was
// stored (a sign-in in flight across that upgrade), which the callback refuses.
func OIDCStateBindingDigest(ctx context.Context, rdb *redis.Client, state string) string {
	data, err := rdb.Get(ctx, oidcStateKey(state)).Result()
	if err != nil {
		return ""
	}
	_, _, digest, err := splitStateData(data)
	if err != nil {
		return ""
	}
	return digest
}

func splitStateData(data string) (nonce, codeVerifier, bindingDigest string, err error) {
	parts := strings.SplitN(data, "|", 3)
	if len(parts) != 3 {
		return "", "", "", fmt.Errorf("malformed state data")
	}
	return parts[0], parts[1], parts[2], nil
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
