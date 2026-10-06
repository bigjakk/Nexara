package handlers

import (
	"bytes"
	"cmp"
	"context"
	"go/ast"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	recoverer "github.com/gofiber/fiber/v3/middleware/recover"
	"github.com/google/uuid"

	"github.com/bigjakk/nexara/internal/auth"
)

// Whose sign-out a sign-out is. /auth/logout is authOptional: the refresh cookie is the
// credential, so a browser whose access token has lapsed can still sign out. But the cookie
// jar is shared by all tabs and another tab may have signed someone else in, so the cookie can
// be another user's and the access token is the only thing that says so. A valid one always
// did (authOptional turns it into user_id); one authOptional turned away (expired, not yet
// valid) was ignored, so the cookie alone ended whatever session it named, and the cookie was
// deleted before anything was checked. The harness stands in for authOptional with the
// X-Test-Acting-User header; which tokens count as someone else's is
// auth.AccessTokenIsSomeoneElses' to say, row by row in internal/auth/jwt_refusal_test.go, so
// these rows pin only that the handler wires it, and the guards below keep it a bool.

// logoutJWTSecret is the secret newAuthRaceApp's JWT service signs with, so the handler's
// service accepts the signature of the tokens below. The rows that expect a 403 prove it: were
// the secrets to differ, every one of them would read as forged and answer 200.
const logoutJWTSecret = "test-secret"

// logoutAccessToken is a regular access token for user, signed with secret, that expires ttl
// from now (a negative ttl is a token that has already lapsed).
func logoutAccessToken(t *testing.T, secret string, ttl time.Duration, user uuid.UUID) string {
	t.Helper()
	tok, _, err := auth.NewJWTService(secret, ttl, time.Hour).GenerateAccessToken(user, "alice@example.com", "admin")
	if err != nil {
		t.Fatalf("GenerateAccessToken: %v", err)
	}
	return tok
}

