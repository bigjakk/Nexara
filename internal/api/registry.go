package api

import (
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"runtime/debug"
	"slices"
	"strings"

	"github.com/gofiber/fiber/v3"

	"github.com/bigjakk/nexara/internal/api/apischema"
	"github.com/bigjakk/nexara/internal/api/handlers"
)

// This file is the declarative endpoint registry: a route states its
// parameters and its permission in one place, the way PVE's
// PVE::RESTHandler::register_method does, and the machinery here turns
// that statement into a mounted Fiber route.
//
// What it replaces is not a style but a hazard. Nexara registers ~550
// routes imperatively in router.go and authorizes them with hand-placed
// require*Perm calls inside handler bodies, so a route whose handler
// forgets the call ships authenticated-but-unauthorized-accessible and
// nothing fails. rbac_route_guard_test.go carries ~700 lines of AST
// call-graph analysis to APPROXIMATE what a declaration simply states —
// and says so in its own LIMITATION note: it proves a check is reachable,
// not that it runs. A declared permission needs no approximation.
//
// The other half is Register calling Compile on every schema. Phase 1's
// apischema fails closed on a malformed declaration, but four of its
// defects can only surface on a request that happens to exercise them: a
// contradictory enum 400s the caller and blames them for our bug, a
// typo'd format name is invisible until someone sends the optional
// parameter that carries it, a nested array renders as "[a b]", and a
// numeric bound on a string 500s every request. Compiling at registration
// turns all four into a startup panic. apischema's
// TestCompileIsSufficientForStartup pins that Compile catches them; this
// is the half that makes it run.

// Handler serves one registry endpoint. It receives the validated,
// coerced, default-filled parameters instead of re-parsing the request:
// params.OptString and friends keep "the caller sent nothing" and "the
// caller sent the zero value" apart, which is the distinction a
// hand-rolled `if req.Index == 0` cannot express.
type Handler func(c fiber.Ctx, params *apischema.Params) error

// Endpoint is one declared route.
type Endpoint struct {
	// Method is the HTTP verb, upper case.
	Method string

	// Path is the route as registered, with Fiber's :param syntax, and
	// starts with /api/v1/.
	Path string

	// Description is the one-line summary shown in the API docs. It is
	// required: the declaration IS the documentation, and an endpoint
	// that cannot say what it does in one line is not ready to ship.
	Description string

	// Group is the docs section this endpoint belongs to ("Clusters",
	// "VMs"). Required, for the same reason.
	Group string

	// Permissions says how the route is authorized. Exactly one field.
	Permissions Permissions

	// Parameters is the route's parameter schema. Register compiles it
	// and panics if it is malformed.
	Parameters apischema.Properties

	// RateLimiter, when set, is attached between authentication and the
	// permission check — so that an unauthorized flood spends the
	// caller's own bucket rather than collecting free 403s, and so that
	// anonymous traffic cannot drain a bucket that legitimate onboarding
	// needs (the reasoning on clusterCreateLimiter in middleware.go).
	RateLimiter fiber.Handler

	// Handler serves the request.
	Handler Handler

	// NodesCheckedByHandler lists, with the reason, the BODY parameters
	// holding a Proxmox node name that serve does NOT check against the
	// path's cluster, because the handler checks them itself. serve checks
	// every other node parameter (see checkedNodeParams); a node name reaches a
	// Proxmox call, or is stored for an engine to send later, either way.
	//
	// Only a route whose permission is Deferred lists one: its handler
	// authorizes the caller itself, so a check in serve would answer 404
	// before the handler's 403 and tell a caller nothing had authorized
	// which nodes a cluster holds — and a route whose cluster is in the
	// body (a migration's two clusters, a registered task's, a console
	// token's) has no path cluster to check against at all. The handler
	// must check each one after its permission check, with
	// handlers.requireNodeInCluster. register refuses an entry that names
	// no such parameter; TestGuard_EveryBodyNodeIsCheckedOrAccountedFor holds
	// the rest.
	NodesCheckedByHandler map[string]string

	// pathParams caches the :param names parsed out of Path at
	// registration, so that neither validation nor extraction re-parses
	// the path per request.
	pathParams []string
}

