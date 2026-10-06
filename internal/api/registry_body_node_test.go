package api

import (
	"context"
	"errors"
	"go/ast"
	"go/token"
	"io"
	"log/slog"
	"maps"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/bigjakk/nexara/internal/api/apischema"
	"github.com/bigjakk/nexara/internal/api/handlers"
	"github.com/bigjakk/nexara/internal/auth"
	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/rolling"
)

// These tests hold every BODY parameter that names a Proxmox node to the node-membership
// check, the way registry_node_membership_test.go holds the URL's. A body node reaches
// Proxmox by name as surely as a URL's does (the node a guest is created on, a backup
// restored to, a schedule replayed against) and pveproxy resolves and dials whatever it
// names; see handlers.RequireNodesInCluster.

// The number of routes whose body names a node, by who checks it. Pinned rather than
// derived, so that a declaration that stops being recognised moves a number somebody
// has to update deliberately.
const (
	bodyNodeServeRouteCount   = 28
	bodyNodeHandlerRouteCount = 4
)

// bodyNodeExempt lists the body parameters spelled like a node that nothing checks
// against the cluster, with the reason, read in Proxmox's own source. None is declared
// with the node-name format (each is a list in Proxmox's own syntax, carried as one
// string), and none is ever sent to Proxmox as the node of a /nodes/{node}/… path.
var bodyNodeExempt = map[string]string{
	"POST " + clusterScope + "/ha/rules nodes": "pve-ha-manager API2/HA/Rules.pm create_rule/update_rule " +
		"hold every listed node to PVE::Cluster::check_node_exists (the in-memory corosync node list, no " +
		"lookup) before storing the rule, and nothing dials a rule's nodes",
	"PUT " + clusterScope + "/ha/rules/:rule nodes": "as POST …/ha/rules",
	"POST " + clusterScope + "/ha/groups nodes": "pve-ha-manager's pve-ha-group-node-list is a format " +
		"(name[:prio]) and the CRM picks nodes from its own node_status, never dialing a group's list; " +
		"groups are deprecated, and refused outright once migrated to rules",
	"PUT " + clusterScope + "/ha/groups/:group nodes": "as POST …/ha/groups",
	"POST " + clusterScope + "/sdn/zones nodes": "pve-network zones take a pve-node-list and only compare it " +
		"with the local node name (API2/Network/SDN/Zones.pm); applying SDN fans out over " +
		"PVE::Cluster::get_nodelist, never the zone's list",
	"POST " + clusterScope + "/sdn/zones exitnodes":              "as nodes: EvpnPlugin compares exitnodes with the local node only",
	"PUT " + clusterScope + "/sdn/zones/:zone nodes":             "as POST …/sdn/zones",
	"PUT " + clusterScope + "/sdn/zones/:zone exitnodes":         "as POST …/sdn/zones",
	"POST " + clusterScope + "/sdn/controllers nodes":            "as the zones' nodes: a pve-node-list compared with the local node only",
	"PUT " + clusterScope + "/sdn/controllers/:controller nodes": "as POST …/sdn/controllers",
	"POST " + clusterScope + "/vm-import-sources/esxi nodes": "the storage definition's nodes restriction " +
		"(a pve-node-list in storage.cfg), read by each node about itself; nothing dials it",
}

// isBodyNodeSpelling reports whether a body parameter's NAME reads as a node, derived
// without holdsNodeNames so a node parameter declared without the node-name format
// still shows up here.
func isBodyNodeSpelling(name string) bool {
	lower := strings.ToLower(name)
	return strings.Contains(lower, "node") || lower == "target"
}