// TestLogout_AnAccessTokenNamesWhoIsSigningOut holds Logout to the rule above. Every row signs
// out the owner's session, by its refresh cookie unless the row says otherwise; what varies is
// what the request says about WHO signs out. A named caller who is not the owner gets 403,
// nothing revoked, no Set-Cookie (the owner's rows are the controls: a sign-out that revoked
// nothing looks like one never wired up). A request that names no one is held to the cookie
// alone: the session ends and the cookie is deleted. A cookie that names no live session never
// reaches the owner check; a body token that is not the cookie leaves the cookie where it is;
// a lookup or a revoke that fails is a 503 and still deletes the cookie.
func TestLogout_AnAccessTokenNamesWhoIsSigningOut(t *testing.T) {
	const noCookie = "-"
	other := uuid.New()
	expiredOf := func(who func(a *authRaceApp) uuid.UUID) func(t *testing.T, a *authRaceApp) string {
		return func(t *testing.T, a *authRaceApp) string {
			return "Bearer " + logoutAccessToken(t, logoutJWTSecret, -time.Hour, who(a))
		}
	}
	ownerOf := func(a *authRaceApp) uuid.UUID { return a.store.user.ID }
	otherOf := func(*authRaceApp) uuid.UUID { return other }
	const (
		ended   = true
		refused = http.StatusForbidden
	)
	tests := []struct {
		name  string
		tweak func(*raceStore)
		// cookie is the refresh cookie the request carries: "" is the session's current token,
		// noCookie none at all. body is the request body, "{}" if empty.
		cookie, body string
		// header is the whole Authorization header the request carries ("" none), and actor
		// stands in for authOptional having validated a token: "owner", "other" or none.
		header func(t *testing.T, a *authRaceApp) string
		actor  string

		want int
		// sent is how many times RevokeSession was sent; ended whether the session is revoked
		// afterwards; cookieWant what the answer does to the cookie.
		sent       int
		ended      bool
		cookieWant cookieOutcome
		// previous is the audit detail of a sign-out that matched the previous token.
		previous bool
	}{
		// An expired token that names someone else than the session's owner.
		{
			name:   "an expired token of another user, on the current cookie",
			header: expiredOf(otherOf),
			want:   refused, cookieWant: cookieUntouched,
		},
		{
			name: "an expired token of another user, on the previous cookie", cookie: racePreviousToken,
			header: expiredOf(otherOf),
			want:   refused, cookieWant: cookieUntouched,
		},
		{
			name: "an expired token of another user, the refresh token in the body", cookie: noCookie,
			body:   `{"refresh_token":"` + raceCurrentToken + `"}`,
			header: expiredOf(otherOf),
			want:   refused, cookieWant: cookieUntouched,
		},
		{
			name:   "an expired token of the owner, on the current cookie",
			header: expiredOf(ownerOf),
			want:   http.StatusOK, sent: 1, ended: ended, cookieWant: cookieCleared,
		},
		{
			name: "an expired token of the owner, on the previous cookie", cookie: racePreviousToken,
			header: expiredOf(ownerOf),
			want:   http.StatusOK, sent: 1, ended: ended, cookieWant: cookieCleared, previous: true,
		},
		{
			// The header is read through the one parser: a second space is part of the token.
			name: "an expired token of another user after a second space in the header",
			header: func(t *testing.T, _ *authRaceApp) string {
				return "Bearer  " + logoutAccessToken(t, logoutJWTSecret, -time.Hour, other)
			},
			want: refused, cookieWant: cookieUntouched,
		},
		{
			// The one parser reads the scheme case-insensitively, as the middleware does.
			name: "an expired token of another user under a lowercase scheme",
			header: func(t *testing.T, _ *authRaceApp) string {
				return "bearer " + logoutAccessToken(t, logoutJWTSecret, -time.Hour, other)
			},
			want: refused, cookieWant: cookieUntouched,
		},

		// A valid token: authOptional's user_id.
		{
			name: "a valid token of another user",
			header: func(t *testing.T, _ *authRaceApp) string {
				return "Bearer " + logoutAccessToken(t, logoutJWTSecret, time.Hour, other)
			},
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
			header: func(t *testing.T, _ *authRaceApp) string {
				return "Bearer " + logoutAccessToken(t, logoutJWTSecret+"-not", -time.Hour, other)
			},
			want: http.StatusOK, sent: 1, ended: ended, cookieWant: cookieCleared,
		},
		{
			name: "an API key that did not authenticate",
			header: func(*testing.T, *authRaceApp) string {
				return "Bearer nxra_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
			},
			want: http.StatusOK, sent: 1, ended: ended, cookieWant: cookieCleared,
		},
		{
			name: "an expired token of another user under a scheme that is not Bearer",
			header: func(t *testing.T, _ *authRaceApp) string {
				return "Basic " + logoutAccessToken(t, logoutJWTSecret, -time.Hour, other)
			},
			want: http.StatusOK, sent: 1, ended: ended, cookieWant: cookieCleared,
		},

		// A cookie that names no live session never reaches the owner check.
		{
			name: "an expired token of another user, a cookie no session holds", cookie: "a-token-nobody-issued",
			header: expiredOf(otherOf),
			want:   http.StatusOK, cookieWant: cookieCleared,
		},
		{
			name:   "an expired token of another user, a session that has expired",
			tweak:  func(s *raceStore) { s.session.ExpiresAt = time.Now().Add(-time.Minute) },
			header: expiredOf(otherOf),
			want:   http.StatusOK, cookieWant: cookieCleared,
		},
		{
			name: "an expired token of another user, no cookie at all", cookie: noCookie,
			header: expiredOf(otherOf),
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
			tweak:  func(s *raceStore) { s.currentErr = errRaceTransient },
			header: expiredOf(otherOf),
			want:   http.StatusServiceUnavailable, cookieWant: cookieCleared,
		},
		{
			name:   "a revoke that could not be made, with an expired token of the owner",
			tweak:  func(s *raceStore) { s.revokeErr = errRaceTransient },
			header: expiredOf(ownerOf),
			want:   http.StatusServiceUnavailable, sent: 1, cookieWant: cookieCleared,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := newAuthRaceApp(t, tt.tweak)
			// The session's Redis row, which a sign-out that ends the session deletes and one
			// that does not must leave.
			rowKey := a.sessionKey()
			a.redis.Set(rowKey, "{}")

			headers := map[string]string{}
			if tt.header != nil {
				headers["Authorization"] = tt.header(t, a)
			}
			switch tt.actor {
			case "owner":
				headers["X-Test-Acting-User"] = a.store.user.ID.String()
			case "other":
				headers["X-Test-Acting-User"] = other.String()
			}
			cookie := cmp.Or(tt.cookie, raceCurrentToken)
			if tt.cookie == noCookie {
				cookie = ""
			}

			resp := a.postBody(t, "/auth/logout", cmp.Or(tt.body, "{}"), cookie, headers)

			if resp.StatusCode != tt.want {
				t.Fatalf("status = %d, want %d", resp.StatusCode, tt.want)
			}
			authRequireCookie(t, resp, tt.cookieWant)
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
			// Redis follows the session: gone when a sign-out ended it, there when nothing did.
			if gone := !a.redis.Exists(rowKey); gone != (tt.sent == 1 && tt.ended) {
				t.Errorf("the session's Redis row is gone = %t, want %t", gone, tt.sent == 1 && tt.ended)
			}
		})
	}
}

