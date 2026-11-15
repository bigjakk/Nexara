package drs

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/proxmox"
)

// activeTasks serves a node's task list as PVE does: running tasks are only on
// the active list, and without source=active the answer is the archive.
func activeTasks(tasks ...proxmox.NodeTask) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("source") != "active" {
			haData(w, []proxmox.NodeTask{})
			return
		}
		haData(w, tasks)
	}
}

// pve-01 holds VM 101 and VM 102 and pve-02 nothing, so the plan shows who was
// left in place: the planner moves 101 (the higher CPU) unless 101 is pinned, and
// then 102.
func TestEvaluate_LeavesBusyAndBackedOffGuestsInPlace(t *testing.T) {
	t.Parallel()
	now := time.Now()
	vm := func(vmid int, cpu float64, mem int64, lock string) proxmox.VirtualMachine {
		return proxmox.VirtualMachine{
			VMID: vmid, Status: "running", Lock: lock, CPU: cpu, CPUs: 4, Mem: mem, MaxMem: mem,
		}
	}
	listVMs := func(vms ...proxmox.VirtualMachine) http.HandlerFunc {
		return func(w http.ResponseWriter, _ *http.Request) { haData(w, vms) }
	}
	// VM 101 as a container, for the listing's other half.
	listCT := func(lock string) map[string]http.HandlerFunc {
		return map[string]http.HandlerFunc{
			node1QemuPath: listVMs(vm(102, 0.25, 4<<30, "")),
			node1LxcPath: func(w http.ResponseWriter, _ *http.Request) {
				haData(w, []proxmox.Container{{
					VMID: 101, Status: "running", Lock: lock, CPU: 0.5, CPUs: 4, Mem: 8 << 30, MaxMem: 8 << 30,
				}})
			},
		}
	}
	task := func(path, taskType string, endTime int64) map[string]http.HandlerFunc {
		return map[string]http.HandlerFunc{path: activeTasks(proxmox.NodeTask{ID: "101", Type: taskType, EndTime: endTime})}
	}
	failed := func(failures int32, ago time.Duration) []db.ListDRSMigrationFailuresRow {
		return []db.ListDRSMigrationFailuresRow{{VmID: 101, Failures: failures, LastFailedAt: now.Add(-ago)}}
	}

	for _, tc := range []struct {
		name     string
		handlers map[string]http.HandlerFunc
		failures []db.ListDRSMigrationFailuresRow
		want     []int
	}{
		{name: "a free guest moves", want: []int{101}},
		{name: "a free container moves", handlers: listCT(""), want: []int{101}},
		{name: "running disk move", handlers: task(node1TasksPath, "qmmove", 0), want: []int{102}},
		{name: "task running on another node", handlers: task(node2TasksPath, "qmigrate", 0), want: []int{102}},
		{name: "ended task still on the list", handlers: task(node1TasksPath, "qmmove", 1), want: []int{101}},
		{name: "open console", handlers: task(node1TasksPath, "vncproxy", 0), want: []int{101}},
		{name: "locked VM", handlers: map[string]http.HandlerFunc{
			node1QemuPath: listVMs(vm(101, 0.5, 8<<30, "backup"), vm(102, 0.25, 4<<30, "")),
		}, want: []int{102}},
		{name: "locked container", handlers: listCT("backup"), want: []int{102}},
		{name: "tasks not listed", handlers: map[string]http.HandlerFunc{node1TasksPath: failListing}},
		{name: "failed a minute ago", failures: failed(1, time.Minute), want: []int{102}},
		{name: "back-off over", failures: failed(1, 16*time.Minute), want: []int{101}},
		{name: "second failure waits longer", failures: failed(2, 16*time.Minute), want: []int{102}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			handlers := evaluateHandlers()
			handlers[node1QemuPath] = listVMs(vm(101, 0.5, 8<<30, ""), vm(102, 0.25, 4<<30, ""))
			handlers[node1VM102Cfg] = func(w http.ResponseWriter, _ *http.Request) { haData(w, proxmox.VMConfig{}) }
			for path, h := range tc.handlers {
				handlers[path] = h
			}
			e, q, _ := newEvaluateEngine(t, handlers)
			q.failures = tc.failures

			result, err := e.Evaluate(context.Background(), uuid.New())
			if err != nil {
				t.Fatalf("Evaluate: %v", err)
			}
			got := make([]int, 0, len(result.Recommendations))
			for _, r := range result.Recommendations {
				got = append(got, r.VMID)
			}
			if !slices.Equal(got, tc.want) {
				t.Errorf("recommended %v, want %v", got, tc.want)
			}
		})
	}
}