// TestGuard_EveryBodyNodeIsCheckedOrAccountedFor holds every body parameter that names a node
// (derived from its SPELLING, and independently from the node-name format) to one of three
// fates: checked by serve (Endpoint.bodyNodeParams), checked by its handler
// (NodesCheckedByHandler), or listed in bodyNodeExempt with the upstream reason. A
// serve-checked one sits behind a cluster-scoped permission gate ahead of serve, so its 404
// never answers a caller the gate refused; a handler-checked one is on a Deferred route. Each
// is then driven: serve's through probeNodeMembership, the handler's through
// TestNodesCheckedByHandler_RefuseANodeTheClusterDoesNotHold, with its source read to check
// it asks after it authorizes.
func TestGuard_EveryBodyNodeIsCheckedOrAccountedFor(t *testing.T) {
	s := sharedRouteStub(t)
	endpoints := s.registry.Endpoints()

	// 1. Spelling and format, against the three fates.
	var serveRoutes, handlerRoutes int
	seenExempt := map[string]bool{}
	for _, e := range endpoints {
		key := e.Method + " " + e.Path
		checked := e.bodyNodeParams()
		if len(checked) > 0 {
			serveRoutes++
		}
		if len(e.NodesCheckedByHandler) > 0 {
			handlerRoutes++
		}
		for _, name := range slices.Sorted(maps.Keys(e.Parameters)) {
			prop := e.Parameters[name]
			if apischema.ResolveSource(name, prop, e.Method, e.pathParams) != apischema.SourceBody {
				continue
			}
			if strings.HasSuffix(strings.ToLower(name), "node_id") {
				if prop.Format != "uuid" && prop.Pattern != emptyOrUUID {
					t.Errorf("%s: %s reads as a Nexara node row id but is declared neither Format uuid nor "+
						"emptyOrUUID, so nothing stops it carrying a node name to Proxmox unchecked", key, name)
				}
				continue
			}
			if !isBodyNodeSpelling(name) && !holdsNodeNames(prop) {
				continue
			}
			_, byHandler := e.NodesCheckedByHandler[name]
			reason, exempt := bodyNodeExempt[key+" "+name]
			switch {
			case slices.Contains(checked, name) || byHandler:
				if exempt {
					t.Errorf("%s %s is checked, so its bodyNodeExempt entry (%q) is stale; remove it", key, name, reason)
				}
			case exempt:
				seenExempt[key+" "+name] = true
			default:
				t.Errorf("%s names a node in its body as %s, but nothing checks it against the cluster: "+
					"declare it with the node-name format (apischema.StdOption(\"node-name\")) so serve checks "+
					"it, or — on a route whose permission is Deferred — list it in NodesCheckedByHandler and "+
					"check it in the handler, or add it to bodyNodeExempt with the reason Proxmox never dials it",
					key, name)
			}
		}
	}
	for _, k := range slices.Sorted(maps.Keys(bodyNodeExempt)) {
		if !seenExempt[k] {
			t.Errorf("bodyNodeExempt lists %q, which names no unchecked body parameter any more; remove it", k)
		}
	}
	if serveRoutes != bodyNodeServeRouteCount {
		t.Errorf("%d routes have serve check a node in their BODY, want bodyNodeServeRouteCount = %d; if a "+
			"route was added or removed on purpose, update the constant", serveRoutes, bodyNodeServeRouteCount)
	}
	if handlerRoutes != bodyNodeHandlerRouteCount {
		t.Errorf("%d routes leave a body node to their handler, want bodyNodeHandlerRouteCount = %d; if a "+
			"route was added or removed on purpose, update the constant — and handlerNodeChecks", handlerRoutes, bodyNodeHandlerRouteCount)
	}

	// 2. Who authorizes first.
	chains := mountedChains(s)
	for _, e := range endpoints {
		key := e.Method + " " + e.Path
		if len(e.NodesCheckedByHandler) > 0 && e.Permissions.Deferred == "" {
			t.Errorf("%s leaves %v to its handler, but its permission is %q, not Deferred: its gate runs ahead "+
				"of serve, so serve should check the node", key, slices.Sorted(maps.Keys(e.NodesCheckedByHandler)),
				e.Permissions.Describe())
		}
		if len(e.bodyNodeParams()) == 0 {
			continue
		}
		if !e.runsClusterGate() {
			t.Errorf("%s has serve check %v in its body but declares %q, which runs no cluster-scoped "+
				"permission gate ahead of serve — its 404 would answer a caller nothing had authorized; list "+
				"them in NodesCheckedByHandler and check them in the handler after its permission check",
				key, e.bodyNodeParams(), e.Permissions.Describe())
		}
		chain := chains[e.Method+" "+normalizeRoutePath(e.Path)]
		perm := slices.IndexFunc(chain, isPermissionGateLink)
		serve := slices.IndexFunc(chain, func(n string) bool { return strings.Contains(n, "internal/api.Endpoint.serve") })
		if perm < 0 || serve < 0 || perm > serve {
			t.Errorf("%s: mounted chain %v does not run a permission gate ahead of serve", key, chain)
		}
	}

	// 3. Behaviour of the serve-checked ones.
	for _, e := range endpoints {
		for _, under := range e.bodyNodeParams() {
			t.Run(e.Method+" "+e.Path+" "+under, func(t *testing.T) {
				probeNodeMembership(t, e, under, e.checkedNodeParams())
			})
		}
	}

	// ... and the handler-checked ones: every route has a behavioural probe, and its
	// handler checks after it authorizes.
	for _, e := range endpoints {
		if len(e.NodesCheckedByHandler) == 0 {
			continue
		}
		key := e.Method + " " + e.Path
		if _, ok := handlerNodeChecks[key]; !ok {
			t.Errorf("%s leaves %v to its handler, but handlerNodeChecks has no probe for it; add one so "+
				"TestNodesCheckedByHandler_RefuseANodeTheClusterDoesNotHold drives it",
				key, slices.Sorted(maps.Keys(e.NodesCheckedByHandler)))
		}
		checkHandlerChecksNodesAfterAuthorizing(t, key, e.Handler, len(e.NodesCheckedByHandler))
	}
	for key := range handlerNodeChecks {
		if e, ok := sharedEndpoints(t)[key]; !ok || len(e.NodesCheckedByHandler) == 0 {
			t.Errorf("handlerNodeChecks probes %s, which leaves no node to its handler any more; remove it", key)
		}
	}
}

