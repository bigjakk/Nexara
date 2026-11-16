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
// live_import, a VM create's extra live-restore — needs execute on its kind, as the
// power route does. A create's extra may not carry a typed parameter's key, and one
// carrying a restore key is held to the restore route (manage:backup, delete for
// force) and recorded as a restore. A refusal reaches Proxmox with nothing.
func TestStartingAGuestNeedsExecute(t *testing.T) {
	t.Parallel()
	const importBody = `{"node":"pve-01","storage":"store01","volume":"store01:import/linux01.ova",` +
		`"target_node":"pve-01","target_storage":"store02"`
	const ctBody = `{"node":"pve-01","vmid":101,"hostname":"linux01","ostemplate":`
	const ctBackup = "store01:backup/vzdump-lxc-101.tar.zst"
	vm := func(q *db.Queries) func(fiber.Ctx, *apischema.Params) error {
		return handlers.NewVMHandler(q, backupRunEncKey, nil).CreateVM
	}
	ct := func(q *db.Queries) func(fiber.Ctx, *apischema.Params) error {
		return handlers.NewContainerHandler(q, backupRunEncKey, nil).CreateContainer
	}
	vms, cts := clusterScope+"/vms", containerScope
	for _, tt := range []struct {
		name, path string
		grants     []string
		body, need string
		status     int    // 403 when zero
		archive    string // the archive a restore that went through records
		bind       func(*db.Queries) func(fiber.Ctx, *apischema.Params) error
	}{
		{"VM create with start", vms, []string{"manage:vm"}, `{"node":"pve-01","vmid":101,"start":true}`, "execute:vm", 0, "", vm},
		{"container create with start", cts, []string{"manage:container"},
			ctBody + `"store01:vztmpl/linux.tar.zst","start":true}`, "execute:container", 0, "", ct},
		{"import with start_after", importScope, []string{"manage:vm_import"}, importBody + `,"start_after":true}`, "execute:vm", 0, "",
			func(q *db.Queries) func(fiber.Ctx, *apischema.Params) error {
				return handlers.NewVMImportHandler(q, backupRunEncKey, nil).StartVMImport
			}},
		{"import with live_import", importScope, []string{"manage:vm_import"}, importBody + `,"live_import":true}`, "execute:vm", 0, "",
			func(q *db.Queries) func(fiber.Ctx, *apischema.Params) error {
				return handlers.NewVMImportHandler(q, backupRunEncKey, nil).StartVMImport
			}},
		{"VM create with extra live-restore", vms, []string{"manage:vm"},
			`{"node":"pve-01","vmid":101,"extra":{"live-restore":"1"}}`, "execute:vm", 0, "", vm},
		// extra is merged last, so a typed key in it would override what was checked.
		{"VM create with extra start: the execute bypass", vms, []string{"manage:vm"},
			`{"node":"pve-01","vmid":101,"extra":{"start":"1"}}`, "extra.start", 400, "", vm},
		{"VM create with extra vmid", vms, []string{"manage:vm"}, `{"node":"pve-01","vmid":101,"extra":{"vmid":"102"}}`, "extra.vmid", 400, "", vm},
		{"container create with extra hostname", cts, []string{"manage:container"},
			ctBody + `"store01:vztmpl/linux.tar.zst","extra":{"hostname":"linux02"}}`, "extra.hostname", 400, "", ct},
		// A restore key makes the create a restore: manage:backup, then delete for force.
		{"VM create with extra archive without manage:backup", vms, []string{"manage:vm"},
			`{"node":"pve-01","vmid":101,"extra":{"archive":"store01:backup/vm/101/2023-11-14T22:13:20Z"}}`, "manage:backup", 0, "", vm},
		{"container create with extra restore without manage:backup", cts, []string{"manage:container"},
			ctBody + `"` + ctBackup + `","extra":{"restore":"1"}}`, "manage:backup", 0, "", ct},
		{"VM create with extra force", vms, []string{"manage:vm"}, `{"node":"pve-01","vmid":101,"extra":{"force":1}}`, "delete:vm", 0, "", vm},
		{"container create with extra restore and force", cts, []string{"manage:container", "manage:backup"},
			ctBody + `"` + ctBackup + `","extra":{"restore":"1","force":"1"}}`, "delete:container", 0, "", ct},
		{"VM create with extra archive and manage:backup: recorded as a restore", vms, []string{"manage:vm", "manage:backup"},
			`{"node":"pve-01","vmid":101,"extra":{"archive":"store01:backup/vm/101/2023-11-14T22:13:20Z"}}`, "", 200,
			"store01:backup/vm/101/2023-11-14T22:13:20Z", vm},
		{"container create with extra restore and manage:backup: recorded as a restore", cts, []string{"manage:container", "manage:backup"},
			ctBody + `"` + ctBackup + `","extra":{"restore":"1"}}`, "", 200, ctBackup, ct},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var asked atomic.Int32
			pve := &backupRunPVE{handle: func(w http.ResponseWriter, r *http.Request) bool {
				asked.Add(1)
				if r.Method != http.MethodPost {
					return false
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"data":"` + restoreRestoreUPID + `"}`))
				return true
			}}
			app, dbtx := newPVEAppAs(t, pve, grantsOf(tt.grants...), fiber.MethodPost, tt.path, tt.bind)
			req := httptest.NewRequest(http.MethodPost, backupRoute(tt.path), strings.NewReader(tt.body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-Test-User", "yes")
			status, body := sendBody(t, app, req)
			want := tt.status
			if want == 0 {
				want = http.StatusForbidden
			}
			if want != http.StatusOK {
				if status != want || !strings.Contains(string(body), tt.need) || asked.Load() != 0 {
					t.Errorf("answered %d %s after %d Proxmox requests; want %d naming %s and none", status, body, asked.Load(), want, tt.need)
				}
				return
			}
			audits := dbtx.decodedAudits(t)
			if status != want || asked.Load() != 1 || len(audits) != 1 || audits[0].details["restore"] != true ||
				audits[0].details["archive"] != tt.archive || audits[0].details["force"] != false {
				t.Errorf("answered %d %s after %d Proxmox requests, audit %v; want one create recorded as a restore of %s",
					status, body, asked.Load(), audits, tt.archive)
			}
		})
	}
}