// TestLogout_WithoutAJWTServiceTheBearerTokenNamesNoOne pins the handler's own guard for a
// service it was not given, which authOptional has too: no service, no parse. The sign-out
// is then held to the cookie, and must not panic on the token it would have handed to the
// parser. The request goes through a route that recovers, so a panic fails this test with a
// 500 instead of taking the test binary down.
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

// TestLogout_ARefusedSignOutLeavesTheOtherUsersSessionToTheirRefresh follows the refusal one
// step further: after the 403 the owner's refresh still works, with the very cookie that was
// sent. A refusal that left the session live and the cookie gone would pass every row above
// and still sign the owner out at their next refresh.
func TestLogout_ARefusedSignOutLeavesTheOtherUsersSessionToTheirRefresh(t *testing.T) {
	a := newAuthRaceApp(t, nil)

	resp := a.post(t, "/auth/logout", raceCurrentToken, map[string]string{
		"Authorization": "Bearer " + logoutAccessToken(t, logoutJWTSecret, -time.Hour, uuid.New()),
	})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("control: the sign-out answered %d, want 403", resp.StatusCode)
	}

	resp = a.post(t, "/auth/refresh", raceCurrentToken, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the owner's refresh answered %d after a refused sign-out, want 200", resp.StatusCode)
	}
	authRequireCookie(t, resp, cookieNew)
}

// guardOnlyReachedFrom is the static check behind the guards below: outside tests, name may
// be mentioned in exactly one place, the function allowedFunc of allowedFile, and once there.
// Both spellings are watched, a call and a bare reference, and it fails if it cannot see the
// one use it permits: a guard that finds nothing proves nothing, however a rename blinds it.
func guardOnlyReachedFrom(t *testing.T, name, allowedFile, allowedFunc, why string) {
	t.Helper()
	fset, sources := authParsedSources(t)
	allowedHits := 0
	for _, src := range sources {
		inAllowedFile := strings.HasSuffix(src.rel, allowedFile)
		for _, decl := range src.file.Decls {
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
				t.Errorf("%s: %s reaches %s; only %s in %s may: %s", fset.Position(sel.Pos()),
					cmp.Or(enclosing, "(package level)"), name, allowedFunc, allowedFile, why)
				return true
			})
		}
	}
	if allowedHits != 1 {
		t.Fatalf("found %d references to %s in %s of %s, want exactly 1: the guard cannot see what it exists "+
			"to restrict", allowedHits, name, allowedFunc, allowedFile)
	}
}

