package api

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"

	"github.com/bigjakk/nexara/internal/api/handlers"
	db "github.com/bigjakk/nexara/internal/db/generated"
)

const (
	restoreStopUPID    = "UPID:pve-02:0000A1B2:00C0FFEE:66F1A2B3:qmstop:101:user@pam!test:"
	restoreRestoreUPID = "UPID:pve-02:0000A1B3:00C0FFEF:66F1A2B4:qmrestore:101:user@pam!test:"
	// restoreStorages lists a pbs storage on another server first, then the
	// requested server's datastore01 as one storage per node, and its datastore02.
	restoreStorages = `[{"storage":"store01","type":"pbs","server":"pbs02.example.com","datastore":"datastore01"},` +
		`{"storage":"store02","type":"pbs","server":"pbs01.example.com","datastore":"datastore01","nodes":"pve-02"},` +
		`{"storage":"store04","type":"pbs","server":"pbs01.example.com","datastore":"datastore01","nodes":"pve-01"},` +
		`{"storage":"store03","type":"pbs","server":"pbs01.example.com","datastore":"datastore02"}]`
	// restoreOtherServerOnly is one pbs storage, which the second Nexara PBS server
	// the test knows of owns; restoreAliasOnly is one nothing but its being the only
	// candidate ties to the requested server.
	restoreOtherServerOnly = `[{"storage":"store01","type":"pbs","server":"pbs02.example.com","datastore":"datastore01"}]`
	restoreAliasOnly       = `[{"storage":"store05","type":"pbs","server":"pbs-alias.example.com","datastore":"datastore01"}]`
	// restoreHangs makes the storage list never answer.
	restoreHangs = "hangs"
)

