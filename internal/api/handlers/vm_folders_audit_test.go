package handlers

import (
	"context"
	"encoding/json"
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

// folderAuditDBTX answers the folder routes' reads — the guest, any folder as
// one of the cluster's under parent (parent itself at the top level), the
// folder a guest leaves (from, "" for none) — and keeps each audit row's
// details.
type folderAuditDBTX struct {
	cluster, parent uuid.UUID
	from            string
	details         []map[string]any
}

func (d *folderAuditDBTX) Exec(_ context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	if strings.Contains(sql, "-- name: InsertAuditLog :exec") {
		var m map[string]any
		_ = json.Unmarshal(args[5].(json.RawMessage), &m)
		d.details = append(d.details, m)
	}
	return pgconn.CommandTag{}, nil
}

func (*folderAuditDBTX) Query(context.Context, string, ...any) (pgx.Rows, error) {
	return nil, errCaptured
}

func (d *folderAuditDBTX) QueryRow(_ context.Context, sql string, args ...any) pgx.Row {
	folder := func(id uuid.UUID) db.VmFolder {
		return db.VmFolder{ID: id, ClusterID: d.cluster, Name: "folder01",
			ParentID: pgtype.UUID{Bytes: d.parent, Valid: id != d.parent}}
	}
	switch {
	case strings.Contains(sql, "-- name: GetVM :one"):
		return replayRow{row: db.Vm{ID: args[0].(uuid.UUID), ClusterID: d.cluster, Vmid: 100}}
	case strings.Contains(sql, "-- name: GetVMFolder :one"):
		return replayRow{row: folder(args[0].(uuid.UUID))}
	case strings.Contains(sql, "-- name: MoveVMFolder :one"):
		return replayRow{row: folder(args[0].(uuid.UUID))}
	case strings.Contains(sql, "-- name: AssignVMToFolder :one"):
		return replayRow{row: struct{ From string }{d.from}}
	case strings.Contains(sql, "-- name: UnassignVMFromFolder :one") && d.from != "":
		return replayRow{row: struct{ From uuid.UUID }{uuid.MustParse(d.from)}}
	}
	return failRow{err: pgx.ErrNoRows}
}

// What left a folder is found in its History by the folder each audit row says
// it left: a guest moved or unfiled (from_folder_id), a subfolder moved
// (from_parent_id) or deleted (parent_id). A guest filed from no folder names
// none.
func TestVMFolderAuditNamesTheFolderLeft(t *testing.T) {
	t.Parallel()
	cluster, parent, left, folder := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	base := "/clusters/" + cluster.String()
	for _, tt := range []struct {
		name, method, path, body, from, key string
		want                                any
	}{
		{"a guest moved", http.MethodPut, "/vms/" + uuid.NewString() + "/folder",
			`{"folder_id":"` + folder.String() + `"}`, left.String(), "from_folder_id", left.String()},
		{"a guest unfiled", http.MethodPut, "/vms/" + uuid.NewString() + "/folder", `{}`, left.String(),
			"from_folder_id", left.String()},
		{"a guest filed from none", http.MethodPut, "/vms/" + uuid.NewString() + "/folder",
			`{"folder_id":"` + folder.String() + `"}`, "", "from_folder_id", nil},
		{"a subfolder moved", http.MethodPatch, "/vm-folders/" + folder.String(),
			`{"parent_id":"` + left.String() + `"}`, "", "from_parent_id", parent.String()},
		{"a subfolder deleted", http.MethodDelete, "/vm-folders/" + folder.String(), ``, "", "parent_id", parent.String()},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			dbtx := &folderAuditDBTX{cluster: cluster, parent: parent, from: tt.from}
			h := NewVMFoldersHandler(db.New(dbtx), nil)
			uuidParam := apischema.Property{Type: apischema.String, Format: "uuid"}
			app := fiber.New(fiber.Config{ErrorHandler: testErrorHandler})
			app.Use(func(c fiber.Ctx) error {
				c.Locals("user_id", uuid.New())
				c.Locals("rbac_engine", clusterGrantEngine{"manage", "vm_folder", cluster})
				return c.Next()
			})
			app.Put("/clusters/:cluster_id/vms/:vm_id/folder", withRequestParams(t, apischema.Properties{
				"cluster_id": uuidParam, "vm_id": uuidParam,
				"folder_id": {Type: apischema.String, Optional: true, Format: "uuid"},
			}, []string{"cluster_id", "vm_id"}, h.AssignVM))
			app.Delete("/clusters/:cluster_id/vm-folders/:folder_id", withRequestParams(t, apischema.Properties{
				"cluster_id": uuidParam, "folder_id": uuidParam,
			}, []string{"cluster_id", "folder_id"}, h.Delete))
			app.Patch("/clusters/:cluster_id/vm-folders/:folder_id", h.Update)
			req := httptest.NewRequest(tt.method, base+tt.path, strings.NewReader(tt.body))
			req.Header.Set("Content-Type", "application/json")
			resp, err := app.Test(req)
			if err != nil {
				t.Fatal(err)
			}
			_ = resp.Body.Close()
			if resp.StatusCode >= 300 || len(dbtx.details) != 1 {
				t.Fatalf("status %d, %d audit rows; want success and one", resp.StatusCode, len(dbtx.details))
			}
			if got, ok := dbtx.details[0][tt.key]; got != tt.want || (tt.want == nil) == ok {
				t.Errorf("%s = %v (present %v), want %v", tt.key, got, ok, tt.want)
			}
		})
	}
}
