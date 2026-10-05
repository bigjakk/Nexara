package handlers

import (
	"bytes"
	"cmp"
	"context"
	"encoding/base64"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	recoverer "github.com/gofiber/fiber/v3/middleware/recover"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"github.com/bigjakk/nexara/internal/auth"
)

// These tests are about whose sign-out a sign-out is.
//
// /auth/logout is authOptional: the refresh cookie is the credential, so a browser
// whose access token has lapsed can still sign out. The browser's cookie jar is
// shared by all of its tabs, though, and another tab may have signed someone else in
// since this tab's token was issued — so the cookie can be another user's, and the
// access token the request carries is the only thing that says so. A valid one
// always did (authOptional turns it into user_id). One that authOptional turned
// away — expired, not yet valid — did not: it was ignored, and the cookie alone
// ended whatever session it named. And the cookie was deleted before anything was
// checked, so even the refused sign-out took the other user's cookie out of the jar.
//
// The harness stands in for authOptional with the X-Test-Acting-User header, as it
// does for every route: it sets user_id, the one thing Logout reads from the real
// middleware, for a token that validates. A token that does not validate is simply
// carried in the Authorization header, where the handler finds it — that is the
// case under test. TestLogoutThroughTheRealAuthOptional in internal/api puts the
// real middleware in front of the real handler.
//
// What Logout asks of the request is signOutIsSomeoneElses: a bool, never an
// identity, and the guards at the bottom of this file are what keep it that way.

// logoutJWTSecret is the secret newAuthRaceApp's JWT service signs with. The tokens
// below are signed with it so that the handler's service accepts their signatures;
// TestLogoutTokensAreSignedWithTheHarnessSecret says so if it ever stops being true,
// because every "forged" row below would then be vacuously right.
const logoutJWTSecret = "test-secret"

// logoutAccessToken is a regular access token for user, signed with secret, that
// expires ttl from now (a negative ttl is a token that has already lapsed).
func logoutAccessToken(t *testing.T, secret string, ttl time.Duration, user uuid.UUID) string {
	t.Helper()
	tok, _, err := auth.NewJWTService(secret, ttl, time.Hour).GenerateAccessToken(user, "alice@example.com", "admin")
	if err != nil {
		t.Fatalf("GenerateAccessToken: %v", err)
	}
	return tok
}

// logoutConsoleToken and logoutWSToken are the two scoped kinds, which authOptional
// does not treat as an identity.
func logoutConsoleToken(t *testing.T, ttl time.Duration, user uuid.UUID) string {
	t.Helper()
	scope := auth.ConsoleScope{ClusterID: uuid.NewString(), Node: "pve-01", Type: "node_shell"}
	tok, _, err := auth.NewJWTService(logoutJWTSecret, time.Hour, time.Hour).
		GenerateConsoleToken(user, "alice@example.com", "admin", scope, ttl)
	if err != nil {
		t.Fatalf("GenerateConsoleToken: %v", err)
	}
	return tok
}

func logoutWSToken(t *testing.T, ttl time.Duration, user uuid.UUID) string {
	t.Helper()
	tok, _, err := auth.NewJWTService(logoutJWTSecret, time.Hour, time.Hour).
		GenerateWSHubToken(user, "alice@example.com", "admin", ttl)
	if err != nil {
		t.Fatalf("GenerateWSHubToken: %v", err)
	}
	return tok
}

// logoutUnsignedToken is an expired token for user with no signature at all.
func logoutUnsignedToken(t *testing.T, user uuid.UUID) string {
	t.Helper()
	claims := auth.Claims{
		RegisteredClaims: jwt.RegisteredClaims{ExpiresAt: jwt.NewNumericDate(time.Now().Add(-time.Hour))},
		UserID:           user,
	}
	tok, err := jwt.NewWithClaims(jwt.SigningMethodNone, claims).SignedString(jwt.UnsafeAllowNoneSignatureType)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return tok
}

// logoutAlteredToken is an expired token for from whose claims were rewritten to name
// to after it was signed: the header and the signature are from's, the payload is not.
func logoutAlteredToken(t *testing.T, from, to uuid.UUID) string {
	t.Helper()
	parts := strings.Split(logoutAccessToken(t, logoutJWTSecret, -time.Hour, from), ".")
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("decode claims: %v", err)
	}
	var claims map[string]any
	if err := json.Unmarshal(raw, &claims); err != nil {
		t.Fatalf("unmarshal claims: %v", err)
	}
	claims["uid"] = to.String()
	if raw, err = json.Marshal(claims); err != nil {
		t.Fatalf("marshal claims: %v", err)
	}
	parts[1] = base64.RawURLEncoding.EncodeToString(raw)
	return strings.Join(parts, ".")
}

// logoutTimedToken is an access token for user, signed with the harness secret,
// whose time claims are exactly what the caller sets: the way to a token that has
// not begun, or one that never ends, which the generators do not issue.
func logoutTimedToken(t *testing.T, user uuid.UUID, notBefore, expires *time.Time) string {
	t.Helper()
	claims := auth.Claims{
		RegisteredClaims: jwt.RegisteredClaims{Subject: user.String(), Issuer: "nexara"},
		UserID:           user,
		Email:            "alice@example.com",
		Role:             "admin",
	}
	if notBefore != nil {
		claims.NotBefore = jwt.NewNumericDate(*notBefore)
	}
	if expires != nil {
		claims.ExpiresAt = jwt.NewNumericDate(*expires)
	}
	tok, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(logoutJWTSecret))
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return tok
}

// timePtr is tm's address, for the optional time claims of logoutTimedToken.
func timePtr(tm time.Time) *time.Time { return &tm }

// TestLogoutTokensAreSignedWithTheHarnessSecret is the control for every row below
// that expects a token to be ignored: a token signed with logoutJWTSecret is one
// the harness's own service validates, so the rows whose token is meant to be
// refused for another reason (expired, forged, scoped) are refused for that one.
func TestLogoutTokensAreSignedWithTheHarnessSecret(t *testing.T) {
	a := newAuthRaceApp(t, nil)
	user := uuid.New()

	claims, err := a.jwt.ValidateAccessToken(logoutAccessToken(t, logoutJWTSecret, time.Hour, user))
	if err != nil || claims.UserID != user {
		t.Fatalf("the harness's JWT service does not accept a token signed with logoutJWTSecret (%v): "+
			"every expired-token row in this file would be reading a forgery", err)
	}
	if _, err := a.jwt.ValidateAccessToken(logoutAccessToken(t, logoutJWTSecret+"-not", time.Hour, user)); err == nil {
		t.Fatal("the harness's JWT service accepts a token signed with another secret")
	}
}

