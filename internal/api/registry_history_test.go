package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"

	"github.com/bigjakk/nexara/internal/api/apischema"
	"github.com/bigjakk/nexara/internal/api/handlers"
)

const historyRouteCount = 3

// historyRoutesOutsideTheClusterCheckShape are the History routes gated on
// AllOf view:audit and the kind's own view permission (historyRoute.Resource).
var historyRoutesOutsideTheClusterCheckShape = map[string]string{
	"GET /api/v1/clusters/:cluster_id/vm-folders/:folder_id/history": "AllOf: view:audit for the rows, " +
		"view:vm_folder for the folder that selects them",
	"GET /api/v1/clusters/:cluster_id/history": "AllOf: view:audit for the rows, view:cluster for the cluster " +
		"they are all filed under",
}

// The guest is the one kind gated on view:audit alone, and the one whose
// guest is not filtered on view:vm/view:container (GuestFromPath); any other
// kind on either list skips a permission its rows need. Leaving an object's
// rows out of a guest's cutoff (ObjectsIgnoreGuestCutoff) is pinned the same
// way: no kind does yet.
func TestHistoryAuditOnlyIsTheGuestAlone(t *testing.T) {
	t.Parallel()
	var auditOnly, fromPath, ignoresCutoff []string
	for _, r := range historyRoutes() {
		if r.AuditOnly != "" {
			auditOnly = append(auditOnly, r.Kind.Name)
		}
		if r.Kind.GuestFromPath != "" {
			fromPath = append(fromPath, r.Kind.Name)
		}
		if r.Kind.ObjectsIgnoreGuestCutoff {
			ignoresCutoff = append(ignoresCutoff, r.Kind.Name)
		}
	}
	if !slices.Equal(auditOnly, []string{"guest"}) || !slices.Equal(fromPath, []string{"guest"}) || len(ignoresCutoff) != 0 {
		t.Errorf("gated on view:audit alone: %v; guests unfiltered: %v; objects free of the cutoff: %v; "+
			"want [guest], [guest], []", auditOnly, fromPath, ignoresCutoff)
	}
}

// A start_time that does not parse, or a category left out that the kind
// does not have, is the caller's 400 before any query runs: this handler has
// no database, so reading one would panic.
func TestHistoryRefusesABadStartTimeFirst(t *testing.T) {
	t.Parallel()
	app := newRegistryApp(t, stubAuth(map[string]bool{"view:audit": true}),
		historyEndpoint(&handlers.HistoryHandler{}, guestHistoryRoute()))
	for _, query := range []string{"start_time=yesterday", "exclude_category=power,nope"} {
		if status, env := send(t, app, authedRequest(http.MethodGet,
			"/api/v1/clusters/"+testClusterID+"/guests/100/history?"+query)); status != fiber.StatusBadRequest {
			t.Errorf("%s: status = %d (%q), want 400", query, status, env.Message)
		}
	}
}

// A global kind reads every cluster's rows, so each permission its gate holds
// is GLOBAL — a grant on one cluster must not open it — and every other
// kind's is the path's cluster's. The probe is a global kind, which no route
// is yet.
func TestHistoryGateScope(t *testing.T) {
	t.Parallel()
	probe := historyRoute{
		Kind: handlers.HistoryKind{Name: "probe", Categories: []string{"other"}, Default: "other", Global: true,
			Select: handlers.SelectObjects("id", handlers.HistoryObject{ResourceType: "pbs_server"})},
		Path: pathPrefix + "probes/:id/history", Resource: "pbs",
		Parameters: apischema.Properties{"id": {Type: apischema.String}},
	}
	for _, r := range append(historyRoutes(), probe) {
		want := ScopeCluster
		if r.Kind.Global {
			want = ScopeGlobal
		}
		perms := historyEndpoint(&handlers.HistoryHandler{}, r).Permissions
		checks := perms.AllOf
		if perms.Check != nil {
			checks = append(checks, *perms.Check)
		}
		for _, c := range checks {
			if c.Scope != want {
				t.Errorf("%s: %s is %s-scoped, want %s", r.Path, c, c.Scope, want)
			}
		}
	}
}

// A route's gate is decided at registration: neither or both of Resource and
// AuditOnly is refused, so no kind can fall back to view:audit alone; so is a
// global kind on view:audit alone, or at a cluster's path, where its gate
// would be one cluster's; and a guest-from-path route that does not declare
// its guest, whose every request would fail on reading it.
func TestHistoryEndpointRefusesAnUndecidedGate(t *testing.T) {
	t.Parallel()
	global := handlers.HistoryKind{Name: "probe", Categories: []string{"other"}, Default: "other", Global: true,
		Select: handlers.SelectObjects("vmid", handlers.HistoryObject{ResourceType: "pbs_server"})}
	for name, r := range map[string]historyRoute{
		"neither":                 {Kind: handlers.GuestHistory()},
		"both":                    {Kind: handlers.GuestHistory(), Resource: "node", AuditOnly: "because"},
		"global on audit alone":   {Kind: global, Path: pathPrefix + "probes/:vmid/history", AuditOnly: "because"},
		"global at a cluster's":   {Kind: global, Resource: "pbs"},
		"a cluster's kind above":  {Kind: handlers.GuestHistory(), Path: pathPrefix + "probes/:vmid/history", Resource: "vm"},
		"a path guest undeclared": {Kind: handlers.GuestHistory(), AuditOnly: "because"},
	} {
		if r.Path == "" {
			r.Path = clusterScope + "/probe/:vmid/history"
		}
		r.Parameters = apischema.Properties{"cluster_id": apischema.StdOption("cluster-id")}
		func() {
			defer func() {
				if msg, _ := recover().(string); !strings.Contains(msg, "history route "+r.Path) {
					t.Errorf("%s: historyEndpoint = %q, want it refused", name, msg)
				}
			}()
			historyEndpoint(&handlers.HistoryHandler{}, r)
		}()
	}
}

// TestHistoryCategoryVocabulary holds each kind's type filter to its labels:
// the categories the route declares (which HistoryKind.Validate holds to the
// rules that produce them) against the keys of the kind's frontend namespace,
// in order. A category with no label shows its raw name; a label with no
// category is a filter option the route refuses with a 400.
func TestHistoryCategoryVocabulary(t *testing.T) {
	t.Parallel()
	for _, r := range historyRoutes() {
		path := "../../frontend/src/locales/en/history-" + r.Kind.Name + ".json"
		labels, err := jsonObjectKeys(path, "categories")
		if err != nil {
			t.Errorf("%s: %v — every kind keeps its strings in its own namespace, history-<Name>", r.Path, err)
			continue
		}
		declared := declaredEndpoint(t, fiber.MethodGet, r.Path).Parameters["category"].Enum
		if want := append([]string{""}, labels...); !slices.Equal(declared, want) {
			t.Errorf("%s: the ?category= enum is %v but %s labels %v", r.Path, declared, path, labels)
		}
	}
}

// jsonObjectKeys returns the keys of the object at the top-level key field of
// the JSON file at path, in the order the file lists them.
func jsonObjectKeys(path, field string) ([]string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(raw, &top); err != nil {
		return nil, err
	}
	obj, ok := top[field]
	if !ok {
		return nil, fmt.Errorf("%s has no %q object", path, field)
	}
	dec := json.NewDecoder(bytes.NewReader(obj))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return nil, fmt.Errorf("%s: %q is not an object", path, field)
	}
	var keys []string
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		keys = append(keys, tok.(string))
		var skip json.RawMessage
		if err := dec.Decode(&skip); err != nil {
			return nil, err
		}
	}
	return keys, nil
}