// knownMethods is the verb vocabulary a registry endpoint may declare.
// CONNECT and TRACE are absent on purpose: Fiber will route them, but
// nothing in this API should answer either, and a typo'd verb that
// silently registers a route nobody can reach is worse than a panic.
var knownMethods = map[string]bool{
	fiber.MethodGet:     true,
	fiber.MethodHead:    true,
	fiber.MethodPost:    true,
	fiber.MethodPut:     true,
	fiber.MethodPatch:   true,
	fiber.MethodDelete:  true,
	fiber.MethodOptions: true,
}

// pathPrefix is the only prefix a registry endpoint may be mounted under.
// Everything outside it (/healthz, the SPA catch-all) is served by the
// legacy blocks in router.go and is not an API endpoint.
const pathPrefix = "/api/v1/"

// Registry holds declared endpoints in registration order.
//
// Order is preserved because Fiber matches routes in the order they are
// registered: a literal path registered after a :param path that also
// matches it is unreachable. Keeping the slice ordered means the route
// table reads the same way the declarations do.
type Registry struct {
	endpoints []Endpoint
	seen      map[string]struct{} // "METHOD path" already registered
}

// NewRegistry returns an empty registry. Production uses the package-level
// one via Register; tests build their own so that a synthetic endpoint
// cannot leak into the real route table.
func NewRegistry() *Registry {
	return &Registry{seen: make(map[string]struct{})}
}

// There is deliberately NO package-level registry, and this note is here
// because the obvious shape — a global populated from init() the way
// PVE's register_method is — cannot work in this codebase and would fail
// in a way that is easy to miss.
//
// An Endpoint's Handler is a bound method value: s.vmHandler.AttachDisk,
// carrying the queries, the encryption key and the event publisher that
// handler was built with. Those exist only once a Server does, so a
// registry populated at package init could only ever hold handlers bound
// to nothing. Declaring endpoints per Server (see buildRegistry) is what
// makes the binding honest — and it also removes the alternative's
// quieter hazard, where a second Server in the same process mounts the
// first one's handlers because the global was already populated.
//
// The guards read the Server's registry (Server.registry, set by
// setupRoutes) for the same reason: a declaration nobody built is a
// declaration no guard can check, and a guard reading an empty global
// passes vacuously.

// Register declares an endpoint. It PANICS rather than returning an
// error, and that is the load-bearing decision of this whole file.
//
// Every failure it reports is a mistake in Nexara's own source that no
// request can cause and no runtime condition can fix — a typo'd format
// name, a path param nothing declares, a route that never said how it is
// authorized. Registration runs at startup, from setupRoutes, so a panic
// is a process that refuses to boot: loud, immediate, and impossible to
// ship past. The alternative — returning an error that the caller logs
// and carries on from — is how a malformed declaration reaches a request
// in the first place.
func (r *Registry) Register(e Endpoint) {
	if err := r.register(e); err != nil {
		panic("api: " + err.Error())
	}
}

