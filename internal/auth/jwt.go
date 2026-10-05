package auth

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

var (
	ErrInvalidToken = errors.New("invalid or expired token")
	ErrInvalidClaim = errors.New("invalid token claims")
)

// Claims represents the JWT claims for access tokens.
//
// Three token kinds share this struct, distinguished by which optional
// scope marker is set (zero or one — never both):
//
//   - Regular access token: ConsoleScope == nil && WSScope == "". Used in
//     Authorization: Bearer headers for API calls. NEVER accepted on any
//     WebSocket upgrade path.
//   - Console-scoped token: ConsoleScope != nil. Issued by POST
//     /api/v1/auth/console-token. Only valid on /ws/console or /ws/vnc and
//     only for the exact cluster/node/vmid/type tuple in the scope.
//   - WebSocket hub token: WSScope == "hub". Issued by POST
//     /api/v1/auth/ws-token. Only valid on the generic /ws hub upgrade.
//
// Splitting access vs WS-upgrade auth lets us carry the WS token in the
// URL or `Sec-WebSocket-Protocol` header without exposing the long-lived
// access token to proxy access logs, browser history, or referrer leakage.
type Claims struct {
	jwt.RegisteredClaims
	UserID       uuid.UUID     `json:"uid"`
	Email        string        `json:"email"`
	Role         string        `json:"role"`
	ConsoleScope *ConsoleScope `json:"console_scope,omitempty"`
	// WSScope marks a token whose only valid use is upgrading a WebSocket
	// connection. Currently the only value is "hub" (the generic /ws hub).
	WSScope string `json:"ws_scope,omitempty"`
}

// ConsoleScope restricts a JWT to a single console WebSocket upgrade.
// A token carrying a ConsoleScope is issued by POST /api/v1/auth/console-token
// with a short TTL (≤ 60 seconds) and can ONLY be used to open a console
// matching the exact cluster/node/vmid/type combination. Any other use is
// rejected.
type ConsoleScope struct {
	ClusterID string `json:"cluster_id"`
	Node      string `json:"node"`
	VMID      int    `json:"vmid,omitempty"`
	Type      string `json:"type"` // node_shell | vm_serial | ct_attach | vm_vnc | ct_vnc
}

// WSScopeHub is the only value currently accepted in Claims.WSScope.
const WSScopeHub = "hub"

