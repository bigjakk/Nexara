package handlers

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"regexp"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/redis/go-redis/v9"

	"github.com/bigjakk/nexara/internal/auth"
	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/events"
)

const (
	totpSetupTTL         = 10 * time.Minute
	totpPendingTTL       = 5 * time.Minute
	totpMaxAttempts      = 5 // max tries per pending login token before it self-destructs
	totpSetupMaxAttempts = 5 // max tries per setup secret before it self-destructs
	totpRecoveryCount    = 10

	// Per-user TOTP brute-force lockout — orthogonal to the per-IP auth limiter
	// at the middleware layer. An attacker botnet distributing attempts across
	// IPs would slip past 15-attempts/min/IP; this catches that pattern.
	totpLockoutThreshold = 5
	totpLockoutWindow    = 10 * time.Minute
	totpLockoutDuration  = 5 * time.Minute
)

var totpCodePattern = regexp.MustCompile(`^\d{6}$`)

// TOTPHandler handles TOTP 2FA endpoints.
type TOTPHandler struct {
	queries      *db.Queries
	totpService  *auth.TOTPService
	rdb          *redis.Client
	eventPub     *events.Publisher
	issueTokens  func(c fiber.Ctx, user db.User, auditAction string) error
}

// NewTOTPHandler creates a new TOTP handler.
func NewTOTPHandler(queries *db.Queries, encryptionKey string, rdb *redis.Client, eventPub *events.Publisher) *TOTPHandler {
	return &TOTPHandler{
		queries:     queries,
		totpService: auth.NewTOTPService(encryptionKey),
		rdb:         rdb,
		eventPub:    eventPub,
	}
}

// SetIssueTokensFn sets the token-issuing function (called from server setup).
func (h *TOTPHandler) SetIssueTokensFn(fn func(c fiber.Ctx, user db.User, auditAction string) error) {
	h.issueTokens = fn
}

// BeginSetup handles POST /api/v1/auth/totp/setup — starts TOTP enrollment.
func (h *TOTPHandler) BeginSetup(c fiber.Ctx) error {
	userID, _ := c.Locals("user_id").(uuid.UUID)
	if userID == uuid.Nil {
		return fiber.NewError(fiber.StatusUnauthorized, "Authentication required")
	}

	row, err := h.queries.GetUserTOTPSecret(c.Context(), userID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to check TOTP status")
	}
	if row.TotpSecret.Valid {
		return fiber.NewError(fiber.StatusConflict, "TOTP is already enabled")
	}

	user, err := h.queries.GetUserByID(c.Context(), userID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to get user")
	}

	encrypted, otpauthURL, plainSecret, err := h.totpService.GenerateSecret(user.Email)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to generate TOTP secret")
	}

	key := totpSetupKey(userID)
	if err := h.rdb.Set(c.Context(), key, encrypted, totpSetupTTL).Err(); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to store setup data")
	}
	// Wipe any leftover counter from a prior setup attempt so the user gets a
	// fresh budget on this enrollment.
	_ = h.rdb.Del(c.Context(), totpSetupAttemptsKey(userID)).Err()

	return c.JSON(fiber.Map{
		"secret":      plainSecret,
		"otpauth_url": otpauthURL,
	})
}