// TestLogout_AnAccessTokenNamesWhoIsSigningOut holds Logout to the rule above, one
// way of arriving at a time.
//
// Every row signs out the session of the account the harness holds (the owner), by
// its refresh cookie unless the row says otherwise. What varies is what the request
// says about WHO is signing out. The rows come in four groups:
//
//   - a caller that is named — a valid token (the stand-in header), or one the
//     handler reads for itself because authOptional turned it away: expired, not
//     yet valid, with no expiry at all — and is not the session's owner: 403,
//     nothing revoked, no Set-Cookie. The same caller being the owner ends the
//     session and deletes the cookie, and those rows are the controls: a sign-out
//     that revoked nothing looks exactly like one that was never wired up;
//   - a request that names no one — no token, a token that fails for any reason but
//     expiry (another secret, no signature, rewritten claims, garbage, not even a
//     bearer, an API key that did not authenticate), or the two scoped kinds, which
//     authOptional ignores whether or not they have expired: held to the cookie
//     alone, so the session is ended and the cookie deleted;
//   - a cookie that names no live session, which the owner check never reaches:
//     200, the cookie deleted (it is dead, and deleting it takes nothing from
//     anyone), whoever the token names;
//   - the cookie not being what the request acted on: a body token that is not
//     the cookie leaves the cookie where it is.
//
// The failure rows are the choice Logout documents: a lookup or a revoke that fails
// is a 503 and still deletes the cookie, a named caller or not.
func TestLogout_AnAccessTokenNamesWhoIsSigningOut(t *testing.T) {
	const noCookie = "-"
	other := uuid.New()
	boom := errRaceTransient
	bearer := func(tok func(t *testing.T, a *authRaceApp) string) func(*testing.T, *authRaceApp) string {
		return func(t *testing.T, a *authRaceApp) string { return "Bearer " + tok(t, a) }
	}
	expiredOf := func(who func(a *authRaceApp) uuid.UUID) func(t *testing.T, a *authRaceApp) string {
		return func(t *testing.T, a *authRaceApp) string {
			return logoutAccessToken(t, logoutJWTSecret, -time.Hour, who(a))
		}
	}
	ownerOf := func(a *authRaceApp) uuid.UUID { return a.store.user.ID }
	otherOf := func(*authRaceApp) uuid.UUID { return other }
	// raw is an Authorization header spelled out in full.
	raw := func(v string) func(*testing.T, *authRaceApp) string {
		return func(*testing.T, *authRaceApp) string { return v }
	}

	const (
		ended   = true
		refused = http.StatusForbidden
	)
	tests := []struct {
		name  string
		tweak func(*raceStore)
		// cookie is the refresh cookie the request carries: "" is the session's
		// current token, noCookie none at all. body is the request body, "{}" if empty.
		cookie, body string
		// header is the whole Authorization header the request carries ("" none), and
		// actor stands in for authOptional having validated a token or a key: "owner",
		// "other" or none.
		header func(t *testing.T, a *authRaceApp) string
		actor  string

		want int
		// sent is how many times RevokeSession was sent; ended whether the session is
		// revoked afterwards; cookieWant what the answer does to the cookie.
		sent       int
		ended      bool
		cookieWant cookieOutcome
		// previous is the audit detail of a sign-out that matched the previous token.
		previous bool
	}{
		// An expired token that names someone else than the session's owner.
		{
			name:   "an expired token of another user, on the current cookie",
			header: bearer(expiredOf(otherOf)),
			want:   refused, cookieWant: cookieUntouched,
		},
		{
			name: "an expired token of another user, on the previous cookie", cookie: racePreviousToken,
			header: bearer(expiredOf(otherOf)),
			want:   refused, cookieWant: cookieUntouched,
		},
		{
			name: "an expired token of another user, the refresh token in the body", cookie: noCookie,
			body:   `{"refresh_token":"` + raceCurrentToken + `"}`,
			header: bearer(expiredOf(otherOf)),
			want:   refused, cookieWant: cookieUntouched,
		},
		{
			name:   "an expired token of the owner, on the current cookie",
			header: bearer(expiredOf(ownerOf)),
			want:   http.StatusOK, sent: 1, ended: ended, cookieWant: cookieCleared,
		},
		{
			name: "an expired token of the owner, on the previous cookie", cookie: racePreviousToken,
			header: bearer(expiredOf(ownerOf)),
			want:   http.StatusOK, sent: 1, ended: ended, cookieWant: cookieCleared, previous: true,
		},

		// Any other token this server signed that authOptional did not name: the refusal
		// does not care why, and a token of the owner is still the owner's.
		{
			name: "an expired token of another user after a second space in the header",
			header: func(t *testing.T, _ *authRaceApp) string {
				return "Bearer  " + logoutAccessToken(t, logoutJWTSecret, -time.Hour, other)
			},
			want: refused, cookieWant: cookieUntouched,
		},
		{
			name: "an expired token of the owner after a second space in the header",
			header: func(t *testing.T, a *authRaceApp) string {
				return "Bearer  " + logoutAccessToken(t, logoutJWTSecret, -time.Hour, a.store.user.ID)
			},
			want: http.StatusOK, sent: 1, ended: ended, cookieWant: cookieCleared,
		},
		{
			name: "a token of another user that is not valid yet",
			header: bearer(func(t *testing.T, _ *authRaceApp) string {
				return logoutTimedToken(t, other, timePtr(time.Now().Add(time.Hour)), timePtr(time.Now().Add(2*time.Hour)))
			}),
			want: refused, cookieWant: cookieUntouched,
		},
		{
			name: "a token of another user with no expiry",
			header: bearer(func(t *testing.T, _ *authRaceApp) string {
				return logoutTimedToken(t, other, nil, nil)
			}),
			want: refused, cookieWant: cookieUntouched,
		},
		{
			name: "a live token of another user that nothing named (no stand-in header)",
			header: bearer(func(t *testing.T, _ *authRaceApp) string {
				return logoutAccessToken(t, logoutJWTSecret, time.Hour, other)
			}),
			want: refused, cookieWant: cookieUntouched,
		},
		{
			name: "a token of the owner that is not valid yet",
			header: bearer(func(t *testing.T, a *authRaceApp) string {
				return logoutTimedToken(t, a.store.user.ID, timePtr(time.Now().Add(time.Hour)), timePtr(time.Now().Add(2*time.Hour)))
			}),
			want: http.StatusOK, sent: 1, ended: ended, cookieWant: cookieCleared,
		},
		{
			name: "a live token of the owner that nothing named (no stand-in header)",
			header: bearer(func(t *testing.T, a *authRaceApp) string {
				return logoutAccessToken(t, logoutJWTSecret, time.Hour, a.store.user.ID)
			}),
			want: http.StatusOK, sent: 1, ended: ended, cookieWant: cookieCleared,
		},

		// A valid token or key: authOptional's user_id.
		{
			name: "a valid token of another user",
			header: bearer(func(t *testing.T, _ *authRaceApp) string {
				return logoutAccessToken(t, logoutJWTSecret, time.Hour, other)
			}),
			actor: "other",
			want:  refused, cookieWant: cookieUntouched,
		},
		{
			name:  "a valid token of another user, on the previous cookie",
			actor: "other", cookie: racePreviousToken,
			want: refused, cookieWant: cookieUntouched,
		},
		{
			name:  "a valid token of the owner",
			actor: "owner",
			want:  http.StatusOK, sent: 1, ended: ended, cookieWant: cookieCleared,
		},

		// A request that names no one: the cookie is the credential, as it always was.
		{
			name: "no access token at all",
			want: http.StatusOK, sent: 1, ended: ended, cookieWant: cookieCleared,
		},
		{
			name: "an expired token signed with another secret, naming another user",
			header: bearer(func(t *testing.T, _ *authRaceApp) string {
				return logoutAccessToken(t, logoutJWTSecret+"-not", -time.Hour, other)
			}),
			want: http.StatusOK, sent: 1, ended: ended, cookieWant: cookieCleared,
		},
		{
			name:   "an expired token with no signature, naming another user",
			header: bearer(func(t *testing.T, _ *authRaceApp) string { return logoutUnsignedToken(t, other) }),
			want:   http.StatusOK, sent: 1, ended: ended, cookieWant: cookieCleared,
		},
		{
			name: "an expired token of the owner whose claims were rewritten to name another user",
			header: bearer(func(t *testing.T, a *authRaceApp) string {
				return logoutAlteredToken(t, a.store.user.ID, other)
			}),
			want: http.StatusOK, sent: 1, ended: ended, cookieWant: cookieCleared,
		},
		{
			name:   "a garbled token",
			header: raw("Bearer not-a-token"),
			want:   http.StatusOK, sent: 1, ended: ended, cookieWant: cookieCleared,
		},
		{
			name: "an expired token of another user under a scheme that is not Bearer",
			header: func(t *testing.T, _ *authRaceApp) string {
				return "Basic " + logoutAccessToken(t, logoutJWTSecret, -time.Hour, other)
			},
			want: http.StatusOK, sent: 1, ended: ended, cookieWant: cookieCleared,
		},
		{
			name:   "an API key that did not authenticate",
			header: raw("Bearer nxra_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"),
			want:   http.StatusOK, sent: 1, ended: ended, cookieWant: cookieCleared,
		},
		{
			// The prefix is looked for past the stray space, so the key never reaches the
			// JWT parser under this spelling either; a key cannot parse as a JWT, so the
			// answer is the same with or without that care, and this row says what it is.
			name:   "an API key that did not authenticate, after a second space",
			header: raw("Bearer  nxra_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"),
			want:   http.StatusOK, sent: 1, ended: ended, cookieWant: cookieCleared,
		},
		{
			name: "an expired console token of another user",
			header: bearer(func(t *testing.T, _ *authRaceApp) string {
				return logoutConsoleToken(t, -time.Hour, other)
			}),
			want: http.StatusOK, sent: 1, ended: ended, cookieWant: cookieCleared,
		},
		{
			name: "an expired WebSocket token of another user",
			header: bearer(func(t *testing.T, _ *authRaceApp) string {
				return logoutWSToken(t, -time.Hour, other)
			}),
			want: http.StatusOK, sent: 1, ended: ended, cookieWant: cookieCleared,
		},
		{
			name: "a console token of another user that has not expired",
			header: bearer(func(t *testing.T, _ *authRaceApp) string {
				return logoutConsoleToken(t, time.Minute, other)
			}),
			want: http.StatusOK, sent: 1, ended: ended, cookieWant: cookieCleared,
		},
		{
			name: "a WebSocket token of another user that has not expired",
			header: bearer(func(t *testing.T, _ *authRaceApp) string {
				return logoutWSToken(t, time.Minute, other)
			}),
			want: http.StatusOK, sent: 1, ended: ended, cookieWant: cookieCleared,
		},

		// A cookie that names no live session never reaches the owner check.
		{
			name: "an expired token of another user, a cookie no session holds", cookie: "a-token-nobody-issued",
			header: bearer(expiredOf(otherOf)),
			want:   http.StatusOK, cookieWant: cookieCleared,
		},
		{
			name:   "an expired token of another user, a session that is already revoked",
			tweak:  func(s *raceStore) { s.session.IsRevoked = true },
			header: bearer(expiredOf(otherOf)),
			want:   http.StatusOK, ended: true, cookieWant: cookieCleared,
		},
		{
			name: "an expired token of another user, a session that has expired",
			tweak: func(s *raceStore) {
				s.session.ExpiresAt = time.Now().Add(-time.Minute)
			},
			header: bearer(expiredOf(otherOf)),
			want:   http.StatusOK, cookieWant: cookieCleared,
		},
		{
			name: "an expired token of another user, no cookie at all", cookie: noCookie,
			header: bearer(expiredOf(otherOf)),
			want:   http.StatusOK, cookieWant: cookieCleared,
		},

		// The cookie not being what the request acted on.
		{
			name: "the owner's token in the body and another cookie in the jar", cookie: "the-cookie-of-some-other-session",
			body: `{"refresh_token":"` + raceCurrentToken + `"}`,
			want: http.StatusOK, sent: 1, ended: ended, cookieWant: cookieUntouched,
		},
		{
			name: "the owner's token in the body and the same cookie in the jar", cookie: raceCurrentToken,
			body: `{"refresh_token":"` + raceCurrentToken + `"}`,
			want: http.StatusOK, sent: 1, ended: ended, cookieWant: cookieCleared,
		},
		{
			name: "a body token that belongs to no session and another cookie in the jar", cookie: raceCurrentToken,
			body: `{"refresh_token":"a-token-nobody-issued"}`,
			want: http.StatusOK, cookieWant: cookieUntouched,
		},
		{
			name: "a body token too long to be one and another cookie in the jar", cookie: raceCurrentToken,
			body: `{"refresh_token":"` + strings.Repeat("a", MaxRefreshTokenLength+1) + `"}`,
			want: http.StatusOK, cookieWant: cookieUntouched,
		},

		// The failures keep the cookie going, whoever the request names.
		{
			name:   "a lookup that could not be made, with an expired token of another user",
			tweak:  func(s *raceStore) { s.currentErr = boom },
			header: bearer(expiredOf(otherOf)),
			want:   http.StatusServiceUnavailable, cookieWant: cookieCleared,
		},
		{
			name:   "a revoke that could not be made, with an expired token of the owner",
			tweak:  func(s *raceStore) { s.revokeErr = boom },
			header: bearer(expiredOf(ownerOf)),
			want:   http.StatusServiceUnavailable, sent: 1, cookieWant: cookieCleared,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := newAuthRaceApp(t, tt.tweak)
			owner := a.store.user.ID
			// The session's Redis row, which a sign-out that ends the session deletes
			// and one that does not must leave.
			rowKey := "nexara:session:" + a.store.session.ID.String()
			a.redis.Set(rowKey, "{}")

			headers := map[string]string{}
			if tt.header != nil {
				headers["Authorization"] = tt.header(t, a)
			}
			switch tt.actor {
			case "owner":
				headers["X-Test-Acting-User"] = owner.String()
			case "other":
				headers["X-Test-Acting-User"] = other.String()
			}
			cookie := cmp.Or(tt.cookie, raceCurrentToken)
			if tt.cookie == noCookie {
				cookie = ""
			}
			body := cmp.Or(tt.body, "{}")

			resp := a.postBody(t, "/auth/logout", body, cookie, headers)

			if resp.StatusCode != tt.want {
				t.Fatalf("status = %d, want %d", resp.StatusCode, tt.want)
			}
			cookies := refreshCookies(resp)
			switch tt.cookieWant {
			case cookieCleared:
				if len(cookies) != 1 || !cookieDeleted(cookies[0]) {
					t.Errorf("Set-Cookie = %+v, want exactly one cookie that deletes the refresh cookie", cookies)
				}
			case cookieUntouched:
				if len(cookies) != 0 {
					t.Errorf("Set-Cookie = %+v, want the cookie left alone: it is not this request's to delete", cookies)
				}
			}
			if got := len(a.store.named("RevokeSession")); got != tt.sent {
				t.Errorf("RevokeSession sent %d times, want %d", got, tt.sent)
			}
			if got := a.store.snapshot().IsRevoked; got != tt.ended {
				t.Errorf("session revoked = %t afterwards, want %t", got, tt.ended)
			}

			wantAudits, wantDetails := []string(nil), []string(nil)
			if tt.ended && tt.sent == 1 {
				wantAudits = []string{"logout"}
				wantDetails = []string{"{}"}
				if tt.previous {
					wantDetails = []string{logoutMatchedPreviousToken}
				}
			}
			if got := a.store.auditActions(); !reflect.DeepEqual(got, wantAudits) {
				t.Errorf("audit actions = %v, want %v", got, wantAudits)
			}
			if got := a.store.auditDetailsWritten(); !reflect.DeepEqual(got, wantDetails) {
				t.Errorf("audit details = %v, want %v", got, wantDetails)
			}
			if tt.want == refused {
				const want = "This refresh token belongs to another user's session, not to the user of the access token or API key sent with it; nothing was revoked."
				if msg := decodeObject(t, resp)["message"]; msg != want {
					t.Errorf("message = %v, want the ownership refusal %q", msg, want)
				}
			}
			// Redis follows the session: gone when a sign-out ended it, there when
			// nothing did.
			if gone := !a.redis.Exists(rowKey); gone != (tt.sent == 1 && tt.ended) {
				t.Errorf("the session's Redis row is gone = %t, want %t", gone, tt.sent == 1 && tt.ended)
			}
		})
	}
}

