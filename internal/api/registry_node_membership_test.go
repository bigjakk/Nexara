package api

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/gofiber/fiber/v3"
	fiberrecover "github.com/gofiber/fiber/v3/middleware/recover"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/bigjakk/nexara/internal/api/apischema"
	"github.com/bigjakk/nexara/internal/api/handlers"
	db "github.com/bigjakk/nexara/internal/db/generated"
)

// These tests hold the registry to refusing, with 404 and before any handler runs, a
// Proxmox node the cluster's nodes table does not hold, on every route that names a
// node in its URL (Endpoint.urlNodeParams, checked in serve) and on the two task
// routes whose node sits inside the UPID (VMHandler.taskUPID). See
// handlers.RequireNodesInCluster: pveproxy resolves and dials a name that is not a
// cluster member, and the error it gets back reaches the caller.

// The number of routes that name a Proxmox node in their URL, by where they carry it.
// Pinned rather than derived, so that a declaration that stops being recognised moves
// a number somebody has to update deliberately.
const (
	nodeNamePathRouteCount  = 62
	nodeNameQueryRouteCount = 6
)

// memberNode is the one node the membership fakes below hold.
const memberNode = "pve-01"

// nodeMembershipFake is a handlers.NodeLookup holding exactly the nodes in members,
// recording every question in order. err, when set, answers every question instead.
type nodeMembershipFake struct {
	mu      sync.Mutex
	members map[uuid.UUID][]string
	err     error
	asked   []db.GetNodeByClusterAndNameParams
}

func (f *nodeMembershipFake) GetNodeByClusterAndName(_ context.Context, arg db.GetNodeByClusterAndNameParams) (db.Node, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.asked = append(f.asked, arg)
	if f.err != nil {
		return db.Node{}, f.err
	}
	if slices.Contains(f.members[arg.ClusterID], arg.Name) {
		return db.Node{ClusterID: arg.ClusterID, Name: arg.Name}, nil
	}
	return db.Node{}, pgx.ErrNoRows
}

func (f *nodeMembershipFake) questions() []db.GetNodeByClusterAndNameParams {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.asked)
}

// holdingMember is a fake that knows memberNode in testClusterID and nothing else.
func holdingMember() *nodeMembershipFake {
	return &nodeMembershipFake{members: map[uuid.UUID][]string{uuid.MustParse(testClusterID): {memberNode}}}
}

// followsNodesSegment reports whether :name is the path segment right after a literal
// "nodes" — the shape a node name has in a URL whatever it is called.
func followsNodesSegment(path, name string) bool {
	segments := strings.Split(path, "/")
	for i := 1; i < len(segments); i++ {
		if segments[i] == ":"+name && strings.EqualFold(segments[i-1], "nodes") {
			return true
		}
	}
	return false
}

// boundHandlerName is the runtime name of an endpoint's Handler — for a bound method
// value, "…/handlers.(*VMHandler).ListNodeUSBMappings-fm".
func boundHandlerName(h Handler) string {
	return runtime.FuncForPC(reflect.ValueOf(h).Pointer()).Name()
}