// register does the work, returning the error Register panics with. Split
// out so the validation can be tested without recover() at every call.
func (r *Registry) register(e Endpoint) error {
	if !knownMethods[e.Method] {
		return fmt.Errorf("endpoint %q declares method %q, which is not a known HTTP verb (%s)",
			e.Path, e.Method, strings.Join(sortedMethods(), ", "))
	}
	if !strings.HasPrefix(e.Path, pathPrefix) {
		return fmt.Errorf("endpoint %s %q does not start with %s", e.Method, e.Path, pathPrefix)
	}
	if strings.TrimSpace(e.Description) == "" {
		return fmt.Errorf("endpoint %s %s has no Description", e.Method, e.Path)
	}
	if strings.TrimSpace(e.Group) == "" {
		return fmt.Errorf("endpoint %s %s has no Group", e.Method, e.Path)
	}
	if e.Handler == nil {
		return fmt.Errorf("endpoint %s %s has no Handler", e.Method, e.Path)
	}

	key := e.Method + " " + e.Path
	if _, dup := r.seen[key]; dup {
		return fmt.Errorf("endpoint %s is already registered", key)
	}

	e.pathParams = pathParamNames(e.Path)
	if err := checkPathParams(e); err != nil {
		return err
	}
	if err := e.Permissions.validate(e.Path, e.pathParams); err != nil {
		return fmt.Errorf("endpoint %s %s %w", e.Method, e.Path, err)
	}
	// A node the request names — in the URL or the body — is checked against
	// the cluster the path names (serve), so a route that names a node and no
	// cluster could only refuse every request, unless its handler checks the
	// node against a cluster of its own (NodesCheckedByHandler). See
	// checkedNodeParams. That such a route also runs a cluster-scoped
	// permission gate ahead of the check — so its 404 is never an answer to a
	// caller the gate refused — is held by
	// TestGuard_EveryRouteNamingANodeRefusesOneTheClusterDoesNotHold and
	// TestGuard_EveryBodyNodeIsCheckedOrAccountedFor rather than here, because
	// the parameter fixtures drive these routes as SelfService.
	if err := e.checkNodesCheckedByHandler(); err != nil {
		return err
	}
	if nodes := e.checkedNodeParams(); len(nodes) > 0 && !namesACluster(e.pathParams, e.Path) {
		return fmt.Errorf("endpoint %s %s names a Proxmox node in %s, but its path names no cluster to check "+
			"that node against; it needs a required :cluster_id as its FIRST path parameter, or to start with %q "+
			"and name no later :cluster_id — or, for a body parameter on a route whose permission is Deferred, "+
			"list it in NodesCheckedByHandler and check it in the handler",
			e.Method, e.Path, strings.Join(nodes, ", "), legacyClusterPrefix)
	}
	// The reason this function exists at registration time rather than at
	// request time. See the file comment.
	if err := e.Parameters.Compile(); err != nil {
		return fmt.Errorf("endpoint %s %s: %w", e.Method, e.Path, err)
	}

	r.seen[key] = struct{}{}
	r.endpoints = append(r.endpoints, e)
	return nil
}

// Endpoints returns the declared endpoints in registration order. The
// slice is a copy; the Endpoint values inside it are not, but every
// exported field is either a scalar or a map the caller has no reason to
// mutate.
func (r *Registry) Endpoints() []Endpoint { return slices.Clone(r.endpoints) }

// Len reports how many endpoints are declared.
func (r *Registry) Len() int { return len(r.endpoints) }

// gateParamNames are the path parameters the permission middleware reads,
// in the order clusterIDFromParam tries them. They are the names that must
// mean the same thing to the gate and to the handler, because the gate
// resolves them from c.Params and nothing else.
var gateParamNames = []string{"cluster_id", "id"}

