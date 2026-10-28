package api

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"

	"github.com/bigjakk/nexara/internal/api/handlers"
	db "github.com/bigjakk/nexara/internal/db/generated"
)

// TestTaskRoutesRedactURLs drives the task log and status routes, which pass
// Proxmox's text to every view:task holder, over a stand-in Proxmox: a download
// task's source, which its log names on every line, comes back without its
// userinfo or query, and so does an exit status naming it.
func TestTaskRoutesRedactURLs(t *testing.T) {
	t.Parallel()
	const probe = "PROBE-TASK-SECRET-NOT-A-REAL-VALUE"
	source := "https://user01:" + probe + "@mirror.example.com/x.iso?X-Amz-Signature=" + probe
	const kept = "https://(set)@mirror.example.com/x.iso?(set)"
	upid := "UPID:pve-01:00001234:00005678:65000000:download:store01:user01@pve:"
	tasks := "/api2/json/nodes/pve-01/tasks/" + upid
	for _, tt := range []struct {
		name, route, path, reply, want string
		handler                        func(*handlers.VMHandler) Handler
	}{
		{"a download's log", clusterScope + "/tasks/:upid/log", tasks + "/log",
			`{"data":[{"n":1,"t":"downloading ` + source + ` to /var/lib/vz/template/iso/x.iso"}]}`,
			`"t":"downloading ` + kept + ` to /var/lib/vz/template/iso/x.iso"`,
			func(h *handlers.VMHandler) Handler { return h.GetTaskLog }},
		{"an exit status naming it", clusterScope + "/tasks/:upid", tasks + "/status",
			`{"data":{"status":"stopped","exitstatus":"download of '` + source + `' failed","upid":"` + upid + `","node":"pve-01","type":"download"}}`,
			`"exit_status":"download of '` + kept + `' failed"`,
			func(h *handlers.VMHandler) Handler { return h.GetTaskStatus }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			pve := &accessUpdatePVE{}
			pve.reply(tt.path, tt.reply)
			store := newClusterStore(t, pve.serve(t), "", "")
			d := blockJobsDB{accessUpdateDB: store, node: db.Node{ID: uuid.New(), ClusterID: store.cluster.ID, Name: "pve-01"}}
			app := mountReal(t, stubAuth(grantsOf("view:task")),
				realRoute{fiber.MethodGet, tt.route, tt.handler(handlers.NewVMHandler(db.New(d), accessUpdateEncKey, nil))})

			escaped := strings.NewReplacer(":", "%3A", "@", "%40").Replace(upid)
			resp, err := app.Test(authedRequest(http.MethodGet,
				strings.NewReplacer(":cluster_id", testClusterID, ":upid", escaped).Replace(tt.route)))
			if err != nil {
				t.Fatalf("app.Test: %v", err)
			}
			body, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if resp.StatusCode != fiber.StatusOK || strings.Contains(string(body), probe) || !strings.Contains(string(body), tt.want) {
				t.Errorf("%d %s, want 200 with %s", resp.StatusCode, body, tt.want)
			}
		})
	}
}