func TestEvaluate_StopsWhenMigrationFailuresCannotBeRead(t *testing.T) {
	t.Parallel()
	e, q, _ := newEvaluateEngine(t, evaluateHandlers())
	q.failuresErr = errors.New("nope")

	result, err := e.Evaluate(context.Background(), uuid.New())
	if err == nil || !strings.Contains(err.Error(), "migration failures") {
		t.Fatalf("Evaluate = %+v, %v; want the failed read, or DRS retries a guest it should leave alone", result, err)
	}
}

func TestMigrationBackoff(t *testing.T) {
	t.Parallel()
	for failures, want := range map[int]time.Duration{
		1:    15 * time.Minute,
		2:    30 * time.Minute,
		5:    4 * time.Hour,
		6:    6 * time.Hour,
		1000: 6 * time.Hour,
	} {
		if got := migrationBackoff(failures); got != want {
			t.Errorf("migrationBackoff(%d) = %v, want %v", failures, got, want)
		}
	}
}

// A node that booted less than the hold-off ago is neither source nor target
// (pve-01 holds VMs 101 and 102, pve-02 nothing, so a free plan moves 101). An
// uptime of 0 is no reading and holds nothing; neither does a hold-off of 0.
func TestEvaluate_LeavesANodeAloneAfterItBoots(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name             string
		holdoff          int32
		uptime1, uptime2 int64
		want             []int
		wantHeld         []string
	}{
		{name: "target booted 2 minutes ago", holdoff: 10, uptime1: 86400, uptime2: 120, wantHeld: []string{"pve-02"}},
		{name: "source booted 2 minutes ago", holdoff: 10, uptime1: 120, uptime2: 86400, wantHeld: []string{"pve-01"}},
		{name: "hold-off over", holdoff: 10, uptime1: 86400, uptime2: 600, want: []int{101}},
		{name: "uptime unknown", holdoff: 10, want: []int{101}},
		{name: "hold-off off", holdoff: 0, uptime1: 120, uptime2: 120, want: []int{101}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			handlers := evaluateHandlers()
			handlers[nodesPath] = func(w http.ResponseWriter, _ *http.Request) {
				haData(w, []proxmox.NodeListEntry{
					{Node: "pve-01", Status: "online", MaxCPU: evalNodeMaxCPU, MaxMem: evalNodeMaxMem, Uptime: tc.uptime1},
					{Node: "pve-02", Status: "online", MaxCPU: evalNodeMaxCPU, MaxMem: evalNodeMaxMem, Uptime: tc.uptime2},
				})
			}
			handlers[node1QemuPath] = func(w http.ResponseWriter, _ *http.Request) {
				haData(w, []proxmox.VirtualMachine{
					{VMID: 101, Status: "running", CPU: 0.5, CPUs: 4, Mem: 8 << 30, MaxMem: 8 << 30},
					{VMID: 102, Status: "running", CPU: 0.25, CPUs: 4, Mem: 4 << 30, MaxMem: 4 << 30},
				})
			}
			handlers[node1VM102Cfg] = func(w http.ResponseWriter, _ *http.Request) { haData(w, proxmox.VMConfig{}) }
			e, q, _ := newEvaluateEngine(t, handlers)
			q.cfg.BootHoldoffMinutes = tc.holdoff

			result, err := e.Evaluate(context.Background(), uuid.New())
			if err != nil {
				t.Fatalf("Evaluate: %v", err)
			}
			got := make([]int, 0, len(result.Recommendations))
			for _, r := range result.Recommendations {
				got = append(got, r.VMID)
			}
			held := make([]string, 0, len(result.HeldNodes))
			for _, h := range result.HeldNodes {
				held = append(held, h.Node)
			}
			if !slices.Equal(got, tc.want) || !slices.Equal(held, tc.wantHeld) {
				t.Errorf("recommended %v with %v held, want %v with %v held", got, held, tc.want, tc.wantHeld)
			}
		})
	}
}