// TestLogout_WithoutAJWTServiceTheBearerTokenNamesNoOne pins the handler's own
// guard for a service it was not given, which authOptional has too: no service, no
// parse. The sign-out is then held to the cookie, and must not panic on the token it
// would otherwise have handed to the parser.
//
// The request goes through a route that recovers, so that a handler which does panic
// fails this test with a 500 instead of taking the test binary down with it.
func TestLogout_WithoutAJWTServiceTheBearerTokenNamesNoOne(t *testing.T) {
	a := newAuthRaceApp(t, nil)
	a.handler.jwtService = nil
	a.app.Post("/auth/logout-recovered", recoverer.New(), a.handler.Logout)

	resp := a.post(t, "/auth/logout-recovered", raceCurrentToken, map[string]string{
		"Authorization": "Bearer " + logoutAccessToken(t, logoutJWTSecret, -time.Hour, uuid.New()),
	})

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200: a sign-out with no JWT service must go on without reading the token", resp.StatusCode)
	}
	if !a.store.snapshot().IsRevoked {
		t.Error("the session was not ended")
	}
}

// TestLogout_ARefusedSignOutLeavesTheOtherUsersSessionToTheirRefresh follows the
// refusal one step further, which is the point of it: after the 403 the owner's
// refresh still works, with the very cookie that was sent. A refusal that left the
// session live and the cookie gone would pass every row above and still sign the
// owner out at their next refresh.
func TestLogout_ARefusedSignOutLeavesTheOtherUsersSessionToTheirRefresh(t *testing.T) {
	a := newAuthRaceApp(t, nil)
	other := uuid.New()

	resp := a.post(t, "/auth/logout", raceCurrentToken, map[string]string{
		"Authorization": "Bearer " + logoutAccessToken(t, logoutJWTSecret, -time.Hour, other),
	})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("control: the sign-out answered %d, want 403", resp.StatusCode)
	}

	resp = a.post(t, "/auth/refresh", raceCurrentToken, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the owner's refresh answered %d after a refused sign-out, want 200", resp.StatusCode)
	}
	if cookies := refreshCookies(resp); len(cookies) != 1 || cookieDeleted(cookies[0]) || cookies[0].Value == "" {
		t.Errorf("Set-Cookie = %+v, want the new refresh cookie", cookies)
	}
}