// checkPathParams enforces the path/schema agreement. Five shapes are
// refused, and they fail in three different ways.
//
// Two are ordinary mistakes. A :param the schema never declares reaches
// the handler as a value no accessor can read — Params panics on an
// undeclared key, so the route 500s on its first request. A parameter
// declared with SourcePath that the path never names is the quieter one:
// it is simply never supplied, so a required one rejects every caller and
// an optional one silently carries its default forever.
//
// Two are holes rather than mistakes, because they DIVERGE instead of
// failing.
//
// The first is a :param that declares an explicit non-path source.
// ResolveSource gives an explicit Source priority over the path match, so
// extraction reads the parameter out of the query or the body while the
// gate still reads the path.
//
// The second is the same divergence reached from the other side, and it
// is a privilege escalation rather than a confusion. clusterIDFromParam
// reads TWO names — cluster_id, falling back to id — so protecting only
// the name this particular path happens to spell leaves the other one
// open. On /api/v1/clusters/:id, declaring cluster_id as a body parameter
// passes both loops above: the path spells "id", and "cluster_id"
// resolves to the body so the SourcePath loop skips it. At request time
// the gate authorizes cluster A from the URL while the handler acts on
// cluster B from the body, so a caller holding manage on A only sends
// POST /api/v1/clusters/<A>/… with {"cluster_id":"<B>"} and acts on B.
// The refusal is therefore about the names the GATE reads, not the names
// the path spells: either of them, declared anywhere, must resolve to the
// path. A parameter that genuinely means some other cluster — a migration
// destination — has to be named something else, which is the point.
//
// The comparison is EqualFold because Fiber's is: CaseSensitive is false
// (buildFiberConfig), so c.Params("cluster_id") matches a route that
// spells it :Cluster_ID, and a case-sensitive guard here would not.
//
// The fifth is an Alias on a path parameter, which is silently dead: the
// alias is read from the parent's source, so c.Params(alias) is always
// empty, and checkMisplaced would then tell the caller to send it "in the
// request path" — which a URL has no way to express.
func checkPathParams(e Endpoint) error {
	if i := strings.IndexAny(e.Path, "*+"); i >= 0 {
		// Fiber names a wildcard "*1", nothing declares it, and
		// pathParamNames skips it — so a wildcard segment is the one
		// piece of a path that reaches a handler unvalidated and
		// un-normalized, which is exactly where path traversal lives. No
		// endpoint needs one today; make the gap a startup failure rather
		// than a quiet hole.
		return fmt.Errorf("endpoint %s %s uses the wildcard %q, which the parameter schema cannot describe",
			e.Method, e.Path, string(e.Path[i]))
	}
	if i := strings.IndexAny(e.Path, "?<"); i >= 0 {
		// An optional parameter (":name?") lets a request match with the
		// segment EMPTY — so does a constrained optional one, since Fiber
		// writes the constraint before an optional's "?" (":name<guid>?"). For a
		// cluster-scoped route that is an authorization bypass rather than a
		// formatting nicety: /clusters//vms/<id> leaves :cluster_id empty,
		// clusterIDFromParam falls back to :id, and the gate authorizes
		// whatever that segment names. A route constraint on its own
		// (":name<guid>") never matches an empty segment; it is refused for a
		// different reason — the schema is where a value's shape is declared,
		// so a constraint adds nothing it cannot — and no route needs either.
		return fmt.Errorf("endpoint %s %s uses %q in its path; an optional parameter can match an empty "+
			"segment, and the parameter schema, not a route constraint, is where a value's shape is declared",
			e.Method, e.Path, string(e.Path[i]))
	}

	for _, name := range e.pathParams {
		prop, ok := e.Parameters[name]
		if !ok {
			return fmt.Errorf("endpoint %s %s has path parameter %q with no matching entry in Parameters",
				e.Method, e.Path, name)
		}
		if prop.Source != apischema.SourceAuto && prop.Source != apischema.SourcePath {
			return fmt.Errorf("endpoint %s %s declares parameter %q with source %q, but :%s is a path parameter — "+
				"the permission middleware always reads it from the path, so the two would disagree",
				e.Method, e.Path, name, prop.Source, name)
		}
		if prop.Alias != "" {
			return fmt.Errorf("endpoint %s %s declares an alias %q on the path parameter %q; "+
				"a URL cannot carry a second spelling of a path segment",
				e.Method, e.Path, prop.Alias, name)
		}
	}

	for _, name := range slices.Sorted(maps.Keys(e.Parameters)) {
		prop := e.Parameters[name]
		src := apischema.ResolveSource(name, prop, e.Method, e.pathParams)

		if prop.Source == apischema.SourcePath && !slices.Contains(e.pathParams, name) {
			return fmt.Errorf("endpoint %s %s declares parameter %q as a path parameter, but the path has no :%s",
				e.Method, e.Path, name, name)
		}

		for _, gate := range gateParamNames {
			if !strings.EqualFold(name, gate) || src == apischema.SourcePath {
				continue
			}
			return fmt.Errorf("endpoint %s %s declares %q as a %s parameter, but the permission middleware "+
				"resolves the cluster from the path parameter of that name — the gate and the handler would "+
				"act on different clusters; name it something else if it means a different cluster",
				e.Method, e.Path, name, src)
		}

		// The same divergence reached through an ALIAS. The loop above
		// matches declared names, so a parameter honestly called
		// target_cluster_id that ALSO answers to "cluster_id" walks past
		// it — and the caller, not the declaration, chooses which spelling
		// to send.
		//
		// The reachable escalation is on a /api/v1/clusters/:id path:
		// clusterIDFromParam finds no "cluster_id" segment, falls back to
		// c.Params("id") and authorizes cluster A, while the body's
		// "cluster_id" binds through the alias and the handler acts on
		// cluster B. A caller holding manage on A alone then operates on B.
		//
		// Only "cluster_id" is refused, and only where a cluster-scoped
		// gate actually runs. The narrowness is deliberate on both counts:
		//   - "id" stays legal as an alias because it does not claim to
		//     mean a cluster. registry_replication.go ships job_id with
		//     Alias "id", which is safe — :cluster_id is in that path, so
		//     the gate never reaches the fallback — and a blanket refusal
		//     would break an API spelling callers already use.
		//   - Deferred/Advisory/Public/SelfService install no middleware,
		//     so there is no gate to diverge from, and a global Check
		//     (RequirePermission) resolves no cluster either. Every
		//     cluster_id alias declared today sits on a Deferred, Advisory,
		//     SelfService or global-Check route — MapPlatform's
		//     platform_cluster_id is the global-Check one.
		if strings.EqualFold(prop.Alias, "cluster_id") && src != apischema.SourcePath && e.runsClusterGate() {
			return fmt.Errorf("endpoint %s %s declares parameter %q with the alias %q, which a caller may send "+
				"as a %s value — but the permission middleware resolves the cluster from the path, so the gate "+
				"and the handler would act on different clusters; choose an alias that does not claim to name a cluster",
				e.Method, e.Path, name, prop.Alias, src)
		}
	}
	return nil
}

