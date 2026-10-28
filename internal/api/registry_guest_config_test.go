package api

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"

	"github.com/bigjakk/nexara/internal/api/handlers"
	db "github.com/bigjakk/nexara/internal/db/generated"
)

// TestGuestConfigAuditRedactsSecrets drives the real guest config writes over
// blockJobsDB: Proxmox receives a secret as sent, and the audit row every Viewer
// can read keeps its key with "(set)", or "(cleared)" when sent empty, in its
// place. A cloud-init password was kept there in plain text.
func TestGuestConfigAuditRedactsSecrets(t *testing.T) {
	t.Parallel()
	const probe = "PROBE-GUEST-SECRET-NOT-A-REAL-VALUE"
	vmConfig, ctConfig := clusterScope+"/vms/:vm_id/config", clusterScope+"/containers/:ct_id/config"
	for _, tt := range []struct {
		name, route, guestType, fields, pvePath, details string
		form                                             url.Values
	}{
		{"a cloud-init password", vmConfig, "qemu", `{"cipassword":"` + probe + `","ciuser":"user01"}`,
			"/api2/json/nodes/pve-01/qemu/101/config", `{"fields":{"cipassword":"(set)","ciuser":"user01"}}`,
			url.Values{"cipassword": {probe}, "ciuser": {"user01"}}},
		{"a cloud-init password sent empty", vmConfig, "qemu", `{"cipassword":""}`,
			"/api2/json/nodes/pve-01/qemu/101/config", `{"fields":{"cipassword":"(cleared)"}}`,
			url.Values{"cipassword": {""}}},
		{"a container's password", ctConfig, "lxc", `{"password":"` + probe + `","onboot":"1"}`,
			"/api2/json/nodes/pve-01/lxc/101/config", `{"fields":{"onboot":"1","password":"(set)"}}`,
			url.Values{"password": {probe}, "onboot": {"1"}}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			pve := &accessUpdatePVE{}
			store := newClusterStore(t, pve.serve(t), "", "")
			nodeID, guestID := uuid.New(), uuid.New()
			d := blockJobsDB{
				accessUpdateDB: store,
				vm:             db.Vm{ID: guestID, ClusterID: store.cluster.ID, NodeID: nodeID, Vmid: 101, Type: tt.guestType},
				node:           db.Node{ID: nodeID, ClusterID: store.cluster.ID, Name: "pve-01"},
			}
			queries := db.New(d)
			handler := handlers.NewVMHandler(queries, accessUpdateEncKey, nil).SetVMConfig
			resourceType := "vm"
			if tt.route == ctConfig {
				handler = handlers.NewContainerHandler(queries, accessUpdateEncKey, nil).SetContainerConfig
				resourceType = "container"
			}
			app := mountReal(t, stubAuth(grantsOf("manage:vm", "manage:container")),
				realRoute{fiber.MethodPut, tt.route, handler})

			target := strings.NewReplacer(":cluster_id", testClusterID, ":vm_id", guestID.String(),
				":ct_id", guestID.String()).Replace(tt.route)
			req := jsonRequest(http.MethodPut, target, `{"fields":`+tt.fields+`}`)
			req.Header.Set("X-Test-User", "yes")
			requireAudited(t, app, pve, store, req, auditWant{
				status: fiber.StatusOK, method: http.MethodPut, path: tt.pvePath, form: tt.form,
				audit: auditRow(resourceType, guestID.String(), "config_update", tt.details),
				never: []string{probe},
			})
		})
	}
}
