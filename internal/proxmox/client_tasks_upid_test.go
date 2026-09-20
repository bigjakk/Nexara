package proxmox

import (
	"context"
	"errors"
	"net/url"
	"strconv"
	"strings"
	"testing"
)

// These cases guard the three node-task calls at the choke point — the client —
// rather than at the routes, for the reason validatePBSTaskUPID records for the
// PBS pair: the route declaration matches the value as it ARRIVES, where an
// escaped dot carries no dot at all, and a check in one handler is a check the
// next handler forgets.
//
// The traversal is expressible because taskUPID (internal/api/handlers/vms.go)
// percent-DECODES the :upid path parameter before the client sees it. What
// reaches GetTaskStatus is the real string "UPID:pve-01:a/../../../status", and
// url.PathEscape does not defuse it. It leaves "." and ".." alone outright, and
// it turns each "/" back into "%2F" — which Proxmox decodes before it resolves
// the path, the far-side behaviour the capture-server run recorded on
// forbiddenVolumeIDChars (client_storage.go) watched happen.
// Where the request then lands is the caller's to pick, one level per "..":
// GetTaskStatus appends "/status", so "A/../.." resolves onto
// /nodes/{node}/status, while StopNodeTask appends nothing at all and hands the
// caller the whole tail — "A/../../certificates/custom" resolves onto DELETE
// /nodes/{node}/certificates/custom. The cases below carry representative
// traversals rather than one per arithmetic variant; what each asserts is that
// NOTHING is sent. Both GETs are reachable by anything holding view:task, which
// the built-in Viewer role holds.
//
// Every case drives one of the three EXPORTED methods. Asserting on
// validateTaskUPID directly would still pass on the day someone drops the call
// from one of them.
//
// Every assertion reads r.RequestURI (newCaptureServer records exactly that),
// never r.URL.Path. net/http has already decoded Path by the time a handler
// runs, so "%2F.." and "/.." are indistinguishable there and the entire bug
// class is invisible.

// recordedNodeTaskUPIDs is the corpus that must survive the guard unchanged.
//
// Half of it is the shapes this repo already pins in its own fixtures
// (internal/db/migration_084_test.go, internal/api/registry_tasks_test.go);
// the other half is a shape census of every task_history row on the development
// database, with node names, realms and token names rewritten to the
// placeholder scheme. The census is recorded as a PROPERTY and not as a row
// count on purpose: the count moves every time the collector runs, and a number
// that rots independently of the sentence it supports teaches readers to
// distrust the sentence. The property: not one recorded UPID carries a "/", a
// "\", a control character, or is "." or ".." — which is what makes a blanket
// separator ban affordable here.
//
// It exists so that nobody later "tightens" this into a full-shape UPID regex.
// PVE mints 8 colon-separated fields and PBS 9; the worker id is legitimately
// empty (aptupdate), legitimately non-numeric (vzdump:local), legitimately
// dotted (osd.1, a ceph mgr id), and legitimately carries an "@"; the user half
// carries a "!" for an API token; and the repo's own task-create route
// round-trips a value with fewer fields than either. A guard that is too strict
// fails SILENTLY rather than loudly: reconcileRunningTasks
// (internal/collector/task_reconcile.go) polls these unattended, swallows the
// error, and after staleTaskGrace marks the task failed — so an over-tight
// guard would not surface as a 400, it would surface as good tasks recorded as
// having vanished.
var recordedNodeTaskUPIDs = []struct{ name, upid string }{
	{"guest start", "UPID:pve-01:001316BE:00B8B463:6A8CE407:qmstart:110:root@pam:"},
	{"guest shutdown by an API token", "UPID:pve-01:001316BE:00B8B463:6A8CE407:qmshutdown:142:root@pam!nexara:"},
	{"token name with a hyphen", "UPID:pve-01:00019B04:000F2B07:6AAACD23:qmstart:111:root@pam!automation-dev:"},
	{"a second realm", "UPID:pve-01:0021A879:02F36E27:6AAAA1E0:qmigrate:102:automation@pve:"},
	{"HA migrate", "UPID:pve-01:001316BE:00B8B463:6A8CE407:hamigrate:125:root@pam:"},
	{"empty worker id on an apt task", "UPID:pve-01:0000A1B2:00000001:6A8CE407:aptupdate::root@pam:"},
	{"empty worker id on a node-wide task", "UPID:pve-01:0000069C:00000843:6AAAA664:startall::root@pam:"},
	{"non-numeric worker id", "UPID:pve-01:0000A1B2:00000001:6A8CE407:vzdump:local:root@pam:"},
	{"dotted worker id", "UPID:pve-01:0000A1B2:00000001:6A8CE407:srvrestart:osd.1:root@pam:"},
	{"dotted ceph mgr worker id", "UPID:pve-01:0000A1B2:00000001:6A8CE407:cephcreatemgr:pve-01.mgr2:root@pam:"},
	{"worker id carrying an at-sign", "UPID:pve-01:0000A1B2:00000001:6A8CE407:imgdel:105@store02:root@pam:"},
	{"zero-padded nothing", "UPID:pve-01:0:0:0:cephdestroypool::root@pam:"},
	// The value internal/api/registry_tasks_test.go round-trips through the
	// task-create route, decoded. Fewer fields than PVE mints, and a full-shape
	// regex would reject it.
	{"the repo's own short round-trip value", "UPID:pve-01:0000A:qmstart::root@pam:"},
	// PBS mints one field more than PVE. A PVE node never answers for one, but
	// the guard must not be the thing that decides that.
	{"a PBS-shaped nine-field UPID", "UPID:pbs-01:0000ABCD:00012345:00000000:66F00000:garbage_collection:datastore01:root@pam:"},
}