// checkHandlerChecksNodesAfterAuthorizing reads the handler's source and requires at
// least want calls to requireNodeInCluster, every one after its LAST
// requireClusterPerm, so a caller the handler refuses learns nothing about which nodes
// a cluster holds. The last, not the first: a migration authorizes two clusters, and a
// target node checked between the two would tell a caller holding only the source
// which nodes the target has.
func checkHandlerChecksNodesAfterAuthorizing(t *testing.T, key string, h Handler, want int) {
	t.Helper()
	name := routeHandlerKey(h)
	decls, err := packageFuncDecls("handlers")
	if err != nil {
		t.Fatalf("parse the handlers: %v", err)
	}
	fn, ok := decls[name]
	if name == "" || !ok {
		t.Errorf("%s: its handler %s is not a bound handlers method declared under handlers/, so its node check cannot be read", key, name)
		return
	}
	var perm token.Pos
	var checks []token.Pos
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		if call, ok := n.(*ast.CallExpr); ok {
			if id, ok := call.Fun.(*ast.Ident); ok {
				switch id.Name {
				case "requireClusterPerm":
					perm = max(perm, call.Pos())
				case "requireNodeInCluster":
					checks = append(checks, call.Pos())
				}
			}
		}
		return true
	})
	if perm == token.NoPos {
		t.Errorf("%s: %s never calls requireClusterPerm, so nothing authorizes the caller before its node check", key, name)
	}
	if len(checks) < want {
		t.Errorf("%s: %s calls requireNodeInCluster %d times, want at least %d — one per node parameter it is left to check",
			key, name, len(checks), want)
	}
	for _, pos := range checks {
		if pos < perm {
			t.Errorf("%s: %s checks a node before it authorizes the caller", key, name)
		}
	}
}

// handlerNodeCheck is how TestNodesCheckedByHandler_RefuseANodeTheClusterDoesNotHold
// drives one route whose handler checks its body nodes.
type handlerNodeCheck struct {
	// handler builds the real handler over queries.
	handler func(q *db.Queries) Handler
	// grants are the permissions that authorize the caller.
	grants []string
	// clusterOf names, for each node parameter, the parameter holding the cluster it belongs to.
	clusterOf map[string]string
	// values are further request values the route needs; adjust, when set, fixes
	// them up after the nodes are chosen.
	values map[string]any
	adjust func(values map[string]any)
	// rows are canned answers, by sqlc query name, for what the handler reads before
	// its node check; every other statement is answered errStop.
	rows map[string]any
}

// otherClusterID is the second cluster the handler probes use: a node of one cluster
// is a stranger to the other.
const otherClusterID = "22222222-3333-4444-5555-666666666666"

// upidOn is a task id naming node, in Proxmox's shape.
func upidOn(node string) string {
	return "UPID:" + node + ":0000A1B2:0001C3D4:66F4E3C0:qmstart:100:root@pam:"
}

