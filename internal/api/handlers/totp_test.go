package handlers

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/pquerna/otp/totp"
	"github.com/redis/go-redis/v9"

	"github.com/bigjakk/nexara/internal/api/apischema"
	"github.com/bigjakk/nexara/internal/auth"
)

// totpTestKey is a valid 64-hex (32-byte) AES key shared with the auth tests.
const totpTestKey = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

// newTOTPTestEnv builds a TOTPHandler wired to a miniredis instance with
// queries == nil. Tests must avoid handler paths that touch the DB.
func newTOTPTestEnv(t *testing.T) (*TOTPHandler, *redis.Client, *miniredis.Miniredis) {
	t.Helper()

	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})

	handler := &TOTPHandler{
		queries:     nil,
		totpService: auth.NewTOTPService(totpTestKey),
		rdb:         rdb,
	}

	t.Cleanup(func() {
		_ = rdb.Close()
	})

	return handler, rdb, mr
}

// newTOTPTestApp wraps a TOTPHandler in a Fiber app that injects a fixed
// user_id into Locals so handlers see an authenticated caller.
func newTOTPTestApp(t *testing.T, handler *TOTPHandler, userID uuid.UUID) *fiber.App {
	t.Helper()

	app := fiber.New(fiber.Config{
		ErrorHandler: func(c fiber.Ctx, err error) error {
			code := fiber.StatusInternalServerError
			message := "Internal Server Error"
			if e, ok := err.(*fiber.Error); ok {
				code = e.Code
				message = e.Message
			}
			return c.Status(code).JSON(fiber.Map{
				"error":   code,
				"message": message,
			})
		},
	})

	app.Use(func(c fiber.Ctx) error {
		c.Locals("user_id", userID)
		return c.Next()
	})

	app.Post("/totp/setup/verify", withRequestParams(t, totpConfirmSetupMirror(), nil, handler.ConfirmSetup))
	app.Post("/totp/verify-login", withRequestParams(t, totpVerifyLoginMirror(), nil, handler.VerifyLogin))

	return app
}

// The two mirrors below are local copies of the parts of these routes'
// declarations (internal/api/registry_totp.go) that the handlers read.
//
// Mirrors for the reason migrationListMirror gives — package api imports this
// package, not the other way round. What each has to get right is the SHAPE
// these tests depend on: the confirm route's code is required and six digits,
// so a malformed one never reaches the handler; the verify-login route's code
// is optional and carries NO pattern, because an explicitly empty code is how a
// client says it is using the recovery code instead.
func totpConfirmSetupMirror() apischema.Properties {
	return apischema.Properties{
		"code": {Type: apischema.String, Pattern: `^[0-9]{6}$`},
	}
}

func totpVerifyLoginMirror() apischema.Properties {
	return apischema.Properties{
		"totp_pending_token": {Type: apischema.String, MinLength: apischema.Ptr(1)},
		"code":               {Type: apischema.String, Optional: true, MaxLength: apischema.Ptr(6)},
		"recovery_code":      {Type: apischema.String, Optional: true, MaxLength: apischema.Ptr(128)},
	}
}

// totpDisableMirror is DELETE /auth/totp's: a code or a recovery code, both optional.
func totpDisableMirror() apischema.Properties {
	return apischema.Properties{
		"code":          {Type: apischema.String, Optional: true, MaxLength: apischema.Ptr(6)},
		"recovery_code": {Type: apischema.String, Optional: true, MaxLength: apischema.Ptr(128)},
	}
}

// newJSONRequest builds a POST request with the JSON body and the
// Content-Type header that Fiber's BodyParser requires.
func newJSONRequest(t *testing.T, path, body string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	return req
}