// ConfirmSetup handles POST /api/v1/auth/totp/setup/verify — confirms TOTP enrollment.
func (h *TOTPHandler) ConfirmSetup(c fiber.Ctx) error {
	userID, _ := c.Locals("user_id").(uuid.UUID)
	if userID == uuid.Nil {
		return fiber.NewError(fiber.StatusUnauthorized, "Authentication required")
	}

	var req struct {
		Code string `json:"code"`
	}
	if err := c.Bind().Body(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}
	if req.Code == "" {
		return fiber.NewError(fiber.StatusBadRequest, "Code is required")
	}
	if !totpCodePattern.MatchString(req.Code) {
		return fiber.NewError(fiber.StatusBadRequest, "Code must be exactly 6 digits")
	}

	setupKey := totpSetupKey(userID)
	attemptsKey := totpSetupAttemptsKey(userID)

	// Peek the secret without consuming — a single typo must not destroy
	// enrollment. The explicit per-secret counter caps brute-force at
	// totpSetupMaxAttempts; the (totpSetupMaxAttempts+1)th attempt nukes the
	// secret so an attacker who somehow reached this authenticated path can't
	// run an unbounded oracle against it.
	encrypted, err := h.rdb.Get(c.Context(), setupKey).Result()
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "No pending TOTP setup — start setup first")
	}

	attempts, err := h.rdb.Incr(c.Context(), attemptsKey).Result()
	if err != nil {
		// Don't fail-open on the budget side either: if Redis can't track
		// the counter, refuse rather than allow unlimited attempts.
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to record attempt")
	}
	if attempts == 1 {
		_ = h.rdb.Expire(c.Context(), attemptsKey, totpSetupTTL).Err()
	}
	if attempts > int64(totpSetupMaxAttempts) {
		_ = h.rdb.Del(c.Context(), setupKey, attemptsKey).Err()
		return fiber.NewError(fiber.StatusTooManyRequests, "Too many failed attempts — please restart setup")
	}

	valid, err := h.totpService.ValidateCode(encrypted, req.Code)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to validate code")
	}
	if !valid {
		return fiber.NewError(fiber.StatusUnauthorized, "Invalid TOTP code")
	}

	// Validation succeeded — consume the secret + clear the counter.
	_ = h.rdb.Del(c.Context(), setupKey, attemptsKey).Err()

	if err := h.queries.SetTOTPSecret(c.Context(), db.SetTOTPSecretParams{
		ID:         userID,
		TotpSecret: pgtype.Text{String: encrypted, Valid: true},
	}); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to enable TOTP")
	}

	plainCodes, hashedCodes, err := h.totpService.GenerateRecoveryCodes(totpRecoveryCount)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to generate recovery codes")
	}

	// Clear any stale recovery codes and store new ones.
	_ = h.queries.DeleteAllRecoveryCodes(c.Context(), userID)
	for _, hash := range hashedCodes {
		if err := h.queries.InsertRecoveryCode(c.Context(), db.InsertRecoveryCodeParams{
			UserID:   userID,
			CodeHash: hash,
		}); err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, "Failed to store recovery codes")
		}
	}

	AuditLog(c, h.queries, h.eventPub, pgtype.UUID{}, "auth", userID.String(), "totp_enabled", nil)

	return c.JSON(fiber.Map{
		"enabled":        true,
		"recovery_codes": plainCodes,
	})
}

// Disable handles DELETE /api/v1/auth/totp — disables TOTP for the current user.
func (h *TOTPHandler) Disable(c fiber.Ctx) error {
	userID, _ := c.Locals("user_id").(uuid.UUID)
	if userID == uuid.Nil {
		return fiber.NewError(fiber.StatusUnauthorized, "Authentication required")
	}

	var req struct {
		Code         string `json:"code"`
		RecoveryCode string `json:"recovery_code"`
	}
	if err := c.Bind().Body(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}
	if req.Code == "" && req.RecoveryCode == "" {
		return fiber.NewError(fiber.StatusBadRequest, "TOTP code or recovery code is required")
	}
	if req.Code != "" && !totpCodePattern.MatchString(req.Code) {
		return fiber.NewError(fiber.StatusBadRequest, "Code must be exactly 6 digits")
	}

	if h.isUserTOTPLocked(c.Context(), userID) {
		return fiber.NewError(fiber.StatusTooManyRequests, "Too many failed 2FA attempts — try again in a few minutes")
	}

	row, err := h.queries.GetUserTOTPSecret(c.Context(), userID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to check TOTP status")
	}
	if !row.TotpSecret.Valid {
		return fiber.NewError(fiber.StatusBadRequest, "TOTP is not enabled")
	}

	validated, err := h.validateCodeOrRecovery(c.Context(), userID, row.TotpSecret.String, req.Code, req.RecoveryCode)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to validate code")
	}
	if !validated {
		if h.recordTOTPFailure(c.Context(), userID) {
			return fiber.NewError(fiber.StatusTooManyRequests, "Too many failed 2FA attempts — try again in a few minutes")
		}
		if req.Code != "" {
			return fiber.NewError(fiber.StatusUnauthorized, "Invalid TOTP code")
		}
		return fiber.NewError(fiber.StatusUnauthorized, "Invalid recovery code")
	}

	if err := h.queries.ClearTOTPSecret(c.Context(), userID); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to disable TOTP")
	}
	_ = h.queries.DeleteAllRecoveryCodes(c.Context(), userID)
	h.clearTOTPFailures(c.Context(), userID)

	AuditLog(c, h.queries, h.eventPub, pgtype.UUID{}, "auth", userID.String(), "totp_disabled", nil)

	return c.JSON(fiber.Map{"message": "TOTP disabled"})
}

