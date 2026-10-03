package handlers

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/bigjakk/nexara/internal/auth"
)

// The 409 refresh_superseded answer is a contract between this server and the
// SPA: the SPA retries a refresh once on exactly that status AND that error code.
// Everything here exists so that neither side can drift without a test saying so.
//
//   - TestRefreshSupersededCode_IsTheLiteralTheClientMatches pins the code's value.
//   - TestRefresh_TheSupersededAnswerCarriesItsOwnEnvelope reads the raw body of
//     the real answer, because errorHandler would have made it {"error":"conflict"}.
//   - TestGuard_TheSPAMatchesTheSupersededContract reads the client's source.
//   - TestRefresh_TheRetryAfterASupersededAnswerGoesThroughTheNormalChecks proves
//     the 409 is only ever a "try again", never a way past the account checks.

// TestRefreshSupersededCode_IsTheLiteralTheClientMatches pins the value of the
// code. The other tests are written against the constant, so without this one
// the code could be renamed on the server alone and every one of them would
// follow it — while the SPA, matching the old spelling, stopped retrying. The
// status is pinned beside it: the client matches both.
func TestRefreshSupersededCode_IsTheLiteralTheClientMatches(t *testing.T) {
	if RefreshSupersededCode != "refresh_superseded" {
		t.Errorf("RefreshSupersededCode = %q; the SPA matches the literal \"refresh_superseded\", so changing it "+
			"here without changing frontend/src/lib/api-client.ts in the same change silently disables the retry",
			RefreshSupersededCode)
	}
}

// TestRefresh_TheSupersededAnswerCarriesItsOwnEnvelope reads the RAW body of the
// answer and not a helper's view of it. errorHandler (internal/api/errors.go)
// derives the envelope's `error` from the status, and for 409 that is "conflict":
// an answer returned as fiber.NewError(409, …) would reach the SPA as
// {"error":"conflict"}, the SPA's exact match would never fire, and the race fix
// would fail safe — as an ordinary failure — and silently do nothing.
//
// The test app's own error handler renders a fiber.Error's STATUS under `error`,
// so that mistake shows up here too, as 409 rather than the code. Both refusal
// sites are covered: the one at validation (the winner had already committed) and
// the one at the rotation (it had not).
func TestRefresh_TheSupersededAnswerCarriesItsOwnEnvelope(t *testing.T) {
	for _, tc := range []struct {
		name   string
		cookie string
		tweak  func(*raceStore)
	}{
		{"refused at validation: the token was replaced a moment ago", racePreviousToken, raceRotatedAgo(2 * time.Second)},
		{"refused at the rotation: another refresh got to the row first", raceCurrentToken, raceWinnerRotatesFirst},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := newAuthRaceApp(t, tc.tweak)

			resp := a.post(t, "/auth/refresh", tc.cookie, nil)
			raw, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatalf("read body: %v", err)
			}

			if resp.StatusCode != http.StatusConflict {
				t.Fatalf("status = %d, want 409: %s", resp.StatusCode, raw)
			}
			if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
				t.Errorf("Content-Type = %q, want JSON", ct)
			}

			// The bytes, spelled out: the SPA reads them with JSON.parse, but this is
			// what a human grepping a capture sees.
			if want := `"error":"` + RefreshSupersededCode + `"`; !bytes.Contains(raw, []byte(want)) {
				t.Errorf("the raw body does not contain %s: %s", want, raw)
			}

			var env map[string]any
			if err := json.Unmarshal(raw, &env); err != nil {
				t.Fatalf("the body is not a JSON object: %v: %s", err, raw)
			}
			if got, ok := env["error"].(string); !ok || got != RefreshSupersededCode {
				t.Errorf("error = %#v, want the string %q", env["error"], RefreshSupersededCode)
			}
			if msg, ok := env["message"].(string); !ok || msg == "" {
				t.Errorf("message = %#v, want plain text telling the caller to retry", env["message"])
			}
			// The same envelope as every other error: two keys, nothing more — in
			// particular no token of any kind.
			keys := make([]string, 0, len(env))
			for k := range env {
				keys = append(keys, k)
			}
			slices.Sort(keys)
			if !slices.Equal(keys, []string{"error", "message"}) {
				t.Errorf("envelope keys = %v, want exactly [error message]", keys)
			}
		})
	}
}

// stripTSComments removes // and /* */ comments from TypeScript source well
// enough for a literal search: a mention in a comment is not a handler. A "//"
// preceded by ':' is left alone, so a URL in a string is not cut in half.
func stripTSComments(src string) string {
	src = regexp.MustCompile(`(?s)/\*.*?\*/`).ReplaceAllString(src, "")
	return regexp.MustCompile(`(^|[^:])//[^\n]*`).ReplaceAllString(src, "$1")
}

