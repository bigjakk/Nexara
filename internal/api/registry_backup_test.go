package api

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"

	"github.com/bigjakk/nexara/internal/api/handlers"
)

// These tests drive the REAL declarations — the ones setupRoutes mounts —
// rather than a fixture shaped like them, so a change to registry_backup.go
// that quietly loosened a parameter would show up here.

// backupRouteCount is how many endpoints registerBackupEndpoints declares.
// See vmRouteCount in registry_vms_test.go for why the registry total is a
// sum of per-domain constants rather than one number.
const backupRouteCount = 28

const testPBSStore = "datastore01"

func backupRoute(path string) string {
	return strings.NewReplacer(
		":cluster_id", testClusterID,
		":pbs_id", testPBSServerID,
		":store", testPBSStore,
		":job_id", "backup-1a2b3c4d",
		":upid", "UPID%3Apbs%3A0000A1B2%3A00000000%3A600D600D%3Agarbage_collection%3A%3Aroot%40pam%3A",
	).Replace(path)
}

// backupRoutesOutsideTheClusterCheckShape is this domain's half of the
// registry-wide exception list in registry_vms_test.go.
//
// TWENTY-ONE of the 28 are here, which is the highest proportion of any
// domain so far and is the domain's own shape rather than an artefact of the
// migration. A PBS server may belong to a cluster or to no cluster at all,
// and which of the two decides whether the grant is cluster-scoped or
// instance-wide — a DB read, on every one of the 19 routes nested under
// /pbs-servers/:pbs_id. The remaining two span every cluster at once and
// filter instead of gating.
//
// The map is built rather than written out because the 19 share one reason
// verbatim, and nineteen copies of one sentence is a list nobody re-reads —
// which is the failure the exception surface exists to avoid.
var backupRoutesOutsideTheClusterCheckShape = func() map[string]string {
	const deferred = "Deferred: requirePBSPerm loads the PBS server row and gates on its cluster, or " +
		"instance-wide when the server is standalone"
	out := map[string]string{
		"GET /api/v1/pbs-snapshots": "Advisory: accessibleClusters builds the filter and the handler applies " +
			"it per PBS server; the snapshots span every server, so there is no single cluster to gate on",
		"GET /api/v1/backup-coverage": "Advisory: two accessibleClusters reads (view:backup and view:veeam) " +
			"become per-cluster predicates handed to backupcoverage.Compute; the report spans every cluster",
	}
	for _, r := range []string{
		"GET /api/v1/pbs-servers/:pbs_id/datastores",
		"GET /api/v1/pbs-servers/:pbs_id/datastores/status",
		"POST /api/v1/pbs-servers/:pbs_id/datastores/:store/gc",
		"DELETE /api/v1/pbs-servers/:pbs_id/datastores/:store/snapshots",
		"PUT /api/v1/pbs-servers/:pbs_id/datastores/:store/snapshots/protect",
		"PUT /api/v1/pbs-servers/:pbs_id/datastores/:store/snapshots/notes",
		"POST /api/v1/pbs-servers/:pbs_id/datastores/:store/prune",
		"GET /api/v1/pbs-servers/:pbs_id/datastores/:store/rrd",
		"GET /api/v1/pbs-servers/:pbs_id/datastores/:store/config",
		"GET /api/v1/pbs-servers/:pbs_id/snapshots",
		"GET /api/v1/pbs-servers/:pbs_id/sync-jobs",
		"POST /api/v1/pbs-servers/:pbs_id/sync-jobs/:job_id/run",
		"GET /api/v1/pbs-servers/:pbs_id/prune-jobs",
		"GET /api/v1/pbs-servers/:pbs_id/verify-jobs",
		"POST /api/v1/pbs-servers/:pbs_id/verify-jobs/:job_id/run",
		"GET /api/v1/pbs-servers/:pbs_id/tasks",
		"GET /api/v1/pbs-servers/:pbs_id/tasks/:upid",
		"GET /api/v1/pbs-servers/:pbs_id/tasks/:upid/log",
		"GET /api/v1/pbs-servers/:pbs_id/metrics",
	} {
		out[r] = deferred
	}
	return out
}()

