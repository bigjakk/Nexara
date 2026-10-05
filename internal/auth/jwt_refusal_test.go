package auth

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

const refusalTestSecret = "refusal-test-secret"

// signedClaims signs claims the way the generators do, with whichever method and
// key a case needs.
func signedClaims(t *testing.T, method jwt.SigningMethod, key any, claims Claims) string {
	t.Helper()
	signed, err := jwt.NewWithClaims(method, claims).SignedString(key)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return signed
}

// claimsExpiring is the claims of a regular access token for user, expiring at exp.
func claimsExpiring(user uuid.UUID, exp time.Time) Claims {
	return Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   user.String(),
			ExpiresAt: jwt.NewNumericDate(exp),
			IssuedAt:  jwt.NewNumericDate(exp.Add(-15 * time.Minute)),
			Issuer:    "nexara",
		},
		UserID: user,
		Email:  "alice@example.com",
		Role:   "admin",
	}
}

// TestAccessTokenIsSomeoneElses pins what AccessTokenIsSomeoneElses will say and,
// as much, what it will not.
//
// It exists for one refusal — a sign-out whose access token authOptional named no
// one from must still be held to "this session is yours" — so it answers true for
// exactly one kind of token: a regular access token this service signed, issued to
// someone other than the user asked about. Time is not part of it: the token may be
// live, expired or not yet valid, because the refusal does not care why authOptional
// left it unnamed. Every row that answers false is a way of getting a refusal out of
// a string the server never vouched for, or out of a token that is the asker's own.
//
// Each token is asked twice, about a stranger and about the user it was issued to.
// The second answer is false for every row, which is the property the sign-out leans
// on to end the owner's own session; the first is the row's. The first row is the
// control for the false ones: on the same service, a sound token is answered true
// for a stranger, so a false answer is the helper refusing to read the token and not
// a helper that answers nothing at all. The rows that are tampered with start from
// that same token.
func TestAccessTokenIsSomeoneElses(t *testing.T) {
	user := uuid.New()
	other := uuid.New()
	stranger := uuid.New()
	secret := []byte(refusalTestSecret)
	ago := func(d time.Duration) time.Time { return time.Now().Add(-d) }

	svc := NewJWTService(refusalTestSecret, 15*time.Minute, 7*24*time.Hour)
	lapsedSvc := NewJWTService(refusalTestSecret, -time.Hour, 7*24*time.Hour)
	foreignSvc := NewJWTService("some-other-secret", -time.Hour, 7*24*time.Hour)

	expired := func(t *testing.T) string {
		t.Helper()
		tok, _, err := lapsedSvc.GenerateAccessToken(user, "alice@example.com", "admin")
		if err != nil {
			t.Fatalf("GenerateAccessToken: %v", err)
		}
		return tok
	}
	// altered keeps the header and the signature of an expired token for user and
	// swaps the claims for ones naming other: the forgery that matters, a signed
	// token re-aimed at someone else.
	altered := func(t *testing.T) string {
		t.Helper()
		parts := strings.Split(expired(t), ".")
		raw, err := base64.RawURLEncoding.DecodeString(parts[1])
		if err != nil {
			t.Fatalf("decode claims: %v", err)
		}
		var claims map[string]any
		if err := json.Unmarshal(raw, &claims); err != nil {
			t.Fatalf("unmarshal claims: %v", err)
		}
		claims["uid"] = other.String()
		raw, err = json.Marshal(claims)
		if err != nil {
			t.Fatalf("marshal claims: %v", err)
		}
		parts[1] = base64.RawURLEncoding.EncodeToString(raw)
		return strings.Join(parts, ".")
	}
	scope := ConsoleScope{ClusterID: uuid.NewString(), Node: "pve-01", Type: "node_shell"}

	tests := []struct {
		name  string
		token func(t *testing.T) string
		// want is the answer about a stranger.
		want bool
		// live is whether ValidateAccessToken accepts the token, which is whether
		// authOptional would have named its user.
		live bool
		// namesNoOne marks a token with no uid, for which "the user it was issued to"
		// is the zero uuid.
		namesNoOne bool
	}{
		{name: "an expired access token", token: expired, want: true},
		{
			name: "an expired access token signed with another HMAC method, which ValidateAccessToken takes too",
			token: func(t *testing.T) string {
				return signedClaims(t, jwt.SigningMethodHS512, secret, claimsExpiring(user, ago(time.Hour)))
			},
			want: true,
		},
		{
			name: "an access token that has not expired, which authOptional would have named",
			token: func(t *testing.T) string {
				tok, _, err := svc.GenerateAccessToken(user, "alice@example.com", "admin")
				if err != nil {
					t.Fatalf("GenerateAccessToken: %v", err)
				}
				return tok
			},
			want: true, live: true,
		},
		{
			name: "an access token that is not valid yet",
			token: func(t *testing.T) string {
				claims := claimsExpiring(user, time.Now().Add(time.Hour))
				claims.NotBefore = jwt.NewNumericDate(time.Now().Add(10 * time.Minute))
				return signedClaims(t, jwt.SigningMethodHS256, secret, claims)
			},
			want: true,
		},
		{
			name: "an access token that is expired and not valid yet",
			token: func(t *testing.T) string {
				claims := claimsExpiring(user, ago(time.Hour))
				claims.NotBefore = jwt.NewNumericDate(time.Now().Add(time.Hour))
				return signedClaims(t, jwt.SigningMethodHS256, secret, claims)
			},
			want: true,
		},
		{
			name: "an access token with no expiry at all",
			token: func(t *testing.T) string {
				claims := claimsExpiring(user, time.Now())
				claims.ExpiresAt = nil
				return signedClaims(t, jwt.SigningMethodHS256, secret, claims)
			},
			want: true, live: true,
		},
		{
			name: "an expired access token after a stray space, the second space of a header",
			token: func(t *testing.T) string {
				return " " + expired(t)
			},
			want: true,
		},
		{
			name: "a live access token with white space on both sides",
			token: func(t *testing.T) string {
				tok, _, err := svc.GenerateAccessToken(user, "alice@example.com", "admin")
				if err != nil {
					t.Fatalf("GenerateAccessToken: %v", err)
				}
				return " \t" + tok + " "
			},
			want: true, live: false,
		},
		{
			// A token this server signed that names no one cannot be the owner's, and
			// "names no one" must not be a way past the check: it refuses, as a
			// valid token with no uid does when authOptional names it.
			name: "an access token that names no user",
			token: func(t *testing.T) string {
				claims := claimsExpiring(user, ago(time.Hour))
				claims.UserID = uuid.Nil
				return signedClaims(t, jwt.SigningMethodHS256, secret, claims)
			},
			want: true, namesNoOne: true,
		},

		{
			name: "an expired token signed with another secret",
			token: func(t *testing.T) string {
				tok, _, err := foreignSvc.GenerateAccessToken(user, "alice@example.com", "admin")
				if err != nil {
					t.Fatalf("GenerateAccessToken: %v", err)
				}
				return tok
			},
		},
		{name: "an expired token whose claims were swapped for another user's", token: altered},
		{
			name: "an expired token with no signature (alg none)",
			token: func(t *testing.T) string {
				return signedClaims(t, jwt.SigningMethodNone, jwt.UnsafeAllowNoneSignatureType, claimsExpiring(user, ago(time.Hour)))
			},
		},
		{
			name: "an expired token that claims an asymmetric algorithm",
			token: func(t *testing.T) string {
				parts := strings.Split(expired(t), ".")
				parts[0] = base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`))
				return strings.Join(parts, ".")
			},
		},

		{
			name: "an expired console token",
			token: func(t *testing.T) string {
				tok, _, err := lapsedSvc.GenerateConsoleToken(user, "alice@example.com", "admin", scope, -time.Hour)
				if err != nil {
					t.Fatalf("GenerateConsoleToken: %v", err)
				}
				return tok
			},
		},
		{
			name: "an expired WebSocket hub token",
			token: func(t *testing.T) string {
				tok, _, err := lapsedSvc.GenerateWSHubToken(user, "alice@example.com", "admin", -time.Hour)
				if err != nil {
					t.Fatalf("GenerateWSHubToken: %v", err)
				}
				return tok
			},
		},
		{
			name: "a console token that has not expired",
			token: func(t *testing.T) string {
				tok, _, err := svc.GenerateConsoleToken(user, "alice@example.com", "admin", scope, time.Minute)
				if err != nil {
					t.Fatalf("GenerateConsoleToken: %v", err)
				}
				return tok
			},
		},
		// Claims of no kind this code knows (Claims.Kind is TokenKindUnknown) name no one,
		// as the scoped kinds do: the refusal asks whether a token is a SESSION, so what
		// is not one — whatever the combination of markers — is turned away from it too.
		// A check that refused only the two kinds it recognised would read these as a
		// stranger's.
		{
			name: "an expired token carrying both scope markers at once",
			token: func(t *testing.T) string {
				claims := claimsExpiring(user, ago(time.Hour))
				claims.ConsoleScope = &scope
				claims.WSScope = WSScopeHub
				return signedClaims(t, jwt.SigningMethodHS256, secret, claims)
			},
		},
		{
			name: "an expired token with a WebSocket scope this code does not issue",
			token: func(t *testing.T) string {
				claims := claimsExpiring(user, ago(time.Hour))
				claims.WSScope = "elsewhere"
				return signedClaims(t, jwt.SigningMethodHS256, secret, claims)
			},
		},

		{name: "an empty string", token: func(*testing.T) string { return "" }},
		{name: "text that is not a token", token: func(*testing.T) string { return "not-a-token" }},
		{name: "two segments", token: func(*testing.T) string { return "eyJhbGciOiJIUzI1NiJ9.e30" }},
		{name: "four segments", token: func(t *testing.T) string { return expired(t) + ".extra" }},
		{
			name: "an API key, which is not a JWT whatever follows its prefix",
			token: func(t *testing.T) string {
				return "nxra_" + expired(t)
			},
		},
		{name: "an expired token with a character of its signature changed", token: func(t *testing.T) string {
			// Not the last character: the last of a 43-character signature carries two
			// bits that decode to nothing, and changing only those leaves the token
			// intact.
			tok := expired(t)
			at := len(tok) - 10
			swap := byte('A')
			if tok[at] == 'A' {
				swap = 'B'
			}
			return tok[:at] + string(swap) + tok[at+1:]
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			token := tt.token(t)

			if got := svc.AccessTokenIsSomeoneElses(token, stranger); got != tt.want {
				t.Errorf("AccessTokenIsSomeoneElses(token, a stranger) = %t, want %t", got, tt.want)
			}
			// The one the token was issued to is never "someone else", whatever the
			// token: that is what lets a sign-out by its owner through.
			issuedTo := user
			if tt.namesNoOne {
				issuedTo = uuid.Nil
			}
			if svc.AccessTokenIsSomeoneElses(token, issuedTo) {
				t.Error("AccessTokenIsSomeoneElses refuses a token for the user it was issued to")
			}
			// Which of the tokens it reads are ones authentication would accept: the
			// rows that say so are the ones authOptional names without the helper.
			if tt.want {
				if _, err := svc.ValidateAccessToken(token); (err == nil) != tt.live {
					t.Errorf("ValidateAccessToken accepts the token = %t, want %t", err == nil, tt.live)
				}
			}
		})
	}
}

// TestBearerToken pins the one reading of an Authorization header that
// authRequired, authOptional (through internal/api's extractBearerToken) and Logout's
// ownership check all share: the scheme without regard to case, the token everything
// after the first space.
//
// The rows that matter beyond the plain ones are the two that a "friendlier" parser
// would change. A second space is part of the token, so the token is one no
// signature verifies — which is how it has been read, and widening it would widen
// what authentication accepts (the refusal helper reads past it by itself, see
// TestAccessTokenIsSomeoneElses). And a bearer with nothing after it is no token.
func TestBearerToken(t *testing.T) {
	tests := []struct {
		header string
		want   string
	}{
		{header: "Bearer abc", want: "abc"},
		{header: "bearer abc", want: "abc"},
		{header: "BEARER abc", want: "abc"},
		{header: "Bearer eyJhbGciOi", want: "eyJhbGciOi"},
		{header: "Bearer  abc", want: " abc"},
		{header: "Bearer a b", want: "a b"},
		{header: "Bearer ", want: ""},
		{header: "Bearer", want: ""},
		{header: "Basic abc123", want: ""},
		{header: "Bearerabc", want: ""},
		{header: "eyJhbGciOi", want: ""},
		{header: "", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.header, func(t *testing.T) {
			if got := BearerToken(tt.header); got != tt.want {
				t.Errorf("BearerToken(%q) = %q, want %q", tt.header, got, tt.want)
			}
		})
	}
}
