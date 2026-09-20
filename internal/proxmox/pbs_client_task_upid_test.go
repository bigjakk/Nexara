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
// what makes a traversal expressible at all: "A%2F..%2F..%2Fstatus" arrives at
// the handler as text, and leaves it as the real string "A/../../status".
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

// TestPBSClientTaskReadsSendTheUPIDTheCallerGave pins what goes out for a real
// UPID: url.PathEscape's output, which leaves the colons and the "@" alone
// because both are legal in a path segment. That is what PBS's own UI sends.
func TestPBSClientTaskReadsSendTheUPIDTheCallerGave(t *testing.T) {
	const upid = "UPID:pbs-01:0000ABCD:00012345:00000000:66F00000:garbage_collection:datastore01:root@pam:"
	escaped := url.PathEscape(upid)

	t.Run("status", func(t *testing.T) {
		client, wire := newPBSUPIDWireServer(t)
		if _, err := client.GetTaskStatus(context.Background(), upid); err != nil {
			t.Fatalf("GetTaskStatus: %v", err)
		}
		want := "/api2/json/nodes/localhost/tasks/" + escaped + "/status"
		if got := wire.seen(); len(got) != 1 || got[0] != want {
			t.Errorf("PBS was asked for %q, want [%q]", got, want)
		}
	})

	t.Run("log", func(t *testing.T) {
		client, wire := newPBSUPIDWireServer(t)
		if _, err := client.GetTaskLog(context.Background(), upid); err != nil {
			t.Fatalf("GetTaskLog: %v", err)
		}
		want := "/api2/json/nodes/localhost/tasks/" + escaped + "/log?start=0&limit=5000"
		if got := wire.seen(); len(got) != 1 || got[0] != want {
			t.Errorf("PBS was asked for %q, want [%q]", got, want)
		}
	})
}

// TestPBSClientTaskReadsRefuseAPathThatIsNotOneSegment drives the guard through
// both protected functions.
//
// Every value here is what the handler now hands over once it has decoded the
// wire form, so each one is reachable from a caller holding only view:backup.
// The refusal must carry ErrInvalidInput, because that is what mapProxmoxError
// turns into a 400 — an unclassified error would surface as a 500 and read as a
// PBS outage rather than as the caller's own input.
func TestPBSClientTaskReadsRefuseAPathThatIsNotOneSegment(t *testing.T) {
	values := map[string]string{
		"empty":                    "",
		"bare traversal":           "..",
		"bare dot":                 ".",
		"traversal with a slash":   "A/../../status",
		"a single separator":       "A/b",
		"a backslash":              `A\..\status`,
		"a newline":                "A\nb",
		"a NUL":                    "A\x00b",
		"an ANSI escape":           "A\x1b[2Kb",
		"a leading absolute slash": "/nodes/localhost/status",
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
		if _, err := client.GetTaskStatus(context.Background(),
			"UPID:pbs-01:0000ABCD:00012345:00000000:66F00000:verify:datastore01:root@pam:"); err != nil {
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
