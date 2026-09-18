package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
)

// guestSnapshotRouteCount is how many endpoints
// registerGuestSnapshotEndpoints declares.
const guestSnapshotRouteCount = 2

// guestSnapshotRoutesOutsideTheClusterCheckShape records why neither route is a
// plain cluster-scoped Check. It is folded into
// routesOutsideTheClusterCheckShape in registry_vms_test.go.
var guestSnapshotRoutesOutsideTheClusterCheckShape = map[string]string{
	"GET /api/v1/guest-snapshots": "Advisory: the listing spans every cluster, so there is none for a gate " +
		"to resolve, and the two guest kinds are separate grants applied per row from the snapshot's own guest_type",
	"POST /api/v1/clusters/:cluster_id/guest-snapshots/resync": "Alternatives: the coarse gate passes on " +
		"EITHER view:vm or view:container, which is what the handler's two hasClusterPerm reads amounted to; " +
		"the precise per-class check needs the guest row and stays in the handler",
}

func declaredGuestSnapshotEndpoints(t *testing.T) map[string]Endpoint {
	t.Helper()
	s := newRouteStubServer(t)
	out := map[string]Endpoint{}
	for _, e := range s.registry.Endpoints() {
		key := e.Method + " " + e.Path
		if _, listed := guestSnapshotRoutesOutsideTheClusterCheckShape[key]; listed {
			out[key] = e
		}
	}
	return out
}

// TestGuestSnapshotRoutesDeclareWhatTheyEnforced is this domain's tally.
//
// Neither handler held a require*Perm call, which is why neither is a Check.
// From `git show HEAD:internal/api/handlers/guest_snapshots.go` at commit
// eaeafa7:
//
//	List    two accessibleClusters reads ("view","vm" and "view","container"),
//	        no gate at all — filterGuestSnapshotRows drops what the caller may
//	        not see.
//	Resync  two hasClusterPerm reads on the same pair, refusing only when
//	        NEITHER is held, then a finer per-class refusal once the guest row
//	        names its type.
//
// The declarations say exactly that: Advisory for the first, Alternatives for
// the coarse half of the second.
func TestGuestSnapshotRoutesDeclareWhatTheyEnforced(t *testing.T) {
	declared := declaredGuestSnapshotEndpoints(t)
	if len(declared) != guestSnapshotRouteCount {
		t.Fatalf("the registry declares %d guest-snapshot routes, want %d", len(declared), guestSnapshotRouteCount)
	}

	list := declared["GET /api/v1/guest-snapshots"]
	if list.Permissions.Advisory == nil {
		t.Fatalf("the listing declares %q, want Advisory — nothing gates it", list.Permissions.Describe())
	}
	if got := list.Permissions.Advisory.String(); got != "view:vm" {
		t.Errorf("the advisory permission is %q, want view:vm — the first of the two pairs the handler filters on", got)
	}
	for _, phrase := range []string{"accessibleClusters", "guest_type", "container"} {
		if !strings.Contains(list.Permissions.Advisory.Reason, phrase) {
			t.Errorf("the Advisory reason does not mention %q, so it does not say what does the filtering: %q",
				phrase, list.Permissions.Advisory.Reason)
		}
	}

	resync := declared["POST /api/v1/clusters/:cluster_id/guest-snapshots/resync"]
	if len(resync.Permissions.Alternatives) != 2 {
		t.Fatalf("the resync declares %q, want two Alternatives", resync.Permissions.Describe())
	}
	if got := normalizePermissionList(resync.Permissions.Describe()); got != "view:container|view:vm" {
		t.Errorf("the resync declares %q, want view:vm and view:container", got)
	}
	for _, alt := range resync.Permissions.Alternatives {
		if alt.Scope != ScopeCluster {
			t.Errorf("the resync's %s alternative is %s-scoped; the coarse gate resolved the cluster from the path",
				alt.String(), alt.Scope)
		}
	}
}