// backupLegacyPermissions is what each handler checked with hand-placed
// calls BEFORE Phase 6f, transcribed from
// `git show HEAD:internal/api/handlers/backup.go` at commit 1d2b59f.
//
// calls counts the permission calls each handler made, and it is 2 for every
// Deferred entry because requirePBSPerm has a per-branch pair
// (requireClusterPerm on the server's cluster OR requirePerm instance-wide);
// GetBackupCoverage is 2 for a different reason — two accessibleClusters
// reads, one for view:backup and one for view:veeam.
var backupLegacyPermissions = map[string]struct {
	permission string
	shape      string
	calls      int
}{
	"GET /api/v1/pbs-servers/:pbs_id/datastores":                          {"view:backup", "Deferred", 2},
	"GET /api/v1/pbs-servers/:pbs_id/datastores/status":                   {"view:backup", "Deferred", 2},
	"POST /api/v1/pbs-servers/:pbs_id/datastores/:store/gc":               {"manage:backup", "Deferred", 2},
	"DELETE /api/v1/pbs-servers/:pbs_id/datastores/:store/snapshots":      {"delete:backup", "Deferred", 2},
	"PUT /api/v1/pbs-servers/:pbs_id/datastores/:store/snapshots/protect": {"manage:backup", "Deferred", 2},
	"PUT /api/v1/pbs-servers/:pbs_id/datastores/:store/snapshots/notes":   {"manage:backup", "Deferred", 2},
	"POST /api/v1/pbs-servers/:pbs_id/datastores/:store/prune":            {"manage:backup", "Deferred", 2},
	"GET /api/v1/pbs-servers/:pbs_id/datastores/:store/rrd":               {"view:backup", "Deferred", 2},
	"GET /api/v1/pbs-servers/:pbs_id/datastores/:store/config":            {"view:backup", "Deferred", 2},
	"GET /api/v1/pbs-servers/:pbs_id/snapshots":                           {"view:backup", "Deferred", 2},
	"GET /api/v1/pbs-servers/:pbs_id/sync-jobs":                           {"view:backup", "Deferred", 2},
	"POST /api/v1/pbs-servers/:pbs_id/sync-jobs/:job_id/run":              {"manage:backup", "Deferred", 2},
	"GET /api/v1/pbs-servers/:pbs_id/prune-jobs":                          {"view:backup", "Deferred", 2},
	"GET /api/v1/pbs-servers/:pbs_id/verify-jobs":                         {"view:backup", "Deferred", 2},
	"POST /api/v1/pbs-servers/:pbs_id/verify-jobs/:job_id/run":            {"manage:backup", "Deferred", 2},
	"GET /api/v1/pbs-servers/:pbs_id/tasks":                               {"view:backup", "Deferred", 2},
	"GET /api/v1/pbs-servers/:pbs_id/tasks/:upid":                         {"view:backup", "Deferred", 2},
	"GET /api/v1/pbs-servers/:pbs_id/tasks/:upid/log":                     {"view:backup", "Deferred", 2},
	"GET /api/v1/pbs-servers/:pbs_id/metrics":                             {"view:backup", "Deferred", 2},

	"POST /api/v1/clusters/:cluster_id/restore":                 {"manage:backup", "Check", 1},
	"POST /api/v1/clusters/:cluster_id/backup":                  {"manage:backup", "Check", 1},
	"GET /api/v1/clusters/:cluster_id/backup-jobs":              {"view:backup", "Check", 1},
	"POST /api/v1/clusters/:cluster_id/backup-jobs":             {"manage:backup", "Check", 1},
	"PUT /api/v1/clusters/:cluster_id/backup-jobs/:job_id":      {"manage:backup", "Check", 1},
	"DELETE /api/v1/clusters/:cluster_id/backup-jobs/:job_id":   {"delete:backup", "Check", 1},
	"POST /api/v1/clusters/:cluster_id/backup-jobs/:job_id/run": {"manage:backup", "Check", 1},

	"GET /api/v1/pbs-snapshots":   {"view:backup", "Advisory", 1},
	"GET /api/v1/backup-coverage": {"view:backup", "Advisory", 2},
}

