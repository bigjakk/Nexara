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
// live_import — needs execute on its kind, as the power route does. A create's
// extra may carry neither a typed parameter's key nor a restore option, nor
// another route's (ha-managed, import-working-storage); no create or config
// write may import a disk (import-from), nor a create name an existing volume,
// nor a media change mount anything but an ISO.
// A refusal reaches Proxmox with nothing and records nothing.
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
		name, path, grant, body, need string // grant: the route's own permissions, space-separated
		status                        int
		bind                          func(*db.Queries) func(fiber.Ctx, *apischema.Params) error
		method                        string // POST when empty
	}
	vms, cts := clusterScope+"/vms", containerScope
	rows := make([]row, 0, 14)
	rows = append(rows, []row{
		{"VM create with start", vms, "manage:vm", `{"node":"pve-01","vmid":101,"start":true}`, "execute:vm", 403, vm, ""},
		{"container create with start", cts, "manage:container", ctBody + `,"start":true}`, "execute:container", 403, ct, ""},
		{"import with start_after", importScope, "manage:vm_import manage:vm", importBody + `,"start_after":true}`, "execute:vm", 403,
			func(q *db.Queries) func(fiber.Ctx, *apischema.Params) error {
				return handlers.NewVMImportHandler(q, backupRunEncKey, nil).StartVMImport
			}, ""},
		{"import with live_import", importScope, "manage:vm_import manage:vm", importBody + `,"live_import":true}`, "execute:vm", 403,
			func(q *db.Queries) func(fiber.Ctx, *apischema.Params) error {
				return handlers.NewVMImportHandler(q, backupRunEncKey, nil).StartVMImport
			}, ""},
		// extra is merged last, so a typed key in it would override what was checked.
		{"VM create with extra start: the execute bypass", vms, "manage:vm",
			`{"node":"pve-01","vmid":101,"extra":{"start":"1"}}`, "extra.start", 400, vm, ""},
		{"VM create with extra vmid", vms, "manage:vm", `{"node":"pve-01","vmid":101,"extra":{"vmid":"102"}}`, "extra.vmid", 400, vm, ""},
		{"container create with extra hostname", cts, "manage:container", ctBody + `,"extra":{"hostname":"linux02"}}`, "extra.hostname", 400, ct, ""},
		{"VM create with extra ha-managed", vms, "manage:vm", `{"node":"pve-01","vmid":101,"extra":{"ha-managed":"1"}}`,
			"extra.ha-managed is not accepted on a create", 400, vm, ""},
		{"VM create with extra import-working-storage", vms, "manage:vm",
			`{"node":"pve-01","vmid":101,"extra":{"import-working-storage":"store01"}}`,
			"extra.import-working-storage is not accepted on a create", 400, vm, ""},
		{"container create with extra ha-managed", cts, "manage:container", ctBody + `,"extra":{"ha-managed":"1"}}`,
			"extra.ha-managed is not accepted on a create", 400, ct, ""},
		{"VM create importing a disk", vms, "manage:vm",
			`{"node":"pve-01","vmid":101,"scsi0":"store01:0,import-from=store02:import/linux01.ova/disk0.vmdk"}`,
			"scsi0 imports a disk", 400, vm, ""},
		{"VM create importing a disk in extra", vms, "manage:vm",
			`{"node":"pve-01","vmid":101,"extra":{"sata1":"store01:0,import-from=store02:vm-102-disk-0"}}`,
			"extra.sata1 imports a disk", 400, vm, ""},
		{"VM create naming another guest's disk", vms, "manage:vm",
			`{"node":"pve-01","vmid":101,"scsi0":"store01:vm-102-disk-0"}`, "scsi0 names a volume", 400, vm, ""},
		{"VM create parking another guest's disk as unused", vms, "manage:vm",
			`{"node":"pve-01","vmid":101,"extra":{"unused0":"store01:vm-102-disk-0"}}`, "extra.unused0 names a volume", 400, vm, ""},
		{"container create naming another guest's volume", cts, "manage:container",
			ctBody + `,"extra":{"mp0":"store01:vm-102-disk-0,mp=/mnt"}}`, "extra.mp0 names a volume", 400, ct, ""},
		{"a config write importing a disk", clusterScope + "/vms/:vm_id/config", "manage:vm",
			`{"fields":{"scsi1":"store01:0,import-from=store02:vm-102-disk-0"}}`, "fields.scsi1 imports a disk", 400,
			func(q *db.Queries) func(fiber.Ctx, *apischema.Params) error {
				return handlers.NewVMHandler(q, backupRunEncKey, nil).SetVMConfig
			}, fiber.MethodPut},
		{"a media change naming a disk", clusterScope + "/vms/:vm_id/media", "execute:vm",
			`{"volid":"store02:vm-102-disk-0"}`, "volid", 400,
			func(q *db.Queries) func(fiber.Ctx, *apischema.Params) error {
				return handlers.NewVMHandler(q, backupRunEncKey, nil).ChangeMedia
			}, ""},
	}...)
	// A create never restores: each restore option of its kind is refused, even "0".
	for _, k := range []string{"archive", "force", "unique", "live-restore"} {
		rows = append(rows, row{"VM create with extra " + k, vms, "manage:vm",
			`{"node":"pve-01","vmid":101,"extra":{"` + k + `":"0"}}`, "extra." + k + " is not accepted on a create", 400, vm, ""})
	}
	for _, k := range []string{"restore", "force", "unique"} {
		rows = append(rows, row{"container create with extra " + k, cts, "manage:container",
			ctBody + `,"extra":{"` + k + `":"0"}}`, "extra." + k + " is not accepted on a create", 400, ct, ""})
	}
	for _, tt := range rows {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var asked atomic.Int32
			pve := &backupRunPVE{handle: func(http.ResponseWriter, *http.Request) bool { asked.Add(1); return false }}
			method := tt.method
			if method == "" {
				method = fiber.MethodPost
			}
			app, dbtx := newPVEAppAs(t, pve, grantsOf(strings.Fields(tt.grant)...), method, tt.path, tt.bind)
			target := strings.NewReplacer(":vm_id", testVMID).Replace(backupRoute(tt.path))
			req := httptest.NewRequest(method, target, strings.NewReader(tt.body))
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