// TestGuard_EveryRouteNamingANodeRefusesOneTheClusterDoesNotHold walks the route table and
// holds every route that names a Proxmox node in its URL to the check, from four sides: (1)
// which routes name a node is derived WITHOUT Endpoint.urlNodeParams, from how the URL spells
// its parameters, and compared with it route by route, both ways, so a node parameter the
// registry does not recognise fails here instead of forwarding whatever name it is given;
// (2) every such route runs a cluster-scoped permission gate ahead of serve, so its 404 never
// tells a refused caller which nodes exist; (3) each is driven with a request valid in every
// other respect (probeNodeMembership); (4) the handlers that dropped their own checks serve
// only routes that carry it.
func TestGuard_EveryRouteNamingANodeRefusesOneTheClusterDoesNotHold(t *testing.T) {
	s := sharedRouteStub(t)
	endpoints := s.registry.Endpoints()
	if len(endpoints) == 0 {
		t.Fatal("the server declared no registry endpoints, so this guard would check nothing")
	}

	// 1. The URL's own spelling, against the registry's classification.
	derived := map[string][]string{}
	for _, e := range endpoints {
		key := e.Method + " " + e.Path
		for _, name := range slices.Sorted(maps.Keys(e.Parameters)) {
			prop := e.Parameters[name]
			src := apischema.ResolveSource(name, prop, e.Method, e.pathParams)
			if src != apischema.SourcePath && src != apischema.SourceQuery {
				continue
			}
			lower := strings.ToLower(name)
			if strings.HasSuffix(lower, "node_id") {
				// A node ROW id is resolved in the database and never forwarded as a
				// name, provided it really is one: a uuid, or the uuid-or-empty rule a
				// filter uses for "every node".
				if prop.Format != "uuid" && prop.Pattern != emptyOrUUID {
					t.Errorf("%s: %s reads as a Nexara node row id but is declared neither Format uuid nor "+
						"emptyOrUUID, so nothing stops it carrying a node name to Proxmox unchecked", key, name)
				}
				continue
			}
			if strings.Contains(lower, "node") || (src == apischema.SourcePath && followsNodesSegment(e.Path, name)) {
				derived[key] = append(derived[key], name)
			}
		}
	}

	classified := map[string][]string{}
	var pathRoutes, queryRoutes int
	for _, e := range endpoints {
		names := e.urlNodeParams()
		if len(names) == 0 {
			continue
		}
		classified[e.Method+" "+e.Path] = names
		var inPath, inQuery bool
		for _, name := range names {
			switch apischema.ResolveSource(name, e.Parameters[name], e.Method, e.pathParams) {
			case apischema.SourcePath:
				inPath = true
			case apischema.SourceQuery:
				inQuery = true
			}
		}
		if inPath {
			pathRoutes++
		}
		if inQuery {
			queryRoutes++
		}
	}
	for _, key := range slices.Sorted(maps.Keys(derived)) {
		if got := classified[key]; !slices.Equal(got, derived[key]) {
			t.Errorf("%s names a node in its URL as %v, but the registry checks %v against the cluster's nodes — "+
				"declare a node parameter with the node-name format (apischema.StdOption(\"node-name\")) or "+
				"emptyOrNodeName, or it reaches Proxmox unchecked", key, derived[key], got)
		}
	}
	for _, key := range slices.Sorted(maps.Keys(classified)) {
		if _, ok := derived[key]; !ok {
			t.Errorf("%s: the registry checks %v as node names, but nothing in its URL is spelled like one; "+
				"name the parameter for what it is, so the next reader can tell", key, classified[key])
		}
	}
	if pathRoutes != nodeNamePathRouteCount {
		t.Errorf("%d routes name a node in their PATH, want nodeNamePathRouteCount = %d; if a route was added or "+
			"removed on purpose, update the constant", pathRoutes, nodeNamePathRouteCount)
	}
	if queryRoutes != nodeNameQueryRouteCount {
		t.Errorf("%d routes name a node in their QUERY, want nodeNameQueryRouteCount = %d; if a route was added or "+
			"removed on purpose, update the constant", queryRoutes, nodeNameQueryRouteCount)
	}

	// 2. A cluster-scoped permission gate ahead of serve, in the mounted chain.
	chains := mountedChains(s)
	for _, e := range endpoints {
		if len(e.urlNodeParams()) == 0 {
			continue
		}
		key := e.Method + " " + e.Path
		if !e.runsClusterGate() {
			t.Errorf("%s names a node in its URL but declares %q, which runs no cluster-scoped permission gate "+
				"ahead of serve — its 404 would tell a caller nothing had authorized which nodes the cluster has",
				key, e.Permissions.Describe())
		}
		chain := chains[e.Method+" "+normalizeRoutePath(e.Path)]
		perm := slices.IndexFunc(chain, isPermissionGateLink)
		serve := slices.IndexFunc(chain, func(n string) bool { return strings.Contains(n, "internal/api.Endpoint.serve") })
		if perm < 0 || serve < 0 || perm > serve {
			t.Errorf("%s: mounted chain %v does not run a permission gate ahead of serve", key, chain)
		}
	}

	// 3. Behaviour, route by route.
	for _, e := range endpoints {
		for _, under := range e.urlNodeParams() {
			t.Run(e.Method+" "+e.Path+" "+under, func(t *testing.T) {
				probeNodeMembership(t, e, under, e.checkedNodeParams())
			})
		}
	}

	// 4. The handlers that no longer check their node themselves.
	for _, suffix := range []string{
		".(*VMHandler).ListNodeUSBMappings-fm",
		".(*VMHandler).ListNodePCIMappings-fm",
		".(*NodeHandler).GetNodeSensors-fm",
	} {
		bound := 0
		for _, e := range endpoints {
			if !strings.HasSuffix(boundHandlerName(e.Handler), suffix) {
				continue
			}
			bound++
			if len(e.urlNodeParams()) == 0 {
				t.Errorf("%s %s is served by %s, which relies on the registry to have refused a node the cluster "+
					"does not hold, but the route names no node in its URL for the registry to check",
					e.Method, e.Path, strings.TrimPrefix(suffix, "."))
			}
		}
		if bound == 0 {
			t.Errorf("no route is served by %s any more, so this half of the guard checks nothing; update the list",
				strings.TrimPrefix(suffix, "."))
		}
	}
}