// declaredBackupEndpoints returns every declaration in this domain, keyed
// "METHOD path".
//
// It filters on backupLegacyPermissions' own keys rather than on a path
// prefix, because this domain spans three unrelated prefixes and the
// /pbs-servers one is SHARED with registry_pbs.go — a prefix filter would
// sweep the 6 PBSHandler routes in and make the count wrong in a way that
// reads like a miscount.
func declaredBackupEndpoints(t *testing.T) map[string]Endpoint {
	t.Helper()
	s := newRouteStubServer(t)
	out := map[string]Endpoint{}
	for _, e := range s.registry.Endpoints() {
		key := e.Method + " " + e.Path
		if _, listed := backupLegacyPermissions[key]; listed {
			out[key] = e
		}
	}
	return out
}

// TestBackupRoutesDeclareTheSamePermissionTheyEnforced is the tally that
// makes this migration a refactor rather than a change.
//
// Seven of the 48 hand-placed calls move into middleware and 41 stay, which
// is the lowest hoist rate of any domain migrated so far. The tally says so
// out loud rather than leaving a reader to infer it from 21 reason strings.
// The 48 counts per ROUTE rather than per source line, the way
// pbsLegacyPermissions does: the 19 Deferred routes share one requirePBSPerm
// whose two branches are the two calls each of them makes.
func TestBackupRoutesDeclareTheSamePermissionTheyEnforced(t *testing.T) {
	declared := declaredBackupEndpoints(t)
	if len(declared) != backupRouteCount {
		t.Fatalf("the registry declares %d backup routes, want %d", len(declared), backupRouteCount)
	}
	if len(backupLegacyPermissions) != backupRouteCount {
		t.Fatalf("backupLegacyPermissions has %d entries, want %d — the table must cover every route",
			len(backupLegacyPermissions), backupRouteCount)
	}

	var hoisted, kept int
	for key, want := range backupLegacyPermissions {
		e, ok := declared[key]
		if !ok {
			t.Errorf("%s was gated %s before the migration but is not declared in the registry", key, want.permission)
			continue
		}
		switch want.shape {
		case "Check":
			hoisted += want.calls
			if e.Permissions.Check == nil {
				t.Errorf("%s declares %q rather than a Check; its cluster is in its own path",
					key, e.Permissions.Describe())
				continue
			}
			if got := e.Permissions.Describe(); got != want.permission {
				t.Errorf("%s declares %q but the handler enforced %q before the migration", key, got, want.permission)
			}
			if e.Permissions.Check.Scope != ScopeCluster {
				t.Errorf("%s is %s-scoped; requireClusterPerm resolved the cluster from the path",
					key, e.Permissions.Check.Scope)
			}
		case "Deferred":
			kept += want.calls
			if e.Permissions.Deferred == "" {
				t.Errorf("%s declares %q, want Deferred: the scope depends on whether the PBS server "+
					"belongs to a cluster", key, e.Permissions.Describe())
				continue
			}
			// A Deferred route renders as the bare word "deferred", so the
			// permission an operator needs has to survive in the prose.
			if !strings.Contains(e.Description, want.permission) {
				t.Errorf("%s is Deferred but its Description never names %q, so the docs tell an operator "+
					"building a role nothing: %q", key, want.permission, e.Description)
			}
			// And the reason has to name both halves of the split, not merely
			// say that the handler looks at something.
			for _, phrase := range []string{"requirePBSPerm", "standalone"} {
				if !strings.Contains(e.Permissions.Deferred, phrase) {
					t.Errorf("%s: the Deferred reason does not name %q, which is half of what an operator "+
						"would otherwise have to guess: %q", key, phrase, e.Permissions.Deferred)
				}
			}
		case "Advisory":
			kept += want.calls
			if e.Permissions.Advisory == nil {
				t.Errorf("%s declares %q, want Advisory: the listing filters rather than gates",
					key, e.Permissions.Describe())
				continue
			}
			if got := e.Permissions.Advisory.String(); got != want.permission {
				t.Errorf("%s filters on %q but the handler used %q", key, got, want.permission)
			}
			if !strings.Contains(e.Permissions.Advisory.Reason, "accessibleClusters") {
				t.Errorf("%s: the Advisory reason does not name accessibleClusters, which is what does "+
					"the filtering: %q", key, e.Permissions.Advisory.Reason)
			}
		default:
			t.Fatalf("%s: unknown shape %q in the tally", key, want.shape)
		}
	}
	if hoisted != 7 || kept != 41 {
		t.Errorf("the tally moves %d permission call(s) into middleware and keeps %d in handlers, want 7 / 41",
			hoisted, kept)
	}

	for key := range declared {
		if _, listed := backupLegacyPermissions[key]; !listed {
			t.Errorf("%s is declared but is not in backupLegacyPermissions — a new backup route must be "+
				"added to the tally, or the tally stops being a review surface", key)
		}
	}
}