// TestGuard_AccessTokenIsSomeoneElsesIsOnlyReachedFromTheSignOutCheck keeps the one function
// that reads whom a token the server has stopped vouching for was issued to from becoming a
// way to be signed in. AccessTokenIsSomeoneElses verifies a token's signature and ignores
// its time claims: right for refusing a sign-out that is for someone else's session, wrong
// for anything that grants, since a middleware that "tolerated" an expired token would sign
// in anyone who ever held one. Every behavioural test passes the day that happens, so a
// static check holds it: the function is named in one place, signOutIsSomeoneElses.
func TestGuard_AccessTokenIsSomeoneElsesIsOnlyReachedFromTheSignOutCheck(t *testing.T) {
	guardOnlyReachedFrom(t, "AccessTokenIsSomeoneElses", "internal/api/handlers/auth.go", "signOutIsSomeoneElses",
		"it answers for a token that may have EXPIRED, which proves who the token was issued to and nothing about "+
			"who holds it: it may refuse a sign-out and must never decide who is signed in (ValidateAccessToken does "+
			"that, and refuses an expired token)")
}

// TestGuard_SignOutIsSomeoneElsesIsOnlyReachedFromLogout is the same fence one layer out:
// the next function that wants to know "who is this?" (LogoutAll, whose caller authRequired
// already named; a new handler taking the answer for an identity) must not be able to borrow
// the read of the Authorization header that exists to turn a sign-out away.
func TestGuard_SignOutIsSomeoneElsesIsOnlyReachedFromLogout(t *testing.T) {
	guardOnlyReachedFrom(t, "signOutIsSomeoneElses", "internal/api/handlers/auth.go", "Logout",
		"it reads a bearer token that may be expired, forged or someone else's, and is a refusal for a sign-out "+
			"and nothing else: a handler that needs to know who is calling takes it from authRequired's user_id")
}

// TestGuard_AccessTokenIsSomeoneElsesReturnsOnlyABool pins the property the two guards above
// lean on, as TestGuard_RefusalSparesCookieReturnsOnlyABool does for its own: the question
// is answered with a bool and nothing else, so its match cannot be turned into an identity
// or a token. A change that makes it return more has to change this test, so it is a
// decision rather than a convenience.
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

// TestGuard_SignOutIsSomeoneElsesReturnsOnlyABool is the same pin for the handler's side. The
// method is unexported, so reflection reaches it through a method expression, which compiles
// for any signature and so reports a changed one at run time, by name.
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
	// cookie is the refresh cookie the request carries, "" for none. actor is whose access
	// token made the request: "owner" is the account whose session the harness holds, "other"
	// a different one.
	cookie string
	actor  string
	// startsRevoked marks a row whose tweak revokes the session before the request, which
	// is what makes "the owner's session is revoked afterwards" true of a row the owner
	// did not ask for.
	startsRevoked bool

	cookieWant cookieOutcome
	// lookups is how many statements asked which session the cookie names.
	lookups int
}