// TestTOTP_ConfirmSetup_BadCodePreservesSetupSecret locks down Finding #23:
// a single typo MUST NOT destroy the pending setup secret.
func TestTOTP_ConfirmSetup_BadCodePreservesSetupSecret(t *testing.T) {
	handler, rdb, _ := newTOTPTestEnv(t)
	userID := uuid.New()
	app := newTOTPTestApp(t, handler, userID)

	encrypted, _, _, err := handler.totpService.GenerateSecret("test@example.com")
	if err != nil {
		t.Fatalf("GenerateSecret: %v", err)
	}
	setupKey := totpSetupKey(userID)
	if err := rdb.Set(context.Background(), setupKey, encrypted, totpSetupTTL).Err(); err != nil {
		t.Fatalf("seed Redis: %v", err)
	}

	resp, err := app.Test(newJSONRequest(t, "/totp/setup/verify", `{"code":"000000"}`))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusUnauthorized)
	}

	got, err := rdb.Get(context.Background(), setupKey).Result()
	if err != nil {
		t.Fatalf("setup secret destroyed by typo: %v (Finding #23 fix would be reverted)", err)
	}
	if got != encrypted {
		t.Errorf("setup secret mutated by typo")
	}

	count, err := rdb.Get(context.Background(), totpSetupAttemptsKey(userID)).Int64()
	if err != nil {
		t.Fatalf("attempt counter not initialised: %v", err)
	}
	if count != 1 {
		t.Errorf("attempt counter = %d, want 1", count)
	}
}

// TestTOTP_ConfirmSetup_BudgetExhaustionDestroysSecret locks the (max+1)th
// attempt path: secret + counter both deleted, response is 429.
func TestTOTP_ConfirmSetup_BudgetExhaustionDestroysSecret(t *testing.T) {
	handler, rdb, _ := newTOTPTestEnv(t)
	userID := uuid.New()
	app := newTOTPTestApp(t, handler, userID)

	encrypted, _, _, err := handler.totpService.GenerateSecret("test@example.com")
	if err != nil {
		t.Fatalf("GenerateSecret: %v", err)
	}
	setupKey := totpSetupKey(userID)
	if err := rdb.Set(context.Background(), setupKey, encrypted, totpSetupTTL).Err(); err != nil {
		t.Fatalf("seed Redis: %v", err)
	}

	// Burn the full budget. Each call returns 401 + secret intact.
	for i := 1; i <= totpSetupMaxAttempts; i++ {
		resp, err := app.Test(newJSONRequest(t, "/totp/setup/verify", `{"code":"000000"}`))
		if err != nil {
			t.Fatalf("request %d: %v", i, err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("attempt %d status = %d, want %d", i, resp.StatusCode, http.StatusUnauthorized)
		}
		if _, err := rdb.Get(context.Background(), setupKey).Result(); err != nil {
			t.Fatalf("secret destroyed early on attempt %d: %v", i, err)
		}
	}

	// The (max+1)th attempt MUST nuke the secret and return 429.
	resp, err := app.Test(newJSONRequest(t, "/totp/setup/verify", `{"code":"000000"}`))
	if err != nil {
		t.Fatalf("over-budget request: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusTooManyRequests {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusTooManyRequests)
	}

	if _, err := rdb.Get(context.Background(), setupKey).Result(); !errors.Is(err, redis.Nil) {
		t.Errorf("setup secret should be deleted after budget exhaustion, got err = %v", err)
	}
	if _, err := rdb.Get(context.Background(), totpSetupAttemptsKey(userID)).Result(); !errors.Is(err, redis.Nil) {
		t.Errorf("attempt counter should be deleted after budget exhaustion, got err = %v", err)
	}
}

// TestTOTP_ConfirmSetup_NoPendingSetupReturns400 verifies the early-exit
// path when no setup is in progress.
func TestTOTP_ConfirmSetup_NoPendingSetupReturns400(t *testing.T) {
	handler, _, _ := newTOTPTestEnv(t)
	userID := uuid.New()
	app := newTOTPTestApp(t, handler, userID)

	resp, err := app.Test(newJSONRequest(t, "/totp/setup/verify", `{"code":"123456"}`))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusBadRequest)
	}
}

