package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"

	"github.com/bigjakk/nexara/internal/api/handlers"
	db "github.com/bigjakk/nexara/internal/db/generated"
)

// A move records the storage the disk was on before it, read from the guest's
// config. Deleting that read from either handler left every other test green.
func TestMoveAuditRecordsTheStorageTheDiskLeft(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name, path, body, kind, config, move, upidType, action string
	}{
		{"a VM's disk", clusterScope + "/vms/:vm_id/disks/move", `{"disk":"scsi0","storage":"store02"}`,
			"qemu", `{"scsi0":"store01:vm-101-disk-0,size=32G"}`, "move_disk", "qmmove", "disk_move"},
		{"a container's volume", clusterScope + "/containers/:ct_id/volumes/move", `{"volume":"rootfs","storage":"store02"}`,
			"lxc", `{"rootfs":"store01:subvol-101-disk-0,size=8G"}`, "move_volume", "move_volume", "volume_move"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			guest := "/api2/json/nodes/pve-01/" + tt.kind + "/101/"
			pve := &accessUpdatePVE{}
			pve.reply(guest+"config", `{"data":`+tt.config+`}`)
			pve.reply(guest+tt.move, `{"data":"UPID:pve-01:0000A1B2:00C0FFEE:66F1A2B3:`+tt.upidType+`:101:user@pam!test:"}`)
			store := newClusterStore(t, pve.serve(t), "", "")
			nodeID, guestID := uuid.New(), uuid.New()
			d := blockJobsDB{
				accessUpdateDB: store,
				vm:             db.Vm{ID: guestID, ClusterID: store.cluster.ID, NodeID: nodeID, Vmid: 101, Name: "linux01", Type: tt.kind},
				node:           db.Node{ID: nodeID, ClusterID: store.cluster.ID, Name: "pve-01"},
			}
			e := declaredEndpoint(t, fiber.MethodPost, tt.path)
			if tt.kind == "qemu" {
				e.Handler = handlers.NewVMHandler(db.New(d), accessUpdateEncKey, nil).MoveDisk
			} else {
				e.Handler = handlers.NewContainerHandler(db.New(d), accessUpdateEncKey, nil).MoveVolume
			}
			e.Permissions = Permissions{SelfService: "route fixture; authorization is exercised separately"}
			app := newRegistryApp(t, noAuth(), e)

			target := strings.NewReplacer(":cluster_id", testClusterID, ":vm_id", guestID.String(), ":ct_id", guestID.String()).Replace(tt.path)
			req := httptest.NewRequest(http.MethodPost, target, strings.NewReader(tt.body))
			req.Header.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
			if status, body := sendBody(t, app, req); status != fiber.StatusOK {
				t.Fatalf("status = %d (%s), want 200", status, body)
			}

			if got, want := pve.lines(), []string{"GET " + guest + "config", "POST " + guest + tt.move}; !slices.Equal(got, want) {
				t.Errorf("requests to Proxmox = %q, want the config read before the move %q", got, want)
			}
			row := oneAccessAuditRow(t, store.auditRows())
			var details map[string]any
			if err := json.Unmarshal([]byte(row.details), &details); err != nil {
				t.Fatalf("details: %v", err)
			}
			if row.action != tt.action || details["from_storage"] != "store01" || details["storage"] != "store02" {
				t.Errorf("audit row = %s (%s), want %s from store01 to store02", row.details, row.action, tt.action)
			}
		})
	}
}
