package handlers

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/bigjakk/nexara/internal/api/apischema"
	db "github.com/bigjakk/nexara/internal/db/generated"
)

// dlqListDBTX answers the DLQ listing with no rows and records the channel
// filter it was handed.
type dlqListDBTX struct {
	channel pgtype.UUID
	listed  bool
}

func (*dlqListDBTX) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, errCaptured
}

func (d *dlqListDBTX) Query(_ context.Context, sql string, args ...any) (pgx.Rows, error) {
	if !strings.Contains(sql, "FROM notification_dlq") {
		return nil, errCaptured
	}
	d.listed = true
	for _, a := range args {
		if u, ok := a.(pgtype.UUID); ok {
			d.channel = u
		}
	}
	return &structRows{}, nil
}

func (*dlqListDBTX) QueryRow(context.Context, string, ...any) pgx.Row {
	return failRow{err: errCaptured}
}

// dlqListMirror is the DLQ listing's declaration (registry_notification_dlq.go)
// as far as its handler reads it.
func dlqListMirror(t *testing.T) apischema.Properties {
	t.Helper()
	return compiledMirror(t, apischema.Properties{
		"limit":      {Type: apischema.Integer, Optional: true, Default: 50},
		"offset":     {Type: apischema.Integer, Optional: true, Default: 0},
		"state":      {Type: apischema.String, Optional: true},
		"channel_id": {Type: apischema.String, Optional: true, Pattern: apischema.Rule("uuid-or-empty")},
	})
}

// TestNotificationDLQListTreatsAnEmptyChannelAsNone drives the real List
// handler with ?channel_id= — "every channel", as it has always been — and
// checks it reaches the listing unfiltered instead of failing to parse as a
// uuid — the 500 it would answer if the declaration let the empty value
// through while the handler still parsed every supplied one.
func TestNotificationDLQListTreatsAnEmptyChannelAsNone(t *testing.T) {
	mirror := dlqListMirror(t)
	channel := uuid.New()

	for _, tt := range []struct {
		name        string
		query       string
		wantChannel pgtype.UUID
	}{
		{"omitted", "", pgtype.UUID{}},
		{"empty", "?state=&channel_id=", pgtype.UUID{}},
		{"a channel", "?channel_id=" + channel.String(), pgtype.UUID{Bytes: channel, Valid: true}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dbtx := &dlqListDBTX{}
			handler := NewNotificationDLQHandler(db.New(dbtx), nil, nil)

			app := fiber.New(fiber.Config{ErrorHandler: testErrorHandler})
			app.Use(func(c fiber.Ctx) error {
				c.Locals("user_id", uuid.New())
				c.Locals("role", "admin")
				return c.Next()
			})
			installStubEngineMiddleware(app)
			app.Get("/notification-dlq", withRequestParams(t, mirror, nil, handler.List))

			resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/notification-dlq"+tt.query, nil))
			if err != nil {
				t.Fatalf("request: %v", err)
			}
			defer func() { _ = resp.Body.Close() }()
			if resp.StatusCode != http.StatusOK {
				body, _ := io.ReadAll(resp.Body)
				t.Fatalf("status = %d, want 200 (body: %s)", resp.StatusCode, body)
			}
			if !dbtx.listed {
				t.Fatal("the listing query never ran")
			}
			if dbtx.channel != tt.wantChannel {
				t.Errorf("channel filter = %+v, want %+v", dbtx.channel, tt.wantChannel)
			}
		})
	}
}

// A CVE notice's dead letter lists a cluster's CVEs; its payload is shown only
// to a holder of view:cve_scan, while an alert's dead letter is shown as before.
func TestNotificationDLQListHidesCVEPayloadWithoutViewCVEScan(t *testing.T) {
	t.Parallel()
	cluster := pgtype.UUID{Bytes: uuid.New(), Valid: true}
	cve := db.NotificationDlq{ID: uuid.New(), ClusterID: cluster, Payload: json.RawMessage(`{"metric":"cve_action_required","message":"CVE-2024-1"}`)}
	alert := db.NotificationDlq{ID: uuid.New(), ClusterID: cluster, RuleID: pgtype.UUID{Bytes: uuid.New(), Valid: true},
		Payload: json.RawMessage(`{"metric":"cpu_usage"}`)}
	for _, canCVE := range []bool{false, true} {
		grants := []string{"view:notification_dlq"}
		if canCVE {
			grants = append(grants, "view:cve_scan")
		}
		dbtx := &auditRowsDBTX{rowsFor: map[string][]any{"ListNotificationDLQ": {cve, alert}}}
		h := NewNotificationDLQHandler(db.New(dbtx), nil, nil)
		app := fiber.New(fiber.Config{ErrorHandler: testErrorHandler})
		app.Use(func(c fiber.Ctx) error {
			c.Locals("user_id", uuid.New())
			c.Locals("rbac_engine", newGrantEngine(grants...))
			return c.Next()
		})
		app.Get("/notification-dlq", withRequestParams(t, dlqListMirror(t), nil, h.List))
		resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/notification-dlq", nil))
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK || strings.Contains(string(body), "CVE-2024-1") != canCVE ||
			!strings.Contains(string(body), "cpu_usage") {
			t.Errorf("view:cve_scan %v: %d %s", canCVE, resp.StatusCode, body)
		}
	}
}