// guardOnlyReachedFrom is the static check behind the guards below: outside tests,
// name may be mentioned in exactly one place, the function allowedFunc of allowedFile,
// and once there. Both spellings are watched, a call and a bare reference, and it
// fails if it cannot see the one use it permits — a guard that finds nothing proves
// nothing, whichever way a rename makes it blind.
func guardOnlyReachedFrom(t *testing.T, name, allowedFile, allowedFunc, why string) {
	t.Helper()
	fset := token.NewFileSet()
	scanned, allowedHits := 0, 0
	for _, path := range goSourceFiles(t) {
		file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		scanned++
		inAllowedFile := strings.HasSuffix(filepath.ToSlash(path), allowedFile)
		for _, decl := range file.Decls {
			enclosing := ""
			if fn, ok := decl.(*ast.FuncDecl); ok {
				enclosing = fn.Name.Name
			}
			ast.Inspect(decl, func(n ast.Node) bool {
				sel, ok := n.(*ast.SelectorExpr)
				if !ok || sel.Sel.Name != name {
					return true
				}
				if inAllowedFile && enclosing == allowedFunc {
					allowedHits++
					return true
				}
				t.Errorf("%s: %s reaches %s; only %s in %s may — %s", fset.Position(sel.Pos()),
					cmp.Or(enclosing, "(package level)"), name, allowedFunc, allowedFile, why)
				return true
			})
		}
	}
	if scanned == 0 {
		t.Fatal("the guard scanned no Go sources")
	}
	if allowedHits != 1 {
		t.Fatalf("found %d references to %s in %s of %s, want exactly 1: the guard cannot see what it exists "+
			"to restrict", allowedHits, name, allowedFunc, allowedFile)
	}
}

// TestGuard_AccessTokenIsSomeoneElsesIsOnlyReachedFromTheSignOutCheck keeps the one
// function that reads whom a token the server has stopped vouching for was issued to
// from becoming a way to be signed in.
//
// AccessTokenIsSomeoneElses verifies a token's signature and ignores its time claims.
// That is right for exactly one thing — refusing a sign-out that is for someone
// else's session — and wrong for anything that grants: a middleware that "tolerated"
// an expired token would sign in anyone who ever held one. Nothing in its signature
// stops that, and every behavioural test passes the day it happens, so a static check
// holds it: the function is named in one place, signOutIsSomeoneElses.
func TestGuard_AccessTokenIsSomeoneElsesIsOnlyReachedFromTheSignOutCheck(t *testing.T) {
	guardOnlyReachedFrom(t, "AccessTokenIsSomeoneElses", "internal/api/handlers/auth.go", "signOutIsSomeoneElses",
		"it answers for a token that may have EXPIRED, which proves who the token was issued to and nothing about "+
			"who holds it: it may refuse a sign-out and must never decide who is signed in (ValidateAccessToken does "+
			"that, and refuses an expired token)")
}

// TestGuard_SignOutIsSomeoneElsesIsOnlyReachedFromLogout is the same fence one layer
// out. signOutIsSomeoneElses is where the request's Authorization header is read
// for a refusal, and the next function that wants to know "who is this?" — LogoutAll,
// whose caller authRequired already named; a new handler that takes the answer for
// an identity — must not be able to borrow it. The answer is a bool, so there is no
// identity to borrow; this keeps the question itself from being asked anywhere but
// at the one place that turns a sign-out away.
func TestGuard_SignOutIsSomeoneElsesIsOnlyReachedFromLogout(t *testing.T) {
	guardOnlyReachedFrom(t, "signOutIsSomeoneElses", "internal/api/handlers/auth.go", "Logout",
		"it reads a bearer token that may be expired, forged or someone else's, and is a refusal for a sign-out "+
			"and nothing else: a handler that needs to know who is calling takes it from authRequired's user_id")
}

// TestGuard_AccessTokenIsSomeoneElsesReturnsOnlyABool pins the property the two
// guards above lean on, in the way TestGuard_RefusalSparesCookieReturnsOnlyABool pins
// its own: the question is answered with a bool and nothing else, so its match cannot
// be turned into an identity or a token. A change that makes it return more has to
// change this test, and so has to be a decision rather than a convenience.
func TestGuard_AccessTokenIsSomeoneElsesReturnsOnlyABool(t *testing.T) {
	m, ok := reflect.TypeOf((*auth.JWTService)(nil)).MethodByName("AccessTokenIsSomeoneElses")
	if !ok {
		t.Fatal("auth.JWTService has no AccessTokenIsSomeoneElses")
	}
	if m.Type.NumOut() != 1 || m.Type.Out(0).Kind() != reflect.Bool {
		t.Errorf("AccessTokenIsSomeoneElses returns %v; it must return exactly one bool, because it reads tokens the "+
			"server has stopped vouching for and must never be able to hand back whom they name", m.Type)
	}
}

// TestGuard_SignOutIsSomeoneElsesReturnsOnlyABool is the same pin for the handler's
// side. The method is unexported, so reflection reaches it through a method
// expression, which compiles for any signature and so reports a changed one at run
// time, by name.
func TestGuard_SignOutIsSomeoneElsesReturnsOnlyABool(t *testing.T) {
	typ := reflect.TypeOf((*AuthHandler).signOutIsSomeoneElses)
	if typ.NumOut() != 1 || typ.Out(0).Kind() != reflect.Bool {
		t.Errorf("signOutIsSomeoneElses returns %v; it must return exactly one bool: it decides a refusal and "+
			"must never be able to hand back an identity", typ)
	}
}

// logoutAllRow is one way LogoutAll can meet the cookie in its request.
type logoutAllRow struct {
	name  string
	tweak func(*raceStore)
	// cookie is the refresh cookie the request carries, "" for none. actor is whose
	// access token made the request: "owner" is the account whose session the
	// harness holds, "other" a different one.
	cookie string
	actor  string

	cookieWant cookieOutcome
	// lookups is how many statements asked which session the cookie names.
	lookups int
}