// probeNodeMembership drives one route, with the node parameter under put through each
// case and every other node parameter serve checks (all) set to the member. An array
// under test carries the member first and the name under test second, so a check that
// reads only an array's first element is caught.
func probeNodeMembership(t *testing.T, e Endpoint, under string, all []string) {
	t.Helper()
	key := e.Method + " " + e.Path
	clusterParam := "cluster_id"
	if _, ok := e.Parameters[clusterParam]; !ok {
		clusterParam = "id"
	}
	grants := map[string]bool{}
	if e.Permissions.Check != nil {
		grants[e.Permissions.Check.String()] = true
	}
	for _, alt := range e.Permissions.Alternatives {
		grants[alt.String()] = true
	}

	// build synthesizes a request that satisfies the whole schema, as the route sweep
	// does, with the cluster and the node parameters set here.
	build := func(node string) sweepRequest {
		t.Helper()
		base := sweepRouteOverrides[key]
		values := maps.Clone(base.values)
		if values == nil {
			values = map[string]any{}
		}
		values[clusterParam] = testClusterID
		for _, name := range all {
			values[name] = memberNode
			if e.Parameters[name].Type == apischema.Array {
				values[name] = []any{memberNode}
			}
		}
		values[under] = node
		if e.Parameters[under].Type == apischema.Array {
			values[under] = []any{memberNode, node}
		}
		// A node parameter that Requires a sibling (the PCI mapping update's add_node
		// needs add_path) brings it along.
		force := append(slices.Clone(base.forceRequired), all...)
		for _, name := range all {
			force = append(force, e.Parameters[name].Requires...)
		}
		req := synthesizeSweepRequestWith(e, false, sweepEndpointOverride{
			values: values, forceRequired: force, excludeFromOptional: base.excludeFromOptional,
		})
		if !req.ok {
			t.Fatalf("could not build a request: %s", req.reason)
		}
		return req
	}

	type outcome struct {
		status  int
		message string
		reached bool
	}
	dispatch := func(auth fiber.Handler, lookup handlers.NodeLookup, node string) outcome {
		t.Helper()
		cap := &capture{}
		probe := e
		probe.Handler = cap.handler()
		status, env := send(t, mountWith(auth, lookup, probe), sweepHTTP(e.Method, build(node)))
		return outcome{status: status, message: env.Message, reached: cap.called}
	}

	cluster := uuid.MustParse(testClusterID)
	granted := stubAuth(grants)

	// The member reaches the handler, asked about in the path's cluster, once however
	// many parameters name it (RequireNodesInCluster).
	lookup := holdingMember()
	if got := dispatch(granted, lookup, memberNode); got.status != fiber.StatusNoContent || !got.reached {
		t.Fatalf("the cluster's own node: got %+v, want 204 with the handler reached", got)
	}
	if got, want := lookup.questions(), []db.GetNodeByClusterAndNameParams{{ClusterID: cluster, Name: memberNode}}; !slices.Equal(got, want) {
		t.Errorf("the cluster's own node: asked %+v, want %+v", got, want)
	}

	// A name the cluster does not hold is refused before the handler — the address is
	// the shape the check exists for.
	for _, stranger := range []string{"pve-02", "192.0.2.10"} {
		lookup := holdingMember()
		got := dispatch(granted, lookup, stranger)
		if got.status != fiber.StatusNotFound || got.message != "Node not found in this cluster" || got.reached {
			t.Errorf("%q: got %+v, want 404 \"Node not found in this cluster\" with the handler not reached", stranger, got)
		}
		if !slices.Contains(lookup.questions(), db.GetNodeByClusterAndNameParams{ClusterID: cluster, Name: stranger}) {
			t.Errorf("%q: the lookup was asked %+v, never about it in cluster %s", stranger, lookup.questions(), cluster)
		}
	}

	// A caller the permission gate refuses, and a malformed name (the validator's 400),
	// are never a lookup.
	for _, tt := range []struct {
		name   string
		auth   fiber.Handler
		node   string
		status int
	}{
		{"no grant", stubAuth(nil), "192.0.2.10", fiber.StatusForbidden},
		{"a malformed name", granted, "pve_01", fiber.StatusBadRequest},
	} {
		lookup = holdingMember()
		if got := dispatch(tt.auth, lookup, tt.node); got.status != tt.status || got.reached {
			t.Errorf("%s: got %+v, want %d with the handler not reached", tt.name, got, tt.status)
		}
		if got := lookup.questions(); len(got) != 0 {
			t.Errorf("%s: the lookup was asked %+v", tt.name, got)
		}
	}

	// A lookup that fails is a 500, never a member — and says why in the log, since
	// the error handler records nothing.
	logged := captureSlog(t)
	failing := &nodeMembershipFake{err: errors.New("connection refused")}
	if got := dispatch(granted, failing, memberNode); got.status != fiber.StatusInternalServerError ||
		got.message != "Failed to look up the node" || got.reached {
		t.Errorf("a failed lookup: got %+v, want 500 \"Failed to look up the node\" with the handler not reached", got)
	}
	if out := logged(); !strings.Contains(out, "node membership lookup failed") || !strings.Contains(out, "connection refused") {
		t.Errorf("a failed lookup logged %q, want the lookup's own error", out)
	}

	// Where "" is a value the parameter takes (emptyOrNodeName's "any node") it names
	// nothing, and nothing is asked.
	if e.Parameters[under].Pattern == emptyOrNodeName {
		lookup = holdingMember()
		if got := dispatch(granted, lookup, ""); got.status != fiber.StatusNoContent || !got.reached {
			t.Errorf("an empty node: got %+v, want 204 with the handler reached", got)
		}
		if got := lookup.questions(); len(got) != min(len(all)-1, 1) {
			t.Errorf("an empty node: the lookup was asked %+v, want only about the other node parameters' member", got)
		}
	}
}