// spaMatchesSupersededContract reports why client source does NOT handle the
// answer, or nil when it does: the status 409 appears as a token, and the code
// appears as a quoted string literal — outside every comment, where a mention
// could not make anything retry.
func spaMatchesSupersededContract(src, code string) error {
	code409 := regexp.MustCompile(`\b409\b`)
	literal := regexp.MustCompile("[\"'`]" + regexp.QuoteMeta(code) + "[\"'`]")

	stripped := stripTSComments(src)
	var problems []string
	if !code409.MatchString(stripped) {
		problems = append(problems, "the status 409 appears nowhere outside comments")
	}
	if !literal.MatchString(stripped) {
		problems = append(problems, fmt.Sprintf("the string literal %q appears nowhere outside comments", code))
	}
	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "; "))
	}
	return nil
}

// TestSPAContractMatcher gives the client check its controls. A check that is
// only ever run against the one file it guards cannot be seen to fail, so this
// runs it on source written to pass and on source written to fail — the typo, the
// comment, the missing status — and requires each verdict.
func TestSPAContractMatcher(t *testing.T) {
	const code = "refresh_superseded"
	tests := []struct {
		name    string
		src     string
		wantErr bool
	}{
		{"both, double-quoted", `if (res.status === 409 && body.error === "refresh_superseded") { retry(); }`, false},
		{"both, single-quoted constant", "const SUPERSEDED = 'refresh_superseded';\nif (status === 409) {}", false},
		{"both, in a template literal", "const c = `refresh_superseded`; status == 409", false},
		{"the code only in a line comment", "// the server answers 409 with \"refresh_superseded\"\nif (status === 409) {}", true},
		{"the code in a line comment after real code", "if (status === 409) {} // \"refresh_superseded\"", true},
		{"the code only in a block comment", "/* \"refresh_superseded\" */ if (status === 409) {}", true},
		{"a typo in the code", `if (res.status === 409 && body.error === "refresh_supersede") {}`, true},
		{"a different spelling of the code", `if (res.status === 409 && body.error === "refresh-superseded") {}`, true},
		{"the code embedded in a longer literal", `if (res.status === 409 && body.error === "x_refresh_superseded_y") {}`, true},
		{"the code as a bare identifier, not a literal", `const refresh_superseded = 1; if (res.status === 409) {}`, true},
		{"the code without the status", `if (body.error === "refresh_superseded") {}`, true},
		{"the status without the code", `if (res.status === 409) {}`, true},
		{"the status only in a comment", "// 409\nif (body.error === \"refresh_superseded\") {}", true},
		{"neither", `export const x = 1;`, true},
		{"empty", ``, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := spaMatchesSupersededContract(tt.src, code)
			if (err != nil) != tt.wantErr {
				t.Errorf("spaMatchesSupersededContract = %v, wantErr %t", err, tt.wantErr)
			}
		})
	}
}

// TestGuard_TheSPAMatchesTheSupersededContract reads frontend/src/lib/api-client.ts
// and requires it to match status 409 and the server's code, as a string literal
// outside comments. It is the cross-language half of the contract: the server's
// side is TestRefreshSupersededCode_IsTheLiteralTheClientMatches plus the raw-body
// test, and this is what stops the client's spelling drifting from it.
//
// It is strict and unconditional on purpose. Nothing in the code and no
// environment variable can switch it off, because a guard that can be switched off
// is off exactly when it is needed. The client's handling and the server's answer
// ship together, so on a tree that has only the server half this test FAILS,
// correctly. Someone working on one half alone can leave it out of their own run
// with go test's -skip flag, which is an explicit request on that command line and
// leaves no way for it to be skipped anywhere else.
func TestGuard_TheSPAMatchesTheSupersededContract(t *testing.T) {
	path := filepath.Join(repoRoot, "frontend", "src", "lib", "api-client.ts")
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if err := spaMatchesSupersededContract(string(src), RefreshSupersededCode); err != nil {
		t.Errorf("%s does not handle the answer the server sends on a concurrent refresh: %v. The SPA must retry a "+
			"refresh once on status 409 with error %q; the client's handling and the server's answer ship together",
			path, err, RefreshSupersededCode)
	}
}

