package handlers

import (
	"errors"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/bigjakk/nexara/internal/db/generated"
)

// sessionResponse is the public shape of a row in the sessions table.
//
// It exists to leave TokenHash behind. db.Session carries the hash of the
// refresh token, and the sessions table is one of the few whose model must
// never be serialised straight to a client — returning the row as-is would
// hand every caller the verifier for their own live sessions.
type sessionResponse struct {
	ID         uuid.UUID `json:"id"`
	DeviceName string    `json:"device_name"`
	DeviceType string    `json:"device_type"`
	UserAgent  string    `json:"user_agent"`
	IPAddress  string    `json:"ip_address"`
	CreatedAt  string    `json:"created_at"`
	LastUsedAt string    `json:"last_used_at"`
	ExpiresAt  string    `json:"expires_at"`
	// IsCurrent marks the session the caller is making this request from, so
	// the UI can label it and warn before revoking it.
	IsCurrent bool `json:"is_current"`
}

// textOrEmpty renders a nullable column as a plain string.
//
// device_name/device_type/device_id arrived in migration 000045, so every
// session created before it has them NULL. Callers get "" rather than null so
// the field is always a string to render.
func textOrEmpty(t pgtype.Text) string {
	if !t.Valid {
		return ""
	}
	return t.String
}

func toSessionResponse(s db.Session, currentID uuid.UUID) sessionResponse {
	return sessionResponse{
		ID:         s.ID,
		DeviceName: textOrEmpty(s.DeviceName),
		DeviceType: textOrEmpty(s.DeviceType),
		UserAgent:  s.UserAgent,
		IPAddress:  s.IpAddress,
		CreatedAt:  s.CreatedAt.Format(time.RFC3339Nano),
		LastUsedAt: s.LastUsedAt.Format(time.RFC3339Nano),
		ExpiresAt:  s.ExpiresAt.Format(time.RFC3339Nano),
		IsCurrent:  s.ID == currentID,
	}
}

// currentSessionID resolves the session the request is being made from, via
// the refresh cookie.
//
// Best-effort by design: the access token carries no session id, so a caller
// without the cookie (an API client, or a browser whose cookie has expired
// while its access token has not) simply gets no session flagged current. That
// is a missing label, not an error — nothing about the response depends on it.
func (h *AuthHandler) currentSessionID(c fiber.Ctx) uuid.UUID {
	token := readRefreshTokenFromCookie(c)
	if token == "" {
		return uuid.Nil
	}
	session, err := h.sessionManager.ValidateRefreshToken(c.Context(), token)
	if err != nil {
		return uuid.Nil
	}
	return session.ID
}

// ListSessions handles GET /api/v1/auth/sessions.
//
// Returns the caller's own active sessions — what is signed in to this
// account, and from where. Self-service: there is no permission to check
// because a user is always entitled to their own sessions, and the query is
// keyed by the authenticated user id so there is no way to ask for another's.
func (h *AuthHandler) ListSessions(c fiber.Ctx) error {
	userID, ok := c.Locals("user_id").(uuid.UUID)
	if !ok {
		return fiber.NewError(fiber.StatusUnauthorized, "Authentication required")
	}

	sessions, err := h.queries.ListUserSessions(c.Context(), userID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to list sessions")
	}

	currentID := h.currentSessionID(c)
	items := make([]sessionResponse, 0, len(sessions))
	for _, s := range sessions {
		items = append(items, toSessionResponse(s, currentID))
	}
	return RespondItems(c, items)
}

// RevokeSessionByID handles DELETE /api/v1/auth/sessions/:id.
//
// Revokes one of the caller's own sessions — the "sign out that other device"
// action. Self-service for the same reason as ListSessions; ownership is
// enforced below rather than by a permission.
func (h *AuthHandler) RevokeSessionByID(c fiber.Ctx) error {
	userID, ok := c.Locals("user_id").(uuid.UUID)
	if !ok {
		return fiber.NewError(fiber.StatusUnauthorized, "Authentication required")
	}

	sessionID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid session ID")
	}

	session, err := h.queries.GetSessionByID(c.Context(), sessionID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fiber.NewError(fiber.StatusNotFound, "Session not found")
		}
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to look up session")
	}

	// Someone else's session answers 404, not 403. A 403 would confirm that
	// the id names a real session on another account, turning this into an
	// oracle for probing session ids; "not found" is true from the caller's
	// point of view, since they have no session by that id.
	if session.UserID != userID {
		return fiber.NewError(fiber.StatusNotFound, "Session not found")
	}

	// Already revoked (or expired) is "not found" too. GetSessionByID applies
	// neither filter, unlike ListUserSessions, so without this the same id can
	// be revoked repeatedly — each call a no-op UPDATE that still writes an
	// audit row. Everything under /api/v1/auth/ is exempt from the general rate
	// limiter and these routes have no bucket of their own, so that is an
	// unbounded audit-table write available to any authenticated user. Refusing
	// here makes the operation observably idempotent and writes nothing.
	if session.IsRevoked || time.Now().After(session.ExpiresAt) {
		return fiber.NewError(fiber.StatusNotFound, "Session not found")
	}

	// Resolved BEFORE the revoke, and it has to be. currentSessionID looks the
	// cookie up through GetSessionByTokenHash, whose SQL filters on
	// `is_revoked = false` — so once RevokeSession has run, the cookie resolves
	// to nothing and this returns uuid.Nil. Asking afterwards makes the
	// comparison below false for every input, silently skipping the cookie
	// clear it exists to perform.
	currentID := h.currentSessionID(c)

	if err := h.sessionManager.RevokeSession(c.Context(), sessionID); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to revoke session")
	}

	// Revoking the session you are using is allowed — it is how you sign this
	// device out from the session list. Clear the cookie so the browser is not
	// left holding a refresh token that is now dead: without this the next
	// refresh fails with no explanation instead of a clean logout.
	if sessionID == currentID {
		clearRefreshCookie(c)
	}

	AuditLog(c, h.queries, h.eventPub, pgtype.UUID{}, "auth", sessionID.String(), "revoke_session", nil)

	return c.JSON(fiber.Map{"message": "Session revoked"})
}
