package handlers

import (
	"errors"
	"strings"
	"unicode"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	db "github.com/bigjakk/nexara/internal/db/generated"
)

// MobileDeviceHandler handles registration and management of mobile push
// devices. Each row represents a single phone/tablet that has installed the
// Nexara mobile app and granted notification permission.
type MobileDeviceHandler struct {
	queries *db.Queries
}

// NewMobileDeviceHandler creates a new mobile device handler.
func NewMobileDeviceHandler(queries *db.Queries) *MobileDeviceHandler {
	return &MobileDeviceHandler{queries: queries}
}

// maxDevicesPerUser caps the number of mobile devices a single user can
// register. Prevents a compromised user account from filling the table or
// amplifying every alert dispatch (security review H3).
const maxDevicesPerUser = 20

type registerDeviceRequest struct {
	DeviceID      string `json:"device_id"`
	DeviceName    string `json:"device_name"`
	Platform      string `json:"platform"`
	ExpoPushToken string `json:"expo_push_token"`
}

// mobileDeviceResponse intentionally excludes the `expo_push_token` field.
// Push tokens are sender-credentials for the Expo gateway and the mobile UI
// only needs the row id to delete devices (security review H1).
type mobileDeviceResponse struct {
	ID         uuid.UUID `json:"id"`
	UserID     uuid.UUID `json:"user_id"`
	DeviceID   string    `json:"device_id"`
	DeviceName string    `json:"device_name"`
	Platform   string    `json:"platform"`
	LastSeenAt string    `json:"last_seen_at"`
	CreatedAt  string    `json:"created_at"`
}

func toMobileDeviceResponse(d db.MobileDevice) mobileDeviceResponse {
	return mobileDeviceResponse{
		ID:         d.ID,
		UserID:     d.UserID,
		DeviceID:   d.DeviceID,
		DeviceName: d.DeviceName,
		Platform:   d.Platform,
		LastSeenAt: d.LastSeenAt.Format("2006-01-02T15:04:05Z"),
		CreatedAt:  d.CreatedAt.Format("2006-01-02T15:04:05Z"),
	}
}

// validatePrintableString rejects strings containing control characters or
// other non-printable code points to prevent admin UI injection / log
// poisoning (security review M3).
func validatePrintableString(field, value string) error {
	for _, r := range value {
		if unicode.IsControl(r) {
			return fiber.NewError(fiber.StatusBadRequest, field+" contains control characters")
		}
	}
	return nil
}

// Register handles POST /api/v1/me/devices.
//
// Authenticated users register the current device for push notifications.
// The device_id is a stable per-install UUID generated client-side. The
// expo_push_token is what the dispatcher sends to the Expo Push API.
//
// Conflict semantics:
//   - If the same expo_push_token is registered for the SAME (user_id, device_id)
//     pair, the row is touched (last_seen_at + name updated).
//   - If the token exists for a DIFFERENT device_id or different user, we
//     return 409 Conflict instead of silently reassigning ownership. The
//     legitimate device must explicitly delete the old row (or an admin
//     must) before re-registering.
func (h *MobileDeviceHandler) Register(c *fiber.Ctx) error {
	userID, ok := c.Locals("user_id").(uuid.UUID)
	if !ok {
		return fiber.NewError(fiber.StatusUnauthorized, "Authentication required")
	}

	var req registerDeviceRequest
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}

	req.DeviceID = strings.TrimSpace(req.DeviceID)
	req.DeviceName = strings.TrimSpace(req.DeviceName)
	req.Platform = strings.ToLower(strings.TrimSpace(req.Platform))
	req.ExpoPushToken = strings.TrimSpace(req.ExpoPushToken)

	if req.DeviceID == "" || len(req.DeviceID) > 128 {
		return fiber.NewError(fiber.StatusBadRequest, "device_id is required and must be ≤ 128 chars")
	}
	if err := validatePrintableString("device_id", req.DeviceID); err != nil {
		return err
	}
	if req.DeviceName == "" || len(req.DeviceName) > 128 {
		return fiber.NewError(fiber.StatusBadRequest, "device_name is required and must be ≤ 128 chars")
	}
	if err := validatePrintableString("device_name", req.DeviceName); err != nil {
		return err
	}
	if req.Platform != "ios" && req.Platform != "android" {
		return fiber.NewError(fiber.StatusBadRequest, "platform must be 'ios' or 'android'")
	}
	// Expo push tokens have a stable prefix `ExponentPushToken[…]` or
	// `ExpoPushToken[…]`. Reject anything that doesn't look like one to
	// keep garbage out of the table.
	if !strings.HasPrefix(req.ExpoPushToken, "ExponentPushToken[") &&
		!strings.HasPrefix(req.ExpoPushToken, "ExpoPushToken[") {
		return fiber.NewError(fiber.StatusBadRequest, "expo_push_token has invalid format")
	}
	if len(req.ExpoPushToken) > 256 {
		return fiber.NewError(fiber.StatusBadRequest, "expo_push_token too long")
	}

	// Per-user device cap (security review H3 + M2). We check BEFORE the
	// upsert so an attacker can't grow the table beyond the cap by
	// repeatedly registering different tokens.
	count, err := h.queries.CountMobileDevicesByUser(c.Context(), userID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to check device count")
	}
	if count >= maxDevicesPerUser {
		return fiber.NewError(fiber.StatusTooManyRequests, "Device limit reached for this account")
	}

	device, err := h.queries.RegisterMobileDevice(c.Context(), db.RegisterMobileDeviceParams{
		UserID:        userID,
		DeviceID:      req.DeviceID,
		DeviceName:    req.DeviceName,
		Platform:      req.Platform,
		ExpoPushToken: req.ExpoPushToken,
	})
	if err != nil {
		// The WHERE clause in the UPSERT blocks updates that would
		// reassign a token across user_id or device_id boundaries. If
		// the underlying token already exists under a different
		// (user_id, device_id) pair, the query returns no rows and sqlc
		// surfaces this as ErrNoRows. Map it to 409 Conflict.
		if errors.Is(err, pgx.ErrNoRows) {
			return fiber.NewError(
				fiber.StatusConflict,
				"This push token is already registered to a different device or user. Delete the existing registration first.",
			)
		}
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to register device")
	}

	return c.Status(fiber.StatusCreated).JSON(toMobileDeviceResponse(device))
}