// TokenPair holds an access token and a refresh token.
//
// RefreshToken is json:"-" on purpose, and that is the whole point of this
// comment. Returning a refresh token in a response body was the v1.9.x leak.
// It is closed in two places, each on the type that carries the hazard:
// authResponse.MarshalJSON in internal/api/handlers/auth.go blanks the field
// on every marshal of the login/register/refresh body (that type must keep a
// live `json:"refresh_token"` tag — the empty string is a published part of
// its shape), and json:"-" here does the same job for this type. The token
// itself travels only in an HttpOnly, SameSite=Strict cookie scoped to
// handlers.RefreshCookiePath.
//
// authResponse cannot protect this type. With a live `json:"refresh_token"`
// tag here, the next thing that marshals a TokenPair — a new endpoint, a debug
// handler, an error path, a %+v on a JSON-ish wrapper — reopens the leak with
// nothing failing. The tag is where the hazard lives, so the tag is where it
// is closed.
//
// Nothing in the tree marshals or unmarshals this type today, so json:"-"
// costs nothing; TestTokenPair_RefreshTokenIsNeverMarshalled pins it. The
// field itself stays — json:"-" suppresses serialisation, not the field — so
// a future in-memory holder of a pair keeps it; nothing reads it today. Its
// existence needs no test of its own: deleting it fails to compile the
// package's tests — the struct literal in
// TestTokenPair_RefreshTokenIsNeverMarshalled is its one reference.
type TokenPair struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"-"`
	ExpiresAt    int64  `json:"expires_at"`
}

// JWTService handles JWT token generation and validation.
type JWTService struct {
	secret          []byte
	accessTokenTTL  time.Duration
	refreshTokenTTL time.Duration
}

// NewJWTService creates a new JWT service.
func NewJWTService(secret string, accessTTL, refreshTTL time.Duration) *JWTService {
	return &JWTService{
		secret:          []byte(secret),
		accessTokenTTL:  accessTTL,
		refreshTokenTTL: refreshTTL,
	}
}

// GenerateAccessToken creates a signed JWT access token.
func (j *JWTService) GenerateAccessToken(userID uuid.UUID, email, role string) (string, time.Time, error) {
	now := time.Now()
	expiresAt := now.Add(j.accessTokenTTL)
	claims := Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			ID:        uuid.New().String(),
			Subject:   userID.String(),
			ExpiresAt: jwt.NewNumericDate(expiresAt),
			IssuedAt:  jwt.NewNumericDate(now),
			Issuer:    "nexara",
		},
		UserID: userID,
		Email:  email,
		Role:   role,
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString(j.secret)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("signing access token: %w", err)
	}
	return signed, expiresAt, nil
}

// GenerateConsoleToken creates a short-lived JWT whose only valid use is opening
// the specific console described by scope. ttl should be small (≤ 60 seconds).
func (j *JWTService) GenerateConsoleToken(userID uuid.UUID, email, role string, scope ConsoleScope, ttl time.Duration) (string, time.Time, error) {
	now := time.Now()
	expiresAt := now.Add(ttl)
	claims := Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			ID:        uuid.New().String(),
			Subject:   userID.String(),
			ExpiresAt: jwt.NewNumericDate(expiresAt),
			IssuedAt:  jwt.NewNumericDate(now),
			Issuer:    "nexara",
		},
		UserID:       userID,
		Email:        email,
		Role:         role,
		ConsoleScope: &scope,
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString(j.secret)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("signing console token: %w", err)
	}
	return signed, expiresAt, nil
}

// GenerateWSHubToken creates a short-lived JWT (ttl should be ≤ 60 seconds)
// whose only valid use is upgrading the generic /ws hub. Per-channel
// authorization happens later in the hub at subscribe time using UserID.
func (j *JWTService) GenerateWSHubToken(userID uuid.UUID, email, role string, ttl time.Duration) (string, time.Time, error) {
	now := time.Now()
	expiresAt := now.Add(ttl)
	claims := Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			ID:        uuid.New().String(),
			Subject:   userID.String(),
			ExpiresAt: jwt.NewNumericDate(expiresAt),
			IssuedAt:  jwt.NewNumericDate(now),
			Issuer:    "nexara",
		},
		UserID:  userID,
		Email:   email,
		Role:    role,
		WSScope: WSScopeHub,
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString(j.secret)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("signing ws hub token: %w", err)
	}
	return signed, expiresAt, nil
}

// GenerateRefreshToken creates a cryptographically random refresh token.
func (j *JWTService) GenerateRefreshToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generating refresh token: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// RefreshTokenTTL returns the configured refresh token TTL.
func (j *JWTService) RefreshTokenTTL() time.Duration {
	return j.refreshTokenTTL
}

// keyFunc is the key lookup of every parse of an access token: the signing
// method must be HMAC, and the key is the service's one secret. ValidateAccessToken
// and AccessTokenIsSomeoneElses both parse with it, so what counts as "signed by
// us" is decided in this one place.
func (j *JWTService) keyFunc(t *jwt.Token) (any, error) {
	if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
		return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
	}
	return j.secret, nil
}

// ValidateAccessToken parses and validates a JWT access token.
func (j *JWTService) ValidateAccessToken(tokenString string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(tokenString, &Claims{}, j.keyFunc)
	if err != nil {
		return nil, ErrInvalidToken
	}

	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return nil, ErrInvalidClaim
	}
	return claims, nil
}

// BearerToken returns the token in the value of an Authorization header of the
// form "Bearer <token>", and "" for anything else: no header, another scheme, no
// token. The scheme is matched without regard to case, and the token is everything
// after the first space, spaces included — so a second space gives a token no
// signature will verify, which is how a header with two spaces has always been
// read. (RFC 9110 allows more than one space; reading them would widen what
// authRequired accepts, which is a decision about authentication and not one for
// this function to make quietly. AccessTokenIsSomeoneElses, which can only refuse,
// does read past them.)
//
// It is the one place a request's access token is read out of its header. The
// middleware (internal/api's extractBearerToken) authenticates with it and Logout's
// ownership check reads the token through it, so the check sees what the middleware
// saw: two readings of one header would let one of them decide on a request the
// other never looked at.
func BearerToken(header string) string {
	if header == "" {
		return ""
	}
	parts := strings.SplitN(header, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return ""
	}
	return parts[1]
}

// AccessTokenIsSomeoneElses reports whether tokenString is a correctly signed
// access token that was issued to a user other than user. It answers false for
// everything else: a token that names user, a bad signature or signing method, a
// malformed string, and a token that is not an interactive session's — a console or
// WebSocket token (the scoped kinds, which authOptional does not treat as an
// identity either) or claims of no kind this code knows (Claims.Kind), which name
// no one for the same reason.
//
// IT IS FOR REFUSING AND NOTHING ELSE, and it cannot be used to authenticate: what
// it returns is a bool and never the user. A true answer turns a request away; a
// false one grants nothing, because false is also what an unreadable token answers.
// Its one caller is Logout's ownership check (AuthHandler.signOutIsSomeoneElses),
// which uses it on an access token that authOptional named no one from, to refuse
// ending a session that belongs to someone else; the session is still ended on the
// strength of the refresh cookie, which is the credential there.
// TestGuard_AccessTokenIsSomeoneElsesIsOnlyReachedFromTheSignOutCheck keeps it the
// only caller and TestGuard_AccessTokenIsSomeoneElsesReturnsOnlyABool keeps the
// answer a bool. Whatever wants to know who is signed in goes through
// ValidateAccessToken.
//
// Time is deliberately not looked at. A refusal does not care whether the token has
// expired, is not valid yet, or was turned away by authOptional for some other
// reason: its signature says whom it was issued to, and something that can only
// refuse is made better at it, not worse, by reading more tokens. So the parse
// validates no claim — one that returns no error has verified the signature and the
// signing method, through the same keyFunc as ValidateAccessToken, and nothing else.
// For the same reason stray white space around the token (the second space of a
// header BearerToken reads exactly, which authentication turns away) is read past: a
// signed token under a spelling the middleware did not accept still says whom it is
// for.
func (j *JWTService) AccessTokenIsSomeoneElses(tokenString string, user uuid.UUID) bool {
	tokenString = strings.TrimSpace(tokenString)
	claims := &Claims{}
	if _, err := jwt.NewParser(jwt.WithoutClaimsValidation()).ParseWithClaims(tokenString, claims, j.keyFunc); err != nil {
		return false
	}
	// Asked of the one predicate authRequired and authOptional ask (claims_kind.go),
	// and not re-derived from the fields: TestGuard_OnlyClaimsKindAndTheWebSocketUpgradeReadTheScopeMarkers
	// keeps it so.
	if !claims.IsSession() {
		return false
	}
	return claims.UserID != user
}
