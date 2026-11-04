package handlers

import (
	"encoding/json"
	"errors"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/bigjakk/nexara/internal/api/apischema"
	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/safeconv"
)

// A UPS's maintenance windows (000137): while one is open the UPS drops out of
// the global UPS alert rules, and its own rules hold. The routes are global,
// gated on global view:ups with view or manage:maintenance_window, and each
// change is audited under the UPS, so it is in the UPS's History.

// windowUps is the UPS the path names, or 404.
func (h *NutHandler) windowUps(c fiber.Ctx, p *apischema.Params) (db.GetUpsDeviceRow, error) {
	id, err := parseParamUUID(p.String("id"))
	if err != nil {
		return db.GetUpsDeviceRow{}, err
	}
	dev, err := h.queries.GetUpsDevice(c.Context(), id)
	if errors.Is(err, pgx.ErrNoRows) {
		return dev, fiber.NewError(fiber.StatusNotFound, "UPS not found")
	} else if err != nil {
		return dev, fiber.NewError(fiber.StatusInternalServerError, "Failed to look up the UPS")
	}
	return dev, nil
}

// upsWindow is the window the path names, which must be the path UPS's: one of
// another UPS or of a cluster is not found here.
func (h *NutHandler) upsWindow(c fiber.Ctx, p *apischema.Params) (db.GetUpsDeviceRow, db.MaintenanceWindow, error) {
	dev, err := h.windowUps(c, p)
	if err != nil {
		return dev, db.MaintenanceWindow{}, err
	}
	id, err := parseParamUUID(p.String("window_id"))
	if err != nil {
		return dev, db.MaintenanceWindow{}, err
	}
	w, err := h.queries.GetMaintenanceWindow(c.Context(), id)
	if err != nil || w.UpsID != (pgtype.UUID{Bytes: dev.ID, Valid: true}) {
		return dev, w, fiber.NewError(fiber.StatusNotFound, "Maintenance window not found")
	}
	return dev, w, nil
}

// auditUpsWindow records a change to a UPS's window under the UPS, with no
// cluster: a NUT server belongs to none. Its description stays out: audit
// details are every Viewer's.
func (h *NutHandler) auditUpsWindow(c fiber.Ctx, dev db.GetUpsDeviceRow, w db.MaintenanceWindow, action string) {
	details, _ := json.Marshal(map[string]any{
		"window_id": w.ID, "ups": dev.UpsName, "server": dev.ServerName,
		"starts_at": w.StartsAt, "ends_at": w.EndsAt,
		"resource_name": auditTruncate(dev.UpsName, maxAuditResourceNameLen),
	})
	AuditLog(c, h.queries, h.eventPub, pgtype.UUID{}, "ups_device", dev.ID.String(), action, details)
}

// ListUpsMaintenanceWindows lists a UPS's maintenance windows, newest first.
func (h *NutHandler) ListUpsMaintenanceWindows(c fiber.Ctx, p *apischema.Params) error {
	dev, err := h.windowUps(c, p)
	if err != nil {
		return err
	}
	windows, err := h.queries.ListUpsMaintenanceWindows(c.Context(), db.ListUpsMaintenanceWindowsParams{
		UpsID:  pgtype.UUID{Bytes: dev.ID, Valid: true},
		Limit:  safeconv.Int32(int(p.Int("limit"))),
		Offset: safeconv.Int32(int(p.Int("offset"))),
	})
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to list maintenance windows")
	}
	result := make([]maintenanceWindowResponse, len(windows))
	for i, w := range windows {
		result[i] = toMaintenanceWindowResponse(w)
	}
	return RespondItems(c, result)
}

// CreateUpsMaintenanceWindow opens a window on a UPS.
func (h *NutHandler) CreateUpsMaintenanceWindow(c fiber.Ctx, p *apischema.Params) error {
	dev, err := h.windowUps(c, p)
	if err != nil {
		return err
	}
	startsAt, endsAt, err := newWindowSpan(p)
	if err != nil {
		return err
	}
	userID, _ := c.Locals("user_id").(uuid.UUID)
	w, err := h.queries.InsertUpsMaintenanceWindow(c.Context(), db.InsertUpsMaintenanceWindowParams{
		UpsID:       pgtype.UUID{Bytes: dev.ID, Valid: true},
		Description: p.String("description"),
		StartsAt:    startsAt,
		EndsAt:      endsAt,
		CreatedBy:   userID,
	})
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to create maintenance window")
	}
	h.auditUpsWindow(c, dev, w, "ups_maintenance_window_created")
	return c.Status(fiber.StatusCreated).JSON(toMaintenanceWindowResponse(w))
}

// UpdateUpsMaintenanceWindow changes one of a UPS's windows.
func (h *NutHandler) UpdateUpsMaintenanceWindow(c fiber.Ctx, p *apischema.Params) error {
	dev, existing, err := h.upsWindow(c, p)
	if err != nil {
		return err
	}
	description, startsAt, endsAt, err := editedWindow(p, existing)
	if err != nil {
		return err
	}
	w, err := h.queries.UpdateMaintenanceWindow(c.Context(), db.UpdateMaintenanceWindowParams{
		ID: existing.ID, Description: description, StartsAt: startsAt, EndsAt: endsAt,
		NodeID: existing.NodeID, // none: a UPS's window names no node
	})
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to update maintenance window")
	}
	h.auditUpsWindow(c, dev, w, "ups_maintenance_window_updated")
	return c.JSON(toMaintenanceWindowResponse(w))
}

// DeleteUpsMaintenanceWindow removes one of a UPS's windows. Alerts its rules
// held meanwhile are judged again at the next tick.
func (h *NutHandler) DeleteUpsMaintenanceWindow(c fiber.Ctx, p *apischema.Params) error {
	dev, existing, err := h.upsWindow(c, p)
	if err != nil {
		return err
	}
	if err := h.queries.DeleteMaintenanceWindow(c.Context(), existing.ID); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to delete maintenance window")
	}
	h.auditUpsWindow(c, dev, existing, "ups_maintenance_window_deleted")
	return c.SendStatus(fiber.StatusNoContent)
}