// TestTOTP_ConfirmSetup_BadFormatReturns400 verifies format validation
// happens before any Redis interaction (no counter increment for non-digit
// inputs).
func TestTOTP_ConfirmSetup_BadFormatReturns400(t *testing.T) {
	handler, rdb, _ := newTOTPTestEnv(t)
	userID := uuid.New()
	app := newTOTPTestApp(t, handler, userID)

	encrypted, _, _, err := handler.totpService.GenerateSecret("test@example.com")
	if err != nil {
		t.Fatalf("GenerateSecret: %v", err)
	}
	if err := rdb.Set(context.Background(), totpSetupKey(userID), encrypted, totpSetupTTL).Err(); err != nil {
		t.Fatalf("seed Redis: %v", err)
	}

	cases := []struct {
		name string
		body string
	}{
		{"too short", `{"code":"123"}`},
		{"too long", `{"code":"1234567"}`},
		{"non-digit", `{"code":"abcdef"}`},
		{"mixed", `{"code":"12345a"}`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := app.Test(newJSONRequest(t, "/totp/setup/verify", tc.body))
			if err != nil {
				t.Fatalf("request: %v", err)
			}
			defer func() { _ = resp.Body.Close() }()
			if resp.StatusCode != http.StatusBadRequest {
				t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusBadRequest)
			}
		})
	}

	// Counter must still be unset — bad format never reaches the counter.
	if _, err := rdb.Get(context.Background(), totpSetupAttemptsKey(userID)).Result(); !errors.Is(err, redis.Nil) {
		t.Errorf("counter incremented for bad-format input: err = %v", err)
	}
}

// TestTOTP_VerifyLogin_PreLockedUserReturns429: while the account's second factor is
// locked even a right code is refused, 429 with a Retry-After, and the pending token is
// kept, so the user can retry after the cooldown without the password step again.
func TestTOTP_VerifyLogin_PreLockedUserReturns429(t *testing.T) {
	user, plain := epochTOTPUser(t, epochLoginEmail, 0)
	a := newEpochApp(t, newEpochStore(user), epochOptions{totp: true})
	token := epochFirstStep(t, a, epochLoginEmail)
	pendingKey := "totp:pending:" + token
	before, _ := a.redis.Get(pendingKey)
	if err := a.redis.Set(totpUserLockKey(user.ID), "1"); err != nil {
		t.Fatalf("seed lock: %v", err)
	}
	a.redis.SetTTL(totpUserLockKey(user.ID), totpLockoutDuration)

	resp := a.post(t, "/auth/totp/verify-login", epochVerifyBody(t, token, plain))
	if resp.StatusCode != http.StatusTooManyRequests || resp.Header.Get("Retry-After") == "" {
		t.Errorf("status = %d, Retry-After %q, want 429 with a Retry-After", resp.StatusCode, resp.Header.Get("Retry-After"))
	}
	if got, err := a.redis.Get(pendingKey); err != nil || got != before {
		t.Errorf("pending token after the lockout response = %q (%v), want it kept unchanged", got, err)
	}
}