var handlerNodeChecks = map[string]handlerNodeCheck{
	"POST " + authScope + "/console-token": {
		handler: func(q *db.Queries) Handler {
			jwt := auth.NewJWTService("test-secret", 15*time.Minute, 7*24*time.Hour)
			return handlers.NewAuthHandler(nil, q, jwt, nil, nil, nil).ConsoleToken
		},
		grants:    []string{"console:node"},
		clusterOf: map[string]string{"node": "console_cluster_id"},
		values:    map[string]any{"type": "node_shell"},
		// The account is read, and must be active, before the node is checked.
		rows: map[string]any{"GetUserByID": db.User{
			ID: uuid.MustParse(testUserID), Email: "user@example.com", Role: "admin", IsActive: true,
		}},
	},
	"POST " + migrationScope: {
		handler: func(q *db.Queries) Handler {
			return handlers.NewMigrationHandler(context.Background(), q, "", nil).Create
		},
		grants:    []string{"manage:migration"},
		clusterOf: map[string]string{"source_node": "source_cluster_id", "target_node": "target_cluster_id"},
		values:    map[string]any{"migration_type": "cross-cluster", "migration_mode": "live"},
	},
	"POST " + taskHistoryScope: {
		handler: func(q *db.Queries) Handler {
			return handlers.NewTaskHandler(q, nil, 0).Create
		},
		grants:    []string{"manage:task"},
		clusterOf: map[string]string{"node": "task_cluster_id"},
		adjust: func(values map[string]any) {
			node, _ := values["node"].(string)
			values["upid"] = upidOn(node)
		},
	},
	"POST " + clusterScope + "/vms/:vm_id/clone-to-template": {
		handler: func(q *db.Queries) Handler {
			return handlers.NewVMHandler(q, "", nil).CloneToTemplate
		},
		grants:    []string{"manage:vm"},
		clusterOf: map[string]string{"target": "cluster_id"},
	},
}

// errStop answers every statement the probes do not fake: the handler has got past its
// node check by then, which is all a probe needs to see.
var errStop = errors.New("stop: past the node check")

// handlerNodeDB is a db.DBTX holding one node per cluster. It answers
// GetNodeByClusterAndName from them (or with lookErr), the statements in rows with their
// canned row and those in errs with their error, and every other statement with
// errStop, logging each in order.
type handlerNodeDB struct {
	t       *testing.T
	members map[uuid.UUID]string
	lookErr error
	rows    map[string]any
	errs    map[string]error
	// tags answer the writes named here with that command tag instead of errStop
	// ("UPDATE 0" reports that no row changed).
	tags map[string]string
	mu   sync.Mutex
	// log is every statement in order: "?cluster/node" for a membership question, the
	// sqlc query name for anything else.
	log []string
}

var sqlcNameRe = regexp.MustCompile(`-- name: (\w+)`)

func (d *handlerNodeDB) note(entry string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.log = append(d.log, entry)
}

func sqlcName(sql string) string {
	if m := sqlcNameRe.FindStringSubmatch(sql); m != nil {
		return m[1]
	}
	return sql
}

// asked is every membership question, as "cluster/node", in order.
func (d *handlerNodeDB) asked() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	var out []string
	for _, entry := range d.log {
		if q, ok := strings.CutPrefix(entry, "?"); ok {
			out = append(out, q)
		}
	}
	return out
}

// beforeFirstQuestion is every statement sent before the first membership question:
// what the handler did before it checked a node.
func (d *handlerNodeDB) beforeFirstQuestion() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	for i, entry := range d.log {
		if strings.HasPrefix(entry, "?") {
			return slices.Clone(d.log[:i])
		}
	}
	return slices.Clone(d.log)
}

// afterLastQuestion is every statement sent after the last membership question: what
// the handler went on to do once it had its answer.
func (d *handlerNodeDB) afterLastQuestion() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	for i := len(d.log) - 1; i >= 0; i-- {
		if strings.HasPrefix(d.log[i], "?") {
			return slices.Clone(d.log[i+1:])
		}
	}
	return slices.Clone(d.log)
}

func (d *handlerNodeDB) Exec(_ context.Context, sql string, _ ...any) (pgconn.CommandTag, error) {
	name := sqlcName(sql)
	d.note(name)
	if tag, ok := d.tags[name]; ok {
		return pgconn.NewCommandTag(tag), nil
	}
	return pgconn.CommandTag{}, errStop
}

func (d *handlerNodeDB) Query(_ context.Context, sql string, _ ...any) (pgx.Rows, error) {
	d.note(sqlcName(sql))
	return nil, errStop
}

func (d *handlerNodeDB) QueryRow(_ context.Context, sql string, args ...any) pgx.Row {
	name := sqlcName(sql)
	if name != "GetNodeByClusterAndName" {
		d.note(name)
		if err, ok := d.errs[name]; ok {
			return fieldRow{err: err}
		}
		if row, ok := d.rows[name]; ok {
			return fieldRow{t: d.t, v: row}
		}
		return fieldRow{err: errStop}
	}
	cluster, _ := args[0].(uuid.UUID)
	node, _ := args[1].(string)
	d.note("?" + cluster.String() + "/" + node)
	switch {
	case d.lookErr != nil:
		return fieldRow{err: d.lookErr}
	case d.members[cluster] == node:
		return fieldRow{t: d.t, v: db.Node{ClusterID: cluster, Name: node}}
	}
	return fieldRow{err: pgx.ErrNoRows}
}