// TestGuard_EveryRouteTakingAUPIDIsAccountedFor keeps the node inside a UPID from
// reaching Proxmox unchecked. The registry cannot see that node — it is part of another
// parameter's value — so the routes that take a UPID are listed here with what happens
// to it, and a new one fails until somebody decides.
func TestGuard_EveryRouteTakingAUPIDIsAccountedFor(t *testing.T) {
	known := map[string]string{
		// VMHandler.taskUPID checks the node the UPID names; see
		// TestTaskRoutesRefuseAUPIDNamingANodeTheClusterDoesNotHold.
		"GET " + clusterScope + "/tasks/:upid":     ".(*VMHandler).GetTaskStatus-fm",
		"GET " + clusterScope + "/tasks/:upid/log": ".(*VMHandler).GetTaskLog-fm",
		// Looks the task up in the database; nothing reaches Proxmox.
		"PUT " + taskHistoryScope + "/:upid": ".(*TaskHandler).",
		// A PBS task: the PBS client always asks /nodes/localhost.
		"GET " + pbsBackupScope + "/tasks/:upid":     ".(*BackupHandler).GetTaskStatus-fm",
		"GET " + pbsBackupScope + "/tasks/:upid/log": ".(*BackupHandler).GetTaskLog-fm",
	}
	seen := map[string]bool{}
	for _, e := range sharedEndpoints(t) {
		if !slices.Contains(e.pathParams, "upid") {
			continue
		}
		key := e.Method + " " + e.Path
		seen[key] = true
		handler, ok := known[key]
		if !ok {
			t.Errorf("%s takes a UPID, whose second field is a node name: if the route forwards it to Proxmox, "+
				"check it with handlers.RequireNodesInCluster as VMHandler.taskUPID does, then add the route here", key)
			continue
		}
		if name := boundHandlerName(e.Handler); !strings.Contains(name, handler) {
			t.Errorf("%s is served by %s, want %s — the reason this route is safe was written about that handler",
				key, name, strings.TrimPrefix(handler, "."))
		}
	}
	for _, key := range slices.Sorted(maps.Keys(known)) {
		if !seen[key] {
			t.Errorf("%s no longer takes a UPID; update this table", key)
		}
	}
}

