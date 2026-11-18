package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"

	"github.com/bigjakk/nexara/internal/api/apischema"
	"github.com/bigjakk/nexara/internal/api/handlers"
	db "github.com/bigjakk/nexara/internal/db/generated"
)

// Whatever powers a guest on — a create's start, an import's start_after or
// live_import — needs execute on its kind, as the power route does, and a cancel's
// delete_vm needs delete:vm, as the delete route does. A create's extra may carry
// neither a typed parameter's key nor a restore option. A refusal reaches Proxmox
// with nothing and records nothing.
func TestStartingAGuestNeedsExecute(t *testing.T) {
	t.Parallel()
	const importBody = `{"node":"pve-01","storage":"store01","volume":"store01:import/linux01.ova",` +
		`"target_node":"pve-01","target_storage":"store02"`
	const ctBody = `{"node":"pve-01","vmid":101,"hostname":"linux01","ostemplate":"store01:vztmpl/linux.tar.zst"`
	vm := func(q *db.Queries) func(fiber.Ctx, *apischema.Params) error {
		return handlers.NewVMHandler(q, backupRunEncKey, nil).CreateVM
	}
	ct := func(q *db.Queries) func(fiber.Ctx, *apischema.Params) error {
		return handlers.NewContainerHandler(q, backupRunEncKey, nil).CreateContainer
	}
	type row struct {
		name, path, grant, body, need string
		status                        int
		bind                          func(*db.Queries) func(fiber.Ctx, *apischema.Params) error
	}
	vms, cts := clusterScope+"/vms", containerScope
	rows := make([]row, 0, 15)
	rows = append(rows, []row{
		{"VM create with start", vms, "manage:vm", `{"node":"pve-01","vmid":101,"start":true}`, "execute:vm", 403, vm},
		{"container create with start", cts, "manage:container", ctBody + `,"start":true}`, "execute:container", 403, ct},
		{"import with start_after", importScope, "manage:vm_import", importBody + `,"start_after":true}`, "execute:vm", 403,
			func(q *db.Queries) func(fiber.Ctx, *apischema.Params) error {
				return handlers.NewVMImportHandler(q, backupRunEncKey, nil).StartVMImport
			}},
		{"import with live_import", importScope, "manage:vm_import", importBody + `,"live_import":true}`, "execute:vm", 403,
			func(q *db.Queries) func(fiber.Ctx, *apischema.Params) error {
				return handlers.NewVMImportHandler(q, backupRunEncKey, nil).StartVMImport
			}},
		{"import cancel with delete_vm", importScope + "/:id/cancel", "manage:vm_import", `{"delete_vm":true}`,
			"delete:vm", 403, func(q *db.Queries) func(fiber.Ctx, *apischema.Params) error {
				return handlers.NewVMImportHandler(q, backupRunEncKey, nil).CancelVMImport
			}},
		// extra is merged last, so a typed key in it would override what was checked.
		{"VM create with extra start: the execute bypass", vms, "manage:vm",
			`{"node":"pve-01","vmid":101,"extra":{"start":"1"}}`, "extra.start", 400, vm},
		{"VM create with extra vmid", vms, "manage:vm", `{"node":"pve-01","vmid":101,"extra":{"vmid":"102"}}`, "extra.vmid", 400, vm},
		{"container create with extra hostname", cts, "manage:container", ctBody + `,"extra":{"hostname":"linux02"}}`, "extra.hostname", 400, ct},
	}...)
	// A create never restores: each restore option of its kind is refused, even "0".
	for _, k := range []string{"archive", "force", "unique", "live-restore"} {
		rows = append(rows, row{"VM create with extra " + k, vms, "manage:vm",
			`{"node":"pve-01","vmid":101,"extra":{"` + k + `":"0"}}`, "extra." + k + " is not accepted on a create", 400, vm})
	}
	for _, k := range []string{"restore", "force", "unique"} {
		rows = append(rows, row{"container create with extra " + k, cts, "manage:container",
			ctBody + `,"extra":{"` + k + `":"0"}}`, "extra." + k + " is not accepted on a create", 400, ct})
	}
	for _, tt := range rows {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var asked atomic.Int32
			pve := &backupRunPVE{handle: func(http.ResponseWriter, *http.Request) bool { asked.Add(1); return false }}
			app, dbtx := newPVEAppAs(t, pve, grantsOf(tt.grant), fiber.MethodPost, tt.path, tt.bind)
			// The cancel's import is running on pve-01, so without its check the
			// destroy would be sent. The other routes read neither row.
			dbtx.lookups = map[string]any{
				"GetVMImportJob": db.VmImportJob{ID: uuid.MustParse(testImportJobID), ClusterID: uuid.MustParse(testClusterID),
					TargetNode: "pve-01", TargetVmid: 101, Status: "running",
					Upid: "UPID:pve-01:0000A1B2:00C0FFEE:66F1A2B3:qmcreate:101:user@pam!test:"},
				"GetCluster": db.Cluster{ID: uuid.MustParse(testClusterID)},
			}
			target := strings.ReplaceAll(backupRoute(tt.path), ":id", testImportJobID)
			req := httptest.NewRequest(http.MethodPost, target, strings.NewReader(tt.body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-Test-User", "yes")
			status, body := sendBody(t, app, req)
			if status != tt.status || !strings.Contains(string(body), tt.need) || asked.Load() != 0 ||
				len(dbtx.decodedTasks(t)) != 0 || len(dbtx.decodedAudits(t)) != 0 {
				t.Errorf("answered %d %s after %d Proxmox requests; want %d naming %s, none, and nothing recorded",
					status, body, asked.Load(), tt.status, tt.need)
			}
		})
	}
}