// TestBackupPathSegmentsAreAnchored is the traversal guard for this domain.
//
// Every one of these values becomes a SEGMENT of a Proxmox or PBS request
// path by concatenation with url.PathEscape, which escapes "/" and leaves
// "." and ".." alone — so an un-anchored store or job id travels as a bare
// dot segment. pveproxy takes it literally, as a job id (see
// proxmox.validatePathSegment); a normalising reverse proxy in front of it
// resolves it — a final "." onto the collection it sits in, a ".." one level
// above — so PUT or DELETE /cluster/backup/{id} lands on /cluster/backup or
// on /cluster. PBS refuses it only because its own normalize_path does, which
// a normalising reverse proxy in front would undo. Nothing but a non-empty
// check stood behind those two, which ".." satisfies. The upid is the
// exception: PBSClient's task reads refuse "." and ".." in
// validatePBSTaskUPID before any path is built, so for it the anchor is the
// first of two layers rather than the only one.
func TestBackupPathSegmentsAreAnchored(t *testing.T) {
	segments := map[string][]string{
		"store": {
			"POST " + pbsBackupScope + "/datastores/:store/gc",
			"DELETE " + pbsBackupScope + "/datastores/:store/snapshots",
			"PUT " + pbsBackupScope + "/datastores/:store/snapshots/protect",
			"PUT " + pbsBackupScope + "/datastores/:store/snapshots/notes",
			"POST " + pbsBackupScope + "/datastores/:store/prune",
			"GET " + pbsBackupScope + "/datastores/:store/rrd",
			"GET " + pbsBackupScope + "/datastores/:store/config",
		},
		"job_id": {
			"POST " + pbsBackupScope + "/sync-jobs/:job_id/run",
			"POST " + pbsBackupScope + "/verify-jobs/:job_id/run",
			"PUT " + clusterScope + "/backup-jobs/:job_id",
			"DELETE " + clusterScope + "/backup-jobs/:job_id",
			"POST " + clusterScope + "/backup-jobs/:job_id/run",
		},
		"upid": {
			"GET " + pbsBackupScope + "/tasks/:upid",
			"GET " + pbsBackupScope + "/tasks/:upid/log",
		},
	}

	// One real value per parameter, so the anchor is proven not to have cost
	// anything: a datastore PBS reports, a job id PVE assigns, and a UPID in
	// both the raw and the percent-encoded form a caller may send.
	realValuesFor := map[string][]string{
		"store":  {testPBSStore, "_internal", "pbs.store-01"},
		"job_id": {"backup-1a2b3c4d", "s-abc123", "verify-1"},
		"upid":   {"UPID:pbs:0000A1B2:00000000:600D600D:garbage_collection::root@pam:", "UPID%3Apbs%3A0000A1B2"},
	}

	declared := declaredBackupEndpoints(t)
	for name, keys := range segments {
		for _, key := range keys {
			e, ok := declared[key]
			if !ok {
				t.Fatalf("%s is not declared", key)
			}
			prop := e.Parameters[name]
			if prop.Pattern == "" {
				t.Errorf("%s declares %q with no pattern; url.PathEscape leaves \".\" and \"..\" alone, so "+
					"unless a client guard stops them first (validatePBSTaskUPID does, for upid) the value "+
					"would travel as a bare dot segment — refused by PBS, taken literally by pveproxy, "+
					"resolved upward by a normalising proxy in front of either", key, name)
				continue
			}
			re, err := regexp.Compile(prop.Pattern)
			if err != nil {
				t.Fatalf("%s: parameter %q has an uncompilable pattern %q: %v", key, name, prop.Pattern, err)
			}
			// The WHOLE-SEGMENT traversals are what this anchor is for.
			// url.PathEscape escapes "/" to "%2F", which PBS keeps inside its
			// segment (normalize_path splits the raw path first) but pveproxy
			// decodes before it splits, so a slash is not inert on the PVE job
			// routes: the store and job-id classes admit none, and the upid,
			// PBS-only, is held to one segment by validatePBSTaskUPID. What
			// PathEscape leaves alone is "." and "..", which a normalising
			// proxy in front of either server resolves upward.
			for _, bad := range []string{".", "..", "../..", " .."} {
				if re.MatchString(bad) {
					t.Errorf("%s: parameter %q accepts %q", key, name, bad)
				}
			}
			// And the anchor must not have cost a value the API itself hands
			// back for this parameter.
			for _, good := range realValuesFor[name] {
				if !re.MatchString(good) {
					t.Errorf("%s: parameter %q refuses %q, which is a value the API itself hands back",
						key, name, good)
				}
			}
		}
	}
}