// TestTaskRoutesRefuseAUPIDNamingANodeTheClusterDoesNotHold drives the real task
// handlers: a UPID whose node is not one of the cluster's is refused before a Proxmox
// client is even built, and one whose node is gets past the check.
func TestTaskRoutesRefuseAUPIDNamingANodeTheClusterDoesNotHold(t *testing.T) {
	// Percent-encoded as the SPA's apiPath sends it (encodeURIComponent escapes the
	// colons and the "@" that url.PathEscape leaves alone), except that the node is
	// spliced in as written so a case can put an escape of its own in it. Sent
	// unencoded, a handler that stopped decoding the UPID would still find its node.
	spa := strings.NewReplacer(":", "%3A", "@", "%40")
	for _, route := range []struct {
		path    string
		handler func(*handlers.VMHandler) Handler
	}{
		{clusterScope + "/tasks/:upid", func(h *handlers.VMHandler) Handler { return h.GetTaskStatus }},
		{clusterScope + "/tasks/:upid/log", func(h *handlers.VMHandler) Handler { return h.GetTaskLog }},
	} {
		for _, tc := range []struct {
			name, node    string
			status        int
			message       string
			clientReached bool
		}{
			{"an address", "192.0.2.10", fiber.StatusNotFound, "Node not found in this cluster", false},
			// Percent-decoded before anything reads it: a NUL or a byte that is not UTF-8
			// would be refused by Postgres as an error, not "no row" — a 500 and an error
			// log line — so the node is held to the format first, and nothing is asked.
			{"a NUL", "pve%0001", fiber.StatusBadRequest, "The UPID does not name a valid node", false},
			{"a byte that is not UTF-8", "pve%FF01", fiber.StatusBadRequest, "The UPID does not name a valid node", false},
			{"a node the cluster does not hold", "pve-02", fiber.StatusNotFound, "Node not found in this cluster", false},
			// Past the check, the handler goes on to build its client, which this
			// database cannot supply a cluster for.
			{"the cluster's own node", memberNode, fiber.StatusNotFound, "Cluster not found", true},
		} {
			t.Run(route.path+" "+tc.name, func(t *testing.T) {
				fake := &handlerNodeDB{t: t, members: map[uuid.UUID]string{uuid.MustParse(testClusterID): memberNode},
					errs: map[string]error{"GetCluster": pgx.ErrNoRows}}
				e := sharedEndpoint(t, fiber.MethodGet, route.path)
				e.Handler = route.handler(handlers.NewVMHandler(db.New(fake), "", nil))
				app := newRegistryApp(t, stubAuth(grantsOf("view:task")), e)

				upid := spa.Replace("UPID:") + tc.node + spa.Replace(":00001A2B:00003344:5F2E1A00:qmstart:100:test-user@pve:")
				target := strings.NewReplacer(":cluster_id", testClusterID, ":upid", upid).Replace(route.path)
				status, env := send(t, app, authedRequest(http.MethodGet, target))
				if status != tc.status || env.Message != tc.message {
					t.Fatalf("status = %d (%q), want %d (%q)", status, env.Message, tc.status, tc.message)
				}
				if tc.status == fiber.StatusBadRequest {
					if len(fake.log) != 0 {
						t.Errorf("asked %v about a node the format refuses", fake.log)
					}
					return
				}
				if asked := fake.asked(); len(asked) == 0 || asked[0] != testClusterID+"/"+tc.node {
					t.Errorf("asked %v, want the node lookup for %q first", asked, tc.node)
				}
				if got := slices.Contains(fake.log, "GetCluster"); got != tc.clientReached {
					t.Errorf("asked %v: reached the Proxmox client = %v, want %v", fake.log, got, tc.clientReached)
				}
			})
		}
	}
}

