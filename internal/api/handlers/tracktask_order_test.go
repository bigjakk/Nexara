package handlers

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/events"
)

// taskCreatedAt is a go-redis hook noting how many statements capture had sent
// when the task_created event was published.
type taskCreatedAt struct {
	capture *captureDBTX
	at      int
}

func (h *taskCreatedAt) DialHook(next redis.DialHook) redis.DialHook { return next }

func (h *taskCreatedAt) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		if args := cmd.Args(); len(args) == 3 && args[0] == "publish" {
			if payload, _ := args[2].([]byte); strings.Contains(string(payload), `"kind":"`+events.KindTaskCreated+`"`) {
				h.at = len(h.capture.calls) // the request goroutine, which also sends the statements
			}
		}
		return next(ctx, cmd)
	}
}

func (h *taskCreatedAt) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return next
}

// TrackTask writes the task row, then the audit entry, then the task_created
// event. The row comes first because the collector records an audited task
// with no row as Proxmox's (TrackTask's comment); the event last, so a tab that
// refetches on it finds both.
func TestTrackTaskWritesTheRowThenTheAuditEntryThenTheEvent(t *testing.T) {
	t.Parallel()
	capture := &captureDBTX{}
	rdb := redis.NewClient(&redis.Options{Addr: miniredis.RunT(t).Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	hook := &taskCreatedAt{capture: capture, at: -1}
	rdb.AddHook(hook)
	pub := events.NewPublisher(rdb, slog.New(slog.DiscardHandler))

	app := fiber.New()
	app.Post("/", func(c fiber.Ctx) error {
		c.Locals("user_id", uuid.New())
		TrackTask(c, db.New(capture), pub, TrackTaskParams{ClusterID: uuid.New(), Node: "pve-01",
			ResourceType: "vm", ResourceID: "101", Action: "start",
			UPID: "UPID:pve-01:0000A1B2:00C0FFEE:66F1A2B3:qmstart:101:root@pam:"})
		return nil
	})
	if _, err := app.Test(httptest.NewRequest(http.MethodPost, "/", nil)); err != nil {
		t.Fatal(err)
	}
	order := make([]string, 0, len(capture.calls))
	for _, q := range capture.calls {
		order = append(order, strings.Fields(strings.TrimPrefix(q.sql, "-- name: "))[0])
	}
	row := slices.Index(order, "InsertTaskHistory")
	audit := slices.Index(order, "InsertAuditLog")
	if row < 0 || audit < row || hook.at <= audit {
		t.Errorf("statements %v, task_created published after %d of them; want the task row, the audit entry, then the event",
			order, hook.at)
	}
}