// TestBackupPathTraversalIsRefusedAtTheRoute proves the anchor bites
// end to end rather than only in the declaration: a ".." datastore reaches
// a 400 and the handler never runs.
func TestBackupPathTraversalIsRefusedAtTheRoute(t *testing.T) {
	const path = pbsBackupScope + "/datastores/:store/gc"
	cap := &capture{}
	app := newRegistryApp(t, noAuth(), probeBackupEndpoint(t, fiber.MethodPost, path, cap))

	target := strings.Replace(backupRoute(path), "/"+testPBSStore+"/", "/../", 1)
	status, _ := send(t, app, httptest.NewRequest(http.MethodPost, target, nil))
	if status == fiber.StatusNoContent {
		t.Fatal("a traversal segment reached the handler")
	}
	if cap.called {
		t.Error("the handler ran for a traversal segment")
	}
}

// probeBackupEndpoint is a declared backup endpoint with its handler
// swapped for a capture.
func probeBackupEndpoint(t *testing.T, method, path string, cap *capture) Endpoint {
	t.Helper()
	e := declaredEndpoint(t, method, path)
	e.Handler = cap.handler()
	e.Permissions = Permissions{SelfService: "parameter fixture; authorization is exercised separately"}
	return e
}

// TestSnapshotRoutesRequireTheSnapshotTriple pins the required SET of the
// three snapshot bodies against what each handler refused before the
// migration, derived from `git show HEAD:internal/api/handlers/backup.go` —
// one combined check for backup_type, backup_id and backup_time, and
// nothing else.
func TestSnapshotRoutesRequireTheSnapshotTriple(t *testing.T) {
	for _, tt := range []struct {
		method string
		path   string
		want   []string
	}{
		{fiber.MethodDelete, pbsBackupScope + "/datastores/:store/snapshots", []string{"backup_id", "backup_time", "backup_type", "pbs_id", "store"}},
		{fiber.MethodPut, pbsBackupScope + "/datastores/:store/snapshots/protect", []string{"backup_id", "backup_time", "backup_type", "pbs_id", "store"}},
		{fiber.MethodPut, pbsBackupScope + "/datastores/:store/snapshots/notes", []string{"backup_id", "backup_time", "backup_type", "pbs_id", "store"}},
	} {
		t.Run(tt.method+" "+tt.path, func(t *testing.T) {
			e := declaredEndpoint(t, tt.method, tt.path)
			var got []string
			for name, prop := range e.Parameters {
				if !prop.Optional {
					got = append(got, name)
				}
			}
			sort.Strings(got)
			if !slices.Equal(got, tt.want) {
				t.Errorf("required parameters = %v, want %v", got, tt.want)
			}
			// The zero timestamp was refused by name before the migration and
			// apischema would otherwise take an explicit 0 as supplied.
			if min := e.Parameters["backup_time"].Minimum; min == nil || *min != 1 {
				t.Errorf("backup_time declares minimum %v, want 1 — zero was refused outright", min)
			}
		})
	}
}