// TestNodeTaskCallsSendEveryRecordedUPIDUnchanged is the control the refusal
// cases below lean on: without it, a stand-in server that answered nothing
// would make every refusal look like a success.
func TestNodeTaskCallsSendEveryRecordedUPIDUnchanged(t *testing.T) {
	const node = "pve-01"

	// One golden literal. Every expectation below is built with the same
	// url.PathEscape the production code uses, which pins "escaped once" but
	// would follow an encoder swap without complaining; this pins what actually
	// goes on the wire. Note what survives untouched: the colons and the "@"
	// are legal in a path segment, and only the "!" of an API-token user is
	// escaped.
	golden := "UPID:pve-01:001316BE:00B8B463:6A8CE407:qmshutdown:142:root@pam!nexara:"
	if want, got := "UPID:pve-01:001316BE:00B8B463:6A8CE407:qmshutdown:142:root@pam%21nexara:", url.PathEscape(golden); want != got {
		t.Fatalf("url.PathEscape(%q) = %q, want %q — the wire form PVE's own UI sends has changed", golden, got, want)
	}

	for _, tc := range recordedNodeTaskUPIDs {
		escaped := url.PathEscape(tc.upid)
		base := "/api2/json/nodes/" + node + "/tasks/" + escaped

		t.Run("status/"+tc.name, func(t *testing.T) {
			srv, seen := newCaptureServer(t, `{"data":null}`)
			c := newTestClient(t, srv.URL)
			if _, err := c.GetTaskStatus(context.Background(), node, tc.upid); err != nil {
				t.Fatalf("GetTaskStatus(%q): %v", tc.upid, err)
			}
			assertOneNodeTaskRequest(t, seen, base+"/status")
		})

		t.Run("log/"+tc.name, func(t *testing.T) {
			srv, seen := newCaptureServer(t, `{"data":null}`)
			c := newTestClient(t, srv.URL)
			if _, err := c.GetTaskLog(context.Background(), node, tc.upid, 0); err != nil {
				t.Fatalf("GetTaskLog(%q): %v", tc.upid, err)
			}
			assertOneNodeTaskRequest(t, seen, base+"/log?start=0&limit=5000")
		})

		t.Run("stop/"+tc.name, func(t *testing.T) {
			srv, seen := newCaptureServer(t, `{"data":null}`)
			c := newTestClient(t, srv.URL)
			if err := c.StopNodeTask(context.Background(), node, tc.upid); err != nil {
				t.Fatalf("StopNodeTask(%q): %v", tc.upid, err)
			}
			assertOneNodeTaskRequest(t, seen, base)
		})
	}
}

