package api

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/gofiber/fiber/v3"

	"github.com/bigjakk/nexara/internal/api/handlers"
	db "github.com/bigjakk/nexara/internal/db/generated"
)

// A disk create that adds a storage records the storage's id (Proxmox names it
// after the volume), which a History of that storage looks for; one that adds
// none records none.
func TestNodeDiskCreateRecordsTheStorageItAdds(t *testing.T) {
	t.Parallel()

	const upid = "UPID:pve-01:0000A1B2:00C0FFEE:66F1A2B3:lvmcreate::user@pam!test:"
	tests := []struct {
		name, kind, body string
		storage          string // "" when the create adds none
	}{
		{"an LVM volume group", "lvm", `{"name":"store05","device":"/dev/sdb","add_storage":true}`, "store05"},
		{"an LVM-thin pool", "lvmthin", `{"name":"store05","device":"/dev/sdb","add_storage":true}`, "store05"},
		{"a directory", "directory", `{"name":"store05","device":"/dev/sdb","filesystem":"ext4","add_storage":true}`, "store05"},
		{"no storage added", "lvm", `{"name":"store05","device":"/dev/sdb"}`, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			pve := &accessUpdatePVE{}
			pve.reply("/api2/json/nodes/"+testNodeName+"/disks/"+tt.kind, `{"data":"`+upid+`"}`)
			store := newClusterStore(t, pve.serve(t), "", "")
			h := handlers.NewNodeHandler(db.New(store), accessUpdateEncKey, nil)
			handler := map[string]Handler{"lvm": h.CreateLVM, "lvmthin": h.CreateLVMThin, "directory": h.CreateDirectory}[tt.kind]
			path := nodeScope + "/:node_name/disks/" + tt.kind
			app := mountReal(t, stubAuth(grantsOf("manage:node")), realRoute{fiber.MethodPost, path, handler})

			req := jsonRequest(http.MethodPost, nodeRoute(path), tt.body)
			req.Header.Set("X-Test-User", "yes")
			if status, body := sendBody(t, app, req); status != fiber.StatusOK {
				t.Fatalf("status = %d (%s), want 200", status, body)
			}

			row := oneAccessAuditRow(t, store.auditRows())
			var details map[string]any
			if err := json.Unmarshal([]byte(row.details), &details); err != nil {
				t.Fatalf("details: %v", err)
			}
			got, has := details["storage"]
			if row.resourceType != "node" || (tt.storage == "") == has || has && got != tt.storage {
				t.Errorf("audit row = (%s, %s) %s, want a node row with storage %q", row.resourceType, row.action, row.details, tt.storage)
			}
		})
	}
}