// TestTOTP_VerifyLogin_PendingTokenBudgetExhaustion locks the per-token
// brute-force cap. After totpMaxAttempts+1 attempts, the pending token + the
// attempt counter are both deleted and the response is 429. We use a
// mismatched user_id in the pending data so the request fails BEFORE the DB
// lookup but AFTER the per-token counter check, isolating the counter
// behaviour from DB-touching paths.
func TestTOTP_VerifyLogin_PendingTokenBudgetExhaustion(t *testing.T) {
	handler, rdb, _ := newTOTPTestEnv(t)
	app := newTOTPTestApp(t, handler, uuid.New())

	pendingToken := "test-pending-token-budget"
	// Use an invalid pending payload so the handler bails out at the JSON
	// unmarshal step — but only AFTER the per-token counter has been
	// incremented and the budget check has run.
	pendingKey := "totp:pending:" + pendingToken
	attemptKey := "totp:attempts:" + pendingToken
	if err := rdb.Set(context.Background(), pendingKey, "not-valid-json", totpPendingTTL).Err(); err != nil {
		t.Fatalf("seed pending: %v", err)
	}

	body := `{"totp_pending_token":"` + pendingToken + `","code":"000000"}`

	// Burn the full budget — each call returns 500 (invalid pending data)
	// AFTER the counter increment, with the pending data still intact
	// because we don't consume on bad payload.
	for i := 1; i <= totpMaxAttempts; i++ {
		resp, err := app.Test(newJSONRequest(t, "/totp/verify-login", body))
		if err != nil {
			t.Fatalf("request %d: %v", i, err)
		}
		_ = resp.Body.Close()
		// Non-429 — anything but TooManyRequests proves we got past the
		// counter check.
		if resp.StatusCode == http.StatusTooManyRequests {
			t.Fatalf("attempt %d: hit 429 too early (counter limit miscalibrated)", i)
		}
	}

	// (max+1)th attempt — counter > max, both keys deleted, 429.
	resp, err := app.Test(newJSONRequest(t, "/totp/verify-login", body))
	if err != nil {
		t.Fatalf("over-budget request: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusTooManyRequests {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusTooManyRequests)
	}

	if _, err := rdb.Get(context.Background(), pendingKey).Result(); !errors.Is(err, redis.Nil) {
		t.Errorf("pending key should be deleted after budget exhaustion, got err = %v", err)
	}
	if _, err := rdb.Get(context.Background(), attemptKey).Result(); !errors.Is(err, redis.Nil) {
		t.Errorf("attempt counter should be deleted after budget exhaustion, got err = %v", err)
	}
}

// TestTOTP_BeginSetupAttemptsKey_DeletedOnReSetup is the contract behind
// "BeginSetup grants a fresh budget": it can't be tested without DB (BeginSetup
// reads users), but we can verify that Del on the attempts key is a no-op
// when the key is absent — locking down the symmetric helper invariant.
func TestTOTP_BeginSetupKeyHelpers_AreUserScoped(t *testing.T) {
	a := uuid.New()
	b := uuid.New()

	if totpSetupKey(a) == totpSetupKey(b) {
		t.Errorf("totpSetupKey collides between users")
	}
	if totpSetupAttemptsKey(a) == totpSetupAttemptsKey(b) {
		t.Errorf("totpSetupAttemptsKey collides between users")
	}
	if totpUserFailKey(a) == totpUserFailKey(b) {
		t.Errorf("totpUserFailKey collides between users")
	}
	if totpUserLockKey(a) == totpUserLockKey(b) {
		t.Errorf("totpUserLockKey collides between users")
	}
	// The setup-attempts key MUST be a different namespace from the setup
	// key — otherwise Del on one would clobber the other.
	if totpSetupKey(a) == totpSetupAttemptsKey(a) {
		t.Errorf("totpSetupKey collides with totpSetupAttemptsKey")
	}
	// Sanity: fail and lock are different namespaces (so clear-failures
	// doesn't accidentally double-delete the same key).
	if totpUserFailKey(a) == totpUserLockKey(a) {
		t.Errorf("totpUserFailKey collides with totpUserLockKey")
	}
}

// TestTOTP_ACodeThatCannotBeCountedIsNotChecked: with Redis unreachable, disabling the
// second factor and regenerating recovery codes are refused 503 even for a right code,
// and change nothing. A code checked without being counted is a guess with no limit.
func TestTOTP_ACodeThatCannotBeCountedIsNotChecked(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, method, path string }{
		{"disable", http.MethodDelete, "/auth/totp"},
		{"regenerate recovery codes", http.MethodPost, "/auth/totp/recovery-codes/regenerate"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			user, plain := epochTOTPUser(t, epochLoginEmail, 0)
			a := newEpochApp(t, newEpochStore(user), epochOptions{totp: true})
			a.redis.Close()
			code, err := totp.GenerateCode(plain, time.Now())
			if err != nil {
				t.Fatalf("generate a code: %v", err)
			}
			resp := a.send(t, tc.method, tc.path, `{"code":"`+code+`"}`, map[string]string{"X-Test-Acting-User": user.ID.String()}, 20*time.Second)
			if resp.StatusCode != http.StatusServiceUnavailable {
				t.Errorf("status = %d, want 503: the code must not be checked when it cannot be counted", resp.StatusCode)
			}
			for _, stmt := range []string{"ClearTOTPSecret", "DeleteAllRecoveryCodes", "InsertRecoveryCode"} {
				if n := len(a.store.named(stmt)); n != 0 {
					t.Errorf("%s was sent %d times: nothing may change", stmt, n)
				}
			}
		})
	}
}