// TestTaskRoutesFailClosedWithNoDatabase pins the other half of taskUPID's lookup: a
// handler built without queries answers "not configured" rather than letting a nil
// *db.Queries through as a lookup, whose first query would dereference it. Unreachable
// in production — a VMHandler is built only when the queries exist — which is exactly
// why nothing else would notice.
func TestTaskRoutesFailClosedWithNoDatabase(t *testing.T) {
	e := sharedEndpoint(t, fiber.MethodGet, clusterScope+"/tasks/:upid")
	e.Handler = handlers.NewVMHandler(nil, "", nil).GetTaskStatus
	// A panic has to come back as a response this test can read rather than take the
	// test binary down with it.
	app := mountWith(stubAuth(grantsOf("view:task")), nil, e, fiberrecover.New())

	spa := strings.NewReplacer(":", "%3A", "@", "%40")
	target := strings.NewReplacer(":cluster_id", testClusterID,
		":upid", spa.Replace("UPID:"+memberNode+":00001A2B:00003344:5F2E1A00:qmstart:100:test-user@pve:"),
	).Replace(e.Path)
	status, env := send(t, app, authedRequest(http.MethodGet, target))
	if status != fiber.StatusInternalServerError || env.Message != "Node lookup not configured" {
		t.Errorf("status = %d (%q), want 500 \"Node lookup not configured\"", status, env.Message)
	}
}