// TestDeleteSnapshotReadsItsBodyNotTheQueryString is the assertion behind
// the one Source override in this domain.
//
// ResolveSource infers QUERY for a DELETE, and the handler has always read
// these three from the body — which is what the delete dialog sends. Without
// the explicit SourceBody the schema would demand them in the URL and reject
// every existing caller, and checkMisplaced would tell them so in as many
// words.
func TestDeleteSnapshotReadsItsBodyNotTheQueryString(t *testing.T) {
	const path = pbsBackupScope + "/datastores/:store/snapshots"
	e := declaredEndpoint(t, fiber.MethodDelete, path)
	for _, name := range []string{"backup_type", "backup_id", "backup_time"} {
		if got := e.Parameters[name].Source; got != "body" {
			t.Errorf("%s declares source %q, want body — a DELETE would otherwise infer the query string", name, got)
		}
	}

	cap := &capture{}
	app := newRegistryApp(t, noAuth(), probeBackupEndpoint(t, fiber.MethodDelete, path, cap))
	body := `{"backup_type":"vm","backup_id":"101","backup_time":1700000000}`
	status, env := send(t, app, jsonRequest(http.MethodDelete, backupRoute(path), body))
	if status != fiber.StatusNoContent {
		t.Fatalf("status = %d (%q), want 204 — this is the payload the delete dialog sends", status, env.Message)
	}
	if got := cap.params.Int("backup_time"); got != 1700000000 {
		t.Errorf("backup_time = %d, want the body value to survive", got)
	}

	// And the same values in the query string are refused with a message that
	// says where they belong.
	cap = &capture{}
	app = newRegistryApp(t, noAuth(), probeBackupEndpoint(t, fiber.MethodDelete, path, cap))
	target := backupRoute(path) + "?backup_type=vm&backup_id=101&backup_time=1700000000"
	status, env = send(t, app, httptest.NewRequest(http.MethodDelete, target, nil))
	if status != fiber.StatusBadRequest {
		t.Fatalf("status = %d (%q), want 400", status, env.Message)
	}
	if !strings.Contains(env.Message, "request body") {
		t.Errorf("message = %q, want it to say the parameter belongs in the body", env.Message)
	}
}

// TestBackupJobBodyKeepsItsFourTristates is the compatibility assertion for
// the partial-update contract.
//
// enabled, all, node and comment were bound as pointers precisely so that
// "the caller never mentioned this" stays distinct from "the caller sent
// zero or empty": clearedProperties() unsets node and comment only on an
// explicit empty, and selectionKeys() reads all=0 as "not a selection". The
// handler's OptInt/OptString reads keep that even with a Default declared
// (apischema.Property.Default) — the end-to-end check below is what pins the
// toggle, which PUTs `{"enabled":0}` and nothing else — so the declaration
// check is about the docs: a default would describe an omitted field as set
// to it, when it is left alone.
func TestBackupJobBodyKeepsItsFourTristates(t *testing.T) {
	for _, path := range []string{clusterScope + "/backup-jobs", clusterScope + "/backup-jobs/:job_id"} {
		method := fiber.MethodPost
		if strings.HasSuffix(path, ":job_id") {
			method = fiber.MethodPut
		}
		t.Run(method+" "+path, func(t *testing.T) {
			e := declaredEndpoint(t, method, path)
			for _, name := range []string{"enabled", "all", "node", "comment"} {
				prop, ok := e.Parameters[name]
				if !ok {
					t.Fatalf("%s is not declared", name)
				}
				if !prop.Optional {
					t.Errorf("%s is required; both handlers bound the body and checked nothing", name)
				}
				if prop.Default != nil {
					t.Errorf("%s declares default %#v; it must have NONE — an omitted one is left alone, "+
						"and a default would document it as set", name, prop.Default)
				}
			}
		})
	}

	// End to end: the enable/disable toggle's payload reaches the handler
	// with only `enabled` supplied.
	const path = clusterScope + "/backup-jobs/:job_id"
	cap := &capture{}
	app := newRegistryApp(t, noAuth(), probeBackupEndpoint(t, fiber.MethodPut, path, cap))
	status, env := send(t, app, jsonRequest(http.MethodPut, backupRoute(path), `{"enabled":0}`))
	if status != fiber.StatusNoContent {
		t.Fatalf("status = %d (%q), want 204", status, env.Message)
	}
	if value, supplied := cap.params.OptInt("enabled"); !supplied || value != 0 {
		t.Errorf("enabled read back as (%d, supplied=%v), want (0, true)", value, supplied)
	}
	for _, absent := range []string{"node", "comment"} {
		if _, supplied := cap.params.OptString(absent); supplied {
			t.Errorf("%s reads as supplied on a body that omitted it; clearedProperties() would unset it", absent)
		}
	}
	if _, supplied := cap.params.OptInt("all"); supplied {
		t.Error("all reads as supplied on a body that omitted it; it would be read as a guest selection")
	}
}