// Status handles GET /api/v1/auth/totp/status — returns TOTP status for current user.
func (h *TOTPHandler) Status(c fiber.Ctx) error {
	userID, _ := c.Locals("user_id").(uuid.UUID)
	if userID == uuid.Nil {
		return fiber.NewError(fiber.StatusUnauthorized, "Authentication required")
	}

	row, err := h.queries.GetUserTOTPSecret(c.Context(), userID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to check TOTP status")
	}

	count, err := h.queries.CountRecoveryCodes(c.Context(), userID)
	if err != nil {
		count = 0
	}

	return c.JSON(fiber.Map{
		"enabled":                  row.TotpSecret.Valid,
		"recovery_codes_remaining": count,
	})
}

// RegenerateRecoveryCodes handles POST /api/v1/auth/totp/recovery-codes/regenerate.
func (h *TOTPHandler) RegenerateRecoveryCodes(c fiber.Ctx) error {
	userID, _ := c.Locals("user_id").(uuid.UUID)
	if userID == uuid.Nil {
		return fiber.NewError(fiber.StatusUnauthorized, "Authentication required")
	}

	var req struct {
		Code string `json:"code"`
	}
	if err := c.Bind().Body(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}
	if req.Code == "" {
		return fiber.NewError(fiber.StatusBadRequest, "TOTP code is required")
	}
	if !totpCodePattern.MatchString(req.Code) {
		return fiber.NewError(fiber.StatusBadRequest, "Code must be exactly 6 digits")
	}

	if h.isUserTOTPLocked(c.Context(), userID) {
		return fiber.NewError(fiber.StatusTooManyRequests, "Too many failed 2FA attempts — try again in a few minutes")
	}

	row, err := h.queries.GetUserTOTPSecret(c.Context(), userID)
	if err != nil || !row.TotpSecret.Valid {
		return fiber.NewError(fiber.StatusBadRequest, "TOTP is not enabled")
	}

	valid, err := h.totpService.ValidateCode(row.TotpSecret.String, req.Code)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to validate code")
	}
	if !valid {
		if h.recordTOTPFailure(c.Context(), userID) {
			return fiber.NewError(fiber.StatusTooManyRequests, "Too many failed 2FA attempts — try again in a few minutes")
		}
		return fiber.NewError(fiber.StatusUnauthorized, "Invalid TOTP code")
	}
	h.clearTOTPFailures(c.Context(), userID)

	_ = h.queries.DeleteAllRecoveryCodes(c.Context(), userID)

	plainCodes, hashedCodes, err := h.totpService.GenerateRecoveryCodes(totpRecoveryCount)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to generate recovery codes")
	}

	for _, hash := range hashedCodes {
		if err := h.queries.InsertRecoveryCode(c.Context(), db.InsertRecoveryCodeParams{
			UserID:   userID,
			CodeHash: hash,
		}); err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, "Failed to store recovery codes")
		}
	}

	AuditLog(c, h.queries, h.eventPub, pgtype.UUID{}, "auth", userID.String(), "totp_recovery_codes_regenerated", nil)

	return c.JSON(fiber.Map{
		"recovery_codes": plainCodes,
	})
}