// fieldRow scans v's fields into the destinations in order — how sqlc scans a row into
// its struct — or, for anything but a struct, v into the one destination; or answers err.
type fieldRow struct {
	t   *testing.T
	v   any
	err error
}

func (r fieldRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	v := reflect.ValueOf(r.v)
	if v.Kind() != reflect.Struct {
		if len(dest) != 1 {
			r.t.Fatalf("scanned %d columns into a single %T", len(dest), r.v)
		}
		reflect.ValueOf(dest[0]).Elem().Set(v)
		return nil
	}
	if len(dest) != v.NumField() {
		r.t.Fatalf("scanned %d columns into a %T with %d fields", len(dest), r.v, v.NumField())
	}
	for i := range dest {
		reflect.ValueOf(dest[i]).Elem().Set(v.Field(i))
	}
	return nil
}

// grantedOn authenticates every request as testUserID holding grants on cluster only —
// the shape of a caller authorized on a migration's source cluster and not its target.
func grantedOn(grants []string, cluster uuid.UUID) fiber.Handler {
	scoped := make([]scopedGrant, len(grants))
	for i, g := range grants {
		scoped[i] = scopedGrant{permission: g, cluster: cluster}
	}
	return authWithEngine(&scopedRBACEngine{grants: scoped})
}