// runsClusterGate reports whether this endpoint mounts middleware that
// resolves a cluster out of the path. Only then can a body or query value
// disagree with what was authorized.
func (e Endpoint) runsClusterGate() bool {
	if e.Permissions.Check != nil && e.Permissions.Check.Scope == ScopeCluster {
		return true
	}
	for _, alt := range e.Permissions.Alternatives {
		if alt.Scope == ScopeCluster {
			return true
		}
	}
	return false
}

// isNodeNameProperty reports whether a declared property holds a Proxmox node
// name: the node-name format every StdOption("node-name") carries, or its
// sentinel twin emptyOrNodeName (registry_vms.go), whose empty string means
// "no node" rather than naming one.
func isNodeNameProperty(p apischema.Property) bool {
	return p.Format == "node-name" || p.Pattern == emptyOrNodeName
}

// holdsNodeNames reports whether a declared property carries Proxmox node
// names: one, or — for an array such as a rolling job's nodes — one per
// element.
func holdsNodeNames(p apischema.Property) bool {
	if p.Type == apischema.Array && p.Items != nil {
		return isNodeNameProperty(*p.Items)
	}
	return isNodeNameProperty(p)
}

// checkedNodeParams returns, sorted, the parameters of e that carry a Proxmox
// node name and that serve checks against the nodes of the cluster the path
// names before it calls the handler: every one — path, query or body, a
// single name or an array of them — except the body parameters the endpoint
// declares in NodesCheckedByHandler.
//
// Nearly all of them become a Proxmox call that pveproxy forwards to the node
// by name — the {node} of a /nodes/{node}/… path, the check-node the two
// mapping listings send, the node a guest is created on or a backup restored
// to — at once, or later when a background engine replays what was stored (a
// schedule, the virtio-win config, a rolling job's nodes). pveproxy picks the
// host to forward to from the name before it validates it: a name that is not
// one of the cluster's, an IP address or an FQDN included, would have the node
// resolve and dial it and hand back what it found (see
// RequireNodesInCluster). Some only travel as a value Proxmox checks itself
// (a clone's or a migration's target, which qemu-server and pve-container
// hold to PVE::Cluster::check_node_exists before anything looks the name up)
// or stay in Nexara (a DRS rule's nodes);
// they are checked all the same, since a valid one must be a member anyway,
// and one rule with no exceptions is one nobody has to remember.
//
// The check is derived from the declaration rather than placed in each handler
// so that no handler can forget it. TestGuard_EveryRouteNamingANodeRefusesOneTheClusterDoesNotHold
// holds every route that names a node in its URL to carrying one, and
// TestGuard_EveryBodyNodeIsCheckedOrAccountedFor every body parameter spelled
// like a node.
//
// It is computed where it is used rather than cached by register, so that a
// test fixture appended to a Registry without Register cannot dodge it.
func (e Endpoint) checkedNodeParams() []string {
	var out []string
	for _, name := range slices.Sorted(maps.Keys(e.Parameters)) {
		if _, byHandler := e.NodesCheckedByHandler[name]; byHandler {
			continue
		}
		if holdsNodeNames(e.Parameters[name]) {
			out = append(out, name)
		}
	}
	return out
}