// TestLogoutAll_TheCookieIsClearedOnlyWhenItIsTheCallers holds LogoutAll to the
// same care as Logout: it ends every session of the CALLER, and deletes the cookie
// in the request only when the cookie is the caller's to delete.
//
// The harness holds one session, the owner's. A request by the owner with its
// cookie is the caller's own cookie, in either of the two spellings a session
// answers to (its current token, and the one it had before its last rotation); a
// request by someone else with that cookie is another user's, and must neither
// revoke the owner's session — it is not the caller's — nor delete the cookie.
// Whatever names no live session is dead, and is deleted: no cookie, one no session
// holds, one for a session already revoked, one rotated away longer ago than the
// sign-out window, one too long to be a token (which is never looked up).
//
// A lookup that could not be made leaves the cookie alone, and the revoke goes
// ahead: the caller asked for their sessions to end, and the cookie is only
// housekeeping once they have.
//
// Every row that revokes shows it, with the caller's id, and the owner's session
// is revoked only when the caller IS the owner — the rows of the other kind are
// controls for each other.
func TestLogoutAll_TheCookieIsClearedOnlyWhenItIsTheCallers(t *testing.T) {
	window := auth.PreviousTokenRevocationWindow
	boom, bug := errRaceTransient, errRaceBug
	rotatedAgo := raceRotatedAgo

	tests := []logoutAllRow{
		// The caller's own cookie.
		{name: "its own cookie", cookie: raceCurrentToken, actor: "owner", cookieWant: cookieCleared, lookups: 1},
		{
			name: "its own cookie, the previous token inside the window", cookie: racePreviousToken, actor: "owner",
			tweak: rotatedAgo(window / 4), cookieWant: cookieCleared, lookups: 2,
		},
		{name: "no cookie", actor: "owner", cookieWant: cookieCleared},
		{name: "a cookie no session holds", cookie: "a-token-nobody-issued", actor: "owner", cookieWant: cookieCleared, lookups: 2},
		{
			name: "a cookie too long to be a token", cookie: strings.Repeat("a", MaxRefreshTokenLength+1), actor: "owner",
			cookieWant: cookieCleared,
		},

		// Another user's cookie.
		{name: "another user's cookie", cookie: raceCurrentToken, actor: "other", cookieWant: cookieUntouched, lookups: 1},
		{
			name: "another user's cookie, the previous token inside the window", cookie: racePreviousToken, actor: "other",
			tweak: rotatedAgo(window / 4), cookieWant: cookieUntouched, lookups: 2,
		},
		{
			// A session that is revoked is nobody's: the cookie is dead.
			name: "another user's cookie, for a session that is already revoked", cookie: raceCurrentToken, actor: "other",
			tweak: func(s *raceStore) { s.session.IsRevoked = true }, cookieWant: cookieCleared, lookups: 2,
		},
		{
			name: "another user's cookie, for a session that has expired", cookie: raceCurrentToken, actor: "other",
			tweak:      func(s *raceStore) { s.session.ExpiresAt = time.Now().Add(-time.Minute) },
			cookieWant: cookieCleared, lookups: 2,
		},
		{
			// Rotated away longer ago than the window: that token signs nothing out, so
			// it names no live session, whoever's the session is.
			name: "another user's cookie, a token rotated away longer ago than the window", cookie: racePreviousToken, actor: "other",
			tweak: rotatedAgo(window + 10*time.Second), cookieWant: cookieCleared, lookups: 2,
		},

		// A lookup that could not be made.
		{
			name: "a lookup that could not be made", cookie: raceCurrentToken, actor: "owner",
			tweak: func(s *raceStore) { s.currentErr = boom }, cookieWant: cookieUntouched, lookups: 1,
		},
		{
			name: "a previous-token lookup that could not be made", cookie: racePreviousToken, actor: "owner",
			tweak: func(s *raceStore) { s.previousErr = boom }, cookieWant: cookieUntouched, lookups: 2,
		},
		{
			name: "a lookup that failed for a defect", cookie: raceCurrentToken, actor: "other",
			tweak: func(s *raceStore) { s.currentErr = bug }, cookieWant: cookieUntouched, lookups: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := newAuthRaceApp(t, tt.tweak)
			other := uuid.New()
			caller := a.store.user.ID
			if tt.actor == "other" {
				caller = other
			}

			resp := a.post(t, "/auth/logout-all", tt.cookie, map[string]string{"X-Test-Acting-User": caller.String()})

			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status = %d, want 200", resp.StatusCode)
			}
			cookies := refreshCookies(resp)
			switch tt.cookieWant {
			case cookieCleared:
				if len(cookies) != 1 || !cookieDeleted(cookies[0]) {
					t.Errorf("Set-Cookie = %+v, want exactly one cookie that deletes the refresh cookie", cookies)
				}
			case cookieUntouched:
				if len(cookies) != 0 {
					t.Errorf("Set-Cookie = %+v, want the cookie left alone: it is not the caller's to delete", cookies)
				}
			}

			// The caller's sessions, and only theirs, were revoked.
			revokes := a.store.named("RevokeAllUserSessions")
			if len(revokes) != 1 || revokes[0].args[0] != caller {
				t.Fatalf("RevokeAllUserSessions = %+v, want one revoke of the caller's sessions (%v)", revokes, caller)
			}
			if got := a.store.auditActions(); !reflect.DeepEqual(got, []string{"logout_all"}) {
				t.Errorf("audit actions = %v, want [logout_all]", got)
			}
			// The owner's session ends exactly when the owner asked: nobody else's
			// sign-out everywhere reaches it, whatever cookie it carried.
			wantEnded := tt.actor == "owner" || (tt.tweak != nil && sessionStartsRevoked(tt.tweak))
			if got := a.store.snapshot().IsRevoked; got != wantEnded {
				t.Errorf("the owner's session is revoked = %t afterwards, want %t", got, wantEnded)
			}

			// The lookup asked about the cookie's token, and not once more than needed.
			asked := len(a.store.named("GetSessionByTokenHash")) + len(a.store.named("GetSessionByPreviousTokenHash"))
			if asked != tt.lookups {
				t.Errorf("%d statements asked which session the cookie names, want %d", asked, tt.lookups)
			}
		})
	}
}

// sessionStartsRevoked reports whether tweak leaves the harness's session revoked
// to begin with, which is what makes "the owner's session is revoked afterwards"
// true of a row the owner did not ask for.
func sessionStartsRevoked(tweak func(*raceStore)) bool {
	s := &raceStore{}
	tweak(s)
	return s.session.IsRevoked
}

// TestLogoutAll_LooksAtTheCookieBeforeItRevokes pins the order, and only the order,
// because that is all anyone can observe of it: the lookup finds live sessions only,
// so asked after the revoke the caller's own cookie would read as a session that no
// longer exists — "no live session", which also clears — and the same answer would
// come out for a reason that is not the cookie's, with the owner test never run on
// the caller's own session. The rows of TestLogoutAll_TheCookieIsClearedOnlyWhenItIsTheCallers
// cannot tell the two apart by outcome; this is what keeps each branch taken for its
// own reason.
//
// The lookup is made to say what it saw — the session live or already revoked — by a
// hook on the store, and must have seen it live.
func TestLogoutAll_LooksAtTheCookieBeforeItRevokes(t *testing.T) {
	a := newAuthRaceApp(t, nil)
	var sawRevoked []bool
	a.store.lockWait = func(_ context.Context, name string) error {
		if name == "GetSessionByTokenHash" {
			sawRevoked = append(sawRevoked, a.store.snapshot().IsRevoked)
		}
		return nil
	}

	resp := a.post(t, "/auth/logout-all", raceCurrentToken, map[string]string{"X-Test-Acting-User": a.store.user.ID.String()})

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if !a.store.snapshot().IsRevoked {
		t.Fatal("the control did not end the session, so the lookup's timing says nothing")
	}
	if !reflect.DeepEqual(sawRevoked, []bool{false}) {
		t.Errorf("the cookie's lookup ran %d time(s), seeing the session revoked = %v: it must run once, before the revoke", len(sawRevoked), sawRevoked)
	}

	var order []string
	for _, st := range a.store.statements() {
		switch st.name {
		case "GetSessionByTokenHash", "RevokeAllUserSessions":
			order = append(order, st.name)
		}
	}
	if want := []string{"GetSessionByTokenHash", "RevokeAllUserSessions"}; !reflect.DeepEqual(order, want) {
		t.Errorf("statements in order = %v, want %v", order, want)
	}
}