// TestLogoutAll_TheCookieIsClearedOnlyWhenItIsTheCallers holds LogoutAll to the same care as
// Logout: it ends every session of the CALLER, and deletes the cookie in the request only when
// it is the caller's. The harness holds one session, the owner's: a request by the owner with
// its cookie (current or previous token) is the caller's own; by someone else it is another
// user's and must neither revoke the owner's session nor delete the cookie. Whatever names no
// live session is dead and is deleted. A lookup that could not be made leaves the cookie alone
// and the revoke goes ahead: the cookie is only housekeeping once the sessions have ended.
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
			tweak: func(s *raceStore) { s.session.IsRevoked = true }, startsRevoked: true, cookieWant: cookieCleared, lookups: 2,
		},
		{
			name: "another user's cookie, for a session that has expired", cookie: raceCurrentToken, actor: "other",
			tweak:      func(s *raceStore) { s.session.ExpiresAt = time.Now().Add(-time.Minute) },
			cookieWant: cookieCleared, lookups: 2,
		},
		{
			// Rotated away longer ago than the window, that token signs nothing out, so it
			// names no live session whoever's the session is.
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
			caller := a.store.user.ID
			if tt.actor == "other" {
				caller = uuid.New()
			}

			resp := a.post(t, "/auth/logout-all", tt.cookie, map[string]string{"X-Test-Acting-User": caller.String()})

			authRequireStatus(t, resp, http.StatusOK)
			authRequireCookie(t, resp, tt.cookieWant)
			// The caller's sessions, and only theirs, were revoked.
			revokes := a.store.named("RevokeAllUserSessions")
			if len(revokes) != 1 || revokes[0].args[0] != caller {
				t.Fatalf("RevokeAllUserSessions = %+v, want one revoke of the caller's sessions (%v)", revokes, caller)
			}
			if got := a.store.auditActions(); !reflect.DeepEqual(got, []string{"logout_all"}) {
				t.Errorf("audit actions = %v, want [logout_all]", got)
			}
			// The owner's session ends exactly when the owner asked: nobody else's sign-out
			// everywhere reaches it, whatever cookie it carried.
			if want := tt.actor == "owner" || tt.startsRevoked; a.store.snapshot().IsRevoked != want {
				t.Errorf("the owner's session is revoked = %t afterwards, want %t", !want, want)
			}
			// The lookup asked about the cookie's token, and not once more than needed.
			asked := len(a.store.named("GetSessionByTokenHash")) + len(a.store.named("GetSessionByPreviousTokenHash"))
			if asked != tt.lookups {
				t.Errorf("%d statements asked which session the cookie names, want %d", asked, tt.lookups)
			}
		})
	}
}

// TestLogoutAll_LooksAtTheCookieBeforeItRevokes pins the order, and only the order, because
// that is all anyone can observe of it: the lookup finds live sessions only, so asked after
// the revoke the caller's own cookie would read as a session that no longer exists ("no live
// session", which also clears) and the same answer would come out for a reason that is not
// the cookie's, with the owner test never run on the caller's own session. The rows of
// TestLogoutAll_TheCookieIsClearedOnlyWhenItIsTheCallers cannot tell the two apart by outcome.
// A hook on the store says whether the lookup saw the session live or already revoked.
func TestLogoutAll_LooksAtTheCookieBeforeItRevokes(t *testing.T) {
	a := newAuthRaceApp(t, nil)
	var sawRevoked []bool
	a.store.lockWait = func(_ context.Context, name string) error {
		if name == "GetSessionByTokenHash" {
			sawRevoked = append(sawRevoked, a.store.snapshot().IsRevoked)
		}
		return nil
	}

	authRequireStatus(t, a.post(t, "/auth/logout-all", raceCurrentToken, map[string]string{"X-Test-Acting-User": a.store.user.ID.String()}), http.StatusOK)

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

// TestLogoutAll_ALookupThatFailsIsLoggedAndTheRevokeStillHappens pins the two things the
// failed lookup leaves behind: a log line (a Warn when the database is away, an Error for a
// defect, naming the caller and nothing that could be replayed) and the revoke, which the
// caller asked for and which a lookup about a cookie has no business cancelling.
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

			authRequireStatus(t, a.post(t, "/auth/logout-all", raceCurrentToken, map[string]string{"X-Test-Acting-User": a.store.user.ID.String()}), http.StatusOK)

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

// TestLogoutAll_AFailedRevokeStillLeavesEveryCookieAlone is the 503 and the 500 of the
// sign-out everywhere with a cookie in the request: whatever the cookie names, nothing was
// revoked, the caller is still signed in, and so the cookie stays. The 503 says the sessions
// may still be active and that the request can be repeated; the 500 names the failure.
func TestLogoutAll_AFailedRevokeStillLeavesEveryCookieAlone(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want int
		msg  []string // what the message must say
	}{
		{"the database is away", errRaceTransient, http.StatusServiceUnavailable, []string{"may still be active", "try again"}},
		{"a defect", errRaceBug, http.StatusInternalServerError, []string{"Failed to revoke sessions"}},
	} {
		for _, actor := range []string{"owner", "other"} {
			t.Run(tc.name+", "+actor+" asked", func(t *testing.T) {
				a := newAuthRaceApp(t, func(s *raceStore) { s.revokeAllErr = tc.err })
				caller := a.store.user.ID
				if actor == "other" {
					caller = uuid.New()
				}

				resp := a.post(t, "/auth/logout-all", raceCurrentToken, map[string]string{"X-Test-Acting-User": caller.String()})

				msg, _ := authRequireStatus(t, resp, tc.want)["message"].(string)
				for _, want := range tc.msg {
					if !strings.Contains(msg, want) {
						t.Errorf("message = %q, want it to say %q", msg, want)
					}
				}
				authRequireCookie(t, resp, cookieUntouched)
				if a.store.snapshot().IsRevoked {
					t.Error("the session is revoked although the revoke failed")
				}
			})
		}
	}
}

