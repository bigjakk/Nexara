package proxmox

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
)

// These cases guard PBSClient's two task reads at the choke point, which is
// where the guard has to be rather than at the route.
//
// The API layer percent-decodes the :upid path parameter before it gets here
// (pbsTaskUPIDFromParams in internal/api/handlers/backup.go), and that decode is
// what puts a real "/" or ".." into the value at all: "A%2F..%2F..%2Fstatus"
// arrives at the handler as text, and leaves it as the real string
// "A/../../status". PBS itself would not traverse on it — the guard's doc
// comment records why — so what these cases pin is that a value no PBS minted
// gets a clear local 400, and that a value PBS DID mint is never refused.
// Asserting through GetTaskLog/GetTaskStatus rather than on validatePBSTaskUPID
// directly is deliberate — a validator with one caller is a validator the next
// caller will forget, and only a test that goes through the protected function
// notices when the call is dropped.

// pbsUPIDWire records the raw request target of every call that reaches the
// stand-in PBS server, and answers each with an envelope the client can decode.
type pbsUPIDWire struct {
	mu      sync.Mutex
	targets []string
}

func (w *pbsUPIDWire) seen() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]string(nil), w.targets...)
}

// newPBSUPIDWireServer stands one up. http.HandlerFunc rather than a ServeMux:
// a mux cleans and redirects paths, which would normalise away the escapes
// these cases exist to observe.
func newPBSUPIDWireServer(t *testing.T) (*PBSClient, *pbsUPIDWire) {
	t.Helper()
	wire := &pbsUPIDWire{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		wire.mu.Lock()
		wire.targets = append(wire.targets, r.RequestURI)
		wire.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		target, _, _ := strings.Cut(r.RequestURI, "?")
		if strings.HasSuffix(target, "/log") {
			_, _ = w.Write([]byte(`{"data":[{"n":1,"t":"TASK OK"}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"data":{"status":"stopped","exitstatus":"OK"}}`))
	}))
	t.Cleanup(srv.Close)

	return &PBSClient{apiClient: &apiClient{
		httpClient: srv.Client(),
		baseURL:    srv.URL,
		authHeader: "PBSAPIToken=test@pam!token:secret",
		auth:       tokenAuth{header: "PBSAPIToken=test@pam!token:secret"},
	}}, wire
}

// realPBSTaskUPIDs are UPIDs in the shapes PBS actually mints, keyed by what
// each one exercises.
//
// PBS writes a UPID's worker id through escape_id (proxmox-schema src/upid.rs):
// "/" becomes "-", and every other byte outside [A-Za-z0-9_.] becomes "\xNN".
// So most everyday task types carry a backslash. Fixtures that use only worker
// ids needing no escaping — "datastore01" — cannot tell a guard that refuses
// real UPIDs from one that does not, which is how the first version of this
// guard shipped refusing them.
var realPBSTaskUPIDs = map[string]string{
	// A worker id with nothing to escape: GC on a datastore whose name is
	// letters and digits only.
	"gc, plain datastore": "UPID:pbs-01:0000ABCD:00012345:00000000:66F00000:garbage_collection:datastore01:root@pam:",
	// "<store>:<type>/<id>" (proxmox-backup src/api2/backup/mod.rs): the colon
	// becomes \x3a and the slash becomes a dash. The most common PBS task.
	"backup": `UPID:pbs-01:0000ABCD:00012345:0000001D:66F00001:backup:datastore01\x3avm-100:root@pam:`,
	// "<store>:<job id>" (proxmox-backup src/server/verify_job.rs), and the job
	// id's own dash is escaped too.
	"verification job": `UPID:pbs-01:0000ABCE:00012346:0000001E:66F00002:verificationjob:datastore01\x3av\x2d0001:root@pam:`,
	// The worker id is the bare store name, so any dash in it is escaped.
	"gc, dashed datastore": `UPID:pbs-01:0000ABCF:00012347:0000001F:66F00003:garbage_collection:datastore\x2d01:backup@pbs:`,
	// Started by an API token, as every task Nexara starts on PBS is: the user
	// field carries the token's "!". A path segment may hold one raw, but
	// url.PathEscape encodes it to "%21" — which is what makes this the case
	// that notices when the client stops escaping.
	"api token user": "UPID:pbs-01:0000ABD0:00012348:00000020:66F00004:garbage_collection:datastore01:root@pam!token01:",
}

