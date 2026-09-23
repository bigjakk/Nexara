package handlers

import (
	"context"
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

// TestNotificationDLQListTreatsAnEmptyChannelAsNone drives the real List
// handler with ?channel_id= — "every channel", as it has always been — and
// checks it reaches the listing unfiltered instead of failing to parse as a
// uuid — the 500 it would answer if the declaration let the empty value
// through while the handler still parsed every supplied one.
func TestNotificationDLQListTreatsAnEmptyChannelAsNone(t *testing.T) {
	mirror := compiledMirror(t, apischema.Properties{
		"limit":      {Type: apischema.Integer, Optional: true, Default: 50},
		"offset":     {Type: apischema.Integer, Optional: true, Default: 0},
		"state":      {Type: apischema.String, Optional: true},
		"channel_id": {Type: apischema.String, Optional: true, Pattern: apischema.Rule("uuid-or-empty")},
	})
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
