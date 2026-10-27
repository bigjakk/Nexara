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

const historyRouteCount = 1

// The guest is the one kind gated on view:audit alone, and the one whose
// guest is not filtered on view:vm/view:container (GuestFromPath); any other
// kind on either list skips a permission its rows need.
func TestHistoryAuditOnlyIsTheGuestAlone(t *testing.T) {
	t.Parallel()
	var auditOnly, fromPath []string
	for _, r := range historyRoutes() {
		if r.AuditOnly != "" {
			auditOnly = append(auditOnly, r.Kind.Name)
		}
		if r.Kind.GuestFromPath {
			fromPath = append(fromPath, r.Kind.Name)
		}
	}
	if !slices.Equal(auditOnly, []string{"guest"}) || !slices.Equal(fromPath, []string{"guest"}) {
		t.Errorf("gated on view:audit alone: %v; guests unfiltered: %v; want [guest] for both", auditOnly, fromPath)
	}
}

// A start_time that does not parse is the caller's 400 before any query
// runs: this handler has no database, so reading one would panic.
func TestHistoryRefusesABadStartTimeFirst(t *testing.T) {
	t.Parallel()
	app := newRegistryApp(t, stubAuth(map[string]bool{"view:audit": true}),
		historyEndpoint(&handlers.HistoryHandler{}, guestHistoryRoute()))
	if status, env := send(t, app, authedRequest(http.MethodGet,
		"/api/v1/clusters/"+testClusterID+"/guests/100/history?start_time=yesterday")); status != fiber.StatusBadRequest {
		t.Errorf("status = %d (%q), want 400", status, env.Message)
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
// would be one cluster's; and a kind that reads a type's observed changes
// under any view but that type's own.
func TestHistoryEndpointRefusesAnUndecidedGate(t *testing.T) {
	t.Parallel()
	global := handlers.HistoryKind{Name: "probe", Categories: []string{"other"}, Default: "other", Global: true,
		Select: handlers.SelectObjects("vmid", handlers.HistoryObject{ResourceType: "pbs_server"})}
	for name, r := range map[string]historyRoute{
		"neither":                {Kind: handlers.GuestHistory()},
		"both":                   {Kind: handlers.GuestHistory(), Resource: "node", AuditOnly: "because"},
		"global on audit alone":  {Kind: global, Path: pathPrefix + "probes/:vmid/history", AuditOnly: "because"},
		"global at a cluster's":  {Kind: global, Resource: "pbs"},
		"a cluster's kind above": {Kind: handlers.GuestHistory(), Path: pathPrefix + "probes/:vmid/history", Resource: "vm"},
		"an observed type under another view": {Kind: handlers.HistoryKind{Name: "probe", Categories: []string{"other"},
			Default: "other", Select: handlers.SelectObjects("vmid"), ObjectTypes: []string{"replication"}}, Resource: "node"},
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