// TestServeChecksTheNodeAHandlerWouldRead pins what namedNodes promises: the node
// checked is the value a handler's plain accessor hands back, a declared default
// included, not only a value the caller sent. No node parameter in the registry
// declares a default today, so this is a synthetic route; the day one does, a check
// that looked only at what was sent would forward the default unchecked.
func TestServeChecksTheNodeAHandlerWouldRead(t *testing.T) {
	cap := &capture{}
	node := apischema.StdOption("node-name")
	node.Optional = true
	node.Default = "pve-02"
	app := mountWith(noAuth(), holdingMember(), Endpoint{
		Method: fiber.MethodGet, Path: clusterScope + "/probe", Description: "Probe a node.", Group: "Nodes",
		Permissions: Permissions{SelfService: "serve fixture; authorization is exercised separately"},
		Parameters:  clusterParams(apischema.Properties{"node": node}),
		Handler:     cap.handler(),
	})

	base := "/api/v1/clusters/" + testClusterID + "/probe"
	if status, env := send(t, app, httptest.NewRequest(http.MethodGet, base, nil)); status != fiber.StatusNotFound || cap.called {
		t.Errorf("defaulted to a node the cluster does not hold: status = %d (%q), handler reached = %v; "+
			"want 404 with the handler not reached", status, env.Message, cap.called)
	}
	if status, env := send(t, app, httptest.NewRequest(http.MethodGet, base+"?node="+memberNode, nil)); status != fiber.StatusNoContent || !cap.called {
		t.Errorf("the cluster's own node, sent: status = %d (%q), handler reached = %v; want 204", status, env.Message, cap.called)
	}
}

// TestMountRegistryRefusesARouteNamingANodeWithoutALookup pins the boot failure: a
// route that names a node, mounted with no way to ask whether the node is the
// cluster's, would forward whatever name it was given.
func TestMountRegistryRefusesARouteNamingANodeWithoutALookup(t *testing.T) {
	named := Endpoint{
		Method: fiber.MethodGet, Path: clusterScope + "/nodes/:node_name/probe", Description: "Probe a node.", Group: "Nodes",
		Permissions: Permissions{SelfService: "mount fixture; authorization is exercised separately"},
		Parameters:  nodeParams(nil),
		Handler:     (&capture{}).handler(),
	}
	func() {
		defer func() {
			r := recover()
			if r == nil {
				t.Fatal("mountRegistry mounted a route naming a node with no node lookup")
			}
			if msg := fmt.Sprint(r); !strings.Contains(msg, "node lookup") || !strings.Contains(msg, named.Path) {
				t.Errorf("panic = %q, want it to name the missing node lookup and the route", msg)
			}
		}()
		mountWith(noAuth(), nil, named)
	}()

	// The precondition twin: with no route naming a node, nil is fine.
	plain := named
	plain.Path = clusterScope + "/probe"
	plain.Parameters = clusterParams(nil)
	mountWith(noAuth(), nil, plain)
}

// TestRegisterRefusesANodeInAURLThatNamesNoCluster pins the startup failure for a node
// the check would have no cluster to ask about: every request to such a route could
// only be refused. The fixtures are SelfService on purpose — a cluster-scoped Check on
// these paths is already refused, for its own reason, and would hide this one.
func TestRegisterRefusesANodeInAURLThatNamesNoCluster(t *testing.T) {
	for _, e := range []Endpoint{
		{Method: fiber.MethodGet, Path: pathPrefix + "nodes/:node_name/probe",
			Parameters: apischema.Properties{"node_name": apischema.StdOption("node-name")}},
		{Method: fiber.MethodGet, Path: pathPrefix + "probe",
			Parameters: apischema.Properties{"node": apischema.StdOption("node-name")}},
	} {
		e.Description = "Probe."
		e.Group = "Nodes"
		e.Permissions = Permissions{SelfService: "register fixture; authorization is exercised separately"}
		e.Handler = (&capture{}).handler()
		err := NewRegistry().register(e)
		if err == nil || !strings.Contains(err.Error(), "names no cluster") {
			t.Errorf("%s: register = %v, want a refusal saying the path names no cluster", e.Path, err)
		}

		// The same parameter under a cluster is accepted.
		e.Path = clusterScope + strings.TrimPrefix(e.Path, strings.TrimSuffix(pathPrefix, "/"))
		e.Parameters = withParams(e.Parameters, clusterParams(nil))
		if err := NewRegistry().register(e); err != nil {
			t.Errorf("%s: register = %v, want it accepted under a cluster", e.Path, err)
		}
	}
}
