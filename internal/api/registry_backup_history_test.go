package api

import (
	"context"
	"net/http"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"

	"github.com/bigjakk/nexara/internal/api/handlers"
	db "github.com/bigjakk/nexara/internal/db/generated"
)

// The Histories of storages, PBS servers, Veeam servers, jobs and runs, and
// Proxmox backup jobs (registry_storage_history.go and its siblings).

// TestBackupHistoryRoutes holds each of these routes to what it promises, one
// row per route (and per shape a route has): the rows its selector asks for
// from the path as the SPA sends it, and a gate that is view:audit and the
// permission of the object's own page, read off a route of that page where the
// page is a cluster's — a History must not open to someone its object's page
// is closed to.
func TestBackupHistoryRoutes(t *testing.T) {
	t.Parallel()
	const (
		server  = "5a7b0001-0000-4000-8000-000000000001"
		job     = "5a7b0002-0000-4000-8000-000000000002"
		session = "5a7b0003-0000-4000-8000-000000000003"
		uuidJob = "5a7b0004-0000-4000-8000-000000000004"
		base    = "/api/v1/clusters/" + testClusterID
	)
	// The keys a storage's disk moves and restores name it by, in each type.
	storageKeys := func(suffix string) string {
		out := make([]string, 0, 6)
		for _, t := range []string{"vm", "container"} {
			for _, k := range []string{"storage", "target_storage", "from_storage"} {
				out = append(out, t+"."+k+"=store01")
			}
		}
		return "storage=store01 " + strings.Join(out, " ") + " backup.storage=store01 backup.from_storage=store01 " +
			"node.storage=store01 pool.storage∋store01 storage=" + testClusterID + suffix
	}
	rows := []struct{ route, url, sibling, want string }{
		{clusterScope + "/storages/:storage/history", base + "/storages/store01/history",
			storageScope, storageKeys("")},
		{clusterScope + "/storages/:storage/history", base + "/storages/store01/history?node=pve-01",
			"", storageKeys(" on pve-01")},
		{pbsBackupScope + "/history", "/api/v1/pbs-servers/" + server + "/history", "",
			"pbs_server=" + server + " backup.pbs_server_id=" + server},
		{pbsBackupScope + "/history", "/api/v1/pbs-servers/" + server + "/history?datastore=datastore01", "",
			"pbs_server[" + server + "].datastore=datastore01 backup.pbs_server_id=" + server + "&datastore=datastore01"},
		{veeamScope + "/:id/history", "/api/v1/veeam-servers/" + server + "/history", "",
			"veeam_server=" + server + " veeam_job.veeam_server_id=" + server + " veeam_session.veeam_server_id=" + server},
		{veeamScope + "/:id/jobs/:job_id/history", "/api/v1/veeam-servers/" + server + "/jobs/" + job + "/history", "",
			"veeam_job[" + job + "].veeam_server_id=" + server + " veeam_session.veeam_job_id=" + job + "&veeam_server_id=" + server},
		{veeamScope + "/:id/sessions/:session_id/history", "/api/v1/veeam-servers/" + server + "/sessions/" + session + "/history", "",
			"veeam_session[" + session + "].veeam_server_id=" + server},
		{clusterScope + "/backup-jobs/:job_id/history", base + "/backup-jobs/backup-1a2b3c4d/history",
			clusterScope + "/backup-jobs", `backup=backup-1a2b3c4d~backup\_job\_% backup=` + testClusterID + `~backup\_job\_%`},
		// A job made without an id is named by a UUID, which starts with a digit as often as not: it
		// is a job's id like any other, the one the job's own routes take.
		{clusterScope + "/backup-jobs/:job_id/history", base + "/backup-jobs/" + uuidJob + "/history", "",
			`backup=` + uuidJob + `~backup\_job\_% backup=` + testClusterID + `~backup\_job\_%`},
		// A guest's number and a node's name are ids of the same type and shape: held to a job's
		// actions, they read none of that guest's or node's backups.
		{clusterScope + "/backup-jobs/:job_id/history", base + "/backup-jobs/100/history", "",
			`backup=100~backup\_job\_% backup=` + testClusterID + `~backup\_job\_%`},
		{clusterScope + "/backup-jobs/:job_id/history", base + "/backup-jobs/pve-01/history", "",
			`backup=pve-01~backup\_job\_% backup=` + testClusterID + `~backup\_job\_%`},
		// The id a create files its row under when it could not find its job's: every such create
		// is there, so it is read only by the rows that name it in details, which no such create does.
		{clusterScope + "/backup-jobs/:job_id/history", base + "/backup-jobs/backup-job/history", "",
			`backup[backup-job].job_id=backup-job backup=` + testClusterID + `~backup\_job\_%`},
	}
	covered := map[string]bool{}
	for _, row := range rows {
		covered[row.route] = true
	}
	for _, r := range backupHistoryRoutes() {
		if !covered[r.Path] {
			t.Errorf("%s has no row", r.Path)
		}
		// Each opens on the week the SPA asks for (backup-histories.ts), and reads a bounded number of rows.
		if r.Kind.DefaultWindow != 7*24*time.Hour || r.Kind.RowCap == 0 {
			t.Errorf("%s opens on %v and reads up to %d rows a source, want a week and a cap", r.Path, r.Kind.DefaultWindow, r.Kind.RowCap)
		}
	}
	for _, row := range rows {
		t.Run(row.url, func(t *testing.T) {
			t.Parallel()
			var route historyRoute
			for _, r := range backupHistoryRoutes() {
				if r.Path == row.route {
					route = r
				}
			}
			if route.Path == "" {
				t.Fatalf("no History route at %s", row.route)
			}
			asked := ""
			orig := route.Kind.Select
			route.Kind.Select = func(ctx context.Context, r handlers.HistoryRequest) (handlers.HistorySelection, error) {
				sel, err := orig(ctx, r)
				asked = describeSelection(sel)
				return sel, err
			}
			dbtx := &backupRunDBTX{lookups: map[string]any{"GetCluster": db.Cluster{}}, lists: map[string][]any{"ListHistory": nil}}
			ep := historyEndpoint(handlers.NewHistoryHandler(dbtx, db.New(dbtx)), route)
			grants := make([]string, 0, 2+len(route.AlsoResources))
			grants = append(grants, "view:audit", "view:"+route.Resource)
			for _, also := range route.AlsoResources {
				grants = append(grants, "view:"+also)
			}
			app := newRegistryApp(t, stubAuth(grantsOf(grants...)), ep)
			if status, env := send(t, app, authedRequest(fiber.MethodGet, row.url)); status != http.StatusOK {
				t.Fatalf("%s answered %d (%q), want 200", row.url, status, env.Message)
			}
			if asked != row.want {
				t.Errorf("%s asks for %q, want %q", row.url, asked, row.want)
			}
			if row.sibling != "" {
				page := *declaredEndpoint(t, fiber.MethodGet, row.sibling).Permissions.Check
				audit := Check{Action: "view", Resource: "audit", Scope: page.Scope}
				if got := ep.Permissions.AllOf; len(got) != 2 || got[0] != audit || got[1] != page {
					t.Errorf("%s is gated on %v, want view:audit and its page's %v (GET %s)", row.url, got, page, row.sibling)
				}
			}
		})
	}
}