// TestPBSClientTaskReadsSendTheUPIDTheCallerGave pins what goes out for a real
// UPID: url.PathEscape's output, which leaves the colons and the "@" alone
// because both are legal in a path segment, and sends a backslash as "%5C" —
// which PBS decodes back into the literal UPID it minted.
func TestPBSClientTaskReadsSendTheUPIDTheCallerGave(t *testing.T) {
	for name, upid := range realPBSTaskUPIDs {
		escaped := url.PathEscape(upid)

		t.Run(name+"/status", func(t *testing.T) {
			client, wire := newPBSUPIDWireServer(t)
			if _, err := client.GetTaskStatus(context.Background(), upid); err != nil {
				t.Fatalf("GetTaskStatus(%q): %v — a UPID PBS minted must be sendable", upid, err)
			}
			want := "/api2/json/nodes/localhost/tasks/" + escaped + "/status"
			if got := wire.seen(); len(got) != 1 || got[0] != want {
				t.Errorf("PBS was asked for %q, want [%q]", got, want)
			}
		})

		t.Run(name+"/log", func(t *testing.T) {
			client, wire := newPBSUPIDWireServer(t)
			if _, err := client.GetTaskLog(context.Background(), upid); err != nil {
				t.Fatalf("GetTaskLog(%q): %v — a UPID PBS minted must be sendable", upid, err)
			}
			want := "/api2/json/nodes/localhost/tasks/" + escaped + "/log?start=0&limit=5000"
			if got := wire.seen(); len(got) != 1 || got[0] != want {
				t.Errorf("PBS was asked for %q, want [%q]", got, want)
			}
		})
	}

	// Targets spelled out rather than derived from url.PathEscape, so the
	// cases above are not checking the encoder against itself. The backslash
	// has to leave as "%5C", neither dropped nor escaped a second time — but
	// that case alone cannot tell whether the client escapes at all, because
	// net/url escapes a raw backslash on its own and then re-escapes the whole
	// path. The token case can: "!" is legal in a path, so only url.PathEscape
	// turns it into "%21".
	t.Run("the token user's ! goes out as %21", func(t *testing.T) {
		client, wire := newPBSUPIDWireServer(t)
		if _, err := client.GetTaskStatus(context.Background(), realPBSTaskUPIDs["api token user"]); err != nil {
			t.Fatalf("GetTaskStatus: %v", err)
		}
		const want = "/api2/json/nodes/localhost/tasks/" +
			"UPID:pbs-01:0000ABD0:00012348:00000020:66F00004:garbage_collection:datastore01:root@pam%21token01:/status"
		if got := wire.seen(); len(got) != 1 || got[0] != want {
			t.Errorf("PBS was asked for %q, want [%q]", got, want)
		}
	})

	t.Run("the backslash goes out as %5C", func(t *testing.T) {
		client, wire := newPBSUPIDWireServer(t)
		if _, err := client.GetTaskStatus(context.Background(), realPBSTaskUPIDs["backup"]); err != nil {
			t.Fatalf("GetTaskStatus: %v", err)
		}
		const want = "/api2/json/nodes/localhost/tasks/" +
			"UPID:pbs-01:0000ABCD:00012345:0000001D:66F00001:backup:datastore01%5Cx3avm-100:root@pam:/status"
		if got := wire.seen(); len(got) != 1 || got[0] != want {
			t.Errorf("PBS was asked for %q, want [%q]", got, want)
		}
	})
}

// TestPBSClientTaskReadsRefuseAPathThatIsNotOneSegment drives the guard through
// both protected functions.
//
// Every value here is what the handler would hand over once it has decoded the
// wire form. The ones that begin with a letter are reachable through the route
// from a caller holding only view:backup; the rest are refused by the route's
// pattern first and are here to pin the client guard on its own, since it is
// the choke point for any caller. The refusal must carry ErrInvalidInput,
// because that is what mapProxmoxError
// turns into a 400 — an unclassified error would surface as a 500 and read as a
// PBS outage rather than as the caller's own input.
func TestPBSClientTaskReadsRefuseAPathThatIsNotOneSegment(t *testing.T) {
	values := map[string]string{
		"empty":                  "",
		"bare traversal":         "..",
		"bare dot":               ".",
		"traversal with a slash": "A/../../status",
		"a single separator":     "A/b",
		// A backslash itself is allowed — most real PBS UPIDs carry one — but
		// not a dot piece between two, which a proxy that reads a backslash
		// as a separator would resolve upward. escape_id never writes one.
		"a dot piece between backslashes": `A\..\status`,
		// The accepted cost of that rule, pinned so it stays a decision: a PBS
		// user name may legally contain "\..\" (PBS writes the user field
		// raw), and such a user's tasks cannot be read through Nexara. See the
		// guard's doc comment for why exempting the user field would remove
		// the defence rather than narrow it.
		"a PBS user whose name has a dot piece": `UPID:pbs-01:0000ABCD:00012345:00000000:66F00000:garbage_collection:datastore01:a\..\b@pbs:`,
		"a newline":                             "A\nb",
		"a NUL":                                 "A\x00b",
		"an ANSI escape":                        "A\x1b[2Kb",
		"a leading absolute slash":              "/nodes/localhost/status",
	}

	for name, value := range values {
		t.Run(name+"/status", func(t *testing.T) {
			client, wire := newPBSUPIDWireServer(t)
			_, err := client.GetTaskStatus(context.Background(), value)
			assertPBSUPIDRefused(t, err, wire)
		})
		t.Run(name+"/log", func(t *testing.T) {
			client, wire := newPBSUPIDWireServer(t)
			_, err := client.GetTaskLog(context.Background(), value)
			assertPBSUPIDRefused(t, err, wire)
		})
	}

	// The control, without which a broken stand-in server would refuse every
	// case above for a reason that has nothing to do with the guard.
	t.Run("control: a real UPID is still sent", func(t *testing.T) {
		client, wire := newPBSUPIDWireServer(t)
		if _, err := client.GetTaskStatus(context.Background(), realPBSTaskUPIDs["backup"]); err != nil {
			t.Fatalf("GetTaskStatus: %v", err)
		}
		if len(wire.seen()) != 1 {
			t.Errorf("the control request made %d PBS calls, want 1", len(wire.seen()))
		}
	})
}

func assertPBSUPIDRefused(t *testing.T, err error, wire *pbsUPIDWire) {
	t.Helper()
	if err == nil {
		t.Fatal("the call succeeded; the value must be refused before it is sent")
	}
	if !errors.Is(err, ErrInvalidInput) {
		t.Errorf("err = %v, want one wrapping ErrInvalidInput — anything else maps to a 500", err)
	}
	if sent := wire.seen(); len(sent) != 0 {
		t.Errorf("the request reached PBS as %q; it must never leave the client", sent)
	}
}