// TestNodesCheckedByHandler_RefuseANodeTheClusterDoesNotHold drives each route in
// handlerNodeChecks through its REAL handler, over a database that knows one node per
// cluster: for each node parameter, a stranger, an address and the other cluster's
// node are 404 before the handler goes any further (the node of the wrong cluster is
// what catches a migration target checked against its source cluster); the members get
// past the check; a caller the handler refuses gets its 403 and no question about
// nodes is asked; a lookup that fails is a 500.
func TestNodesCheckedByHandler_RefuseANodeTheClusterDoesNotHold(t *testing.T) {
	clusters := []uuid.UUID{uuid.MustParse(testClusterID), uuid.MustParse(otherClusterID)}
	memberOf := map[uuid.UUID]string{clusters[0]: "pve-01", clusters[1]: "pve-02"}

	for _, key := range slices.Sorted(maps.Keys(handlerNodeChecks)) {
		check := handlerNodeChecks[key]
		method, path, _ := strings.Cut(key, " ")
		e := sharedEndpoint(t, method, path)
		if got, want := slices.Sorted(maps.Keys(check.clusterOf)), slices.Sorted(maps.Keys(e.NodesCheckedByHandler)); !slices.Equal(got, want) {
			t.Errorf("%s: handlerNodeChecks probes %v, but the route leaves %v to its handler", key, got, want)
			continue
		}

		// Each cluster parameter gets its own cluster, in name order, so two node
		// parameters with different clusters sit in different ones.
		clusterIDs := map[string]uuid.UUID{}
		var distinct []uuid.UUID
		for i, cp := range slices.Compact(slices.Sorted(maps.Values(check.clusterOf))) {
			clusterIDs[cp] = clusters[min(i, 1)]
			if !slices.Contains(distinct, clusterIDs[cp]) {
				distinct = append(distinct, clusterIDs[cp])
			}
		}
		granted := grantsOf(check.grants...)

		dispatch := func(t *testing.T, fake *handlerNodeDB, authn fiber.Handler, nodes map[string]string) (int, string) {
			t.Helper()
			values := maps.Clone(check.values)
			if values == nil {
				values = map[string]any{}
			}
			for cp, id := range clusterIDs {
				values[cp] = id.String()
			}
			for param, node := range nodes {
				values[param] = node
			}
			if check.adjust != nil {
				check.adjust(values)
			}
			req := synthesizeSweepRequestWith(e, false, sweepEndpointOverride{values: values, forceRequired: slices.Collect(maps.Keys(values))})
			if !req.ok {
				t.Fatalf("could not build a request: %s", req.reason)
			}
			probe := e
			probe.Handler = check.handler(db.New(fake))
			status, env := send(t, mountWith(authn, everyNodeIsAMember(), probe), sweepHTTP(method, req))
			return status, env.Message
		}
		newDB := func(t *testing.T, lookErr error) *handlerNodeDB {
			return &handlerNodeDB{t: t, members: memberOf, lookErr: lookErr, rows: check.rows}
		}
		nodesAll := func(f func(param string) string) map[string]string {
			out := map[string]string{}
			for param := range check.clusterOf {
				out[param] = f(param)
			}
			return out
		}
		members := func() map[string]string {
			return nodesAll(func(param string) string { return memberOf[clusterIDs[check.clusterOf[param]]] })
		}
		strangers := func() map[string]string { return nodesAll(func(string) string { return "192.0.2.10" }) }

		t.Run(key+": members", func(t *testing.T) {
			fake := newDB(t, nil)
			status, msg := dispatch(t, fake, stubAuth(granted), members())
			if status == fiber.StatusNotFound || status == fiber.StatusForbidden {
				t.Errorf("got %d %q, want the handler past its node check", status, msg)
			}
			var want []string
			for param, node := range members() {
				want = append(want, clusterIDs[check.clusterOf[param]].String()+"/"+node)
			}
			if got := slices.Sorted(slices.Values(fake.asked())); !slices.Equal(got, slices.Sorted(slices.Values(want))) {
				t.Errorf("asked %v, want %v", got, want)
			}
		})

		for _, param := range slices.Sorted(maps.Keys(check.clusterOf)) {
			own := clusterIDs[check.clusterOf[param]]
			otherNode := memberOf[clusters[0]]
			if own == clusters[0] {
				otherNode = memberOf[clusters[1]]
			}
			for _, stranger := range []string{"192.0.2.10", "unknown-node", otherNode} {
				t.Run(key+": "+param+"="+stranger, func(t *testing.T) {
					fake := newDB(t, nil)
					nodes := members()
					nodes[param] = stranger
					status, msg := dispatch(t, fake, stubAuth(granted), nodes)
					if status != fiber.StatusNotFound || msg != "Node not found in this cluster" {
						t.Errorf("got %d %q, want 404 \"Node not found in this cluster\"", status, msg)
					}
					if !slices.Contains(fake.asked(), own.String()+"/"+stranger) {
						t.Errorf("asked %v, never about %q in its own cluster %s", fake.asked(), stranger, own)
					}
					if after := fake.afterLastQuestion(); len(after) != 0 {
						t.Errorf("the handler went on to %v after refusing the node", after)
					}
					// Before it, only the reads the route is known to need: nothing (an
					// audit row, a write) may reach the database ahead of the check for a
					// request it is about to refuse.
					for _, stmt := range fake.beforeFirstQuestion() {
						if _, canned := check.rows[stmt]; !canned {
							t.Errorf("the handler sent %s before its node check; only %v may precede it", stmt, slices.Sorted(maps.Keys(check.rows)))
						}
					}
				})
			}
		}

		t.Run(key+": no grant", func(t *testing.T) {
			fake := newDB(t, nil)
			if status, msg := dispatch(t, fake, stubAuth(nil), strangers()); status != fiber.StatusForbidden {
				t.Errorf("got %d %q, want 403", status, msg)
			}
			if asked := fake.asked(); len(asked) != 0 {
				t.Errorf("asked %v before refusing the caller", asked)
			}
		})

		// A route authorizing more than one cluster refuses a caller holding only one
		// of them before it asks about ANY node, the held cluster's included.
		if len(distinct) > 1 {
			for _, only := range distinct {
				t.Run(key+": granted only on "+only.String(), func(t *testing.T) {
					fake := newDB(t, nil)
					if status, msg := dispatch(t, fake, grantedOn(check.grants, only), strangers()); status != fiber.StatusForbidden {
						t.Errorf("got %d %q, want 403", status, msg)
					}
					if asked := fake.asked(); len(asked) != 0 {
						t.Errorf("asked %v before refusing a caller the handler had not authorized on every cluster", asked)
					}
				})
			}
		}

		t.Run(key+": failed lookup", func(t *testing.T) {
			fake := newDB(t, errors.New("connection refused"))
			status, msg := dispatch(t, fake, stubAuth(granted), members())
			if status != fiber.StatusInternalServerError || msg != "Failed to look up the node" {
				t.Errorf("got %d %q, want 500 \"Failed to look up the node\"", status, msg)
			}
			if after := fake.afterLastQuestion(); len(after) != 0 {
				t.Errorf("the handler went on to %v after a failed lookup", after)
			}
		})
	}
}