// List handles GET /api/v1/me/devices — returns the current user's devices.
func (h *MobileDeviceHandler) List(c *fiber.Ctx) error {
	userID, ok := c.Locals("user_id").(uuid.UUID)
	if !ok {
		return fiber.NewError(fiber.StatusUnauthorized, "Authentication required")
	}

	devices, err := h.queries.ListMobileDevicesByUser(c.Context(), userID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to list devices")
	}

	resp := make([]mobileDeviceResponse, len(devices))
	for i, d := range devices {
		resp[i] = toMobileDeviceResponse(d)
	}
	return c.JSON(resp)
}

// Delete handles DELETE /api/v1/me/devices/:id.
//
// The user can only delete their own devices. The handler uses
// `DeleteMobileDeviceForUser` which scopes the WHERE by both id AND user_id
// so a malicious user can't delete another user's device by guessing IDs.
func (h *MobileDeviceHandler) Delete(c *fiber.Ctx) error {
	userID, ok := c.Locals("user_id").(uuid.UUID)
	if !ok {
		return fiber.NewError(fiber.StatusUnauthorized, "Authentication required")
	}

	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid device ID")
	}

	if err := h.queries.DeleteMobileDeviceForUser(c.Context(), db.DeleteMobileDeviceForUserParams{
		ID:     id,
		UserID: userID,
	}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fiber.NewError(fiber.StatusNotFound, "Device not found")
		}
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to delete device")
	}

	return c.JSON(fiber.Map{"message": "Device removed"})
}

// AdminListByUser handles GET /api/v1/admin/users/:id/devices.
// Requires the manage:user RBAC permission.
func (h *MobileDeviceHandler) AdminListByUser(c *fiber.Ctx) error {
	if err := requirePerm(c, "manage", "user"); err != nil {
		return err
	}

	targetID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid user ID")
	}

	devices, err := h.queries.ListMobileDevicesByUser(c.Context(), targetID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to list devices")
	}

	resp := make([]mobileDeviceResponse, len(devices))
	for i, d := range devices {
		resp[i] = toMobileDeviceResponse(d)
	}
	return c.JSON(resp)
}

// AdminDelete handles DELETE /api/v1/admin/devices/:id.
// Requires the manage:user RBAC permission.
func (h *MobileDeviceHandler) AdminDelete(c *fiber.Ctx) error {
	if err := requirePerm(c, "manage", "user"); err != nil {
		return err
	}

	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid device ID")
	}

	if err := h.queries.DeleteMobileDevice(c.Context(), id); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fiber.NewError(fiber.StatusNotFound, "Device not found")
		}
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to delete device")
	}
	return c.JSON(fiber.Map{"message": "Device removed"})
}
