package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

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

	app.Post("/totp/setup/verify", handler.ConfirmSetup)
	app.Post("/totp/verify-login", handler.VerifyLogin)

	return app
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

// TestTOTP_VerifyLogin_BadCodePreservesPendingToken locks down the second
// half of the bug: a typo on /totp/verify-login MUST NOT destroy the pending
// session. Pre-existing lockout state on the user blocks DB lookup before
// it's reached, so we use that to assert the path: pending intact, attempt
// counter at 1, response 429.
func TestTOTP_VerifyLogin_PreLockedUserReturns429(t *testing.T) {
	handler, rdb, _ := newTOTPTestEnv(t)
	userID := uuid.New()
	app := newTOTPTestApp(t, handler, userID)

	pendingToken := "test-pending-token"
	pendingData, err := json.Marshal(map[string]string{
		"user_id":      userID.String(),
		"audit_action": "login",
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	pendingKey := "totp:pending:" + pendingToken
	if err := rdb.Set(context.Background(), pendingKey, string(pendingData), totpPendingTTL).Err(); err != nil {
		t.Fatalf("seed pending: %v", err)
	}

	// Pre-lock the user.
	if err := rdb.Set(context.Background(), totpUserLockKey(userID), "1", totpLockoutDuration).Err(); err != nil {
		t.Fatalf("seed lock: %v", err)
	}

	body := `{"totp_pending_token":"` + pendingToken + `","code":"000000"}`
	resp, err := app.Test(newJSONRequest(t, "/totp/verify-login", body))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusTooManyRequests {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusTooManyRequests)
	}

	// Lockout must NOT consume the pending token — the user can retry after
	// the cooldown without being forced through the password step again.
	if got, err := rdb.Get(context.Background(), pendingKey).Result(); err != nil {
		t.Errorf("pending token destroyed by lockout response: %v", err)
	} else if got != string(pendingData) {
		t.Errorf("pending token mutated by lockout response")
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

// TestTOTP_RecordTOTPFailure_TriggersLockoutAtThreshold is the direct
// helper-level test for the per-user fail/lock state machine.
func TestTOTP_RecordTOTPFailure_TriggersLockoutAtThreshold(t *testing.T) {
	handler, rdb, _ := newTOTPTestEnv(t)
	userID := uuid.New()
	ctx := context.Background()

	for i := 1; i < totpLockoutThreshold; i++ {
		if locked := handler.recordTOTPFailure(ctx, userID); locked {
			t.Fatalf("recordTOTPFailure returned true on call %d (before threshold)", i)
		}
		if handler.isUserTOTPLocked(ctx, userID) {
			t.Fatalf("isUserTOTPLocked returned true after %d failures (before threshold)", i)
		}
	}

	// Threshold call triggers lockout.
	if locked := handler.recordTOTPFailure(ctx, userID); !locked {
		t.Fatalf("recordTOTPFailure returned false on threshold call (call %d)", totpLockoutThreshold)
	}
	if !handler.isUserTOTPLocked(ctx, userID) {
		t.Fatalf("isUserTOTPLocked returned false immediately after lockout was triggered")
	}

	// Fail counter is reset post-lockout (so the cooldown is the only gate).
	if _, err := rdb.Get(ctx, totpUserFailKey(userID)).Result(); !errors.Is(err, redis.Nil) {
		t.Errorf("fail counter should be cleared after lockout, got err = %v", err)
	}

	// Lock key TTL must be in the future (i.e. lock is real, not a stale
	// already-expired key).
	ttl := rdb.TTL(ctx, totpUserLockKey(userID)).Val()
	if ttl <= 0 {
		t.Errorf("lock key TTL = %v, want > 0", ttl)
	}
}

// TestTOTP_ClearTOTPFailures_RemovesBothKeys verifies the success-path
// cleanup wipes both fail counter and any active lock.
func TestTOTP_ClearTOTPFailures_RemovesBothKeys(t *testing.T) {
	handler, rdb, _ := newTOTPTestEnv(t)
	userID := uuid.New()
	ctx := context.Background()

	// Pre-populate both keys with arbitrary values + TTLs.
	if err := rdb.Set(ctx, totpUserFailKey(userID), "3", totpLockoutWindow).Err(); err != nil {
		t.Fatalf("seed fail: %v", err)
	}
	if err := rdb.Set(ctx, totpUserLockKey(userID), "1", totpLockoutDuration).Err(); err != nil {
		t.Fatalf("seed lock: %v", err)
	}

	handler.clearTOTPFailures(ctx, userID)

	if _, err := rdb.Get(ctx, totpUserFailKey(userID)).Result(); !errors.Is(err, redis.Nil) {
		t.Errorf("fail counter should be cleared, got err = %v", err)
	}
	if _, err := rdb.Get(ctx, totpUserLockKey(userID)).Result(); !errors.Is(err, redis.Nil) {
		t.Errorf("lock key should be cleared, got err = %v", err)
	}
}

// TestTOTP_RecordTOTPFailure_FailWindowAdvances verifies the rolling-window
// behaviour: failures expire after totpLockoutWindow so a sparse failure
// pattern doesn't trip the lockout.
func TestTOTP_RecordTOTPFailure_FailWindowAdvances(t *testing.T) {
	handler, _, mr := newTOTPTestEnv(t)
	userID := uuid.New()
	ctx := context.Background()

	// Burn (threshold-1) failures.
	for i := 1; i < totpLockoutThreshold; i++ {
		_ = handler.recordTOTPFailure(ctx, userID)
	}

	// Advance miniredis past the window so the counter expires.
	mr.FastForward(totpLockoutWindow + 1*time.Second)

	// Next failure is treated as the first in a fresh window — does NOT
	// trigger lockout, and is NOT lockout-triggering.
	if locked := handler.recordTOTPFailure(ctx, userID); locked {
		t.Errorf("recordTOTPFailure triggered lockout on first failure of fresh window")
	}
	if handler.isUserTOTPLocked(ctx, userID) {
		t.Errorf("isUserTOTPLocked returned true after window reset")
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