// TestTaskCreate_RefusesANodeTheUPIDDoesNotName: the collector polls a registered task
// at its row's node, so the node must be the one the UPID itself says the task runs on.
func TestTaskCreate_RefusesANodeTheUPIDDoesNotName(t *testing.T) {
	check := handlerNodeChecks["POST "+taskHistoryScope]
	e := sharedEndpoint(t, fiber.MethodPost, taskHistoryScope)
	fake := &handlerNodeDB{t: t, members: map[uuid.UUID]string{uuid.MustParse(testClusterID): "pve-01"}}
	req := synthesizeSweepRequestWith(e, false, sweepEndpointOverride{
		values:        map[string]any{"task_cluster_id": testClusterID, "node": "pve-01", "upid": upidOn("pve-02")},
		forceRequired: []string{"task_cluster_id", "node", "upid"},
	})
	if !req.ok {
		t.Fatalf("could not build a request: %s", req.reason)
	}
	e.Handler = check.handler(db.New(fake))
	status, env := send(t, mountWith(stubAuth(grantsOf("manage:task")), everyNodeIsAMember(), e), sweepHTTP("POST", req))
	if status != fiber.StatusBadRequest || env.Message != "node does not match the node the UPID names" {
		t.Errorf("got %d %q, want 400 \"node does not match the node the UPID names\"", status, env.Message)
	}
	if len(fake.log) != 0 {
		t.Errorf("the handler sent %v; it should refuse before asking anything", fake.log)
	}
}

// TestRegisterHoldsBodyNodesToAClusterOrAHandler pins register's two refusals for a
// body node: one on a path naming no cluster that nothing claims to check (serve would
// have no cluster to ask about), and a NodesCheckedByHandler entry that could hide a
// node from serve with no node behind it — a parameter that is not declared, holds no
// node name, sits in the URL, or comes with no reason.
func TestRegisterHoldsBodyNodesToAClusterOrAHandler(t *testing.T) {
	base := func(path string, params apischema.Properties, byHandler map[string]string) Endpoint {
		return Endpoint{
			Method: fiber.MethodPost, Path: path, Description: "Probe.", Group: "Nodes",
			Permissions:           Permissions{Deferred: "register fixture"},
			Parameters:            params,
			NodesCheckedByHandler: byHandler,
			Handler:               (&capture{}).handler(),
		}
	}
	node := apischema.Properties{"node": apischema.StdOption("node-name")}
	nodes := apischema.Properties{"nodes": {Type: apischema.Array, Items: &apischema.Property{Type: apischema.String, Format: "node-name"}}}

	for _, tt := range []struct {
		name string
		e    Endpoint
		want string // "" = accepted
	}{
		{"a body node on a path naming no cluster", base(pathPrefix+"probe", node, nil), "names no cluster"},
		{"a body node list on a path naming no cluster", base(pathPrefix+"probe", nodes, nil), "names no cluster"},
		{"the same, left to the handler", base(pathPrefix+"probe", node, map[string]string{"node": "checked in the handler"}), ""},
		{"the list, left to the handler", base(pathPrefix+"probe", nodes, map[string]string{"nodes": "checked in the handler"}), ""},
		{"a body node under a cluster", base(clusterScope+"/probe", withParams(node, clusterParams(nil)), nil), ""},
		{"an undeclared parameter left to the handler", base(pathPrefix+"probe", node,
			map[string]string{"node": "x", "target": "x"}), "declares no parameter"},
		{"a parameter holding no node left to the handler", base(pathPrefix+"probe",
			withParams(node, apischema.Properties{"comment": {Type: apischema.String}}),
			map[string]string{"node": "x", "comment": "x"}), "declares no parameter"},
		{"a reasonless entry", base(pathPrefix+"probe", node, map[string]string{"node": " "}), "no reason"},
		{"a URL node left to the handler", base(clusterScope+"/nodes/:node_name/probe",
			withParams(apischema.Properties{"node_name": apischema.StdOption("node-name")}, clusterParams(nil)),
			map[string]string{"node_name": "x"}), "not a body parameter"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			err := NewRegistry().register(tt.e)
			switch {
			case tt.want == "" && err != nil:
				t.Errorf("register = %v, want it accepted", err)
			case tt.want != "" && (err == nil || !strings.Contains(err.Error(), tt.want)):
				t.Errorf("register = %v, want a refusal containing %q", err, tt.want)
			}
		})
	}
}