// TestLogoutAll_ALookupThatFailsIsLoggedAndTheRevokeStillHappens pins the two things
// the failed lookup leaves behind: a line in the log — the database being away a Warn
// and a defect an Error, as everywhere in these handlers, with the caller named and
// nothing that could be replayed — and the revoke, which the caller asked for and
// which a lookup about a cookie has no business cancelling.
func TestLogoutAll_ALookupThatFailsIsLoggedAndTheRevokeStillHappens(t *testing.T) {
	const line = "logout-all: could not tell whose session the refresh cookie names"
	for _, tc := range []struct {
		name      string
		err       error
		wantLevel string
	}{
		{"the database is away", errRaceTransient, `"level":"WARN"`},
		{"a defect", errRaceBug, `"level":"ERROR"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			logs := captureProductionLog(t)
			a := newAuthRaceApp(t, func(s *raceStore) { s.currentErr = tc.err })

			resp := a.post(t, "/auth/logout-all", raceCurrentToken, map[string]string{"X-Test-Acting-User": a.store.user.ID.String()})

			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status = %d, want 200", resp.StatusCode)
			}
			if !a.store.snapshot().IsRevoked {
				t.Error("the sessions were not revoked: a failed lookup about the cookie cancelled what the caller asked for")
			}
			out := logs.String()
			if !strings.Contains(out, line) || !strings.Contains(out, tc.wantLevel) {
				t.Errorf("the failed lookup did not leave a %s line saying %q: %q", tc.wantLevel, line, out)
			}
			if !strings.Contains(out, a.store.user.ID.String()) {
				t.Errorf("the line does not name the caller: %q", out)
			}
			if strings.Contains(out, raceCurrentToken) || strings.Contains(out, auth.HashToken(raceCurrentToken)) {
				t.Errorf("the log carries the token or its hash: %q", out)
			}
		})
	}
}

// TestLogoutAll_AFailedRevokeStillLeavesEveryCookieAlone is the 503 and the 500 of
// the sign-out everywhere with a cookie in the request: whatever the cookie names,
// nothing was revoked, the caller is still signed in, and so the cookie stays.
func TestLogoutAll_AFailedRevokeStillLeavesEveryCookieAlone(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want int
	}{
		{"the database is away", errRaceTransient, http.StatusServiceUnavailable},
		{"a defect", errRaceBug, http.StatusInternalServerError},
	} {
		for _, actor := range []string{"owner", "other"} {
			t.Run(tc.name+", "+actor+" asked", func(t *testing.T) {
				a := newAuthRaceApp(t, func(s *raceStore) { s.revokeAllErr = tc.err })
				caller := a.store.user.ID
				if actor == "other" {
					caller = uuid.New()
				}

				resp := a.post(t, "/auth/logout-all", raceCurrentToken, map[string]string{"X-Test-Acting-User": caller.String()})

				if resp.StatusCode != tc.want {
					t.Fatalf("status = %d, want %d", resp.StatusCode, tc.want)
				}
				if cookies := refreshCookies(resp); len(cookies) != 0 {
					t.Errorf("Set-Cookie = %+v, want the cookie left alone", cookies)
				}
				if a.store.snapshot().IsRevoked {
					t.Error("the session is revoked although the revoke failed")
				}
			})
		}
	}
}

// postCookieLines sends a POST carrying exactly the Cookie header lines it is given,
// one header line each — the way a browser sends one line and a proxy or an HTTP/2
// hop may send several — and no other cookie.
func (a *authRaceApp) postCookieLines(t *testing.T, path, body string, lines []string, headers map[string]string) *http.Response {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	for _, line := range lines {
		req.Header.Add("Cookie", line)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := a.app.Test(req, fiber.TestConfig{Timeout: time.Second, FailOnTimeout: true})
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

// TestLogout_AJarWithMoreThanOneRefreshCookieIsClearedNotSpared holds the case in
// which Logout does not spare a cookie it otherwise would: a request that carries
// more than one refresh cookie.
//
// The cookie is host-only and path-scoped, but a page on a sibling subdomain can
// plant one of the same name for the parent domain, and the browser then sends both,
// in an order the server does not control; the server reads the first. When the
// first is a planted cookie that names another user's session — or any session that
// is not the signer's — the 403 would spare "the cookie", and the cookie it spares is
// not the host-only one the browser would have deleted: the real credential stays in
// the browser through the sign-out meant to remove it. So with more than one refresh
// cookie nothing is spared, and the answer is what it was before Logout learned to
// spare anything: the cookie is deleted. The refusal itself is not softened — the
// session is still not revoked.
//
// The first row of each kind is the control: with one refresh cookie the same request
// spares it, so the rows that clear are the jar being ambiguous and not the harness
// being unable to tell. Other cookies — a name that only begins like the refresh
// cookie's, one that differs from it only in case — and a pair the server cannot
// parse (fasthttp drops a value with a quote inside it, and such a pair cannot
// displace the real cookie in what the handlers read either), are not refresh
// cookies: the name is matched exactly, as the handlers read it.
func TestLogout_AJarWithMoreThanOneRefreshCookieIsClearedNotSpared(t *testing.T) {
	const planted = "a-cookie-planted-from-a-sibling-subdomain"
	pair := func(v string) string { return RefreshCookieName + "=" + v }
	ownBody := `{"refresh_token":"` + raceCurrentToken + `"}`

	tests := []struct {
		name  string
		lines []string
		body  string
		// otherUsers adds an expired access token of another user to the request.
		otherUsers bool

		want       int
		sent       int
		cookieWant cookieOutcome
	}{
		{
			name:  "one refresh cookie, turned away: spared (the control)",
			lines: []string{pair(raceCurrentToken)}, otherUsers: true,
			want: http.StatusForbidden, cookieWant: cookieUntouched,
		},
		{
			name:  "two refresh cookies in one header line, turned away: cleared",
			lines: []string{pair(raceCurrentToken) + "; " + pair(planted)}, otherUsers: true,
			want: http.StatusForbidden, cookieWant: cookieCleared,
		},
		{
			name:  "two refresh cookies in two header lines, turned away: cleared",
			lines: []string{pair(raceCurrentToken), pair(planted)}, otherUsers: true,
			want: http.StatusForbidden, cookieWant: cookieCleared,
		},
		{
			name:  "three refresh cookies, turned away: cleared",
			lines: []string{pair(raceCurrentToken) + "; " + pair(planted) + "; " + pair("another")}, otherUsers: true,
			want: http.StatusForbidden, cookieWant: cookieCleared,
		},
		{
			name:  "a cookie of another name is not a second refresh cookie: spared",
			lines: []string{"theme=dark; " + pair(raceCurrentToken)}, otherUsers: true,
			want: http.StatusForbidden, cookieWant: cookieUntouched,
		},
		{
			name:  "a second pair the server cannot parse is not a second refresh cookie: spared",
			lines: []string{pair(raceCurrentToken) + "; " + RefreshCookieName + `=pla"nted`}, otherUsers: true,
			want: http.StatusForbidden, cookieWant: cookieUntouched,
		},
		{
			name:  "a cookie whose name only begins like the refresh cookie's is not a second refresh cookie: spared",
			lines: []string{pair(raceCurrentToken) + "; " + RefreshCookieName + "_x=" + planted}, otherUsers: true,
			want: http.StatusForbidden, cookieWant: cookieUntouched,
		},
		{
			name:  "a cookie whose name differs from the refresh cookie's only in case is not a second refresh cookie: spared",
			lines: []string{pair(raceCurrentToken) + "; " + strings.ToUpper(RefreshCookieName) + "=" + planted}, otherUsers: true,
			want: http.StatusForbidden, cookieWant: cookieUntouched,
		},
		{
			name:  "one refresh cookie, a body token that is not it: spared (the control)",
			lines: []string{pair(planted)}, body: ownBody,
			want: http.StatusOK, sent: 1, cookieWant: cookieUntouched,
		},
		{
			name:  "two refresh cookies, a body token that is not the first: cleared",
			lines: []string{pair(planted) + "; " + pair("another")}, body: ownBody,
			want: http.StatusOK, sent: 1, cookieWant: cookieCleared,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := newAuthRaceApp(t, nil)
			headers := map[string]string{}
			if tt.otherUsers {
				headers["Authorization"] = "Bearer " + logoutAccessToken(t, logoutJWTSecret, -time.Hour, uuid.New())
			}

			resp := a.postCookieLines(t, "/auth/logout", cmp.Or(tt.body, "{}"), tt.lines, headers)

			if resp.StatusCode != tt.want {
				t.Fatalf("status = %d, want %d", resp.StatusCode, tt.want)
			}
			cookies := refreshCookies(resp)
			switch tt.cookieWant {
			case cookieCleared:
				if len(cookies) != 1 || !cookieDeleted(cookies[0]) {
					t.Errorf("Set-Cookie = %+v, want exactly one cookie that deletes the refresh cookie", cookies)
				}
			case cookieUntouched:
				if len(cookies) != 0 {
					t.Errorf("Set-Cookie = %+v, want the cookie left alone", cookies)
				}
			}
			if got := len(a.store.named("RevokeSession")); got != tt.sent {
				t.Errorf("RevokeSession sent %d times, want %d: the refusal must not be softened by the cookie rule", got, tt.sent)
			}
			if got := a.store.snapshot().IsRevoked; got != (tt.sent == 1) {
				t.Errorf("session revoked = %t afterwards, want %t", got, tt.sent == 1)
			}
		})
	}
}