// postCookieLines sends a POST carrying exactly the Cookie header lines it is given, one
// header line each (a browser sends one line; a proxy or an HTTP/2 hop may send several), and
// no other cookie.
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

// TestLogout_AJarWithMoreThanOneRefreshCookieIsClearedNotSpared holds the case in which Logout
// does not spare a cookie it otherwise would. A page on a sibling subdomain can plant a refresh
// cookie of the same name, the browser sends both in an order the server does not control, and
// the server reads the first: if that is a planted cookie naming a session that is not the
// signer's, the 403 would spare "the cookie", not the host-only one a deletion removes, and the
// real credential would stay through the sign-out meant to remove it. So with more than one
// nothing is spared and the cookie is deleted, while the refusal is not softened. The first row
// of each kind is the control (one cookie is spared). The name is matched exactly, as the
// handlers read it: other names, a case difference and a pair fasthttp drops are not refresh cookies.
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
			authRequireCookie(t, resp, tt.cookieWant)
			if got := len(a.store.named("RevokeSession")); got != tt.sent {
				t.Errorf("RevokeSession sent %d times, want %d: the refusal must not be softened by the cookie rule", got, tt.sent)
			}
			if got := a.store.snapshot().IsRevoked; got != (tt.sent == 1) {
				t.Errorf("session revoked = %t afterwards, want %t", got, tt.sent == 1)
			}
		})
	}
}

// TestLogoutAll_AJarWithMoreThanOneRefreshCookieIsClearedNotLookedAt is the same ambiguity
// for the sign-out everywhere. The lookup would read the first cookie, which may be the
// planted one, and "another user's, leave it" would then spare a cookie that is not the one a
// deletion removes. So the cookie is cleared without being looked at (no statement asks
// which session it names) and the caller's sessions are revoked all the same. The first row
// is the control: with one refresh cookie the same request leaves the other user's cookie
// alone, after one lookup. How a cookie is counted as a refresh cookie is pinned above.
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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := newAuthRaceApp(t, nil)
			caller := uuid.New()
			if tt.asOwner {
				caller = a.store.user.ID
			}

			resp := a.postCookieLines(t, "/auth/logout-all", "{}", tt.lines, map[string]string{"X-Test-Acting-User": caller.String()})

			authRequireStatus(t, resp, http.StatusOK)
			authRequireCookie(t, resp, tt.cookieWant)
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

// TestLogout_ARefusalIsLoggedWithTheSessionAndNothingElse pins the line a 403 leaves, the
// only trace of it: the caller is told "no", and whoever finds that a sign-out did nothing
// must be able to see why. It names the session and its user, never a token or a hash, and
// never the caller (signOutIsSomeoneElses answers a bool, so the identity is not there to
// log). The control is a sign-out that works, which leaves no such line.
func TestLogout_ARefusalIsLoggedWithTheSessionAndNothingElse(t *testing.T) {
	const line = "logout: refused"
	other := uuid.New()

	logs := captureProductionLog(t)
	a := newAuthRaceApp(t, nil)
	bearer := logoutAccessToken(t, logoutJWTSecret, -time.Hour, other)
	authRequireStatus(t, a.post(t, "/auth/logout", raceCurrentToken, map[string]string{"Authorization": "Bearer " + bearer}), http.StatusForbidden)
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
	authRequireStatus(t, b.post(t, "/auth/logout", raceCurrentToken, nil), http.StatusOK)
	if !b.store.snapshot().IsRevoked {
		t.Fatal("control: the sign-out did not end the session")
	}
	if strings.Contains(quiet.String(), line) {
		t.Errorf("a sign-out that worked logged a refusal: %q", quiet.String())
	}
}

