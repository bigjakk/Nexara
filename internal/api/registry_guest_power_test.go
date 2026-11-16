package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/gofiber/fiber/v3"

	"github.com/bigjakk/nexara/internal/api/apischema"
	"github.com/bigjakk/nexara/internal/api/handlers"
	db "github.com/bigjakk/nexara/internal/db/generated"
)

// Whatever powers a guest on — a create's start, an import's start_after or
// live_import — needs execute on its kind, as the power route does; manage alone
// is refused 403 naming it, before Proxmox is asked anything.
func TestStartingAGuestNeedsExecute(t *testing.T) {
	t.Parallel()
	const importBody = `{"node":"pve-01","storage":"store01","volume":"store01:import/linux01.ova",` +
		`"target_node":"pve-01","target_storage":"store02"`
	for _, tt := range []struct {
		name, path, manage, body, need string
		bind                           func(*db.Queries) func(fiber.Ctx, *apischema.Params) error
	}{
		{"VM create with start", clusterScope + "/vms", "manage:vm", `{"node":"pve-01","vmid":101,"start":true}`, "execute:vm",
			func(q *db.Queries) func(fiber.Ctx, *apischema.Params) error {
				return handlers.NewVMHandler(q, backupRunEncKey, nil).CreateVM
			}},
		{"container create with start", containerScope, "manage:container",
			`{"node":"pve-01","vmid":101,"hostname":"linux01","ostemplate":"store01:vztmpl/linux.tar.zst","start":true}`,
			"execute:container", func(q *db.Queries) func(fiber.Ctx, *apischema.Params) error {
				return handlers.NewContainerHandler(q, backupRunEncKey, nil).CreateContainer
			}},
		// extra's force, beside archive or restore, overwrites a guest: delete, as the restore route asks.
		{"VM create with extra force", clusterScope + "/vms", "manage:vm", `{"node":"pve-01","vmid":101,"extra":{"force":1}}`, "delete:vm",
			func(q *db.Queries) func(fiber.Ctx, *apischema.Params) error {
				return handlers.NewVMHandler(q, backupRunEncKey, nil).CreateVM
			}},
		{"container create with extra force", containerScope, "manage:container",
			`{"node":"pve-01","vmid":101,"hostname":"linux01","ostemplate":"store01:backup/vzdump-lxc-101.tar.zst","extra":{"restore":"1","force":"1"}}`,
			"delete:container", func(q *db.Queries) func(fiber.Ctx, *apischema.Params) error {
				return handlers.NewContainerHandler(q, backupRunEncKey, nil).CreateContainer
			}},
		{"import with start_after", importScope, "manage:vm_import", importBody + `,"start_after":true}`, "execute:vm",
			func(q *db.Queries) func(fiber.Ctx, *apischema.Params) error {
				return handlers.NewVMImportHandler(q, backupRunEncKey, nil).StartVMImport
			}},
		{"import with live_import", importScope, "manage:vm_import", importBody + `,"live_import":true}`, "execute:vm",
			func(q *db.Queries) func(fiber.Ctx, *apischema.Params) error {
				return handlers.NewVMImportHandler(q, backupRunEncKey, nil).StartVMImport
			}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var asked atomic.Int32
			pve := &backupRunPVE{handle: func(http.ResponseWriter, *http.Request) bool { asked.Add(1); return false }}
			app, _ := newPVEAppAs(t, pve, grantsOf(tt.manage), fiber.MethodPost, tt.path, tt.bind)
			req := httptest.NewRequest(http.MethodPost, backupRoute(tt.path), strings.NewReader(tt.body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-Test-User", "yes")
			status, body := sendBody(t, app, req)
			if status != http.StatusForbidden || !strings.Contains(string(body), tt.need) || asked.Load() != 0 {
				t.Errorf("answered %d %s after %d Proxmox requests; want 403 naming %s and none", status, body, asked.Load(), tt.need)
			}
		})
	}
}