// TestNodeTaskCallsRefuseAUPIDThatIsNotOnePathSegment drives the guard through
// all three protected methods.
//
// Reachability differs by value, and only one of the two paths runs through the
// task routes. taskUPID (internal/api/handlers/vms.go) 400s anything
// extractNodeFromUPID cannot read a node out of, which needs at least two
// colon-separated parts — so of the values below only "the probe value" arrives
// at this client from a caller holding view:task. The colon-free ones reach it
// the other way: POST /api/v1/tasks takes an unpatterned upid (manage:task),
// files the row as running, and reconcileRunningTasks replays it through
// GetTaskStatus on the next sync tick with the server's own credentials and
// nobody watching. A guard in either handler would have covered one of those
// two and not the other, which is the argument for putting it here.
//
// The refusal has to wrap ErrInvalidInput: that is what mapProxmoxError turns
// into a 400, and an unclassified error surfaces as a 500 that reads like a
// Proxmox outage rather than like the caller's own input.
func TestNodeTaskCallsRefuseAUPIDThatIsNotOnePathSegment(t *testing.T) {
	const node = "pve-01"

	cases := []struct{ name, upid string }{
		{"empty", ""},
		{"a bare dot", "."},
		{"a bare traversal", ".."},
		// The probe value. GET .../tasks/UPID%3Apve-01%3Aa%2F..%2F..%2F..%2Fstatus/status
		// resolves to /nodes/pve-01/status once Proxmox decodes the escapes.
		{"the probe value", "UPID:pve-01:a/../../../status"},
		{"a traversal onto the node's own status", "A/../../status"},
		// Dot-free, and therefore refused ONLY by the separator ban. Swapping in
		// validatePathSegmentAllowingSlash lets exactly these two through, which
		// is how we know the separator half is load-bearing on its own.
		{"a single separator", "A/b"},
		{"a descent one level deeper than the segment", "tasks/log"},
		{"a backslash form", `A\..\status`},
		{"a newline", "A\nb"},
		{"a NUL", "A\x00b"},
		{"an ANSI escape", "A\x1b[2Kb"},
		{"a leading absolute slash", "/cluster/log"},
	}

	for _, tc := range cases {
		t.Run("status/"+tc.name, func(t *testing.T) {
			srv, seen := newCaptureServer(t, `{"data":null}`)
			c := newTestClient(t, srv.URL)
			_, err := c.GetTaskStatus(context.Background(), node, tc.upid)
			assertNodeTaskUPIDRefused(t, err, seen)
		})

		t.Run("log/"+tc.name, func(t *testing.T) {
			srv, seen := newCaptureServer(t, `{"data":null}`)
			c := newTestClient(t, srv.URL)
			_, err := c.GetTaskLog(context.Background(), node, tc.upid, 0)
			assertNodeTaskUPIDRefused(t, err, seen)
		})

		// The DELETE matters most: it has no suffix after {upid}, so the caller
		// chooses the whole tail of the path rather than just a middle segment.
		t.Run("stop/"+tc.name, func(t *testing.T) {
			srv, seen := newCaptureServer(t, `{"data":null}`)
			c := newTestClient(t, srv.URL)
			err := c.StopNodeTask(context.Background(), node, tc.upid)
			assertNodeTaskUPIDRefused(t, err, seen)
		})
	}
}

// TestNodeTaskLogKeepsItsQueryParametersAfterTheGuard pins the half of
// GetTaskLog's path the guard does not touch, so that a rewrite of the
// validation cannot quietly take the paging arguments with it.
func TestNodeTaskLogKeepsItsQueryParametersAfterTheGuard(t *testing.T) {
	const (
		node = "pve-01"
		upid = "UPID:pve-01:001316BE:00B8B463:6A8CE407:qmstart:110:root@pam:"
	)
	for _, start := range []int{0, 250} {
		t.Run("start="+strconv.Itoa(start), func(t *testing.T) {
			srv, seen := newCaptureServer(t, `{"data":null}`)
			c := newTestClient(t, srv.URL)
			if _, err := c.GetTaskLog(context.Background(), node, upid, start); err != nil {
				t.Fatalf("GetTaskLog: %v", err)
			}
			want := "/api2/json/nodes/" + node + "/tasks/" + url.PathEscape(upid) +
				"/log?start=" + strconv.Itoa(start) + "&limit=5000"
			assertOneNodeTaskRequest(t, seen, want)
		})
	}
}

func assertOneNodeTaskRequest(t *testing.T, seen *[]string, want string) {
	t.Helper()
	got := *seen
	if len(got) != 1 {
		t.Fatalf("issued %d requests %q, want exactly 1", len(got), got)
	}
	if got[0] != want {
		t.Errorf("request target = %q, want %q", got[0], want)
	}
}

func assertNodeTaskUPIDRefused(t *testing.T, err error, seen *[]string) {
	t.Helper()
	if sent := *seen; len(sent) != 0 {
		// Printed first and in full: this line IS the vulnerability. Anything
		// with a "%2F" or a ".." in it resolves somewhere the route never
		// addressed once Proxmox decodes the escapes.
		t.Errorf("the request reached Proxmox as %q; it must never leave the client", sent)
		if strings.Contains(sent[0], "..") {
			t.Errorf("…and %q walks the path upward", sent[0])
		}
	}
	if err == nil {
		t.Fatal("the call succeeded; the value must be refused before it is sent")
	}
	if !errors.Is(err, ErrInvalidInput) {
		t.Errorf("err = %v, want one wrapping ErrInvalidInput — anything else maps to a 500", err)
	}
}