// TestConsoleToken_ADisabledAccountIsRefusedBeforeItsNodeIsChecked: a JWT outlives the
// account it was issued to by up to its lifetime, and the console-token handler is where
// a disabled account is refused. It must get the same 401 whatever node it names, so
// the node is only checked after the account is.
func TestConsoleToken_ADisabledAccountIsRefusedBeforeItsNodeIsChecked(t *testing.T) {
	check := handlerNodeChecks["POST "+authScope+"/console-token"]
	e := sharedEndpoint(t, fiber.MethodPost, authScope+"/console-token")
	fake := &handlerNodeDB{t: t, members: map[uuid.UUID]string{uuid.MustParse(testClusterID): "pve-01"},
		rows: map[string]any{"GetUserByID": db.User{ID: uuid.MustParse(testUserID), Email: "user@example.com", IsActive: false}}}
	e.Handler = check.handler(db.New(fake))
	app := mountWith(stubAuth(grantsOf("console:node")), everyNodeIsAMember(), e)
	for _, node := range []string{"pve-01", "192.0.2.10"} {
		req := jsonRequest(fiber.MethodPost, authScope+"/console-token",
			`{"cluster_id":"`+testClusterID+`","node":"`+node+`","type":"node_shell"}`)
		req.Header.Set("X-Test-User", "yes")
		if status, env := send(t, app, req); status != fiber.StatusUnauthorized || env.Message != "Account is disabled" {
			t.Errorf("%s: got %d %q, want 401 \"Account is disabled\"", node, status, env.Message)
		}
	}
	if asked := fake.asked(); len(asked) != 0 {
		t.Errorf("asked %v for a disabled account", asked)
	}
}

// TestConfirmUpgrade_RefusesANodeTheClusterDoesNotHold drives the real confirm-upgrade
// route over a rolling job whose node the cluster does not hold (one created before the
// API checked it): confirming answers 409 with the reason, or 500 when the lookup
// fails, and never builds the Proxmox client the drain check and the reboot would use —
// the cluster row is never read. The stranger's node is failed ("UPDATE 0" here, so
// failNode stops before failing the job, which is not what this test is about).
func TestConfirmUpgrade_RefusesANodeTheClusterDoesNotHold(t *testing.T) {
	e := sharedEndpoint(t, fiber.MethodPost, rollingScope+"/:id/nodes/:node_id/confirm-upgrade")
	jobID, nodeID := uuid.New(), uuid.New()
	for _, tt := range []struct {
		name       string
		lookErr    error
		wantStatus int
		wantMsg    string
		wantFailed bool
	}{
		{name: "a stranger", wantStatus: fiber.StatusConflict, wantFailed: true,
			wantMsg: `node "192.0.2.10" is not one of this cluster's nodes; it was not contacted`},
		{name: "a failed lookup", lookErr: errors.New("connection refused"),
			wantStatus: fiber.StatusInternalServerError, wantMsg: "Failed to look up the node"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			fake := &handlerNodeDB{t: t, lookErr: tt.lookErr,
				rows: map[string]any{
					"GetRollingUpdateJob": db.RollingUpdateJob{ID: jobID, ClusterID: uuid.MustParse(testClusterID), RebootAfterUpdate: true},
					"GetRollingUpdateNode": db.RollingUpdateNode{ID: nodeID, JobID: jobID, NodeName: "192.0.2.10",
						Step: "awaiting_upgrade"},
				},
				tags: map[string]string{"FailRollingUpdateNode": "UPDATE 0"},
			}
			q := db.New(fake)
			orch := rolling.NewOrchestrator(context.Background(), q, "", slog.New(slog.NewTextHandler(io.Discard, nil)), nil, nil)
			probe := e
			probe.Handler = handlers.NewRollingUpdateHandler(q, "", nil, orch).ConfirmUpgrade
			app := mountWith(stubAuth(grantsOf("manage:rolling_update")), everyNodeIsAMember(), probe)

			target := strings.NewReplacer(":cluster_id", testClusterID, ":id", jobID.String(), ":node_id", nodeID.String()).Replace(probe.Path)
			req := authedRequest(fiber.MethodPost, target)
			status, env := send(t, app, req)

			if status != tt.wantStatus || env.Message != tt.wantMsg {
				t.Errorf("got %d %q, want %d %q", status, env.Message, tt.wantStatus, tt.wantMsg)
			}
			if slices.Contains(fake.log, "GetCluster") {
				t.Errorf("statements %v: the cluster row was read, so a Proxmox client was built", fake.log)
			}
			if got := slices.Contains(fake.log, "FailRollingUpdateNode"); got != tt.wantFailed {
				t.Errorf("statements %v: node failed = %v, want %v", fake.log, got, tt.wantFailed)
			}
		})
	}
}
