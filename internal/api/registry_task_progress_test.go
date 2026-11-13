package api

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"

	"github.com/bigjakk/nexara/internal/api/handlers"
	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/proxmox"
)

// A running task's progress is read from the end of its log, which a backup job
// over many guests pages past: the first page's last percentage is long stale
// by the time the job is on its second. The parsing itself is
// TestTaskLogProgress's.
func TestTaskStatusReadsProgressFromTheEndOfALongLog(t *testing.T) {
	t.Parallel()
	upid := "UPID:pve-01:00001234:00005678:65000000:vzdump:101:user01@pve:"
	tasks := "/api2/json/nodes/pve-01/tasks/" + upid
	// page is n log lines ending at last.
	page := func(n int, last string) string {
		entries := make([]proxmox.TaskLogEntry, n)
		for i := range entries {
			entries[i] = proxmox.TaskLogEntry{N: i + 1, T: "INFO: Starting Backup of VM 101 (qemu)"}
		}
		entries[n-1].T = last
		raw, err := json.Marshal(map[string]any{"data": entries})
		if err != nil {
			t.Fatalf("Marshal: %v", err)
		}
		return string(raw)
	}

	pve := &accessUpdatePVE{}
	pve.reply(tasks+"/status",
		`{"data":{"status":"running","upid":"`+upid+`","node":"pve-01","type":"vzdump"}}`)
	pve.replyEach(tasks+"/log",
		page(proxmox.TaskLogPageLines, "INFO:  37% (30.0 GiB of 81.0 GiB) in 1m, read: 1 MiB/s, write: 1 MiB/s"),
		page(3, "INFO:  81% (65.7 GiB of 81.0 GiB) in 2m, read: 1 MiB/s, write: 1 MiB/s"))
	store := newClusterStore(t, pve.serve(t), "", "")
	d := blockJobsDB{accessUpdateDB: store, node: db.Node{ID: uuid.New(), ClusterID: store.cluster.ID, Name: "pve-01"}}
	route := clusterScope + "/tasks/:upid"
	app := mountReal(t, stubAuth(grantsOf("view:task")),
		realRoute{fiber.MethodGet, route, handlers.NewVMHandler(db.New(d), accessUpdateEncKey, nil).GetTaskStatus})

	escaped := strings.NewReplacer(":", "%3A", "@", "%40").Replace(upid)
	resp, err := app.Test(authedRequest(http.MethodGet,
		strings.NewReplacer(":cluster_id", testClusterID, ":upid", escaped).Replace(route)))
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	var got struct {
		Progress *float64 `json:"progress"`
	}
	if err := json.Unmarshal(body, &got); err != nil || got.Progress == nil || *got.Progress != 0.81 {
		t.Errorf("%d %s, want progress 0.81", resp.StatusCode, body)
	}
}