// VerifyLogin handles POST /api/v1/auth/totp/verify-login — completes two-step login.
func (h *TOTPHandler) VerifyLogin(c fiber.Ctx) error {
	var req struct {
		TOTPPendingToken string `json:"totp_pending_token"`
		Code             string `json:"code"`
		RecoveryCode     string `json:"recovery_code"`
	}
	if err := c.Bind().Body(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}
	if req.TOTPPendingToken == "" {
		return fiber.NewError(fiber.StatusBadRequest, "Pending token is required")
	}
	if req.Code == "" && req.RecoveryCode == "" {
		return fiber.NewError(fiber.StatusBadRequest, "TOTP code or recovery code is required")
	}

	if req.Code != "" && !totpCodePattern.MatchString(req.Code) {
		return fiber.NewError(fiber.StatusBadRequest, "Code must be exactly 6 digits")
	}

	pendingKey := fmt.Sprintf("totp:pending:%s", req.TOTPPendingToken)
	attemptKey := fmt.Sprintf("totp:attempts:%s", req.TOTPPendingToken)

	// Per-pending-token attempt counter — caps brute-force on a single token
	// regardless of which user it belongs to.
	attempts, err := h.rdb.Incr(c.Context(), attemptKey).Result()
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to record attempt")
	}
	if attempts == 1 {
		_ = h.rdb.Expire(c.Context(), attemptKey, totpPendingTTL).Err()
	}
	if attempts > int64(totpMaxAttempts) {
		_ = h.rdb.Del(c.Context(), pendingKey, attemptKey).Err()
		return fiber.NewError(fiber.StatusTooManyRequests, "Too many attempts — please log in again")
	}

	// Peek the pending token without consuming. A typo no longer destroys the
	// pending session — the per-token counter above bounds the budget; the
	// per-user lockout below catches attackers cycling through fresh tokens.
	data, err := h.rdb.Get(c.Context(), pendingKey).Result()
	if err != nil {
		return fiber.NewError(fiber.StatusUnauthorized, "Invalid or expired pending token")
	}

	var pending struct {
		UserID      string `json:"user_id"`
		AuditAction string `json:"audit_action"`
	}
	if err := json.Unmarshal([]byte(data), &pending); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Invalid pending data")
	}

	userID, err := uuid.Parse(pending.UserID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Invalid user in pending data")
	}

	if h.isUserTOTPLocked(c.Context(), userID) {
		return fiber.NewError(fiber.StatusTooManyRequests, "Too many failed 2FA attempts — try again in a few minutes")
	}

	user, err := h.queries.GetUserByID(c.Context(), userID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "User not found")
	}
	if !user.IsActive {
		return fiber.NewError(fiber.StatusForbidden, "Account is disabled")
	}
	if !user.TotpSecret.Valid {
		return fiber.NewError(fiber.StatusInternalServerError, "TOTP not configured for user")
	}

	validated, verr := h.validateCodeOrRecovery(c.Context(), userID, user.TotpSecret.String, req.Code, req.RecoveryCode)
	if verr != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to validate code")
	}
	if !validated {
		if h.recordTOTPFailure(c.Context(), userID) {
			// Lockout just triggered — invalidate this pending session too so
			// the attacker can't burn the rest of the per-token budget while
			// the lock is in effect.
			_ = h.rdb.Del(c.Context(), pendingKey, attemptKey).Err()
			return fiber.NewError(fiber.StatusTooManyRequests, "Too many failed 2FA attempts — try again in a few minutes")
		}
		if req.Code != "" {
			return fiber.NewError(fiber.StatusUnauthorized, "Invalid TOTP code")
		}
		return fiber.NewError(fiber.StatusUnauthorized, "Invalid recovery code")
	}

	// Validation succeeded — consume pending state. If two concurrent requests
	// both validate, both will issue tokens (each producing a session for the
	// same user with a real second factor); not a security hole, just an
	// edge-case ergonomic. Race-detection via Del-result is deliberately not
	// done because a recovery code might already be burned at this point.
	_ = h.rdb.Del(c.Context(), pendingKey, attemptKey).Err()
	h.clearTOTPFailures(c.Context(), userID)

	if h.issueTokens == nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Token issuer not configured")
	}

	return h.issueTokens(c, user, pending.AuditAction)
}

// AdminReset handles DELETE /api/v1/users/:id/totp — admin resets user's TOTP.
func (h *TOTPHandler) AdminReset(c fiber.Ctx) error {
	if err := requirePerm(c, "manage", "user"); err != nil {
		return err
	}

	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid user ID")
	}

	// Prevent admins from resetting their own TOTP — use the self-service disable flow.
	callerID, _ := c.Locals("user_id").(uuid.UUID)
	if id == callerID {
		return fiber.NewError(fiber.StatusForbidden, "Cannot reset your own TOTP via admin endpoint — use the self-service disable flow")
	}

	row, err := h.queries.GetUserTOTPSecret(c.Context(), id)
	if err != nil {
		return fiber.NewError(fiber.StatusNotFound, "User not found")
	}
	if !row.TotpSecret.Valid {
		return fiber.NewError(fiber.StatusBadRequest, "TOTP is not enabled for this user")
	}

	if err := h.queries.ClearTOTPSecret(c.Context(), id); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to reset TOTP")
	}
	_ = h.queries.DeleteAllRecoveryCodes(c.Context(), id)

	details, _ := json.Marshal(map[string]string{"target_user_id": id.String()})
	AuditLog(c, h.queries, h.eventPub, pgtype.UUID{}, "auth", id.String(), "totp_admin_reset", details)

	return c.JSON(fiber.Map{"message": "TOTP reset for user"})
}

// CreateTOTPPendingToken generates a random token and stores it in Redis with user data.
func (h *TOTPHandler) CreateTOTPPendingToken(ctx context.Context, userID uuid.UUID, auditAction string) (string, error) {
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		return "", fmt.Errorf("generate pending token: %w", err)
	}
	token := base64.RawURLEncoding.EncodeToString(tokenBytes)

	data, _ := json.Marshal(map[string]string{
		"user_id":      userID.String(),
		"audit_action": auditAction,
	})

	key := fmt.Sprintf("totp:pending:%s", token)
	if err := h.rdb.Set(ctx, key, string(data), totpPendingTTL).Err(); err != nil {
		return "", fmt.Errorf("store pending token: %w", err)
	}

	return token, nil
}