// TestTOTP_AWrongCodeGivesItsCounterAnExpiry: a counter left without an expiry — what
// the old INCR and separate EXPIRE left behind when the EXPIRE failed — gets its window
// back with the next wrong code, so a count can never outlive its window.
func TestTOTP_AWrongCodeGivesItsCounterAnExpiry(t *testing.T) {
	t.Parallel()
	user, _ := epochTOTPUser(t, epochLoginEmail, 0)
	a := newEpochApp(t, newEpochStore(user), epochOptions{totp: true})
	if err := a.redis.Set(totpManageLockoutPolicy.failKey(user.ID), "2"); err != nil {
		t.Fatalf("seed the counter: %v", err)
	}
	resp := a.send(t, http.MethodPost, "/auth/totp/recovery-codes/regenerate", `{"code":"000000"}`,
		map[string]string{"X-Test-Acting-User": user.ID.String()}, 20*time.Second)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 for a wrong code", resp.StatusCode)
	}
	if ttl := a.redis.TTL(totpManageLockoutPolicy.failKey(user.ID)); ttl != totpLockoutWindow {
		t.Errorf("the counter expires in %v, want its window, %v", ttl, totpLockoutWindow)
	}
}

// TestTOTP_ASessionsWrongCodesDoNotLockTheSignInStep: wrong codes sent to Disable from
// one session use up that session's budget, and the owner's second step of sign-in is
// not locked by them: a stolen session must not lock the owner out of signing in.
func TestTOTP_ASessionsWrongCodesDoNotLockTheSignInStep(t *testing.T) {
	t.Parallel()
	user, plain := epochTOTPUser(t, epochLoginEmail, 0)
	a := newEpochApp(t, newEpochStore(user), epochOptions{totp: true})
	thief := map[string]string{"X-Test-Acting-User": user.ID.String(), "X-Test-Session": uuid.NewString()}
	var last int
	for range totpLockoutThreshold {
		last = a.send(t, http.MethodDelete, "/auth/totp", `{"code":"000000"}`, thief, 20*time.Second).StatusCode
	}
	if last != http.StatusTooManyRequests {
		t.Fatalf("the session's last wrong code = %d, want 429: this test proved nothing", last)
	}
	token := epochFirstStep(t, a, epochLoginEmail)
	if resp := a.post(t, "/auth/totp/verify-login", epochVerifyBody(t, token, plain)); resp.StatusCode != http.StatusOK {
		t.Errorf("the owner's second step = %d, want 200: one session's wrong codes locked the sign-in", resp.StatusCode)
	}
}

// TestTOTP_VerifyLogin_ARightCodeClearsAndTheLastWrongOneLocks: four wrong codes and a
// right one leave nothing counted; five wrong ones after that lock, the fifth itself
// answering 429 and destroying its pending token.
func TestTOTP_VerifyLogin_ARightCodeClearsAndTheLastWrongOneLocks(t *testing.T) {
	t.Parallel()
	user, plain := epochTOTPUser(t, epochLoginEmail, 0)
	a := newEpochApp(t, newEpochStore(user), epochOptions{totp: true})
	wrong := func(token string) int {
		t.Helper()
		return a.post(t, "/auth/totp/verify-login", `{"totp_pending_token":"`+token+`","code":"000000"}`).StatusCode
	}
	token := epochFirstStep(t, a, epochLoginEmail)
	for range totpLockoutThreshold - 1 {
		wrong(token)
	}
	if resp := a.post(t, "/auth/totp/verify-login", epochVerifyBody(t, token, plain)); resp.StatusCode != http.StatusOK {
		t.Fatalf("the right code = %d, want 200", resp.StatusCode)
	}
	token = epochFirstStep(t, a, epochLoginEmail)
	for i := 1; i < totpLockoutThreshold; i++ {
		if status := wrong(token); status != http.StatusUnauthorized {
			t.Fatalf("wrong code %d after the right one = %d, want 401: the right code must clear the count", i, status)
		}
	}
	if status := wrong(token); status != http.StatusTooManyRequests {
		t.Errorf("the fifth wrong code = %d, want the 429 of the lock it arms", status)
	}
	if a.redis.Exists("totp:pending:" + token) {
		t.Error("the pending token survived the lock")
	}
}