// urlNodeParams is the part of checkedNodeParams the URL carries: a path
// segment such as :node_name or :node, or a query parameter such as the
// firewall log's ?node=.
func (e Endpoint) urlNodeParams() []string {
	return e.nodeParamsFrom(apischema.SourcePath, apischema.SourceQuery)
}

// bodyNodeParams is the part of checkedNodeParams the request body carries.
func (e Endpoint) bodyNodeParams() []string {
	return e.nodeParamsFrom(apischema.SourceBody)
}

func (e Endpoint) nodeParamsFrom(sources ...apischema.Source) []string {
	var out []string
	for _, name := range e.checkedNodeParams() {
		if slices.Contains(sources, apischema.ResolveSource(name, e.Parameters[name], e.Method, e.pathParams)) {
			out = append(out, name)
		}
	}
	return out
}

// checkNodesCheckedByHandler refuses a NodesCheckedByHandler entry that names
// no body parameter holding a node name, or gives no reason. That only a
// Deferred route lists one, and that a body node serve checks sits behind a
// cluster-scoped gate, are held by TestGuard_EveryBodyNodeIsCheckedOrAccountedFor
// rather than here, because the parameter fixtures re-declare these routes as
// SelfService.
func (e Endpoint) checkNodesCheckedByHandler() error {
	for _, name := range slices.Sorted(maps.Keys(e.NodesCheckedByHandler)) {
		prop, declared := e.Parameters[name]
		switch {
		case !declared || !holdsNodeNames(prop):
			return fmt.Errorf("endpoint %s %s lists %q in NodesCheckedByHandler, but declares no parameter by that "+
				"name holding a Proxmox node name", e.Method, e.Path, name)
		case apischema.ResolveSource(name, prop, e.Method, e.pathParams) != apischema.SourceBody:
			return fmt.Errorf("endpoint %s %s lists %q in NodesCheckedByHandler, but it is not a body parameter; "+
				"a node in the URL is always checked by serve", e.Method, e.Path, name)
		case strings.TrimSpace(e.NodesCheckedByHandler[name]) == "":
			return fmt.Errorf("endpoint %s %s lists %q in NodesCheckedByHandler with no reason",
				e.Method, e.Path, name)
		}
	}
	return nil
}

