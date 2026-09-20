package proxmox

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"testing"
)

// The fixture endpoint. Synthetic throughout: the token id is the house
// fixture, the secret is a made-up UUID, and the fingerprint is synthetic hex.
const (
	fixtureEndpointHost   = "pve-target.example.com"
	fixtureEndpointTokID  = "nexara@pve!api"
	fixtureEndpointSecret = "11111111-2222-3333-4444-555555555555"
	fixtureEndpointToken  = fixtureEndpointTokID + "=" + fixtureEndpointSecret
	fixtureEndpointFP     = "AA:BB:CC:DD:EE:FF:00:11:22:33:44:55:66:77:88:99"

	// The exact bytes Proxmox must receive. Written out by hand rather than
	// built from PropertyString, so the assertion cannot agree with a broken
	// implementation.
	fixtureEndpointProperty = "apitoken=PVEAPIToken=nexara@pve!api=11111111-2222-3333-4444-555555555555," +
		"host=pve-target.example.com,fingerprint=AA:BB:CC:DD:EE:FF:00:11:22:33:44:55:66:77:88:99"
)

func fixtureEndpoint() TargetEndpoint {
	return TargetEndpoint{
		Host:        fixtureEndpointHost,
		APIToken:    fixtureEndpointToken,
		Fingerprint: fixtureEndpointFP,
	}
}

// Guard C-1. TargetEndpoint.APIToken is the target cluster's live, long-lived
// credential, and every rendering route below is one a person reaches by
// reflex while debugging: a %v in an error, a slog.Any in a log line, a
// json.Marshal of the whole params struct. Each is checked separately because
// each is served by a different method — String, GoString, LogValue and
// MarshalJSON — and removing any one of them must fail this test on its own
// shape. GoString is the easy one to forget: fmt dispatches Stringer for
// %v/%s/%q/%x/%X but GoStringer for %#v.
//
// Every case also asserts the host SURVIVES. Without that half, a String()
// that returned "" — or a MarshalJSON that emitted {} — would satisfy an
// absence-only assertion while destroying the diagnostics the redaction is
// supposed to preserve.
func TestGuard_TargetEndpointNeverPrintsItsToken(t *testing.T) {
	e := fixtureEndpoint()

	vmParams := RemoteMigrateVMParams{
		TargetEndpoint: e,
		TargetBridge:   "vmbr0:vmbr0",
		TargetVMID:     999,
		Online:         true,
	}
	ctParams := RemoteMigrateCTParams{
		TargetEndpoint: e,
		TargetBridge:   "vmbr0:vmbr0",
		TargetVMID:     999,
		Restart:        true,
	}

	mustMarshal := func(v any) string {
		t.Helper()
		raw, err := json.Marshal(v)
		if err != nil {
			t.Fatalf("json.Marshal: %v", err)
		}
		return string(raw)
	}

	cases := []struct {
		name string
		// rendered is the text the route produces.
		rendered string
		// survives is the substring proving the rendering still says something.
		survives string
	}{
		{"fmt %v", fmt.Sprintf("%v", e), fixtureEndpointHost},
		//nolint:staticcheck // S1025: the %s verb dispatching to String is exactly what is under test
		{"fmt %s", fmt.Sprintf("%s", e), fixtureEndpointHost},
		{"fmt.Sprint", fmt.Sprint(e), fixtureEndpointHost},
		{"String", e.String(), fixtureEndpointHost},
		// %#v dispatches GoStringer, not Stringer, so String does not cover
		// it. All three shapes leaked the raw token before GoString existed.
		{"fmt %#v", fmt.Sprintf("%#v", e), fixtureEndpointHost},
		{"fmt %#v pointer", fmt.Sprintf("%#v", &e), fixtureEndpointHost},
		{"fmt %#v RemoteMigrateVMParams", fmt.Sprintf("%#v", vmParams), fixtureEndpointHost},
		{"fmt %+v RemoteMigrateCTParams", fmt.Sprintf("%+v", ctParams), fixtureEndpointHost},
		// An endpoint that lands inside an error is the route that reaches a
		// persisted, Viewer-readable column.
		{"error wrapping", fmt.Errorf("remote migrate: %v", e).Error(), fixtureEndpointHost},
		{"json.Marshal endpoint", mustMarshal(e), `"host":"` + fixtureEndpointHost + `"`},
		{"json.Marshal RemoteMigrateVMParams", mustMarshal(vmParams), `"host":"` + fixtureEndpointHost + `"`},
		{"json.Marshal RemoteMigrateCTParams", mustMarshal(ctParams), `"host":"` + fixtureEndpointHost + `"`},
		// The grouped key is what proves LogValue ran: without it slog would
		// fall through to String and log endpoint=TargetEndpoint{...}.
		{"slog.Any", logLine(t, e), "endpoint.host=" + fixtureEndpointHost},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if strings.Contains(tc.rendered, fixtureEndpointSecret) {
				t.Errorf("the token secret is in the rendering: %s", tc.rendered)
			}
			if strings.Contains(tc.rendered, fixtureEndpointToken) {
				t.Errorf("the full apitoken is in the rendering: %s", tc.rendered)
			}
			if !strings.Contains(tc.rendered, tc.survives) {
				t.Errorf("the rendering dropped %q and is no longer worth emitting: %s",
					tc.survives, tc.rendered)
			}
		})
	}
}