// TestGuestSnapshotResyncGateIsEitherGuestPermission proves the Alternatives
// declaration end to end: EITHER grant opens the route, and neither is refused.
//
// This is the assertion that would have caught the tempting simplification —
// declaring a single view:vm Check — which would have locked every
// container-only operator out of a route they could use before.
func TestGuestSnapshotResyncGateIsEitherGuestPermission(t *testing.T) {
	const path = clusterScope + "/guest-snapshots/resync"
	e := declaredEndpoint(t, fiber.MethodPost, path)
	target := strings.ReplaceAll(path, ":cluster_id", testClusterID)

	for _, tt := range []struct {
		name   string
		grants map[string]bool
		want   int
	}{
		{"view:vm alone", map[string]bool{"view:vm": true}, fiber.StatusNoContent},
		{"view:container alone", map[string]bool{"view:container": true}, fiber.StatusNoContent},
		{"both", map[string]bool{"view:vm": true, "view:container": true}, fiber.StatusNoContent},
		{"neither", map[string]bool{"view:node": true}, fiber.StatusForbidden},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cap := &capture{}
			gated := e
			gated.Handler = cap.handler()
			app := newRegistryApp(t, stubAuth(tt.grants), gated)

			req := jsonRequest(http.MethodPost, target, `{"vmid":101}`)
			req.Header.Set("X-Test-User", "yes")
			status, env := send(t, app, req)
			if status != tt.want {
				t.Fatalf("status = %d (%q), want %d", status, env.Message, tt.want)
			}
			if cap.called != (tt.want == fiber.StatusNoContent) {
				t.Errorf("handler called = %v, want %v", cap.called, tt.want == fiber.StatusNoContent)
			}
		})
	}
}

// TestGuestSnapshotFilterIsNotNamedClusterID is the escalation guard this
// route's declaration had to route around, recorded so the workaround is not
// mistaken for an arbitrary name.
//
// checkPathParams refuses a parameter NAMED cluster_id that resolves to
// anything but the path, because clusterIDFromParam reads that name to decide
// which cluster a gate authorizes. The filter here is genuinely a query
// parameter, so it is declared under another name with "cluster_id" as an
// alias — which is the spelling the snapshots page sends.
func TestGuestSnapshotFilterIsNotNamedClusterID(t *testing.T) {
	e := declaredEndpoint(t, fiber.MethodGet, guestSnapshotScope)
	if _, declared := e.Parameters["cluster_id"]; declared {
		t.Fatal("declares a parameter NAMED cluster_id; the permission middleware reads that name, so " +
			"Register would have refused it")
	}
	prop, ok := e.Parameters["filter_cluster_id"]
	if !ok {
		t.Fatal("declares no filter_cluster_id")
	}
	if prop.Alias != "cluster_id" {
		t.Errorf("filter_cluster_id declares alias %q, want cluster_id — that is the spelling the page sends", prop.Alias)
	}
	if prop.Format != "uuid" {
		t.Errorf("filter_cluster_id declares format %q, want uuid", prop.Format)
	}

	for _, tt := range []struct {
		query    string
		want     int
		supplied bool
	}{
		{query: "", want: fiber.StatusNoContent, supplied: false},
		{query: "?cluster_id=" + testClusterID, want: fiber.StatusNoContent, supplied: true},
		{query: "?filter_cluster_id=" + testClusterID, want: fiber.StatusNoContent, supplied: true},
		{query: "?cluster_id=not-a-uuid", want: fiber.StatusBadRequest},
		{query: "?cluster=" + testClusterID, want: fiber.StatusBadRequest},
	} {
		cap := &capture{}
		probe := e
		probe.Handler = cap.handler()
		probe.Permissions = Permissions{SelfService: "parameter fixture; authorization is exercised separately"}
		app := newRegistryApp(t, noAuth(), probe)

		status, env := send(t, app, httptest.NewRequest(http.MethodGet, guestSnapshotScope+tt.query, nil))
		if status != tt.want {
			t.Errorf("%q: status = %d (%q), want %d", tt.query, status, env.Message, tt.want)
			continue
		}
		if tt.want != fiber.StatusNoContent {
			continue
		}
		if _, supplied := cap.params.OptString("filter_cluster_id"); supplied != tt.supplied {
			t.Errorf("%q: filter_cluster_id supplied = %v, want %v", tt.query, supplied, tt.supplied)
		}
	}
}

// TestEveryGuestSnapshotEndpointIsDocumented holds the declarations to the
// standard that makes this whole effort worth doing.
func TestEveryGuestSnapshotEndpointIsDocumented(t *testing.T) {
	for key, e := range declaredGuestSnapshotEndpoints(t) {
		if err := e.Parameters.Compile(); err != nil {
			t.Errorf("%s: %v", key, err)
		}
		if strings.TrimSpace(e.Description) == "" {
			t.Errorf("%s has no description", key)
		}
		if e.Group != "Guest Snapshots" {
			t.Errorf("%s is in group %q, want Guest Snapshots", key, e.Group)
		}
		for name, prop := range e.Parameters {
			if strings.TrimSpace(prop.Description) == "" {
				t.Errorf("%s: parameter %q has no description", key, name)
			}
		}
	}
}