// TestRefresh_TheRetryAfterASupersededAnswerGoesThroughTheNormalChecks proves the
// 409 is only ever a "try again" and never a way past the account checks.
//
// Each row sends the loser's request (the replaced token) and gets the 409 — with
// the user in whatever state the row sets, because the 409 does not look at the
// user: it is answered from the session's rotation history alone, so it grants
// nothing and says nothing about the account. Then comes the RETRY, which
// presents the cookie the winner set — the session's current token — and goes
// through the ordinary path: a disabled account, a changed role and a missing
// user are each refused with a 401, the session is revoked, and nothing is
// issued. The first row is the control: with nothing wrong with the account the
// same retry succeeds, so the refusals below are the checks and not the harness.
func TestRefresh_TheRetryAfterASupersededAnswerGoesThroughTheNormalChecks(t *testing.T) {
	tests := []struct {
		name  string
		tweak func(*raceStore)

		wantStatus    int
		wantCookie    cookieOutcome
		wantRotation  bool
		wantCommitted bool
		wantRevoke    bool
		wantAudits    []string
	}{
		{
			name:       "the account is fine: the retry succeeds",
			wantStatus: http.StatusOK, wantCookie: cookieNew, wantRotation: true, wantCommitted: true,
		},
		{
			name:       "the account was disabled",
			tweak:      func(s *raceStore) { s.user.IsActive = false },
			wantStatus: http.StatusUnauthorized, wantCookie: cookieCleared, wantRevoke: true,
		},
		{
			name:       "the user's role changed",
			tweak:      func(s *raceStore) { s.user.Role = "viewer" },
			wantStatus: http.StatusUnauthorized, wantCookie: cookieCleared, wantRevoke: true,
			wantAudits: []string{"refresh_denied_role_changed"},
		},
		{
			name:       "the user no longer exists",
			tweak:      func(s *raceStore) { s.userErr = pgx.ErrNoRows },
			wantStatus: http.StatusUnauthorized, wantCookie: cookieCleared, wantRevoke: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// The session was rotated two seconds ago: the previous token is the
			// loser's, the current one is the winner's cookie.
			const rotatedAgo = 2 * time.Second
			if rotatedAgo >= auth.ConcurrentRefreshTolerance {
				t.Fatalf("a rotation %v ago is not inside the tolerance (%v), so the loser would not get a 409 and "+
					"this test would be about something else", rotatedAgo, auth.ConcurrentRefreshTolerance)
			}
			a := newAuthRaceApp(t, func(s *raceStore) {
				raceRotatedAgo(rotatedAgo)(s)
				if tt.tweak != nil {
					tt.tweak(s)
				}
			})

			// 1. The loser. A 409 that touched nothing.
			first := a.post(t, "/auth/refresh", racePreviousToken, nil)
			if first.StatusCode != http.StatusConflict {
				t.Fatalf("the loser's answer = %d, want 409", first.StatusCode)
			}
			if cookies := refreshCookies(first); len(cookies) != 0 {
				t.Errorf("the 409 set cookies %+v, want the jar left alone", cookies)
			}
			for _, name := range []string{"GetUserByID", "RotateSessionToken", "RevokeSession", "InsertAuditLog"} {
				if n := len(a.store.named(name)); n != 0 {
					t.Errorf("the 409 sent %s %d times; it is answered from the rotation history alone and must "+
						"neither read the account nor change anything", name, n)
				}
			}
			a.pool.mu.Lock()
			began := len(a.pool.txs)
			a.pool.mu.Unlock()
			if began != 0 {
				t.Errorf("the 409 began %d transactions, want 0", began)
			}

			// 2. The retry, with the winner's cookie.
			retry := a.post(t, "/auth/refresh", raceCurrentToken, nil)
			body := decodeObject(t, retry)

			if retry.StatusCode != tt.wantStatus {
				t.Fatalf("the retry's answer = %d, want %d (body %v)", retry.StatusCode, tt.wantStatus, body)
			}
			cookies := refreshCookies(retry)
			switch tt.wantCookie {
			case cookieNew:
				if len(cookies) != 1 || cookies[0].Value == "" || cookieDeleted(cookies[0]) {
					t.Errorf("the retry's Set-Cookie = %+v, want one fresh refresh cookie", cookies)
				}
			case cookieCleared:
				if len(cookies) != 1 || !cookieDeleted(cookies[0]) {
					t.Errorf("the retry's Set-Cookie = %+v, want the cookie deleted", cookies)
				}
			}
			if _, issued := body["access_token"]; issued != (tt.wantStatus == http.StatusOK) {
				t.Errorf("the retry's body carries access_token = %t, want %t: %v", issued, tt.wantStatus == http.StatusOK, body)
			}
			if got := len(a.store.named("RotateSessionToken")) > 0; got != tt.wantRotation {
				t.Errorf("the retry rotated = %t, want %t", got, tt.wantRotation)
			}
			if got := len(a.store.named("RevokeSession")) > 0; got != tt.wantRevoke {
				t.Errorf("the retry revoked the session = %t, want %t", got, tt.wantRevoke)
			}
			if got := a.store.auditActions(); !reflect.DeepEqual(got, tt.wantAudits) {
				t.Errorf("audit actions = %v, want %v", got, tt.wantAudits)
			}
			committed, _ := a.pool.only(t).state()
			if committed != tt.wantCommitted {
				t.Errorf("the retry's transaction committed = %t, want %t", committed, tt.wantCommitted)
			}
			if stored := a.store.snapshot(); tt.wantRevoke != stored.IsRevoked {
				t.Errorf("the session is revoked = %t, want %t", stored.IsRevoked, tt.wantRevoke)
			}
		})
	}
}