// TestBackupJobBodyAcceptsWhatTheDialogSends is the other half: the job
// dialog sends node and comment on every save, empty ones included, and an
// empty node has to survive as the empty sentinel rather than be rejected by
// the node-name format.
func TestBackupJobBodyAcceptsWhatTheDialogSends(t *testing.T) {
	const path = clusterScope + "/backup-jobs"
	cap := &capture{}
	app := newRegistryApp(t, noAuth(), probeBackupEndpoint(t, fiber.MethodPost, path, cap))
	body := `{"enabled":1,"schedule":"mon..fri 02:00","storage":"store01","node":"","mode":"snapshot",` +
		`"compress":"zstd","comment":"","all":1}`
	status, env := send(t, app, jsonRequest(http.MethodPost, backupRoute(path), body))
	if status != fiber.StatusNoContent {
		t.Fatalf("status = %d (%q), want 204 — this is the payload the job dialog sends", status, env.Message)
	}
	if value, supplied := cap.params.OptString("node"); !supplied || value != "" {
		t.Errorf("node read back as (%q, supplied=%v), want the empty sentinel to survive as supplied", value, supplied)
	}
	if value, supplied := cap.params.OptString("comment"); !supplied || value != "" {
		t.Errorf("comment read back as (%q, supplied=%v), want the empty sentinel to survive as supplied", value, supplied)
	}

	// A non-empty node is still held to the node-name shape.
	cap = &capture{}
	app = newRegistryApp(t, noAuth(), probeBackupEndpoint(t, fiber.MethodPost, path, cap))
	if status, _ := send(t, app, jsonRequest(http.MethodPost, backupRoute(path), `{"node":"../etc"}`)); status != fiber.StatusBadRequest {
		t.Errorf("status = %d, want 400 for a node name carrying a traversal", status)
	}
}

// TestRestoreRequiresWhatTheHandlerDid pins the required SET of the restore
// body against the one combined check the handler made, derived from
// `git show HEAD:internal/api/handlers/backup.go`.
func TestRestoreRequiresWhatTheHandlerDid(t *testing.T) {
	e := declaredEndpoint(t, fiber.MethodPost, clusterScope+"/restore")
	var got []string
	for name, prop := range e.Parameters {
		if !prop.Optional {
			got = append(got, name)
		}
	}
	sort.Strings(got)
	want := []string{"backup_id", "backup_time", "backup_type", "cluster_id", "pbs_server_id", "target_node", "vmid"}
	if !slices.Equal(got, want) {
		t.Errorf("required parameters = %v, want %v", got, want)
	}
	// vmid <= 0 and backup_time == 0 were refused outright; apischema would
	// otherwise take an explicit zero as a supplied value.
	if min := e.Parameters["vmid"].Minimum; min == nil || *min != 1 {
		t.Errorf("vmid declares minimum %v, want 1", min)
	}
	if min := e.Parameters["backup_time"].Minimum; min == nil || *min != 1 {
		t.Errorf("backup_time declares minimum %v, want 1", min)
	}
	// The enum is the handler's own switch, which answered
	// "backup_type must be 'vm' or 'ct'" AFTER it may already have stopped a
	// running guest.
	if got := e.Parameters["backup_type"].Enum; !slices.Equal(got, []string{"vm", "ct"}) {
		t.Errorf("backup_type declares enum %v, want [vm ct]", got)
	}
}

