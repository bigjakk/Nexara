package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/bigjakk/nexara/internal/api/apischema"
	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/migration"
)

// TestNewMigrationHandler_SlotCapacity asserts the slot semaphore has the
// expected capacity. Constructor wiring this wrong (e.g. unbuffered chan)
// would silently deadlock or pass everything through.
func TestNewMigrationHandler_SlotCapacity(t *testing.T) {
	h := NewMigrationHandler(context.Background(), nil, "", nil)
	if cap(h.slots) != MigrationConcurrencyLimit {
		t.Fatalf("slots capacity = %d, want %d", cap(h.slots), MigrationConcurrencyLimit)
	}
	if MigrationConcurrencyLimit < 1 {
		t.Fatalf("MigrationConcurrencyLimit = %d, want >= 1", MigrationConcurrencyLimit)
	}
}

// TestNewMigrationHandler_NilShutdownCtxFallback ensures partial construction
// (e.g. tests passing nil) doesn't nil-panic when h.shutdownCtx is read later.
func TestNewMigrationHandler_NilShutdownCtxFallback(t *testing.T) {
	h := NewMigrationHandler(nil, nil, "", nil) //nolint:staticcheck // nil ctx is the path under test
	if h.shutdownCtx == nil {
		t.Fatal("shutdownCtx is nil; constructor must fall back to context.Background()")
	}
	// background never expires.
	select {
	case <-h.shutdownCtx.Done():
		t.Fatal("background-derived ctx should not be Done()")
	default:
	}
}

// TestMigrationSlots_NonBlockingSendRejectsWhenFull mirrors the
// `case h.slots <- struct{}{}: default: 429` pattern in Execute. If the
// slots channel is saturated, the non-blocking send must fail (so the
// handler returns 429) — not block the request goroutine indefinitely.
func TestMigrationSlots_NonBlockingSendRejectsWhenFull(t *testing.T) {
	h := NewMigrationHandler(context.Background(), nil, "", nil)

	// Fill every slot.
	for i := 0; i < MigrationConcurrencyLimit; i++ {
		select {
		case h.slots <- struct{}{}:
		default:
			t.Fatalf("slot %d/%d unexpectedly rejected; channel cap is wrong", i+1, MigrationConcurrencyLimit)
		}
	}

	// Next non-blocking send must fail (this is the 429 path).
	select {
	case h.slots <- struct{}{}:
		t.Fatal("expected non-blocking send to fail when slots are saturated")
	default:
		// expected — Execute would return 429 here.
	}

	// Releasing a slot must let the next send succeed.
	<-h.slots
	select {
	case h.slots <- struct{}{}:
		// expected — slot was released.
	default:
		t.Fatal("expected non-blocking send to succeed after a slot is released")
	}
}

// migrationJobDBTX serves one migration job in status, answers the cancel, the
// claim and a check's start as their WHERE clauses do (matching nothing once
// taken, when the job moved on after the handler read it), and notes whether an
// audit row was written.
type migrationJobDBTX struct {
	cluster uuid.UUID
	status  string
	taken   bool
	audited bool
}

func (d *migrationJobDBTX) Exec(_ context.Context, sql string, _ ...any) (pgconn.CommandTag, error) {
	rows := func(ok bool) (pgconn.CommandTag, error) {
		if ok && !d.taken {
			return pgconn.NewCommandTag("UPDATE 1"), nil
		}
		return pgconn.NewCommandTag("UPDATE 0"), nil
	}
	switch {
	case strings.Contains(sql, "-- name: CancelMigrationJob "):
		return rows(d.status == migration.StatusPending)
	case strings.Contains(sql, "-- name: ClaimMigrationJob "), strings.Contains(sql, "-- name: BeginMigrationJobCheck "):
		return rows(d.status == migration.StatusPending || d.status == migration.StatusChecking)
	case strings.Contains(sql, "-- name: InsertAuditLog "):
		d.audited = true
	}
	return pgconn.CommandTag{}, nil
}

func (*migrationJobDBTX) Query(context.Context, string, ...any) (pgx.Rows, error) {
	return nil, errCaptured
}

func (d *migrationJobDBTX) QueryRow(_ context.Context, sql string, _ ...any) pgx.Row {
	if strings.Contains(sql, "-- name: GetMigrationJob ") {
		return replayRow{row: db.MigrationJob{SourceClusterID: d.cluster, TargetClusterID: d.cluster,
			Status: d.status, Vmid: 100}}
	}
	return failRow{err: pgx.ErrNoRows}
}

// A cancel that changed nothing answered "cancelled" and audited it, for a job
// already migrating; two Executes that both read "pending" both started the job;
// a check put a cancelled job back to pending. The first row proves the fake
// cancels and audits at all.
func TestMigrationJob_RefusesAJobThatHasStarted(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name, action, status string
		taken                bool
		want                 int
		audited              bool
	}{
		{"cancel pending", "cancel", migration.StatusPending, false, fiber.StatusOK, true},
		{"cancel migrating", "cancel", migration.StatusMigrating, false, fiber.StatusConflict, false},
		{"execute started by another request", "execute", migration.StatusPending, true, fiber.StatusConflict, false},
		{"check cancelled", "check", migration.StatusCancelled, false, fiber.StatusConflict, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			d := &migrationJobDBTX{cluster: uuid.New(), status: tt.status, taken: tt.taken}
			h := NewMigrationHandler(context.Background(), db.New(d), "", nil)
			app := fiber.New(fiber.Config{ErrorHandler: testErrorHandler})
			app.Use(func(c fiber.Ctx) error {
				c.Locals("user_id", uuid.New())
				c.Locals("rbac_engine", clusterGrantEngine{"manage", "migration", d.cluster})
				return c.Next()
			})
			id := apischema.Properties{"id": {Type: apischema.String, Format: "uuid"}}
			app.Post("/migrations/:id/cancel", withRequestParams(t, id, []string{"id"}, h.Cancel))
			app.Post("/migrations/:id/execute", withRequestParams(t, id, []string{"id"}, h.Execute))
			app.Post("/migrations/:id/check", withRequestParams(t, id, []string{"id"}, h.RunCheck))
			resp, err := app.Test(httptest.NewRequest(http.MethodPost, "/migrations/"+uuid.NewString()+"/"+tt.action, nil))
			if err != nil {
				t.Fatal(err)
			}
			_ = resp.Body.Close()
			if resp.StatusCode != tt.want || d.audited != tt.audited || len(h.slots) != 0 {
				t.Errorf("status %d, audited %v, %d slots held; want %d, %v, none",
					resp.StatusCode, d.audited, len(h.slots), tt.want, tt.audited)
			}
		})
	}
}
