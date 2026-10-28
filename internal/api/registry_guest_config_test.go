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

// TestGuestConfigAuditRow drives the real guest config writes over blockJobsDB.
// Proxmox receives a secret as sent, and the audit row every Viewer can read
// keeps its key with "(set)", or "(cleared)" when sent empty, in its place (a
// cloud-init password was kept there in plain text). changed holds what the
// write changes beside the config read just before, or every key it sent when
// that read fails (fields_unverified).
func TestGuestConfigAuditRow(t *testing.T) {
	t.Parallel()
	const probe = "PROBE-GUEST-SECRET-NOT-A-REAL-VALUE"
	vmConfig, ctConfig := clusterScope+"/vms/:vm_id/config", clusterScope+"/containers/:ct_id/config"
	qemu, lxc := "/api2/json/nodes/pve-01/qemu/101/config", "/api2/json/nodes/pve-01/lxc/101/config"
	for _, tt := range []struct {
		name, route, fields, config, details string
		form                                 url.Values
	}{
		{"a cloud-init password", vmConfig, `{"cipassword":"` + probe + `","ciuser":"user01"}`,
			`{"data":{"ciuser":"user01","cipassword":"**********","digest":"d1"}}`,
			`{"changed":["cipassword"],"fields":{"cipassword":"(set)","ciuser":"user01"}}`,
			url.Values{"cipassword": {probe}, "ciuser": {"user01"}}},
		{"a cloud-init password sent empty", vmConfig, `{"cipassword":""}`,
			`{"data":{"cipassword":"**********"}}`, `{"changed":["cipassword"],"fields":{"cipassword":"(cleared)"}}`,
			url.Values{"cipassword": {""}}},
		{"a container's password", ctConfig, `{"password":"` + probe + `","onboot":"1"}`,
			`{"data":{"onboot":1}}`, `{"changed":["password"],"fields":{"onboot":"1","password":"(set)"}}`,
			url.Values{"password": {probe}, "onboot": {"1"}}},
		{"a config that cannot be read first", vmConfig, `{"memory":"2048","delete":"description"}`,
			``, `{"changed":["description","memory"],"fields":{"delete":"description","memory":"2048"},"fields_unverified":true}`,
			url.Values{"memory": {"2048"}, "delete": {"description"}}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			pve := &accessUpdatePVE{}
			read := qemu
			if tt.route == ctConfig {
				read = lxc
			}
			if tt.config == "" {
				pve.refuse(http.MethodGet+" "+read, http.StatusInternalServerError, `{"data":null}`)
			} else {
				pve.reply(http.MethodGet+" "+read, tt.config)
			}
			store := newClusterStore(t, pve.serve(t), "", "")
			nodeID, guestID := uuid.New(), uuid.New()
			guestType, resourceType := "qemu", "vm"
			if tt.route == ctConfig {
				guestType, resourceType = "lxc", "container"
			}
			d := blockJobsDB{
				accessUpdateDB: store,
				vm:             db.Vm{ID: guestID, ClusterID: store.cluster.ID, NodeID: nodeID, Vmid: 101, Type: guestType},
				node:           db.Node{ID: nodeID, ClusterID: store.cluster.ID, Name: "pve-01"},
			}
			queries := db.New(d)
			handler := handlers.NewVMHandler(queries, accessUpdateEncKey, nil).SetVMConfig
			if tt.route == ctConfig {
				handler = handlers.NewContainerHandler(queries, accessUpdateEncKey, nil).SetContainerConfig
			}
			app := mountReal(t, stubAuth(grantsOf("manage:vm", "manage:container")),
				realRoute{fiber.MethodPut, tt.route, handler})

			target := strings.NewReplacer(":cluster_id", testClusterID, ":vm_id", guestID.String(),
				":ct_id", guestID.String()).Replace(tt.route)
			req := jsonRequest(http.MethodPut, target, `{"fields":`+tt.fields+`}`)
			req.Header.Set("X-Test-User", "yes")
			requireAudited(t, app, pve, store, req, auditWant{
				status: fiber.StatusOK, method: http.MethodPut, path: read, read: read, form: tt.form,
				audit: auditRow(resourceType, guestID.String(), "config_update", tt.details),
				never: []string{probe},
			})
		})
	}
}
