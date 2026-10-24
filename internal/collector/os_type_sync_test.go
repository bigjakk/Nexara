package collector

import (
	"context"
	"fmt"
	"net/http"
	"sync/atomic"
	"testing"

	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/events"
	"github.com/bigjakk/nexara/internal/proxmox"
)

// A guest-agent call to a VM whose agent never answers costs PVE's 3 s
// guest-ping timeout, so the metrics pass must ask no agent. The OS loop asks
// instead, stores what it learns and announces it so the icon refreshes.
func TestGuestOSDetectionRunsOnItsOwnLoop(t *testing.T) {
	t.Parallel()
	var agentCalls atomic.Int32
	srv := newTestServer(t, map[string]http.HandlerFunc{
		"/api2/json/nodes": func(w http.ResponseWriter, _ *http.Request) {
			jsonResponse(w, []proxmox.NodeListEntry{{Node: "pve1", Status: "online", MaxCPU: 4}})
		},
		"/api2/json/nodes/pve1/status": func(w http.ResponseWriter, _ *http.Request) {
			jsonResponse(w, proxmox.NodeStatus{Node: "pve1", CPUInfo: proxmox.CPUInfo{CPUs: 4}})
		},
		"/api2/json/nodes/pve1/qemu": func(w http.ResponseWriter, _ *http.Request) {
			jsonResponse(w, []proxmox.VirtualMachine{{VMID: 100, Name: "linux01", Status: "running"}})
		},
		"/api2/json/nodes/pve1/lxc": func(w http.ResponseWriter, _ *http.Request) {
			jsonResponse(w, []proxmox.Container{})
		},
		"/api2/json/nodes/pve1/storage": func(w http.ResponseWriter, _ *http.Request) {
			jsonResponse(w, []proxmox.StoragePool{})
		},
		"/api2/json/nodes/pve1/qemu/100/config": func(w http.ResponseWriter, _ *http.Request) {
			jsonResponse(w, map[string]any{"ostype": "l26"})
		},
		"/api2/json/nodes/pve1/qemu/100/agent/get-osinfo": func(w http.ResponseWriter, _ *http.Request) {
			agentCalls.Add(1)
			jsonResponse(w, map[string]any{"result": proxmox.GuestOSInfo{ID: "ubuntu"}})
		},
	})
	defer srv.Close()

	mq := newMockQueries()
	pub := &mockEventPub{}
	syncer := newTestSyncer(mq)
	syncer.SetEventPublisher(pub)
	cluster := makeCluster(t, srv.URL)

	if _, err := syncer.SyncCluster(context.Background(), cluster); err != nil {
		t.Fatalf("SyncCluster: %v", err)
	}
	if n := agentCalls.Load(); n != 0 {
		t.Fatalf("the metrics pass made %d guest-agent calls; each can hold it up 3 s", n)
	}

	mq.clusters = []db.Cluster{cluster}
	for _, vm := range mq.vms {
		mq.vmsByCluster = append(mq.vmsByCluster, vm)
	}
	syncer.SyncAllGuestOSTypes(context.Background())

	vm := mq.vms[fmt.Sprintf("%s:%d", cluster.ID, 100)]
	if vm.Ostype != "ubuntu" || vm.ConfigOstype != "l26" {
		t.Errorf("after the OS loop: ostype %q, config_ostype %q; want ubuntu, l26", vm.Ostype, vm.ConfigOstype)
	}
	announced := false
	for _, e := range pub.events {
		announced = announced || (e.Kind == events.KindVMStateChange && e.ResourceID == vm.ID.String() && e.Action == "ostype_sync")
	}
	if !announced {
		t.Error("the OS loop changed the guest's OS but published no vm_state_change for it")
	}

	// Nothing new on the next pass, so nothing published: the loop runs every minute.
	mq.vmsByCluster = []db.Vm{vm}
	before := pub.countKind(events.KindVMStateChange)
	syncer.SyncAllGuestOSTypes(context.Background())
	if n := pub.countKind(events.KindVMStateChange) - before; n != 0 {
		t.Errorf("a pass that changed nothing published %d vm_state_change events", n)
	}
}
