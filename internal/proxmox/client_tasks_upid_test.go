package proxmox

import (
	"context"
	"errors"
	"net/url"
	"strconv"
	"strings"
	"testing"
)

// The three node-task calls are guarded at the client, as validatePBSTaskUPID's comment says
// why: taskUPID decodes :upid, so "UPID:pve-01:a/../../../status" reaches GetTaskStatus, and
// PathEscape leaves the dots. Behind a normalising proxy the caller picks the target (StopNodeTask
// appends nothing: DELETE /nodes/{node}/certificates/custom). Each case asserts nothing is sent,
// through the exported methods, on r.RequestURI (r.URL.Path is already decoded).

// recordedNodeTaskUPIDs must survive the guard unchanged: the repo's own fixtures and a shape
// census of the dev database's task_history (names rewritten), in which no UPID carries "/",
// "\", a control character, or is "." or "..". It is here so nobody tightens the guard into a
// full-shape regex: client_tasks.go records the legitimate shapes, and why too strict a guard
// fails silently (good tasks recorded as vanished).
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
		// Each is sent to the node it names, the only one the client dials.
		node, err := TaskNode(tc.upid)
		if err != nil {
			t.Fatalf("TaskNode(%q): %v", tc.upid, err)
		}
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

// TestNodeTaskCallsRefuseAUPIDThatIsNotOnePathSegment drives all three methods. Only the probe
// value passes taskUPID (it needs a node to extract); the colon-free ones arrive via POST
// /api/v1/tasks (manage:task), replayed by reconcileRunningTasks with the server's credentials.
// A guard in either handler covers one path, hence the client. The refusal wraps
// ErrInvalidInput, which mapProxmoxError makes a 400, not a 500.
func TestNodeTaskCallsRefuseAUPIDThatIsNotOnePathSegment(t *testing.T) {
	const node = "pve-01"

	cases := []struct{ name, upid string }{
		{"empty", ""},
		{"a bare dot", "."},
		{"a bare traversal", ".."},
		// The probe value. GetTaskStatus would send it as
		// .../tasks/UPID:pve-01:a%2F..%2F..%2F..%2Fstatus/status (url.PathEscape
		// leaves the colons alone), which, once a proxy decodes the escapes and
		// removes the dot segments, is /nodes/status/status: three ".." pop the
		// UPID, "tasks" and the node, so this is the status of a node NAMED
		// "status". "A/../.." is the value that lands on /nodes/pve-01/status.
		// pveproxy itself decodes but resolves nothing (see validatePathSegment).
		{"the probe value", "UPID:pve-01:a/../../../status"},
		// Named for StopNodeTask, which appends nothing, so there it lands on
		// /nodes/pve-01/status; the two GETs append "/status" or "/log" to it.
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
		// Whole segments, refused because the client dials only the node a
		// UPID names (TaskNode), and these name another node or none.
		{"a task on another node", "UPID:pve-02:0000A1B2:00000001:6A8CE407:imgcopy::root@pam:"},
		{"a UPID naming no node", "UPID:192.0.2.10:0000A1B2:00000001:6A8CE407:qmstart:100:root@pam:"},
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
		// Printed first and in full: this line IS the vulnerability. A "%2F"
		// becomes a separator once pveproxy decodes it, and a ".." is walked
		// upward by any proxy in front of pveproxy that normalises the path,
		// so either can land the request somewhere the route never addressed.
		t.Errorf("the request reached Proxmox as %q; it must never leave the client", sent)
		if strings.Contains(sent[0], "..") {
			t.Errorf("…and %q carries a \"..\": as a whole segment any normalising proxy would walk it "+
				"upward; inside an escaped segment, only one that first decodes the escaped separator would",
				sent[0])
		}
	}
	if err == nil {
		t.Fatal("the call succeeded; the value must be refused before it is sent")
	}
	if !errors.Is(err, ErrInvalidInput) {
		t.Errorf("err = %v, want one wrapping ErrInvalidInput — anything else maps to a 500", err)
	}
}

// TestFindNodeTaskNarrowsTheListToTheTask: PVE filters since/until on start
// time, so the list is asked for the UPID's start second and type, and the
// entry returned is the one whose UPID matches.
func TestFindNodeTaskNarrowsTheListToTheTask(t *testing.T) {
	t.Parallel()
	upid := "UPID:pve-01:0000ABCD:00001234:6700000A:qmigrate:100:alice@pve:"
	srv, seen := newCaptureServer(t, `{"data":[{"upid":"UPID:pve-01:0000ABCE:00001235:6700000A:qmigrate:101:alice@pve:","endtime":5},{"upid":"`+upid+`","endtime":1728053260}]}`)
	c := newTestClient(t, srv.URL)
	task, err := c.FindNodeTask(context.Background(), "pve-01", upid)
	if err != nil {
		t.Fatalf("FindNodeTask: %v", err)
	}
	if task == nil || task.EndTime != 1728053260 {
		t.Fatalf("FindNodeTask = %+v, want the entry for %s", task, upid)
	}
	want := "/api2/json/nodes/pve-01/tasks?limit=50&since=1728053258&source=all&typefilter=qmigrate&until=1728053258"
	if len(*seen) != 1 || (*seen)[0] != want {
		t.Fatalf("requests = %v, want [%s]", *seen, want)
	}
}