// pathParamNames returns the :param names in path, in order. Fiber allows
// several params in one segment (":a-:b") and suffixes on a param
// (":id<int>", ":name?"), so the name is read as the identifier run after
// the colon rather than as the rest of the segment. Wildcards are not
// named parameters and are skipped: Fiber calls them "*1", and nothing
// declares them.
func pathParamNames(path string) []string {
	var out []string
	for i := 0; i < len(path); i++ {
		if path[i] != ':' {
			continue
		}
		j := i + 1
		for j < len(path) && isParamNameByte(path[j]) {
			j++
		}
		if j > i+1 {
			out = append(out, path[i+1:j])
		}
		i = j - 1
	}
	return out
}

func isParamNameByte(b byte) bool {
	return b == '_' ||
		(b >= 'a' && b <= 'z') ||
		(b >= 'A' && b <= 'Z') ||
		(b >= '0' && b <= '9')
}

func sortedMethods() []string { return slices.Sorted(maps.Keys(knownMethods)) }

// mountRegistry attaches every endpoint in reg to router.
//
// The chain is authRequired -> rate limiter -> permission -> handler — the
// handler being serve, which validates the parameters and refuses a node the
// cluster does not hold before it calls the endpoint's Handler — and every
// link is attached PER ROUTE rather than through a Group. Fiber v3
// applies group middleware at match time, so it never appears in a
// route's Handlers slice — a permission attached to a group is invisible
// to route-table introspection and therefore unverifiable by a guard
// test, which is the whole reason the declaration is worth having. The
// same reasoning is spelled out at length on clusterCreateLimiter in
// middleware.go.
//
// The limiter sits AHEAD of the permission check on purpose: a caller
// hammering an endpoint they are not entitled to should spend their own
// bucket rather than collect free 403s at full rate.
//
// auth is the authentication middleware, passed in rather than taken off
// a Server so that this is testable against a bare fiber.App. It must not
// be nil: "no authentication middleware" would mount every endpoint that
// declared itself authenticated with no session check at all, silently,
// and a fail-open default is exactly what a declarative registry is here
// to remove. A test that wants unauthenticated routes passes a
// pass-through, which reads as the deliberate stub it is.
//
// nodes answers whether a node is one of a cluster's, for the endpoints
// that name a node (checkedNodeParams) — passed in for the same
// reason auth is. It must not be nil when any endpoint names one: a
// missing lookup is a boot failure, not a route that forwards whatever node
// it is given. It may be nil when none does. A test that is not about
// membership passes everyNodeIsAMember, which, like noAuth, says so.
func mountRegistry(router fiber.Router, reg *Registry, auth fiber.Handler, nodes handlers.NodeLookup) {
	if auth == nil {
		panic("api: mountRegistry needs an authentication middleware; pass an explicit pass-through if that is really the intent")
	}
	// Checked for every endpoint before any is mounted, so a refusal never
	// leaves half a route table behind it.
	if nodes == nil {
		for _, e := range reg.endpoints {
			if names := e.checkedNodeParams(); len(names) > 0 {
				panic(fmt.Sprintf("api: mountRegistry needs a node lookup: %s %s names a Proxmox node in %s, "+
					"which is checked against the cluster's nodes before its handler runs",
					e.Method, e.Path, strings.Join(names, ", ")))
			}
		}
	}
	for _, e := range reg.endpoints {
		// []any rather than []fiber.Handler because that is what Fiber
		// v3's Add takes; the elements are all fiber.Handler.
		chain := make([]any, 0, 4)
		if e.Permissions.authenticated() {
			chain = append(chain, auth)
		}
		if e.RateLimiter != nil {
			chain = append(chain, e.RateLimiter)
		}
		if perm := e.Permissions.middleware(); perm != nil {
			chain = append(chain, perm)
		}
		chain = append(chain, e.serve(nodes, e.checkedNodeParams()))
		router.Add([]string{e.Method}, e.Path, chain[0], chain[1:]...)
	}
}