// TestLogoutAll_AJarWithMoreThanOneRefreshCookieIsClearedNotLookedAt is the same
// ambiguity for the sign-out everywhere. The lookup would read the first cookie, which
// may be the planted one, and "another user's, leave it" would then spare a cookie
// that is not the one a deletion removes. So the cookie is cleared without being
// looked at — no statement asks which session it names — and the caller's sessions
// are revoked all the same. The first row is the control: with one refresh cookie
// the same request leaves the other user's cookie alone, after one lookup.
func TestLogoutAll_AJarWithMoreThanOneRefreshCookieIsClearedNotLookedAt(t *testing.T) {
	const planted = "a-cookie-planted-from-a-sibling-subdomain"
	pair := func(v string) string { return RefreshCookieName + "=" + v }

	tests := []struct {
		name  string
		lines []string
		// asOwner makes the caller the account whose session the cookie names.
		asOwner    bool
		cookieWant cookieOutcome
		lookups    int
	}{
		{
			name:  "one refresh cookie that names another user's session: left alone (the control)",
			lines: []string{pair(raceCurrentToken)}, cookieWant: cookieUntouched, lookups: 1,
		},
		{
			name:  "two refresh cookies in one header line, the first another user's",
			lines: []string{pair(raceCurrentToken) + "; " + pair(planted)}, cookieWant: cookieCleared,
		},
		{
			name:  "two refresh cookies in two header lines, the first another user's",
			lines: []string{pair(raceCurrentToken), pair(planted)}, cookieWant: cookieCleared,
		},
		{
			name:  "two refresh cookies, the caller's own first",
			lines: []string{pair(raceCurrentToken) + "; " + pair(planted)}, asOwner: true, cookieWant: cookieCleared,
		},
		{
			name:  "a cookie of another name is not a second refresh cookie",
			lines: []string{"theme=dark; " + pair(raceCurrentToken)}, cookieWant: cookieUntouched, lookups: 1,
		},
		{
			name:  "a cookie whose name only begins like the refresh cookie's is not a second refresh cookie",
			lines: []string{pair(raceCurrentToken) + "; " + RefreshCookieName + "_x=" + planted}, cookieWant: cookieUntouched, lookups: 1,
		},
		{
			name:  "a cookie whose name differs from the refresh cookie's only in case is not a second refresh cookie",
			lines: []string{pair(raceCurrentToken) + "; " + strings.ToUpper(RefreshCookieName) + "=" + planted}, cookieWant: cookieUntouched, lookups: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := newAuthRaceApp(t, nil)
			caller := uuid.New()
			if tt.asOwner {
				caller = a.store.user.ID
			}

			resp := a.postCookieLines(t, "/auth/logout-all", "{}", tt.lines, map[string]string{"X-Test-Acting-User": caller.String()})

			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status = %d, want 200", resp.StatusCode)
			}
			cookies := refreshCookies(resp)
			switch tt.cookieWant {
			case cookieCleared:
				if len(cookies) != 1 || !cookieDeleted(cookies[0]) {
					t.Errorf("Set-Cookie = %+v, want exactly one cookie that deletes the refresh cookie", cookies)
				}
			case cookieUntouched:
				if len(cookies) != 0 {
					t.Errorf("Set-Cookie = %+v, want the cookie left alone", cookies)
				}
			}
			if revokes := a.store.named("RevokeAllUserSessions"); len(revokes) != 1 || revokes[0].args[0] != caller {
				t.Errorf("RevokeAllUserSessions = %+v, want one revoke of the caller's sessions (%v)", revokes, caller)
			}
			asked := len(a.store.named("GetSessionByTokenHash")) + len(a.store.named("GetSessionByPreviousTokenHash"))
			if asked != tt.lookups {
				t.Errorf("%d statements asked which session the cookie names, want %d", asked, tt.lookups)
			}
		})
	}
}

// TestLogout_ARefusalIsLoggedWithTheSessionAndNothingElse pins the line a 403
// leaves, which is the only trace of it: the caller is told "no", and whoever finds
// that a sign-out did nothing has to be able to see why. It names the session and the
// user it belongs to — what an operator needs to find it — and never a token or a
// hash, and never the caller either: signOutIsSomeoneElses answers a bool, so the
// identity it compared with is not available to log, by design. The control is a
// sign-out that works, which leaves no such line.
func TestLogout_ARefusalIsLoggedWithTheSessionAndNothingElse(t *testing.T) {
	const line = "logout: refused"
	other := uuid.New()

	logs := captureProductionLog(t)
	a := newAuthRaceApp(t, nil)
	bearer := logoutAccessToken(t, logoutJWTSecret, -time.Hour, other)
	resp := a.post(t, "/auth/logout", raceCurrentToken, map[string]string{"Authorization": "Bearer " + bearer})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", resp.StatusCode)
	}
	out := logs.String()
	if !strings.Contains(out, line) || !strings.Contains(out, `"level":"INFO"`) {
		t.Fatalf("a refused sign-out left no Info line saying %q: %q", line, out)
	}
	if !strings.Contains(out, a.store.session.ID.String()) || !strings.Contains(out, a.store.session.UserID.String()) {
		t.Errorf("the line names neither the session nor its user: %q", out)
	}
	for _, secret := range []string{raceCurrentToken, auth.HashToken(raceCurrentToken), bearer, other.String()} {
		if strings.Contains(out, secret) {
			t.Errorf("the log carries %q: a token, a hash or the caller's identity", secret)
		}
	}

	quiet := captureProductionLog(t)
	b := newAuthRaceApp(t, nil)
	if resp := b.post(t, "/auth/logout", raceCurrentToken, nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("control status = %d, want 200", resp.StatusCode)
	}
	if !b.store.snapshot().IsRevoked {
		t.Fatal("control: the sign-out did not end the session")
	}
	if strings.Contains(quiet.String(), line) {
		t.Errorf("a sign-out that worked logged a refusal: %q", quiet.String())
	}
}

