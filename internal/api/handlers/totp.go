package handlers

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"regexp"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/redis/go-redis/v9"

	"github.com/proxdash/proxdash/internal/auth"
	db "github.com/proxdash/proxdash/internal/db/generated"
	"github.com/proxdash/proxdash/internal/events"
)

const (
	totpSetupTTL      = 10 * time.Minute
	totpPendingTTL    = 5 * time.Minute
	totpMaxAttempts   = 5
	totpRecoveryCount = 10
)

var totpCodePattern = regexp.MustCompile(`^\d{6}$`)

// TOTPHandler handles TOTP 2FA endpoints.
type TOTPHandler struct {
	queries      *db.Queries
	totpService  *auth.TOTPService
	rdb          *redis.Client
	eventPub     *events.Publisher
	issueTokens  func(c *fiber.Ctx, user db.User, auditAction string) error
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
func (h *TOTPHandler) SetIssueTokensFn(fn func(c *fiber.Ctx, user db.User, auditAction string) error) {
	h.issueTokens = fn
}

// BeginSetup handles POST /api/v1/auth/totp/setup — starts TOTP enrollment.
func (h *TOTPHandler) BeginSetup(c *fiber.Ctx) error {
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

	key := fmt.Sprintf("totp:setup:%s", userID.String())
	if err := h.rdb.Set(c.Context(), key, encrypted, totpSetupTTL).Err(); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to store setup data")
	}

	return c.JSON(fiber.Map{
		"secret":      plainSecret,
		"otpauth_url": otpauthURL,
	})
}

// ConfirmSetup handles POST /api/v1/auth/totp/setup/verify — confirms TOTP enrollment.
func (h *TOTPHandler) ConfirmSetup(c *fiber.Ctx) error {
	userID, _ := c.Locals("user_id").(uuid.UUID)
	if userID == uuid.Nil {
		return fiber.NewError(fiber.StatusUnauthorized, "Authentication required")
	}

	var req struct {
		Code string `json:"code"`
	}
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}
	if req.Code == "" {
		return fiber.NewError(fiber.StatusBadRequest, "Code is required")
	}

	key := fmt.Sprintf("totp:setup:%s", userID.String())
	encrypted, err := h.rdb.GetDel(c.Context(), key).Result()
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "No pending TOTP setup — start setup first")
	}

	if !totpCodePattern.MatchString(req.Code) {
		return fiber.NewError(fiber.StatusBadRequest, "Code must be exactly 6 digits")
	}

	valid, err := h.totpService.ValidateCode(encrypted, req.Code)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to validate code")
	}
	if !valid {
		return fiber.NewError(fiber.StatusUnauthorized, "Invalid TOTP code")
	}

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
func (h *TOTPHandler) Disable(c *fiber.Ctx) error {
	userID, _ := c.Locals("user_id").(uuid.UUID)
	if userID == uuid.Nil {
		return fiber.NewError(fiber.StatusUnauthorized, "Authentication required")
	}

	var req struct {
		Code         string `json:"code"`
		RecoveryCode string `json:"recovery_code"`
	}
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}
	if req.Code == "" && req.RecoveryCode == "" {
		return fiber.NewError(fiber.StatusBadRequest, "TOTP code or recovery code is required")
	}

	row, err := h.queries.GetUserTOTPSecret(c.Context(), userID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to check TOTP status")
	}
	if !row.TotpSecret.Valid {
		return fiber.NewError(fiber.StatusBadRequest, "TOTP is not enabled")
	}

	if req.Code != "" {
		if !totpCodePattern.MatchString(req.Code) {
			return fiber.NewError(fiber.StatusBadRequest, "Code must be exactly 6 digits")
		}
		valid, err := h.totpService.ValidateCode(row.TotpSecret.String, req.Code)
		if err != nil || !valid {
			return fiber.NewError(fiber.StatusUnauthorized, "Invalid TOTP code")
		}
	} else {
		if err := h.consumeRecoveryCode(c.Context(), userID, req.RecoveryCode); err != nil {
			return fiber.NewError(fiber.StatusUnauthorized, "Invalid recovery code")
		}
	}

	if err := h.queries.ClearTOTPSecret(c.Context(), userID); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to disable TOTP")
	}
	_ = h.queries.DeleteAllRecoveryCodes(c.Context(), userID)

	AuditLog(c, h.queries, h.eventPub, pgtype.UUID{}, "auth", userID.String(), "totp_disabled", nil)

	return c.JSON(fiber.Map{"message": "TOTP disabled"})
}

