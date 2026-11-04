package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/bigjakk/nexara/internal/api/apischema"
	db "github.com/bigjakk/nexara/internal/db/generated"
)

// A UPS's windows are reached only through its own path: a cluster's window is
// not found there, a span that ends before it starts is refused, and each
// change is audited under the UPS, where its History reads it.
func TestUpsMaintenanceWindowRoutes(t *testing.T) {
	t.Parallel()
	dev := db.GetUpsDeviceRow{ID: uuid.New(), UpsName: "ups01", ServerName: "nut01"}
	start := time.Date(2026, 11, 4, 10, 0, 0, 0, time.UTC)
	own := db.MaintenanceWindow{ID: uuid.New(), UpsID: pgtype.UUID{Bytes: dev.ID, Valid: true}, StartsAt: start,
		EndsAt: start.Add(time.Hour)}
	clusters := db.MaintenanceWindow{ID: uuid.New(), ClusterID: pgtype.UUID{Bytes: uuid.New(), Valid: true},
		StartsAt: start, EndsAt: start.Add(time.Hour)}
	str := apischema.Property{Type: apischema.String, Optional: true}
	props := apischema.Properties{"id": str, "window_id": str, "description": str, "starts_at": str, "ends_at": str}
	for _, tc := range []struct {
		name, method, target, body string
		window                     db.MaintenanceWindow // what the window lookup finds
		want                       int
		audit                      string
	}{
		{"open one", http.MethodPost, "", `{"starts_at":"2026-11-04T10:00:00Z","ends_at":"2026-11-04T11:00:00Z"}`, own,
			fiber.StatusCreated, "ups_maintenance_window_created"},
		{"a span that ends before it starts", http.MethodPost, "", `{"starts_at":"2026-11-04T11:00:00Z","ends_at":"2026-11-04T10:00:00Z"}`,
			own, fiber.StatusBadRequest, ""},
		{"a cluster's window, through the UPS", http.MethodDelete, "/" + clusters.ID.String(), `{}`, clusters, fiber.StatusNotFound, ""},
		{"its own window", http.MethodDelete, "/" + own.ID.String(), `{}`, own, fiber.StatusNoContent, "ups_maintenance_window_deleted"},
	} {
		dbtx := &auditRowsDBTX{
			rowFor: map[string]any{"GetUpsDevice": dev, "GetMaintenanceWindow": tc.window, "InsertUpsMaintenanceWindow": own},
			execOK: map[string]bool{"DeleteMaintenanceWindow": true, "InsertAuditLog": true},
		}
		h := NewNutHandler(db.New(dbtx), testEncryptionKey, nil)
		app := fiber.New(fiber.Config{ErrorHandler: testErrorHandler})
		app.Use(func(c fiber.Ctx) error {
			c.Locals("user_id", uuid.New())
			return c.Next()
		})
		app.Post("/ups/:id/windows", withRequestParams(t, props, []string{"id"}, h.CreateUpsMaintenanceWindow))
		app.Delete("/ups/:id/windows/:window_id", withRequestParams(t, props, []string{"id", "window_id"}, h.DeleteUpsMaintenanceWindow))
		req := httptest.NewRequest(tc.method, "/ups/"+dev.ID.String()+"/windows"+tc.target, strings.NewReader(tc.body))
		req.Header.Set("Content-Type", "application/json")
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != tc.want {
			t.Errorf("%s: status %d, want %d", tc.name, resp.StatusCode, tc.want)
		}
		audits := dbtx.execsOf("InsertAuditLog")
		if tc.audit == "" && len(audits) != 0 {
			t.Errorf("%s: audited %v, want nothing", tc.name, audits)
		} else if tc.audit != "" && (len(audits) != 1 || audits[0][2] != "ups_device" || audits[0][4] != tc.audit) {
			t.Errorf("%s: audit rows %v, want one %s under the UPS", tc.name, audits, tc.audit)
		}
	}
}