// TestLogoutAll_TheCookieLookupHasADeadlineOfItsOwn pins what the lookup of the
// cookie runs under, and what the revoke runs under after it. The lookup is one
// statement the revoke does not depend on, so it gets the follow-up bound from a
// fresh start; the deciding bound begins only afterwards, whole.
//
// The harness reports the deadline each statement's context carries, and the bounds
// are far apart on purpose: a lookup on the deciding context would carry about ten
// seconds, one on a context with no deadline would carry none, and each is a
// failure here and not a hang. (The stall tests below are the behavioural half.)
func TestLogoutAll_TheCookieLookupHasADeadlineOfItsOwn(t *testing.T) {
	const deciding, lookup = 10 * time.Second, 150 * time.Millisecond
	a := newAuthRaceAppWith(t, nil, raceOptions{dbTimeout: deciding, followUpTimeout: lookup})

	type statement struct {
		name     string
		deadline bool
		left     time.Duration
	}
	var (
		mu   sync.Mutex
		seen []statement
	)
	a.store.lockWait = func(ctx context.Context, name string) error {
		d, ok := ctx.Deadline()
		mu.Lock()
		defer mu.Unlock()
		seen = append(seen, statement{name: name, deadline: ok, left: time.Until(d)})
		return nil
	}

	resp := a.post(t, "/auth/logout-all", raceCurrentToken, map[string]string{"X-Test-Acting-User": a.store.user.ID.String()})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	mu.Lock()
	defer mu.Unlock()
	var lookups, revokes int
	for _, st := range seen {
		switch st.name {
		case "GetSessionByTokenHash", "GetSessionByPreviousTokenHash":
			lookups++
			if !st.deadline || st.left <= 0 || st.left > lookup {
				t.Errorf("%s ran with deadline = %t and %v left, want a deadline no more than %v away: the lookup has one of its own",
					st.name, st.deadline, st.left, lookup)
			}
		case "ListUserSessions", "RevokeAllUserSessions":
			revokes++
			if !st.deadline || st.left < deciding-time.Second {
				t.Errorf("%s ran with deadline = %t and %v left, want nearly the whole %v: the lookup must not spend the revoke's bound",
					st.name, st.deadline, st.left, deciding)
			}
		}
	}
	if lookups == 0 || revokes == 0 {
		t.Fatalf("saw %d lookups and %d revoke statements (%+v): the probe is not on the path, so this test proved nothing", lookups, revokes, seen)
	}
}

// TestLogoutAll_AStalledLookupLeavesTheRevokeItsWholeBound is the behaviour the
// deadline above is for. The cookie's lookup stalls until its own deadline; the
// revoke, then, needs most of its bound (its first statement is made to take
// 600 ms of the 800 ms it is given). It must still go through: the caller asked for
// their sessions to end, and a lookup about a cookie is not allowed to cancel that.
//
// The numbers are chosen so that each way of sharing the budget fails, with 200 ms to
// spare on either side. A lookup on the deciding context stalls for all 800 ms and
// leaves the revoke none. A deciding bound that began before the lookup would have
// only 400 ms left of it when the revoke begins, and the revoke needs 600. A lookup
// with no deadline at all never returns (and a hang is a failure of this test, not a
// wait).
func TestLogoutAll_AStalledLookupLeavesTheRevokeItsWholeBound(t *testing.T) {
	const (
		lookupBound = 400 * time.Millisecond
		deciding    = 800 * time.Millisecond
		revokeWork  = 600 * time.Millisecond
	)
	a := newAuthRaceAppWith(t, nil, raceOptions{dbTimeout: deciding, followUpTimeout: lookupBound})
	a.store.lockWait = func(ctx context.Context, name string) error {
		switch name {
		case "GetSessionByTokenHash":
			<-ctx.Done()
			return ctx.Err()
		case "ListUserSessions":
			select {
			case <-time.After(revokeWork):
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		return nil
	}

	resp, elapsed := a.postTimed(t, "/auth/logout-all", "{}", raceCurrentToken,
		map[string]string{"X-Test-Acting-User": a.store.user.ID.String()}, 5*time.Second)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200: a lookup that gave up must leave the revoke its whole bound (body %v)", resp.StatusCode, decodeObject(t, resp))
	}
	if cookies := refreshCookies(resp); len(cookies) != 0 {
		t.Errorf("Set-Cookie = %+v, want the cookie left alone: the lookup could not say whose it is", cookies)
	}
	if !a.store.snapshot().IsRevoked {
		t.Error("the session is not revoked")
	}
	if elapsed < lookupBound+revokeWork-50*time.Millisecond || elapsed > 3*time.Second {
		t.Errorf("answered after %v, want about the lookup's %v plus the revoke's %v: the stall and the work are the control that the hooks were on the path",
			elapsed, lookupBound, revokeWork)
	}
	if got := a.store.auditActions(); !reflect.DeepEqual(got, []string{"logout_all"}) {
		t.Errorf("audit actions = %v, want [logout_all]", got)
	}
}

// TestLogoutAll_AStarvedPoolIsA503AfterTheLookupAndTheRevokeHaveBothGivenUp is the
// same bound with the pool, not one statement, gone: with its only connection held,
// the lookup gives up at its own deadline and the revoke at the deciding one. The
// answer is the 503 the revoke has always been, within the documented total and not
// before both bounds have run, with the cookie left alone and nothing revoked — and,
// the pool free again, the same request goes through. Modelled on
// TestLogout_AStarvedPoolIsA503NotAHang.
func TestLogoutAll_AStarvedPoolIsA503AfterTheLookupAndTheRevokeHaveBothGivenUp(t *testing.T) {
	const lookupBound, deciding = 200 * time.Millisecond, 200 * time.Millisecond
	a := newAuthRaceAppWith(t, nil, raceOptions{gateSize: 1, dbTimeout: deciding, followUpTimeout: lookupBound})
	if err := a.gate.acquire(context.Background()); err != nil {
		t.Fatalf("take the pool's only connection: %v", err)
	}
	held := true
	defer func() {
		if held {
			a.gate.release()
		}
	}()
	headers := map[string]string{"X-Test-Acting-User": a.store.user.ID.String()}

	resp, elapsed := a.postTimed(t, "/auth/logout-all", "{}", raceCurrentToken, headers, 5*time.Second)
	body := decodeObject(t, resp)

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 (body %v)", resp.StatusCode, body)
	}
	if msg, _ := body["message"].(string); !strings.Contains(msg, "may still be active") || !strings.Contains(msg, "try again") {
		t.Errorf("message = %q, want it to say the sessions may still be active and that the request can be repeated", msg)
	}
	// Both bounds ran, one after the other: a lookup that shared the deciding bound
	// would have answered after only the second.
	if elapsed < lookupBound+deciding-50*time.Millisecond || elapsed > 3*time.Second {
		t.Errorf("answered after %v, want about the lookup's %v plus the deciding bound's %v", elapsed, lookupBound, deciding)
	}
	if cookies := refreshCookies(resp); len(cookies) != 0 {
		t.Errorf("Set-Cookie = %+v, want the cookie left alone: the caller is still signed in", cookies)
	}
	if n := len(a.store.named("RevokeAllUserSessions")); n != 0 || a.store.snapshot().IsRevoked {
		t.Errorf("RevokeAllUserSessions sent %d times and the session is revoked = %t, want neither", n, a.store.snapshot().IsRevoked)
	}

	a.gate.release()
	held = false
	if again := a.post(t, "/auth/logout-all", raceCurrentToken, headers); again.StatusCode != http.StatusOK {
		t.Fatalf("with the pool free again the sign-out everywhere answered %d, want 200", again.StatusCode)
	}
	if !a.store.snapshot().IsRevoked {
		t.Error("with the pool free again the sign-out did not end the session: the control proves nothing")
	}
}