// TestPBSMetricsTimeframesCoverTheTable holds the declared enum and the
// window table together, the way TestCephMetricsTimeframesCoverTheTable does
// for Ceph: the enum is what validates a request and the table is what maps a
// timeframe to its window, and nothing but this test stops the two drifting.
func TestPBSMetricsTimeframesCoverTheTable(t *testing.T) {
	e := declaredEndpoint(t, fiber.MethodGet, pbsBackupScope+"/metrics")
	enum := e.Parameters["timeframe"].Enum
	if !slices.Equal(enum, handlers.PBSMetricsTimeframes) {
		t.Fatalf("the declaration's enum is %v, want %v", enum, handlers.PBSMetricsTimeframes)
	}
	if got := e.Parameters["timeframe"].Default; got != handlers.PBSMetricsLatestTimeframe {
		t.Errorf("timeframe declares default %#v, want %q — a request naming none asks for the latest "+
			"sample per datastore, not a window", got, handlers.PBSMetricsLatestTimeframe)
	}
	if !slices.Contains(enum, handlers.PBSMetricsLatestTimeframe) {
		t.Errorf("the enum %v does not contain the default %q, so the documented default would 400",
			enum, handlers.PBSMetricsLatestTimeframe)
	}
	// The other direction — that every non-"latest" member resolves to a real
	// window — needs the unexported table, so it lives beside it in
	// TestPBSMetricsTimeframesCoverTheWindowTable
	// (internal/api/handlers/backup_test.go), exactly as the Ceph pair does.
}

// TestBackupClusterRoutesAreGatedByTheirDeclaration proves the seven
// hoisted permissions are the permissions the routes enforce, end to end.
func TestBackupClusterRoutesAreGatedByTheirDeclaration(t *testing.T) {
	for key, want := range backupLegacyPermissions {
		if want.shape != "Check" {
			continue
		}
		method, path, _ := strings.Cut(key, " ")
		t.Run(key, func(t *testing.T) {
			e := declaredEndpoint(t, method, path)
			if got := e.Permissions.Describe(); got != want.permission {
				t.Fatalf("declares %q, want %q", got, want.permission)
			}
			cap := &capture{}
			gated := e
			gated.Handler = cap.handler()
			target := backupRoute(path)

			app := newRegistryApp(t, stubAuth(map[string]bool{"view:pbs": true}), gated)
			status, _ := send(t, app, authedRequest(method, target))
			if status != fiber.StatusForbidden {
				t.Fatalf("a caller holding an unrelated grant got %d, want 403", status)
			}
			if cap.called {
				t.Error("the handler ran for a caller without the declared permission")
			}

			cap.called = false
			app = newRegistryApp(t, stubAuth(map[string]bool{want.permission: true}), gated)
			status, _ = send(t, app, authedRequest(method, target))
			if status == fiber.StatusForbidden {
				t.Fatalf("a caller holding %q got 403", want.permission)
			}

			cap.called = false
			app = newRegistryApp(t, stubAuth(map[string]bool{want.permission: true}), gated)
			status, _ = send(t, app, httptest.NewRequest(method, target, nil))
			if status != fiber.StatusUnauthorized {
				t.Fatalf("an anonymous caller got %d, want 401", status)
			}
			if cap.called {
				t.Error("the handler ran for a request carrying no session")
			}
		})
	}
}

// TestEveryBackupEndpointIsDocumented holds the declarations to the standard
// that makes this whole effort worth doing.
func TestEveryBackupEndpointIsDocumented(t *testing.T) {
	for key, e := range declaredBackupEndpoints(t) {
		if err := e.Parameters.Compile(); err != nil {
			t.Errorf("%s: %v", key, err)
		}
		if strings.TrimSpace(e.Description) == "" {
			t.Errorf("%s has no description", key)
		}
		if e.Group != "Backup" {
			t.Errorf("%s is in group %q, want Backup", key, e.Group)
		}
		for name, prop := range e.Parameters {
			if strings.TrimSpace(prop.Description) == "" {
				t.Errorf("%s: parameter %q has no description", key, name)
			}
		}
	}
}