// Status handles GET /api/v1/auth/totp/status — returns TOTP status for current user.
func (h *TOTPHandler) Status(c *fiber.Ctx) error {
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
func (h *TOTPHandler) RegenerateRecoveryCodes(c *fiber.Ctx) error {
	userID, _ := c.Locals("user_id").(uuid.UUID)
	if userID == uuid.Nil {
		return fiber.NewError(fiber.StatusUnauthorized, "Authentication required")
	}

	var req struct {
		Code string `json:"code"`
	}
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}
	if req.Code == "" {
		return fiber.NewError(fiber.StatusBadRequest, "TOTP code is required")
	}
	if !totpCodePattern.MatchString(req.Code) {
		return fiber.NewError(fiber.StatusBadRequest, "Code must be exactly 6 digits")
	}

	row, err := h.queries.GetUserTOTPSecret(c.Context(), userID)
	if err != nil || !row.TotpSecret.Valid {
		return fiber.NewError(fiber.StatusBadRequest, "TOTP is not enabled")
	}

	valid, err := h.totpService.ValidateCode(row.TotpSecret.String, req.Code)
	if err != nil || !valid {
		return fiber.NewError(fiber.StatusUnauthorized, "Invalid TOTP code")
	}

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
func (h *TOTPHandler) VerifyLogin(c *fiber.Ctx) error {
	var req struct {
		TOTPPendingToken string `json:"totp_pending_token"`
		Code             string `json:"code"`
		RecoveryCode     string `json:"recovery_code"`
	}
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}
	if req.TOTPPendingToken == "" {
		return fiber.NewError(fiber.StatusBadRequest, "Pending token is required")
	}
	if req.Code == "" && req.RecoveryCode == "" {
		return fiber.NewError(fiber.StatusBadRequest, "TOTP code or recovery code is required")
	}

	attemptKey := fmt.Sprintf("totp:attempts:%s", req.TOTPPendingToken)
	attempts, _ := h.rdb.Incr(c.Context(), attemptKey).Result()
	if attempts == 1 {
		h.rdb.Expire(c.Context(), attemptKey, totpPendingTTL)
	}
	if attempts > int64(totpMaxAttempts) {
		h.rdb.Del(c.Context(), fmt.Sprintf("totp:pending:%s", req.TOTPPendingToken))
		h.rdb.Del(c.Context(), attemptKey)
		return fiber.NewError(fiber.StatusTooManyRequests, "Too many attempts — please log in again")
	}

	pendingKey := fmt.Sprintf("totp:pending:%s", req.TOTPPendingToken)
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

	if req.Code != "" {
		if !totpCodePattern.MatchString(req.Code) {
			return fiber.NewError(fiber.StatusBadRequest, "Code must be exactly 6 digits")
		}
		valid, verr := h.totpService.ValidateCode(user.TotpSecret.String, req.Code)
		if verr != nil || !valid {
			return fiber.NewError(fiber.StatusUnauthorized, "Invalid TOTP code")
		}
	} else {
		if err := h.consumeRecoveryCode(c.Context(), userID, req.RecoveryCode); err != nil {
			return fiber.NewError(fiber.StatusUnauthorized, "Invalid recovery code")
		}
	}

	// Atomically consume the pending token — prevents concurrent use.
	if _, err := h.rdb.GetDel(c.Context(), pendingKey).Result(); err != nil {
		return fiber.NewError(fiber.StatusUnauthorized, "Token already consumed")
	}
	h.rdb.Del(c.Context(), attemptKey)

	if h.issueTokens == nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Token issuer not configured")
	}

	return h.issueTokens(c, user, pending.AuditAction)
}

// AdminReset handles DELETE /api/v1/users/:id/totp — admin resets user's TOTP.
func (h *TOTPHandler) AdminReset(c *fiber.Ctx) error {
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