// TestLogoutAll_TheCookieLookupHasADeadlineOfItsOwn pins what the cookie's lookup runs
// under, and what the revoke runs under after it. The lookup is one statement the revoke
// does not depend on, so it gets the follow-up bound from a fresh start and the deciding
// bound begins only afterwards, whole. The lookup here stalls until its own deadline, so the
// revoke's deadline must fall a whole deciding bound after the lookup's: one begun before the
// lookup falls at most deciding-lookup after it, and a lookup on the deciding context never
// leaves the revoke any. Deadlines are compared with deadlines, never with the time left when
// a statement runs, which a loaded box shortens; the gap between two deadlines does not move.
func TestLogoutAll_TheCookieLookupHasADeadlineOfItsOwn(t *testing.T) {
	const deciding, lookup = 10 * time.Second, 400 * time.Millisecond
	a := newAuthRaceAppWith(t, nil, raceOptions{dbTimeout: deciding, followUpTimeout: lookup})

	type statement struct {
		name            string
		began, deadline time.Time // deadline is zero when the statement's context had none
	}
	var (
		mu      sync.Mutex
		seen    []statement
		stalled bool
	)
	a.store.lockWait = func(ctx context.Context, name string) error {
		d, _ := ctx.Deadline()
		mu.Lock()
		seen = append(seen, statement{name: name, began: time.Now(), deadline: d})
		mu.Unlock()
		if name != "GetSessionByTokenHash" {
			return nil
		}
		<-ctx.Done()
		mu.Lock()
		stalled = true
		mu.Unlock()
		return ctx.Err()
	}

	sent := time.Now()
	resp := a.post(t, "/auth/logout-all", raceCurrentToken, map[string]string{"X-Test-Acting-User": a.store.user.ID.String()})

	authRequireStatus(t, resp, http.StatusOK) // a lookup that gave up must leave the revoke its whole bound
	authRequireCookie(t, resp, cookieUntouched)
	if !a.store.snapshot().IsRevoked {
		t.Error("the session is not revoked")
	}
	if got := a.store.auditActions(); !reflect.DeepEqual(got, []string{"logout_all"}) {
		t.Errorf("audit actions = %v, want [logout_all]", got)
	}
	mu.Lock()
	defer mu.Unlock()
	var lookupEnds time.Time // the deadline of the first lookup
	var lookups, revokes int
	for _, st := range seen {
		switch st.name {
		case "GetSessionByTokenHash", "GetSessionByPreviousTokenHash":
			lookups++
			if st.deadline.IsZero() || !st.deadline.After(sent) || st.deadline.Sub(st.began) > lookup {
				t.Errorf("%s began with deadline %v, request sent %v: want a deadline of its own no more than %v away: the lookup has one of its own",
					st.name, st.deadline, sent, lookup)
			}
			if lookupEnds.IsZero() {
				lookupEnds = st.deadline
			}
		}
	}
	for _, st := range seen {
		switch st.name {
		case "BumpUserAuthEpoch", "ListUserSessions", "RevokeAllUserSessions":
			revokes++
			// Half a lookup bound of margin on each side of the gap.
			if st.deadline.IsZero() || st.deadline.Sub(lookupEnds) < deciding-lookup/2 {
				t.Errorf("%s has its deadline %v after the lookup's (none: %t), want a whole %v: the lookup must not spend the revoke's bound",
					st.name, st.deadline.Sub(lookupEnds), st.deadline.IsZero(), deciding)
			}
		}
	}
	if lookups == 0 || revokes == 0 || !stalled {
		t.Fatalf("saw %d lookups, %d revoke statements, stalled = %t (%+v): the probe is not on the path, so this test proved nothing", lookups, revokes, stalled, seen)
	}
}
