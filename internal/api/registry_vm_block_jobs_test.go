package api

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/bigjakk/nexara/internal/api/handlers"
	db "github.com/bigjakk/nexara/internal/db/generated"
)

// blockJobsDB is the cluster store with the guest and its node: what
// resolveVM (and resolveCT, which reads the same row as a container) reads
// before it dials the cluster.
type blockJobsDB struct {
	*accessUpdateDB
	vm   db.Vm
	node db.Node
}

func (d blockJobsDB) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	switch {
	case strings.Contains(sql, "-- name: GetVM :one"), strings.Contains(sql, "-- name: GetContainer :one"):
		return structRow{d.vm}
	case strings.Contains(sql, "-- name: GetNode :one"):
		return structRow{d.node}
	}
	return d.accessUpdateDB.QueryRow(ctx, sql, args...)
}

// TestBlockJobsRoute drives the real handler through its declaration against
// a stand-in Proxmox: a running VM's jobs come back parsed, a VM PVE says is
// not running answers running=false, and a container is refused before any
// call reaches Proxmox.
func TestBlockJobsRoute(t *testing.T) {
	t.Parallel()
	const monitor = "/api2/json/nodes/pve-01/qemu/101/monitor"
	for _, tt := range []struct {
		name       string
		guestType  string
		pve        func(*accessUpdatePVE)
		wantStatus int
		wantBody   string
		wantCalls  int
	}{
		{"running", "qemu", func(p *accessUpdatePVE) {
			p.reply(monitor, `{"data":"Type mirror, device mirror-scsi0: Completed 5 of 10 bytes, speed limit 0 bytes/s\n"}`)
		}, fiber.StatusOK, `{"running":true,"jobs":[{"type":"mirror","device":"mirror-scsi0","completed":5,"total":10,"speed_limit":0}],"unparsed":[]}`, 1},
		// qemu-server answers a monitor failure with 200 and "ERROR: ..." text.
		{"not running", "qemu", func(p *accessUpdatePVE) {
			p.reply(monitor, `{"data":"ERROR: VM 101 not running\n"}`)
		}, fiber.StatusOK, `{"running":false,"jobs":[],"unparsed":[]}`, 1},
		{"monitor failed", "qemu", func(p *accessUpdatePVE) {
			p.reply(monitor, `{"data":"ERROR: VM 101 qmp command 'human-monitor-command' failed - got timeout\n"}`)
		}, fiber.StatusBadGateway, "got timeout", 1},
		{"container", "lxc", func(*accessUpdatePVE) {}, fiber.StatusBadRequest, "Only a QEMU VM has block jobs", 0},
	} {
		pve := &accessUpdatePVE{}
		tt.pve(pve)
		store := newClusterStore(t, pve.serve(t), "", "")
		nodeID, vmID := uuid.New(), uuid.New()
		d := blockJobsDB{
			accessUpdateDB: store,
			vm:             db.Vm{ID: vmID, ClusterID: store.cluster.ID, NodeID: nodeID, Vmid: 101, Type: tt.guestType},
			node:           db.Node{ID: nodeID, ClusterID: store.cluster.ID, Name: "pve-01"},
		}
		path := clusterScope + "/vms/:vm_id/block-jobs"
		e := declaredEndpoint(t, fiber.MethodGet, path)
		e.Handler = handlers.NewVMHandler(db.New(d), accessUpdateEncKey, nil).GetBlockJobs
		e.Permissions = Permissions{SelfService: "route fixture; authorization is exercised separately"}
		app := newRegistryApp(t, noAuth(), e)

		target := strings.NewReplacer(":cluster_id", testClusterID, ":vm_id", vmID.String()).Replace(path)
		resp, err := app.Test(httptest.NewRequest(http.MethodGet, target, nil))
		if err != nil {
			t.Fatalf("%s: app.Test: %v", tt.name, err)
		}
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode != tt.wantStatus || !strings.Contains(string(body), tt.wantBody) {
			t.Errorf("%s: %d %s, want %d with %s", tt.name, resp.StatusCode, body, tt.wantStatus, tt.wantBody)
		}
		if got := len(pve.requests()); got != tt.wantCalls {
			t.Errorf("%s: Proxmox received %d requests, want %d", tt.name, got, tt.wantCalls)
		}
	}
}
