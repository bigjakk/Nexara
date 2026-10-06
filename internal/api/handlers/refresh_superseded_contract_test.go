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

	"github.com/bigjakk/nexara/internal/auth"
)

// The 409 refresh_superseded answer is a contract between this server and the SPA, which
// retries a refresh once on exactly that status AND that error code. Neither side can drift
// without a test saying so: the code's literal value, the raw body of the real answer (the
// error handler would have made it {"error":"conflict"}), the client's source, and the proof
// that the 409 is only ever a "try again", never a way past the account checks.

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

// TestRefresh_TheSupersededAnswerCarriesItsOwnEnvelope reads the RAW body of the answer, not
// a helper's view of it: errorHandler derives the envelope's `error` from the status, which for
// 409 is "conflict", so an answer returned as fiber.NewError(409, …) would reach the SPA as
// {"error":"conflict"}, its exact match would never fire, and the race fix would fail safe and
// silently do nothing. The test app's handler renders a fiber.Error's STATUS under `error`, so
// that mistake shows up here too. Both refusal sites are covered: at validation and at the rotation.
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
		{"the code embedded in a longer literal", `if (res.status === 409 && body.error === "x_refresh_superseded_y") {}`, true},
		{"the code as a bare identifier, not a literal", `const refresh_superseded = 1; if (res.status === 409) {}`, true},
		{"the code without the status", `if (body.error === "refresh_superseded") {}`, true},
		{"the status without the code", `if (res.status === 409) {}`, true},
		{"the status only in a comment", "// 409\nif (body.error === \"refresh_superseded\") {}", true},
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

// TestGuard_TheSPAMatchesTheSupersededContract reads frontend/src/lib/api-client.ts and
// requires it to match status 409 and the server's code as a string literal outside comments:
// the cross-language half of the contract (the server's side is
// TestRefreshSupersededCode_IsTheLiteralTheClientMatches plus the raw-body test). It is strict
// and unconditional on purpose, because a guard that can be switched off is off exactly when it
// is needed: the client's handling and the server's answer ship together, so on a tree with
// only the server half it FAILS, correctly. Someone working on one half alone can leave it out
// of their own run with go test's -skip flag.
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

// TestRefresh_TheRetryAfterASupersededAnswerGoesThroughTheNormalChecks proves the 409 is only
// ever a "try again". Each row sends the loser's request (the replaced token) and gets the 409
// whatever the state of the user, because it is answered from the session's rotation history
// alone and grants nothing. Then the RETRY presents the winner's cookie (the session's current
// token) and goes through the ordinary path: a disabled account, a changed role and a missing
// user are each refused 401, the session revoked and nothing issued. The first row is the
// control: with nothing wrong the same retry succeeds, so the refusals are the checks and not
// the harness.
func TestRefresh_TheRetryAfterASupersededAnswerGoesThroughTheNormalChecks(t *testing.T) {
	type row struct {
		name  string
		tweak func(*raceStore)
		audit string // the audit action a refusal writes, if any
		ok    bool   // the account is fine, so the retry succeeds
	}
	rows := make([]row, 0, 1+len(authRefusedAccounts))
	rows = append(rows, row{name: "the account is fine: the retry succeeds", ok: true})
	for _, r := range authRefusedAccounts {
		rows = append(rows, row{name: r.name, tweak: r.tweak, audit: r.audit})
	}

	for _, tt := range rows {
		t.Run(tt.name, func(t *testing.T) {
			// The session was rotated two seconds ago: the previous token is the loser's, the
			// current one is the winner's cookie.
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
			authRequireCookie(t, first, cookieUntouched)
			for _, name := range []string{"GetUserByID", "RotateSessionToken", "RevokeSession", "InsertAuditLog"} {
				if n := len(a.store.named(name)); n != 0 {
					t.Errorf("the 409 sent %s %d times; it is answered from the rotation history alone and must "+
						"neither read the account nor change anything", name, n)
				}
			}
			if began := a.pool.txCount(); began != 0 {
				t.Errorf("the 409 began %d transactions, want 0", began)
			}

			// 2. The retry, with the winner's cookie.
			retry := a.post(t, "/auth/refresh", raceCurrentToken, nil)
			wantStatus, wantCookie := http.StatusUnauthorized, cookieCleared
			if tt.ok {
				wantStatus, wantCookie = http.StatusOK, cookieNew
			}
			body := authRequireStatus(t, retry, wantStatus)
			authRequireCookie(t, retry, wantCookie)
			if _, issued := body["access_token"]; issued != tt.ok {
				t.Errorf("the retry's body carries access_token = %t, want %t: %v", issued, tt.ok, body)
			}
			if got := len(a.store.named("RotateSessionToken")) > 0; got != tt.ok {
				t.Errorf("the retry rotated = %t, want %t", got, tt.ok)
			}
			if got := len(a.store.named("RevokeSession")) > 0; got != !tt.ok {
				t.Errorf("the retry revoked the session = %t, want %t", got, !tt.ok)
			}
			var wantAudits []string
			if tt.audit != "" {
				wantAudits = []string{tt.audit}
			}
			if got := a.store.auditActions(); !reflect.DeepEqual(got, wantAudits) {
				t.Errorf("audit actions = %v, want %v", got, wantAudits)
			}
			if committed, _ := a.pool.only(t).state(); committed != tt.ok {
				t.Errorf("the retry's transaction committed = %t, want %t", committed, tt.ok)
			}
			if stored := a.store.snapshot(); stored.IsRevoked != !tt.ok {
				t.Errorf("the session is revoked = %t, want %t", stored.IsRevoked, !tt.ok)
			}
		})
	}
}