// serve adapts the endpoint's Handler to a fiber.Handler: extract, then
// validate, then refuse a node the cluster does not hold, then call.
//
// The node check sits here, between validation and the handler, rather than
// in a middleware link of its own, because here it reads the very Params the
// handler is handed. A link ahead of serve would have to read the request a
// second time — the path, the query string, a repeated key, an alias — and a
// check that reads a request differently from the code that acts on it is a
// check an attacker can shape a request around. Here the node checked is, by
// construction, the node the handler forwards. It still runs after the
// permission gate, which is route middleware ahead of serve, so a caller the
// gate refuses learns nothing about which nodes exist; and a malformed name is
// still the validator's 400, never a lookup.
//
// nodeNames is e.checkedNodeParams(), computed once by mountRegistry.
func (e Endpoint) serve(nodes handlers.NodeLookup, nodeNames []string) fiber.Handler {
	return func(c fiber.Ctx) error {
		raw, err := e.extract(c)
		if err != nil {
			return err
		}
		params, err := e.Parameters.Validate(raw)
		if err != nil {
			return e.validationError(err)
		}
		if len(nodeNames) > 0 {
			if err := handlers.RequireNodesInCluster(c, nodes, e.namedNodes(params, nodeNames)); err != nil {
				return err
			}
		}
		return e.call(c, params)
	}
}

// namedNodes returns the node names params holds for names — every element of
// an array — skipping empty ones: "" is emptyOrNodeName's "no node", which
// names nothing to check. The values are read with the accessor a handler
// reads them with, defaults included, so every name the handler can forward
// is a name that was checked.
func (e Endpoint) namedNodes(params *apischema.Params, names []string) []string {
	out := make([]string, 0, len(names))
	for _, name := range names {
		values := []string{}
		if e.Parameters[name].Type == apischema.Array {
			values = params.Strings(name)
		} else {
			values = append(values, params.String(name))
		}
		for _, v := range values {
			if v != "" {
				out = append(out, v)
			}
		}
	}
	return out
}

// call runs the handler and makes sure a panic inside it leaves a trace.
//
// The Params accessors panic by design on a handler bug — an undeclared
// key, or the accessor for the wrong type — because a silent zero value
// is the failure apischema exists to prevent. That reaches the app-level
// recover middleware, which is configured with EnableStackTrace false,
// and errorHandler then drops everything that is not a *fiber.Error. The
// operator gets a bare 500 and the server records nothing: the exact
// outcome validationError goes out of its way to avoid one branch up.
//
// The panic is re-raised rather than swallowed, so the response is
// unchanged and recover keeps its single responsibility. All this adds is
// the record of which endpoint died and where.
func (e Endpoint) call(c fiber.Ctx, params *apischema.Params) error {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("registry handler panicked",
				"method", e.Method, "path", e.Path,
				"panic", r, "stack", string(debug.Stack()))
			panic(r)
		}
	}()
	return e.Handler(c, params)
}

// validationError splits the caller's mistakes from ours.
//
// A *apischema.ValidationError is something about the REQUEST: it names
// the field, so it goes back as a 400 reading "field: message" through
// the standard envelope, the way mapProxmoxError hands Proxmox's own
// sentence back for a parameter rejection.
//
// Anything else is a defect in the DECLARATION — an unregistered format,
// a default that fails its own rules — which Compile should have caught
// at registration and which no caller can fix. It is a 500, the detail is
// logged server-side, and the client is told nothing about our schema:
// the message would name internal parameter plumbing to whoever happened
// to send the request.
func (e Endpoint) validationError(err error) error {
	var verr *apischema.ValidationError
	if errors.As(err, &verr) {
		return fiber.NewError(fiber.StatusBadRequest, verr.Error())
	}
	slog.Error("endpoint parameter schema is invalid",
		"method", e.Method, "path", e.Path, "error", err)
	return fiber.NewError(fiber.StatusInternalServerError, "Request validation failed")
}