// A PBS server's rows are mostly the backups', so its History takes global
// view:backup beside global view:pbs: either alone leaves it closed.
func TestPBSHistoryGate(t *testing.T) {
	t.Parallel()
	global := func(resource string) Check { return Check{Action: "view", Resource: resource, Scope: ScopeGlobal} }
	ep := declaredEndpoint(t, fiber.MethodGet, pbsBackupScope+"/history")
	if want := []Check{global("audit"), global("pbs"), global("backup")}; !slices.Equal(ep.Permissions.AllOf, want) {
		t.Errorf("the PBS History is gated on %v, want %v", ep.Permissions.AllOf, want)
	}
}

// The SPA asks for these Histories at the paths in backup-histories.ts, each
// parameter an interpolation; a route renamed on one side only is a History
// that answers 404. Compared by shape — the literal segments and where the
// parameters are — one for one.
func TestBackupHistoryPathsAreTheSPAs(t *testing.T) {
	t.Parallel()
	src, err := os.ReadFile("../../frontend/src/features/history/lib/backup-histories.ts")
	if err != nil {
		t.Fatal(err)
	}
	templates := regexp.MustCompile("apiPath`(/api/v1/[^`?]+)").FindAllStringSubmatch(string(src), -1)
	routes := backupHistoryRoutes()
	spa, server := make([]string, 0, len(templates)), make([]string, 0, len(routes))
	for _, m := range templates {
		spa = append(spa, regexp.MustCompile(`\$\{[^}]*\}`).ReplaceAllString(m[1], ":p"))
	}
	for _, r := range routes {
		server = append(server, regexp.MustCompile(`:[a-z_]+`).ReplaceAllString(r.Path, ":p"))
	}
	slices.Sort(spa)
	slices.Sort(server)
	if !slices.Equal(spa, server) {
		t.Errorf("the SPA reads %d History paths and the server declares %d; the SPA's:\n%s\nthe server's:\n%s",
			len(spa), len(server), strings.Join(spa, "\n"), strings.Join(server, "\n"))
	}
}
