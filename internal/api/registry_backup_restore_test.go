package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/bigjakk/nexara/internal/api/handlers"
	"github.com/bigjakk/nexara/internal/auth"
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
	withExec := grantsOf("manage:backup", "view:backup", "execute:vm")
	otherCluster := "5c4b3a29-1817-4615-9413-0000000000aa"
	tests := []struct {
		name    string
		grants  map[string]bool
		storage string
		force   bool
		status  int
		calls   []string
		from    string // the storage the archive is read through; empty when refused
		by      string // how it was tied to the server
		start   bool   // start_after_restore, sent as Proxmox's own "start"
		ct      bool   // backup_type ct
		guest   string // the existing guest's type; qemu when empty
		stopped bool   // the existing guest's row says stopped
	}{
		{"the storage of the requested server and datastore, on the node asked for", both, restoreStorages, false, 200,
			[]string{storageGet, "POST /api2/json/nodes/pve-01/qemu"}, "store04", "address", false, false, "", false},
		{"start after restore is asked of Proxmox with the restore itself", withExec, restoreStorages, false, 200,
			[]string{storageGet, "POST /api2/json/nodes/pve-01/qemu"}, "store04", "address", true, false, "", false},
		// Restoring is not starting: the start needs what the VM's power route needs.
		{"start after restore without execute:vm: refused, nothing asked of Proxmox", both, restoreStorages, false, 403, nil, "", "", true, false, "", false},
		{"a container's start needs execute:container", withExec, restoreStorages, false, 403, nil, "", "", true, true, "", false},
		{"execute:vm held on another cluster does not count", grantsOf("manage:backup", "view:backup", "execute:vm@"+otherCluster),
			restoreStorages, false, 403, nil, "", "", true, false, "", false},
		// Over a running guest the restore stops it first: execute on the guest's own kind, from its row.
		{"over a running container without execute:container: nothing is stopped", withExec, restoreStorages, true, 403,
			nil, "", "", false, false, "lxc", false},
		// A row that says stopped needs no execute, and sends no stop: Proxmox
		// refuses the restore itself if the guest is running after all.
		{"over a guest whose row says stopped: restored without a stop", both, restoreStorages, true, 200,
			[]string{storageGet, restore}, "store02", "address", false, false, "", true},
		{"another Nexara PBS server owns the only pbs storage: nothing is dispatched", both, restoreOtherServerOnly, false, 400,
			[]string{storageGet}, "", "", false, false, "", false},
		{"the only candidate is used, and recorded as such", both, restoreAliasOnly, false, 200,
			[]string{storageGet, "POST /api2/json/nodes/pve-01/qemu"}, "store05", "only_candidate", false, false, "", false},
		{"over an existing guest: the storage is chosen on the guest's node, then the stop, then the restore", withExec, restoreStorages, true, 200,
			[]string{storageGet, stopPost, stopPoll, restore}, "store02", "address", false, false, "", false},
		{"over an existing guest with no such storage: it is not stopped", withExec, restoreOtherServerOnly, true, 400,
			[]string{storageGet}, "", "", false, false, "", false},
		{"a storage list that never answers: refused in time, and the guest is not stopped", withExec, restoreHangs, true, 502,
			[]string{storageGet}, "", "", false, false, "", false},
		// A server the caller may not view is a missing one, and nothing is asked of Proxmox.
		{"a caller who may manage backups here but not view the PBS server", grantsOf("manage:backup"), restoreStorages, false, 404, nil, "", "", false, false, "", false},
		{"a caller who may view the PBS server but not manage backups here", grantsOf("view:backup"), restoreStorages, false, 403, nil, "", "", false, false, "", false},
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
			guest, backupType := "qemu", "vm"
			status := "running"
			if tt.stopped {
				status = "stopped"
			}
			if tt.guest != "" {
				guest = tt.guest
			}
			if tt.ct {
				backupType = "ct"
			}
			pbs01 := db.PbsServer{ID: uuid.MustParse(testPBSServerID), Name: "pbs01", ApiUrl: "https://pbs01.example.com:8007"}
			pbs02 := db.PbsServer{ID: uuid.MustParse("5c4b3a29-1817-4615-9413-000000000007"), Name: "pbs02", ApiUrl: "https://pbs02.example.com:8007"}
			dbtx.lists = map[string][]any{"ListPBSServers": {pbs01, pbs02}}
			dbtx.lookups = map[string]any{
				"GetPBSServer": pbs01,
				"GetCluster":   db.Cluster{ID: clusterID},
				// The guest lives on pve-02; the request names pve-01.
				"GetVMByClusterAndVmid": db.Vm{ID: uuid.MustParse("5c4b3a29-1817-4615-9413-000000000003"), ClusterID: clusterID, NodeID: nodeID, Vmid: 101, Name: "linux01", Type: guest, Status: status},
				"GetNode":               db.Node{ID: nodeID, ClusterID: clusterID, Name: "pve-02"},
				// The node the request names is a member; the handler checks it itself.
				"GetNodeByClusterAndName": db.Node{ID: uuid.MustParse("5c4b3a29-1817-4615-9413-000000000004"), ClusterID: clusterID, Name: "pve-01"},
			}

			body := `{"pbs_server_id":"` + testPBSServerID + `","backup_type":"` + backupType + `","backup_id":"101","backup_time":1700000000,` +
				`"target_node":"pve-01","vmid":101,"datastore":"datastore01","force":` + strconv.FormatBool(tt.force) +
				`,"start_after_restore":` + strconv.FormatBool(tt.start) + `}`
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
			if want := map[bool]string{true: "execute:container", false: "execute:vm"}[tt.ct || tt.guest == "lxc"]; tt.status == 403 &&
				tt.grants["manage:backup"] && !strings.Contains(string(answer), want) {
				t.Errorf("answer %q, want it to name %s", answer, want)
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
				if !tt.stopped {
					wantTasks = []string{restoreStopUPID, restoreRestoreUPID}
				}
			}
			if got, want := pve.restoreForm.Get("start"), map[bool]string{true: "1"}[tt.start]; got != want {
				t.Errorf("start = %q, want %q", got, want)
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
			if tt.force && !tt.stopped {
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

// A guest's Backups tab lists a snapshot only when this cluster has a PBS
// storage reading its server and datastore: the same VMID on a datastore no
// storage here reads is another cluster's. The tie rules are TestClusterReadsDatastore's.
func TestClusterSnapshotsAreTheClustersOwn(t *testing.T) {
	t.Parallel()
	pve := &restorePVE{storage: restoreStorages}
	listed := []auth.ScopedPermission{{Action: "view", Resource: "backup", ScopeType: "cluster", ScopeID: testClusterID}}
	app, dbtx := newBackupAppAs(t, &backupRunPVE{handle: pve.handle, listed: listed}, grantsOf("view:backup"), fiber.MethodGet,
		clusterScope+"/pbs-snapshots", (*handlers.BackupHandler).ListClusterSnapshotsByBackupID)
	// Both attached to this cluster, so the caller may view either.
	bound := pgtype.UUID{Bytes: uuid.MustParse(testClusterID), Valid: true}
	pbs01 := db.PbsServer{ID: uuid.MustParse(testPBSServerID), Name: "pbs01", ApiUrl: "https://pbs01.example.com:8007", ClusterID: bound}
	pbs02 := db.PbsServer{ID: uuid.MustParse("5c4b3a29-1817-4615-9413-000000000007"), Name: "pbs02", ApiUrl: "https://pbs02.example.com:8007", ClusterID: bound}
	snap := func(server uuid.UUID, store string) db.PbsSnapshot {
		return db.PbsSnapshot{ID: uuid.New(), PbsServerID: server, Datastore: store, BackupType: "vm", BackupID: "101", BackupTime: 1700000000}
	}
	dbtx.lists = map[string][]any{
		"ListPBSServers": {pbs01, pbs02},
		"ListPBSSnapshotsByBackupID": {snap(pbs01.ID, "datastore01"), snap(pbs02.ID, "datastore01"),
			snap(pbs01.ID, "datastore09"), snap(pbs02.ID, "datastore02")},
	}
	req := httptest.NewRequest(http.MethodGet, backupRoute(clusterScope+"/pbs-snapshots")+"?backup_id=101", nil)
	req.Header.Set("X-Test-User", "yes")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	var got struct {
		Items []db.PbsSnapshot `json:"items"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, decode: %v", resp.StatusCode, err)
	}
	names := map[uuid.UUID]string{pbs01.ID: "pbs01", pbs02.ID: "pbs02"}
	where := make([]string, 0, len(got.Items))
	for _, sn := range got.Items {
		where = append(where, names[sn.PbsServerID]+"/"+sn.Datastore)
	}
	if want := []string{"pbs01/datastore01", "pbs02/datastore01"}; !slices.Equal(where, want) {
		t.Errorf("listed %q, want %q", where, want)
	}
}