// A restore reads the snapshot through the Proxmox storage that points at the
// requested PBS server and datastore on the node it runs on, and decides that
// before it stops anything; the stop it makes over an existing guest is a task
// of its own. The matching rules are in TestRestoreStorage (handlers).
func TestRestoreReadsThroughTheRequestedServersStorage(t *testing.T) {
	t.Parallel()

	const (
		storageGet = "GET /api2/json/storage"
		stopPost   = "POST /api2/json/nodes/pve-02/qemu/101/status/stop"
		stopPoll   = "GET /api2/json/nodes/pve-02/tasks/" + restoreStopUPID + "/status"
		restore    = "POST /api2/json/nodes/pve-02/qemu"
	)
	both := grantsOf("manage:backup", "view:backup")
	tests := []struct {
		name    string
		grants  map[string]bool
		storage string
		force   bool
		status  int
		calls   []string
		from    string // the storage the archive is read through; empty when refused
		by      string // how it was tied to the server
	}{
		{"the storage of the requested server and datastore, on the node asked for", both, restoreStorages, false, 200,
			[]string{storageGet, "POST /api2/json/nodes/pve-01/qemu"}, "store04", "address"},
		{"another Nexara PBS server owns the only pbs storage: nothing is dispatched", both, restoreOtherServerOnly, false, 400,
			[]string{storageGet}, "", ""},
		{"the only candidate is used, and recorded as such", both, restoreAliasOnly, false, 200,
			[]string{storageGet, "POST /api2/json/nodes/pve-01/qemu"}, "store05", "only_candidate"},
		{"over an existing guest: the storage is chosen on the guest's node, then the stop, then the restore", both, restoreStorages, true, 200,
			[]string{storageGet, stopPost, stopPoll, restore}, "store02", "address"},
		{"over an existing guest with no such storage: it is not stopped", both, restoreOtherServerOnly, true, 400,
			[]string{storageGet}, "", ""},
		{"a storage list that never answers: refused in time, and the guest is not stopped", both, restoreHangs, true, 502,
			[]string{storageGet}, "", ""},
		// A server the caller may not view is a missing one, and nothing is asked of Proxmox.
		{"a caller who may manage backups here but not view the PBS server", grantsOf("manage:backup"), restoreStorages, false, 404, nil, "", ""},
		{"a caller who may view the PBS server but not manage backups here", grantsOf("view:backup"), restoreStorages, false, 403, nil, "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			pve := &restorePVE{storage: tt.storage}
			base := &backupRunPVE{handle: pve.handle, stopPoll: time.Millisecond}
			if tt.storage == restoreHangs {
				base.readTimeout = 300 * time.Millisecond
			}
			app, dbtx := newBackupAppAs(t, base, tt.grants, fiber.MethodPost, clusterScope+"/restore",
				(*handlers.BackupHandler).RestoreBackup)
			clusterID, nodeID := uuid.MustParse(testClusterID), uuid.MustParse("5c4b3a29-1817-4615-9413-000000000002")
			pbs01 := db.PbsServer{ID: uuid.MustParse(testPBSServerID), Name: "pbs01", ApiUrl: "https://pbs01.example.com:8007"}
			pbs02 := db.PbsServer{ID: uuid.MustParse("5c4b3a29-1817-4615-9413-000000000007"), Name: "pbs02", ApiUrl: "https://pbs02.example.com:8007"}
			dbtx.lists = map[string][]any{"ListPBSServers": {pbs01, pbs02}}
			dbtx.lookups = map[string]any{
				"GetPBSServer": pbs01,
				"GetCluster":   db.Cluster{ID: clusterID},
				// The guest lives on pve-02; the request names pve-01.
				"GetVMByClusterAndVmid": db.Vm{ID: uuid.MustParse("5c4b3a29-1817-4615-9413-000000000003"), ClusterID: clusterID, NodeID: nodeID, Vmid: 101, Name: "linux01", Type: "qemu"},
				"GetNode":               db.Node{ID: nodeID, ClusterID: clusterID, Name: "pve-02"},
				// The node the request names is a member; the handler checks it itself.
				"GetNodeByClusterAndName": db.Node{ID: uuid.MustParse("5c4b3a29-1817-4615-9413-000000000004"), ClusterID: clusterID, Name: "pve-01"},
			}

			body := `{"pbs_server_id":"` + testPBSServerID + `","backup_type":"vm","backup_id":"101","backup_time":1700000000,` +
				`"target_node":"pve-01","vmid":101,"datastore":"datastore01","force":` + map[bool]string{true: "true", false: "false"}[tt.force] + `}`
			req := httptest.NewRequest(http.MethodPost, backupRoute(clusterScope+"/restore"), strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-Test-User", "yes")
			resp, err := app.Test(req, fiber.TestConfig{Timeout: 30 * time.Second, FailOnTimeout: true})
			if err != nil {
				t.Fatalf("app.Test: %v", err)
			}
			answer, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()

			if resp.StatusCode != tt.status {
				t.Errorf("status = %d, want %d", resp.StatusCode, tt.status)
			}
			if tt.status == 404 && !strings.Contains(string(answer), "PBS server not found") {
				t.Errorf("answer %q, want the missing-server message", answer)
			}
			if got := pve.requests(); !slices.Equal(got, tt.calls) {
				t.Errorf("requests to Proxmox = %q, want %q", got, tt.calls)
			}
			tasks, audits := dbtx.decodedTasks(t), dbtx.decodedAudits(t)
			if tt.from == "" {
				if len(tasks) != 0 || len(audits) != 0 {
					t.Errorf("a refused restore recorded %d tasks and %d audit rows", len(tasks), len(audits))
				}
				return
			}

			wantArchive := tt.from + ":backup/vm/101/2023-11-14T22:13:20Z"
			node := "pve-01"
			wantTasks := []string{restoreRestoreUPID}
			if tt.force {
				node = "pve-02"
				wantTasks = []string{restoreStopUPID, restoreRestoreUPID}
			}
			if got := pve.restoreForm.Get("archive"); got != wantArchive {
				t.Errorf("archive = %q, want %q", got, wantArchive)
			}
			var gotTasks []string
			for _, task := range tasks {
				gotTasks = append(gotTasks, task.upid)
				if task.node != node {
					t.Errorf("task %s recorded on %q, want %q", task.upid, task.node, node)
				}
			}
			if !slices.Equal(gotTasks, wantTasks) {
				t.Errorf("tasks recorded = %q, want %q", gotTasks, wantTasks)
			}

			last := audits[len(audits)-1]
			if last.action != "backup_restored" || last.details["from_storage"] != tt.from || last.details["archive"] != wantArchive ||
				last.details["matched_by"] != tt.by {
				t.Errorf("restore audit row = %s (%s), want backup_restored reading through %s (%s) as %s", last.raw, last.action, tt.from, tt.by, wantArchive)
			}
			if tt.force {
				stop := audits[0]
				if len(audits) != 2 || stop.action != "stop" || stop.resourceType != "vm" || stop.resourceID != "5c4b3a29-1817-4615-9413-000000000003" ||
					stop.details["reason"] != "force restore" {
					t.Errorf("audit rows = %d, first %s (%s %s), want the guest's stop, reason force restore", len(audits), stop.raw, stop.resourceType, stop.action)
				}
			}
		})
	}
}

// restorePVE answers the storage list, the stop, the restore and the polling of
// the stop, and keeps the order they arrived in.
type restorePVE struct {
	storage string

	mu          sync.Mutex
	calls       []string
	restoreForm url.Values
}

func (p *restorePVE) requests() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.calls)
}

func (p *restorePVE) handle(w http.ResponseWriter, r *http.Request) bool {
	reply := func(data string) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":` + data + `}`))
	}
	path := r.URL.Path
	record := func() {
		p.mu.Lock()
		defer p.mu.Unlock()
		p.calls = append(p.calls, r.Method+" "+path)
	}
	switch {
	case r.Method == http.MethodGet && path == "/api2/json/storage":
		record()
		if p.storage == restoreHangs {
			<-r.Context().Done()
			return true
		}
		reply(p.storage)
	case r.Method == http.MethodPost && strings.HasSuffix(path, "/status/stop"):
		record()
		reply(`"` + restoreStopUPID + `"`)
	case r.Method == http.MethodGet && strings.Contains(path, "/tasks/") && strings.HasSuffix(path, "/status"):
		record()
		reply(`{"status":"stopped","exitstatus":"OK"}`)
	case r.Method == http.MethodPost && (strings.HasSuffix(path, "/qemu") || strings.HasSuffix(path, "/lxc")):
		record()
		_ = r.ParseForm()
		p.mu.Lock()
		p.restoreForm = r.PostForm
		p.mu.Unlock()
		reply(`"` + restoreRestoreUPID + `"`)
	default:
		return false
	}
	return true
}
