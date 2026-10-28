package api

import (
	"context"
	"net/http"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/bigjakk/nexara/internal/api/handlers"
	"github.com/bigjakk/nexara/internal/auth"
	db "github.com/bigjakk/nexara/internal/db/generated"
)

// instanceWideVeeamViewer is a caller who holds view:veeam on the whole install,
// which the run listing reads from the permission engine's list.
type instanceWideVeeamViewer struct{ *stubRBACEngine }

func (*instanceWideVeeamViewer) LoadUserPermissions(context.Context, uuid.UUID) (*auth.UserPermissions, error) {
	return &auth.UserPermissions{Permissions: []auth.ScopedPermission{
		{Action: "view", Resource: "veeam", ScopeType: "global"},
	}}, nil
}

// A job's History lists its own runs, which only the listing can promise on a
// busy server: it cuts at the newest few hundred runs of every job. So a job
// named in the request reaches the query as that job, and none as every run.
func TestVeeamRunListIsNarrowedToTheJobNamed(t *testing.T) {
	t.Parallel()
	server, job := uuid.New(), uuid.New()
	for _, tt := range []struct {
		name, query string
		want        pgtype.UUID
	}{
		{"a job named", "?job_id=" + job.String(), pgtype.UUID{Bytes: job, Valid: true}},
		{"no job named", "", pgtype.UUID{}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			dbtx := &backupRunDBTX{
				lookups: map[string]any{"GetVeeamServer": db.VeeamServer{ID: server}},
				lists:   map[string][]any{"ListVeeamPlatformsByServer": nil, "ListVeeamSessionsByServer": nil},
			}
			e := declaredEndpoint(t, fiber.MethodGet, veeamScope+"/:id/sessions")
			e.Handler = handlers.NewVeeamHandler(db.New(dbtx), "", nil).ListSessions
			viewer := func(c fiber.Ctx) error {
				c.Locals("user_id", uuid.MustParse(testUserID))
				c.Locals("rbac_engine", &instanceWideVeeamViewer{&stubRBACEngine{}})
				return c.Next()
			}
			app := newRegistryApp(t, viewer, e)

			status, env := send(t, app, authedRequest(fiber.MethodGet, "/api/v1/veeam-servers/"+server.String()+"/sessions"+tt.query))

			if status != http.StatusOK {
				t.Fatalf("answered %d (%q), want 200", status, env.Message)
			}
			args := dbtx.queried["ListVeeamSessionsByServer"]
			if len(args) < 3 || args[2] != tt.want {
				t.Errorf("the listing asked for the job %v, want %v", args, tt.want)
			}
		})
	}
}