// logLine renders the endpoint through a real slog pipeline and returns the
// emitted line.
func logLine(t *testing.T, e TargetEndpoint) string {
	t.Helper()
	var buf bytes.Buffer
	slog.New(slog.NewTextHandler(&buf, nil)).Info("cross-cluster migration", slog.Any("endpoint", e))
	return buf.String()
}

// PropertyString is the one route that must still carry the credential: it is
// what the Proxmox API is handed. Pinned so "redact everything" cannot quietly
// take it too.
func TestTargetEndpointPropertyStringCarriesTheToken(t *testing.T) {
	if got := fixtureEndpoint().PropertyString(); got != fixtureEndpointProperty {
		t.Errorf("PropertyString() = %q, want %q", got, fixtureEndpointProperty)
	}

	// Without a fingerprint the field is omitted rather than left empty —
	// Proxmox treats an empty fingerprint= as a rejected parameter.
	noFP := TargetEndpoint{Host: fixtureEndpointHost, APIToken: fixtureEndpointToken}
	want := "apitoken=PVEAPIToken=" + fixtureEndpointToken + ",host=" + fixtureEndpointHost
	if got := noFP.PropertyString(); got != want {
		t.Errorf("PropertyString() without fingerprint = %q, want %q", got, want)
	}
}

// Guard C-2. The mirror of C-1: now that String() is redacted, writing
// params.TargetEndpoint.String() at the call site still compiles, still reads
// as correct, and would send "apitoken:REDACTED" to Proxmox — breaking
// cross-cluster migration with nothing else in the suite failing.
//
// The assertion is on the EXACT form value, not on "it contains the token":
// a substring check would survive a PropertyString that also appended the
// redacted rendering.
func TestRemoteMigrate_SendsTheTokenInTheTargetEndpointForm(t *testing.T) {
	const (
		node   = "pve-01"
		vmid   = 100
		ctid   = 200
		vmPath = "/api2/json/nodes/pve-01/qemu/100/remote_migrate"
		ctPath = "/api2/json/nodes/pve-01/lxc/200/remote_migrate"
	)

	for _, tc := range []struct {
		name string
		path string
		call func(c *Client) (string, error)
	}{
		{
			name: "VM",
			path: vmPath,
			call: func(c *Client) (string, error) {
				return c.RemoteMigrateVM(context.Background(), node, vmid, RemoteMigrateVMParams{
					TargetEndpoint: fixtureEndpoint(),
					TargetBridge:   "vmbr0:vmbr0",
				})
			},
		},
		{
			name: "CT",
			path: ctPath,
			call: func(c *Client) (string, error) {
				return c.RemoteMigrateCT(context.Background(), node, ctid, RemoteMigrateCTParams{
					TargetEndpoint: fixtureEndpoint(),
					TargetBridge:   "vmbr0:vmbr0",
				})
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Both of these are diagnostics, not extra coverage: the
			// gotEndpoint comparison below already fails on "", so a request
			// that never arrived is caught either way. What they add is
			// saying WHY — the catch-all names the path the request actually
			// went to, and called distinguishes "handler never ran" from
			// "handler ran and read the wrong form field".
			var called bool
			var gotEndpoint string

			srv := newTestServer(t, map[string]http.HandlerFunc{
				"/": func(w http.ResponseWriter, r *http.Request) {
					t.Errorf("request went to %s, not %s — the client path changed "+
						"and this test is no longer looking at the right request",
						r.URL.Path, tc.path)
					w.WriteHeader(http.StatusNotFound)
				},
				tc.path: func(w http.ResponseWriter, r *http.Request) {
					called = true
					if err := r.ParseForm(); err != nil {
						t.Errorf("ParseForm: %v", err)
					}
					gotEndpoint = r.PostFormValue("target-endpoint")
					jsonResponse(w, "UPID:"+node+":00001234:00000000:68000000:qmigrate:100:root@pam:")
				},
			})
			defer srv.Close()

			if _, err := tc.call(newTestClient(t, srv.URL)); err != nil {
				t.Fatalf("remote migrate: %v", err)
			}
			if !called {
				t.Fatalf("the %s handler never ran — the request went somewhere else, "+
					"so this test proves nothing", tc.path)
			}
			if gotEndpoint != fixtureEndpointProperty {
				t.Errorf("target-endpoint = %q, want %q", gotEndpoint, fixtureEndpointProperty)
			}
		})
	}
}
