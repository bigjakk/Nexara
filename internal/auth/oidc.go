package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/url"
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
// boolean, the number 0, or the strings "false", "0" and "no" that some providers
// send. A missing claim, or any other value, is not a refusal.
func emailUnverified(claims map[string]interface{}) bool {
	switch v := claims["email_verified"].(type) {
	case bool:
		return !v
	case float64:
		return v == 0
	case string:
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "false", "0", "no":
			return true
		}
		return false
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
//
// Issuer and Subject are the token's iss and sub: who the person is at the provider,
// the pair an SSO account is bound to. The verifier has matched Issuer to the
// configuration's issuer; both are always set on a value ExchangeAndVerify returns.
type OIDCUserInfo struct {
	Email       string
	DisplayName string
	Groups      []string
	Subject     string
	Issuer      string
	// AuthTime is the ID token's auth_time, when the user last authenticated at the
	// provider; zero when the token carries none.
	AuthTime time.Time
	// StepUp is what GenerateStepUpURL stored with the state: not empty only for the
	// callback of a step-up, which must never be taken for a sign-in.
	StepUp string
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
	return NewOIDCProviderWithClient(ctx, cfg, rdb, notifications.SafeHTTPClientAllowingPrivate(15*time.Second, cfg.AllowPrivateAddress))
}

// NewOIDCProviderWithClient is NewOIDCProvider over client instead of the
// address-checked one. Production code calls NewOIDCProvider; this exists for tests
// whose provider is on loopback, which that client always refuses.
func NewOIDCProviderWithClient(ctx context.Context, cfg OIDCConfig, rdb *redis.Client, client *http.Client) (*OIDCProvider, error) {
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
	return p.generateAuthURL(ctx, bindingDigest, "")
}

// GenerateStepUpURL is GenerateAuthURL for a step-up: a fresh authentication at the
// provider by somebody who is signed in already. With fresh it asks the provider to
// authenticate the user again whatever session it holds (prompt=login) and to say when
// it did (max_age=0, which makes auth_time a required claim); without it (the
// configuration's relaxed step-up) it asks for neither. It stores stepUp, which says
// whose step-up it is, with the state, so the callback (ExchangeAndVerify's StepUp)
// knows it is one and never takes it for a sign-in. stepUp must not be empty or hold
// a "|".
func (p *OIDCProvider) GenerateStepUpURL(ctx context.Context, bindingDigest, stepUp string, fresh bool) (authURL, state string, err error) {
	if stepUp == "" || strings.Contains(stepUp, "|") {
		return "", "", fmt.Errorf("generate step-up URL: unusable step-up record")
	}
	if !fresh {
		return p.generateAuthURL(ctx, bindingDigest, stepUp)
	}
	return p.generateAuthURL(ctx, bindingDigest, stepUp,
		oauth2.SetAuthURLParam("prompt", "login"), oauth2.SetAuthURLParam("max_age", "0"))
}

func (p *OIDCProvider) generateAuthURL(ctx context.Context, bindingDigest, stepUp string, extra ...oauth2.AuthCodeOption) (authURL, state string, err error) {
	if err := CheckAuthorizationEndpoint(p.oauth2Cfg.Endpoint.AuthURL); err != nil {
		return "", "", err
	}
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

	// Store state data in Redis for OIDCStateTTL: a sign-in's in three fields, a
	// step-up's with its record as a fourth.
	stateData := nonce + "|" + codeVerifier + "|" + bindingDigest
	if stepUp != "" {
		stateData += "|" + stepUp
	}
	if err := p.rdb.Set(ctx, oidcStateKey(state), stateData, OIDCStateTTL).Err(); err != nil {
		return "", "", fmt.Errorf("store state: %w", err)
	}

	// Generate PKCE S256 challenge
	codeChallenge := generateCodeChallenge(codeVerifier)

	opts := append([]oauth2.AuthCodeOption{
		oidc.Nonce(nonce),
		oauth2.SetAuthURLParam("code_challenge", codeChallenge),
		oauth2.SetAuthURLParam("code_challenge_method", "S256"),
	}, extra...)
	authURL = p.oauth2Cfg.AuthCodeURL(state, opts...)

	return authURL, state, nil
}

// CheckAuthorizationEndpoint refuses the authorization endpoint a discovery document
// names when a sign-in must not send the browser to it (the SPA assigns it to
// window.location): anything but an http(s) URL with a host — javascript:, data: — is
// refused. Every sign-in and step-up checks it, and so does the configuration's test.
func CheckAuthorizationEndpoint(raw string) error {
	if u, err := url.Parse(raw); err != nil || u.Host == "" ||
		!strings.EqualFold(u.Scheme, "https") && !strings.EqualFold(u.Scheme, "http") {
		return fmt.Errorf("%w: the authorization endpoint is not an http or https URL", ErrOIDCProviderInit)
	}
	return nil
}

// ExchangeAndVerify validates state, exchanges the code with PKCE, verifies the ID token,
// and extracts user claims.
func (p *OIDCProvider) ExchangeAndVerify(ctx context.Context, code, state string) (*OIDCUserInfo, error) {
	// Validate state — atomic GetDel ensures single-use
	stateData, err := p.rdb.GetDel(ctx, oidcStateKey(state)).Result()
	if err != nil {
		return nil, fmt.Errorf("%w: state not found or expired", ErrOIDCInvalidState)
	}

	nonce, codeVerifier, _, stepUp, err := splitStateData(stateData)
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

	// The identity an account is bound to: a token without one cannot be told apart
	// from another person's.
	if idToken.Subject == "" || idToken.Issuer == "" {
		return nil, fmt.Errorf("%w: the ID token has no subject or issuer", ErrOIDCClaimExtract)
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
		Issuer:      idToken.Issuer,
		AuthTime:    authTimeClaim(claims),
		StepUp:      stepUp,
	}, nil
}

// authTimeClaim is the ID token's auth_time (a JSON number of Unix seconds), or the
// zero time when it is missing or not a positive number.
func authTimeClaim(claims map[string]interface{}) time.Time {
	v, ok := claims["auth_time"].(float64)
	if !ok || v <= 0 {
		return time.Time{}
	}
	return time.Unix(int64(v), 0)
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
// expired state, or state data written before the binding was stored (a sign-in in
// flight across that upgrade), which the callback refuses. err is Redis failing to
// answer, which is not a sign-in from another browser.
func OIDCStateBindingDigest(ctx context.Context, rdb *redis.Client, state string) (string, error) {
	digest, _, err := OIDCStatePeek(ctx, rdb, state)
	return digest, err
}

// DiscardOIDCState deletes state's data, so that the state can no longer be completed.
func DiscardOIDCState(ctx context.Context, rdb *redis.Client, state string) error {
	if err := rdb.Del(ctx, oidcStateKey(state)).Err(); err != nil {
		return fmt.Errorf("discard the sign-in's state: %w", err)
	}
	return nil
}

// OIDCStatePeek is OIDCStateBindingDigest and, beside it, the step-up record stored
// with the state ("" for a sign-in), without spending the state.
func OIDCStatePeek(ctx context.Context, rdb *redis.Client, state string) (bindingDigest, stepUp string, err error) {
	data, err := rdb.Get(ctx, oidcStateKey(state)).Result()
	if errors.Is(err, redis.Nil) {
		return "", "", nil
	}
	if err != nil {
		return "", "", fmt.Errorf("reading the sign-in's state: %w", err)
	}
	_, _, digest, stepUp, err := splitStateData(data)
	if err != nil {
		return "", "", nil //nolint:nilerr // old state data: no binding, refused as one
	}
	return digest, stepUp, nil
}

// splitStateData reads what generateAuthURL stored: three fields for a sign-in, four
// for a step-up, whose fourth may not be empty.
func splitStateData(data string) (nonce, codeVerifier, bindingDigest, stepUp string, err error) {
	parts := strings.SplitN(data, "|", 4)
	if len(parts) < 3 || len(parts) == 4 && parts[3] == "" {
		return "", "", "", "", fmt.Errorf("malformed state data")
	}
	if len(parts) == 4 {
		stepUp = parts[3]
	}
	return parts[0], parts[1], parts[2], stepUp, nil
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