// totpSetupKey returns the Redis key that holds the pending (encrypted) TOTP
// secret for a given user.
func totpSetupKey(userID uuid.UUID) string {
	return fmt.Sprintf("totp:setup:%s", userID.String())
}

// totpSetupAttemptsKey returns the Redis key that holds the per-secret failed
// attempt counter during enrollment confirmation.
func totpSetupAttemptsKey(userID uuid.UUID) string {
	return fmt.Sprintf("totp:setup:attempts:%s", userID.String())
}

// totpUserFailKey returns the Redis key that holds the rolling-window failed
// 2FA attempt counter for a user (across all pending tokens / endpoints).
func totpUserFailKey(userID uuid.UUID) string {
	return fmt.Sprintf("totp:user:fail:%s", userID.String())
}

// totpUserLockKey returns the Redis key that, when present, indicates the
// user is in a 2FA cooldown period.
func totpUserLockKey(userID uuid.UUID) string {
	return fmt.Sprintf("totp:user:lock:%s", userID.String())
}

// isUserTOTPLocked reports whether the user is currently in a 2FA cooldown.
// Fail-open on Redis errors: a transient outage must not block legitimate
// logins. The per-IP auth limiter at the middleware layer remains in force.
func (h *TOTPHandler) isUserTOTPLocked(ctx context.Context, userID uuid.UUID) bool {
	n, err := h.rdb.Exists(ctx, totpUserLockKey(userID)).Result()
	if err != nil {
		return false
	}
	return n > 0
}

// recordTOTPFailure increments the per-user fail counter and applies a
// cooldown if the threshold is reached. Returns true iff this call triggered
// the lockout (so the caller can return 429 instead of 401 on the boundary).
// Fail-open on Redis errors.
func (h *TOTPHandler) recordTOTPFailure(ctx context.Context, userID uuid.UUID) bool {
	failKey := totpUserFailKey(userID)
	fails, err := h.rdb.Incr(ctx, failKey).Result()
	if err != nil {
		return false
	}
	if fails == 1 {
		_ = h.rdb.Expire(ctx, failKey, totpLockoutWindow).Err()
	}
	if fails >= int64(totpLockoutThreshold) {
		_ = h.rdb.Set(ctx, totpUserLockKey(userID), "1", totpLockoutDuration).Err()
		_ = h.rdb.Del(ctx, failKey).Err()
		return true
	}
	return false
}

// clearTOTPFailures wipes per-user fail and lock state. Called on every
// successful TOTP authentication so a near-miss followed by a real success
// doesn't carry over.
func (h *TOTPHandler) clearTOTPFailures(ctx context.Context, userID uuid.UUID) {
	_ = h.rdb.Del(ctx, totpUserFailKey(userID), totpUserLockKey(userID)).Err()
}

// validateCodeOrRecovery checks an input TOTP code or recovery code against
// the user's stored secret/recovery hashes. Returns (validated, internalErr).
// `internalErr` is non-nil only for genuine internal errors (e.g. decryption
// failure); a wrong code returns (false, nil) so the caller can register a
// failure rather than a 500. Format checks on `code` are the caller's
// responsibility — call the regex first, then this helper.
func (h *TOTPHandler) validateCodeOrRecovery(ctx context.Context, userID uuid.UUID, encryptedSecret, code, recoveryCode string) (bool, error) {
	if code != "" {
		valid, err := h.totpService.ValidateCode(encryptedSecret, code)
		if err != nil {
			return false, err
		}
		return valid, nil
	}
	if err := h.consumeRecoveryCode(ctx, userID, recoveryCode); err != nil {
		// Same handling as the original inline path: any error from the
		// recovery flow (no match, DB blip) is treated as "no match." A
		// rigorous fix to distinguish DB errors from misses would require
		// sentinel errors in consumeRecoveryCode; left as-is to keep this
		// commit's blast radius bounded.
		return false, nil
	}
	return true, nil
}

// consumeRecoveryCode finds and deletes a matching recovery code.
func (h *TOTPHandler) consumeRecoveryCode(ctx context.Context, userID uuid.UUID, inputCode string) error {
	codes, err := h.queries.ListRecoveryCodes(ctx, userID)
	if err != nil {
		return fmt.Errorf("list recovery codes: %w", err)
	}

	for _, rc := range codes {
		if h.totpService.ValidateRecoveryCode(rc.CodeHash, inputCode) {
			if err := h.queries.DeleteRecoveryCode(ctx, rc.ID); err != nil {
				return fmt.Errorf("delete recovery code: %w", err)
			}
			return nil
		}
	}

	return fmt.Errorf("no matching recovery code")
}
