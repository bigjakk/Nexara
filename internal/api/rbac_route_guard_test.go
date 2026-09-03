package api

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/bigjakk/nexara/internal/api/handlers"
)

// This file is the RBAC counterpart to
// internal/api/handlers/tracktask_guard_test.go: it turns "every endpoint
// checks permissions" from a convention maintained by review into an
// invariant the build enforces.
//
// Authorization here is 438 hand-placed require*Perm calls in handler bodies;
// the router applies only authRequired. A route whose handler forgets the call
// ships as authenticated-but-unauthorized-accessible, and nothing fails. That
// is not hypothetical — console-token minting shipped gated on `view`, which
// every default Viewer holds, so a read-only account could open a root shell
// on a hypervisor (fixed in 17a7cc0).
//
// The guard resolves each registered route to the function that serves it,
// then walks the handlers package's call graph to decide whether that function
// can reach a permission check at all.
//
// LIMITATION, stated plainly: this proves a permission check is REACHABLE from
// the handler, not that it executes on every path. A check behind a condition
// counts as reached — settingScopeID (handlers/settings.go) calls requirePerm
// only when write is true, so the settings READ routes pass this guard while
// performing no check on the read path. Catching that class needs type-aware
// dataflow (golang.org/x/tools/go/packages + types.Info), which is a much
// larger dependency. What this catches is the class that actually shipped a
// vulnerability: a handler with no check at all.

// permissionLeaves are the package-level functions that actually consult the
// RBAC engine. Reaching any of them counts as enforcing.
//
// engineFromContext is deliberately NOT a leaf: it only fetches the engine and
// user id, and counting it would let any function that merely touches request
// locals pass as authorized.
var permissionLeaves = map[string]bool{
	"requirePerm":        true,
	"requireClusterPerm": true,
	"requirePBSPerm":     true,
	"hasClusterPerm":     true,
	"hasGlobalPerm":      true,
	"accessibleClusters": true,
}

// knownActions is the action vocabulary of the permission catalog (see
// migrations/000016_rbac.up.sql and the per-feature permission migrations).
// It disambiguates the action argument from the other string literals a
// permission helper may take.
var knownActions = map[string]bool{
	"view": true, "manage": true, "delete": true, "execute": true,
	"acknowledge": true, "generate": true, "console": true,
}

// publicRoutes is the complete set of routes served WITHOUT authRequired.
// Whether a route is authenticated is read from its actual middleware chain,
// not from this list — the list exists so that making a route public becomes a
// deliberate, reviewed edit here rather than an omission nobody notices.
//
// Every entry states why the route can be reached with no session.
var publicRoutes = map[string]string{
	"POST /api/v1/auth/login":               "issues the session",
	"POST /api/v1/auth/refresh":             "exchanges the refresh cookie for a new access token",
	"POST /api/v1/auth/logout":              "authOptional: a valid refresh cookie must be able to revoke its session after the access token expires",
	"POST /api/v1/auth/register":            "authOptional: the first user claims admin; afterwards the handler itself requires an admin caller",
	"GET /api/v1/auth/setup-status":         "login page asks whether an admin exists yet",
	"GET /api/v1/auth/sso-status":           "login page asks whether SSO is configured; returns only a bool and the provider display name",
	"GET /api/v1/auth/oidc/authorize":       "starts the OIDC redirect flow, before any identity exists",
	"GET /api/v1/auth/oidc/callback":        "OIDC provider callback",
	"POST /api/v1/auth/oidc/token-exchange": "exchanges the OIDC one-time code for tokens",
	"POST /api/v1/auth/totp/verify-login":   "second factor of an in-progress login",
	"GET /api/v1/version":                   "build version; the SPA reads it before login to render the shell",
	"GET /api/v1/changelog":                 "release notes proxied from the public GitHub releases feed",
	"GET /healthz":                          "container health check; must answer before the app is ready to authenticate",
}

// instanceSharedRoutes are authenticated routes returning instance-level data
// that is identical for every caller and contains no tenant information. There
// is no subject to authorize.
//
// Kept separate from selfServiceRoutes deliberately: lumping them in would
// make that list's stated invariant ("acts on the caller's own identity")
// false, which is how exemption lists rot into rubber stamps.
var instanceSharedRoutes = map[string]string{
	"GET /api/v1/api-docs":                       "route catalog derived from the router; no tenant data",
	"GET /api/v1/settings/branding":              "instance branding (title/logo/favicon) rendered in every signed-in SPA; reserved keys are filtered out",
	"GET /api/v1/settings/branding/logo-file":    "serves the instance branding asset; the filename is server-chosen, never caller-supplied",
	"GET /api/v1/settings/branding/favicon-file": "serves the instance branding asset; the filename is server-chosen, never caller-supplied",
}

// selfServiceRoutes are authenticated routes that act solely on the caller's
// own identity, taken from the session rather than from user input. A
// permission check would be meaningless — a user cannot be denied access to
// their own profile — but each must be listed with a reason so the set stays
// small and reviewed, and so an IDOR (acting on a subject named in the path)
// cannot hide here.
var selfServiceRoutes = map[string]string{
	"GET /api/v1/auth/me":                              "returns the caller's own profile",
	"PUT /api/v1/auth/profile":                         "updates the caller's own profile",
	"POST /api/v1/auth/change-password":                "changes the caller's own password",
	"POST /api/v1/auth/logout-all":                     "revokes the caller's own sessions",
	"POST /api/v1/auth/ws-token":                       "mints a hub token for the caller; per-cluster authorization is enforced at subscribe time in internal/ws",
	"POST /api/v1/auth/totp/setup":                     "enrolls the caller's own second factor",
	"POST /api/v1/auth/totp/setup/verify":              "confirms the caller's own enrollment",
	"DELETE /api/v1/auth/totp":                         "disables the caller's own second factor",
	"GET /api/v1/auth/totp/status":                     "reports the caller's own enrollment",
	"POST /api/v1/auth/totp/recovery-codes/regenerate": "regenerates the caller's own recovery codes",
	"GET /api/v1/rbac/me/permissions":                  "returns the caller's own grants",
	"POST /api/v1/api-keys":                            "manages the caller's own API keys",
	"GET /api/v1/api-keys":                             "manages the caller's own API keys",
	"DELETE /api/v1/api-keys":                          "manages the caller's own API keys",
	"DELETE /api/v1/api-keys/:id":                      "manages the caller's own API keys",
}

// publicRouteKeys parses router.go and returns the "METHOD path" keys of every
// route registered WITHOUT authRequired.
//
// This is read from the source rather than the runtime route table because
// Fiber v3 applies group-level middleware at match time — `v1.Group("/clusters",
// s.authRequired())` never appears in the child routes' Handlers slice, so
// runtime introspection reports every cluster route as unauthenticated.
//
// Groups are resolved transitively (totpGroup inherits authGroup's middleware),
// and a route counts as authenticated if its own registration passes
// s.authRequired() even when its group does not.
func publicRouteKeys(t *testing.T) map[string]bool {
	t.Helper()

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "router.go", nil, 0)
	if err != nil {
		t.Fatalf("parse router.go: %v", err)
	}

	type group struct {
		path   string
		authed bool
	}
	groups := map[string]group{}
	public := map[string]bool{}

	hasAuthRequired := func(args []ast.Expr) bool {
		for _, arg := range args {
			call, ok := arg.(*ast.CallExpr)
			if !ok {
				continue
			}
			if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "authRequired" {
				return true
			}
		}
		return false
	}
	literal := func(e ast.Expr) (string, bool) {
		lit, ok := e.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return "", false
		}
		v, err := strconv.Unquote(lit.Value)
		return v, err == nil
	}

	methods := map[string]bool{
		"Get": true, "Post": true, "Put": true, "Delete": true,
		"Patch": true, "Head": true, "Options": true,
	}

	// Single ordered pass: Go requires a group to be declared before use, so
	// assignments are always seen before the routes registered on them.
	ast.Inspect(file, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.AssignStmt:
			if len(node.Lhs) != 1 || len(node.Rhs) != 1 {
				return true
			}
			name, ok := node.Lhs[0].(*ast.Ident)
			if !ok {
				return true
			}
			call, ok := node.Rhs[0].(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "Group" || len(call.Args) == 0 {
				return true
			}
			path, ok := literal(call.Args[0])
			if !ok {
				return true
			}
			parent := group{}
			if base, ok := sel.X.(*ast.Ident); ok {
				parent = groups[base.Name]
			}
			// s.app.Group(...) roots at "" with no middleware.
			groups[name.Name] = group{
				path:   parent.path + path,
				authed: parent.authed || hasAuthRequired(call.Args[1:]),
			}

		case *ast.CallExpr:
			sel, ok := node.Fun.(*ast.SelectorExpr)
			if !ok || !methods[sel.Sel.Name] || len(node.Args) == 0 {
				return true
			}
			var g group
			switch base := sel.X.(type) {
			case *ast.Ident:
				known := false
				if g, known = groups[base.Name]; !known {
					return true
				}
			case *ast.SelectorExpr:
				// Routes hung straight off the Fiber app (s.app.Get("/healthz", …)):
				// root path, no middleware, so they are public by construction.
				if base.Sel.Name != "app" {
					return true
				}
			default:
				return true
			}
			sub, ok := literal(node.Args[0])
			if !ok {
				return true
			}
			if g.authed || hasAuthRequired(node.Args[1:]) {
				return true
			}
			public[strings.ToUpper(sel.Sel.Name)+" "+normalizeRoutePath(g.path+sub)] = true
		}
		return true
	})

	return public
}

// callGraph maps a function key to the keys it calls. Methods are keyed
// "TypeName.Method"; package-level functions by bare name.
type callGraph struct {
	calls map[string]map[string]bool
	// literalActions records the string-literal action arguments each function
	// passes to a permission leaf, used by the drift check below.
	literalActions map[string]map[string]bool
}

// buildCallGraph parses the handlers package and records, per function, both
// the functions it calls and the literal action strings it gates on.
func buildCallGraph(t *testing.T) *callGraph {
	t.Helper()

	fset := token.NewFileSet()

	// Explicit glob rather than parser.ParseDir (deprecated in Go 1.25): this
	// package has no build-tagged files, so the file set is simply every
	// non-test .go file in handlers/.
	paths, err := filepath.Glob(filepath.Join("handlers", "*.go"))
	if err != nil {
		t.Fatalf("glob handlers package: %v", err)
	}

	files := make([]*ast.File, 0, len(paths))
	for _, path := range paths {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		parsed, parseErr := parser.ParseFile(fset, path, nil, 0)
		if parseErr != nil {
			t.Fatalf("parse %s: %v", path, parseErr)
		}
		files = append(files, parsed)
	}

	g := &callGraph{
		calls:          map[string]map[string]bool{},
		literalActions: map[string]map[string]bool{},
	}

	{
		for _, file := range files {
			for _, decl := range file.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok {
					continue
				}
				key := funcKey(fn)
				if g.calls[key] == nil {
					g.calls[key] = map[string]bool{}
				}

				recvType := receiverType(fn)
				ast.Inspect(fn.Body, func(n ast.Node) bool {
					call, ok := n.(*ast.CallExpr)
					if !ok {
						return true
					}
					callee := calleeKey(call.Fun, recvType)
					if callee == "" {
						return true
					}
					g.calls[key][callee] = true

					// Record the literal action passed to a permission leaf.
					// Argument order differs between the package-level helpers
					// (c, action, resource, …) and the PBS wrapper
					// (c, pbsID, action), so scan every string-literal
					// argument and keep the ones that name a known action.
					if isPermissionLeaf(callee) {
						for _, arg := range call.Args {
							lit, ok := arg.(*ast.BasicLit)
							if !ok || lit.Kind != token.STRING {
								continue
							}
							val, err := strconv.Unquote(lit.Value)
							if err != nil || !knownActions[val] {
								continue
							}
							if g.literalActions[key] == nil {
								g.literalActions[key] = map[string]bool{}
							}
							g.literalActions[key][val] = true
						}
					}
					return true
				})
			}
		}
	}
	return g
}

// receiverType returns the receiver's type name for a method, "" for a
// package-level function.
func receiverType(fn *ast.FuncDecl) string {
	if fn.Recv == nil || len(fn.Recv.List) == 0 {
		return ""
	}
	typ := fn.Recv.List[0].Type
	if star, ok := typ.(*ast.StarExpr); ok {
		typ = star.X
	}
	if ident, ok := typ.(*ast.Ident); ok {
		return ident.Name
	}
	return ""
}

// funcKey names a declaration: "TypeName.Method" for methods, bare name
// otherwise.
func funcKey(fn *ast.FuncDecl) string {
	if recv := receiverType(fn); recv != "" {
		return recv + "." + fn.Name.Name
	}
	return fn.Name.Name
}

// calleeKey names a call target the same way funcKey names a declaration.
//
// Resolution is receiver-aware: inside a method on VMHandler, `h.resolveVM`
// resolves to "VMHandler.resolveVM" specifically, not to any method named
// resolveVM on any type. Being loose here silently defeated the guard — a
// handler whose own check was deleted still passed because some unrelated
// same-named method elsewhere reached a leaf.
//
// Selectors whose base is not the receiver (h.queries.GetVM, c.Params) are
// calls into other packages and cannot reach a permission leaf, so they
// resolve to "" and are ignored.
func calleeKey(fun ast.Expr, recvType string) string {
	switch f := fun.(type) {
	case *ast.Ident:
		// Bare call: a package-level function in this package.
		return f.Name
	case *ast.SelectorExpr:
		base, ok := f.X.(*ast.Ident)
		if !ok {
			return ""
		}
		// A method on this function's own receiver.
		if recvType != "" && isReceiverName(base.Name) {
			return recvType + "." + f.Sel.Name
		}
		return ""
	}
	return ""
}

// isReceiverName reports whether an identifier is the conventional receiver
// name in this codebase. Handlers uniformly use `h`; `s` appears on the
// Server type in package api.
func isReceiverName(name string) bool {
	return name == "h" || name == "s"
}

// isPermissionLeaf matches both the package-level helpers and the
// receiver-qualified wrappers around them (BackupHandler.requirePBSPerm).
func isPermissionLeaf(callee string) bool {
	if permissionLeaves[callee] {
		return true
	}
	if _, method, found := strings.Cut(callee, "."); found {
		return permissionLeaves[method]
	}
	return false
}

// reachesPermissionCheck reports whether fn transitively calls a permission
// leaf, so a handler that delegates its gate to a helper on the same type
// still counts as enforcing.
func (g *callGraph) reachesPermissionCheck(fn string) bool {
	seen := map[string]bool{}
	var walk func(string) bool
	walk = func(key string) bool {
		if seen[key] {
			return false
		}
		seen[key] = true
		for callee := range g.calls[key] {
			if isPermissionLeaf(callee) || walk(callee) {
				return true
			}
		}
		return false
	}
	return walk(fn)
}

// literalActionsFor collects the gated actions reachable from fn, following
// the same call graph so a handler that delegates still reports them.
func (g *callGraph) literalActionsFor(fn string) map[string]bool {
	out := map[string]bool{}
	seen := map[string]bool{}
	var walk func(string)
	walk = func(key string) {
		if seen[key] {
			return
		}
		seen[key] = true
		for action := range g.literalActions[key] {
			out[action] = true
		}
		for callee := range g.calls[key] {
			walk(callee)
		}
	}
	walk(fn)
	return out
}

// routeHandlerKey resolves the terminal handler of a route to a call-graph
// key. Fiber stores the middleware chain plus the handler; the last entry is
// the handler. Runtime names look like
// ".../internal/api/handlers.(*VMHandler).PerformAction-fm".
func routeHandlerKey(h any) string {
	full := runtime.FuncForPC(reflect.ValueOf(h).Pointer()).Name()
	full = strings.TrimSuffix(full, "-fm")

	idx := strings.LastIndex(full, "/handlers.")
	if idx == -1 {
		return ""
	}
	name := full[idx+len("/handlers."):]
	name = strings.ReplaceAll(name, "(*", "")
	name = strings.ReplaceAll(name, ")", "")
	return name
}

// normalizeRoutePath mirrors GetDocs' trailing-slash handling so route keys
// match the curated endpointMeta keys.
func normalizeRoutePath(path string) string {
	if len(path) > len("/api/v1/") && strings.HasSuffix(path, "/") {
		return strings.TrimSuffix(path, "/")
	}
	return path
}

// TestGuard_EveryRouteEnforcesPermission is the invariant: every registered
// route either performs an RBAC check or appears in one of the two exemption
// lists above with a stated reason.
//
// If this fails for a route you just added, add the permission check to the
// handler — do not add the route to an exemption list unless it is genuinely
// unauthenticated or acts solely on the caller's own identity.
func TestGuard_EveryRouteEnforcesPermission(t *testing.T) {
	s := newRouteStubServer(t)

	graph := buildCallGraph(t)

	public := publicRouteKeys(t)

	var unguarded []string
	for _, r := range s.app.GetRoutes(true) {
		if r.Method == "USE" || len(r.Handlers) == 0 {
			continue
		}
		key := r.Method + " " + normalizeRoutePath(r.Path)
		if public[key] {
			continue // no identity to authorize; covered by TestGuard_PublicRoutesAreExpected
		}
		if _, ok := selfServiceRoutes[key]; ok {
			continue
		}
		if _, ok := instanceSharedRoutes[key]; ok {
			continue
		}

		handlerKey := routeHandlerKey(r.Handlers[len(r.Handlers)-1])
		if handlerKey == "" {
			continue // not a handlers-package function (frontend catch-all, etc.)
		}
		if !graph.reachesPermissionCheck(handlerKey) {
			unguarded = append(unguarded, key+"  →  handlers."+handlerKey)
		}
	}

	sort.Strings(unguarded)
	for _, u := range unguarded {
		t.Errorf("authenticated route performs no RBAC check: %s", u)
	}
	if len(unguarded) > 0 {
		t.Logf("%d unguarded route(s). Add a require*Perm call to the handler, or — "+
			"only if the route acts solely on the caller's own identity taken from the "+
			"session — list it in selfServiceRoutes with a reason.", len(unguarded))
	}
}

// TestGuard_PublicRoutesAreExpected pins the set of routes served without
// authRequired. A route that loses its auth middleware — or a new one that
// never had it — becomes exposed to the internet with no session, and today
// nothing would notice. This makes that a build failure, and makes going
// public a deliberate edit to publicRoutes with a stated reason.
func TestGuard_PublicRoutesAreExpected(t *testing.T) {
	s := newRouteStubServer(t)

	registered := map[string]bool{}
	for _, r := range s.app.GetRoutes(true) {
		if r.Method == "USE" || len(r.Handlers) == 0 {
			continue
		}
		registered[r.Method+" "+normalizeRoutePath(r.Path)] = true
	}

	actual := map[string]bool{}
	for key := range publicRouteKeys(t) {
		// Only routes that actually registered — router.go gates several
		// blocks on a handler being non-nil.
		if registered[key] {
			actual[key] = true
		}
	}

	for key := range actual {
		if _, expected := publicRoutes[key]; !expected {
			t.Errorf("route is served WITHOUT authentication and is not in publicRoutes: %s — "+
				"add s.authRequired() to its registration in router.go, or add it to "+
				"publicRoutes with the reason it must be reachable anonymously", key)
		}
	}
	for key := range publicRoutes {
		if !actual[key] {
			t.Errorf("publicRoutes lists %s but it is not registered as a public route — "+
				"remove the stale entry (a list that drifts stops being a review surface)", key)
		}
	}
}

// TestGuard_ExemptionKeysMatchRegisteredRoutes keeps the three exemption lists
// honest. A key that matches no route is dead weight that silently stops
// exempting anything — and worse, it hides the fact that some genuinely
// unguarded route is passing for an unrelated reason. Same guarantee
// TestEndpointMetaMatchesRegisteredRoutes gives the docs overlay.
func TestGuard_ExemptionKeysMatchRegisteredRoutes(t *testing.T) {
	s := newRouteStubServer(t)

	registered := map[string]bool{}
	for _, r := range s.app.GetRoutes(true) {
		if r.Method == "USE" || len(r.Handlers) == 0 {
			continue
		}
		registered[r.Method+" "+normalizeRoutePath(r.Path)] = true
	}

	for _, list := range []struct {
		name    string
		entries map[string]string
	}{
		{"publicRoutes", publicRoutes},
		{"selfServiceRoutes", selfServiceRoutes},
		{"instanceSharedRoutes", instanceSharedRoutes},
	} {
		for key, reason := range list.entries {
			if !registered[key] {
				t.Errorf("%s lists %q (%q) but no such route is registered — "+
					"fix the key or drop the entry", list.name, key, reason)
			}
			if strings.TrimSpace(reason) == "" {
				t.Errorf("%s entry %q has no reason; every exemption must say why", list.name, key)
			}
		}
	}
}

// TestGuard_DocumentedPermissionMatchesEnforcement catches the other half of
// the problem: a route that checks *something* but not what the API docs
// promise. endpointMeta's Permission field is what operators read when
// building roles, so drift there is a security-relevant lie — the console
// token endpoint documented view:vm long after it moved to console:vm.
//
// Only the action is compared. Resources are frequently computed (the console
// endpoint picks node/vm/container from the request), whereas the action is
// almost always a literal.
func TestGuard_DocumentedPermissionMatchesEnforcement(t *testing.T) {
	s := newRouteStubServer(t)

	graph := buildCallGraph(t)
	meta := handlers.EndpointMetaPermissions()

	for _, r := range s.app.GetRoutes(true) {
		if r.Method == "USE" || len(r.Handlers) == 0 {
			continue
		}
		key := r.Method + " " + normalizeRoutePath(r.Path)
		declared, ok := meta[key]
		if !ok || declared == "" {
			continue // auto-derived metadata; nothing curated to contradict
		}
		if _, exempt := publicRoutes[key]; exempt {
			continue
		}
		if _, exempt := selfServiceRoutes[key]; exempt {
			continue
		}

		handlerKey := routeHandlerKey(r.Handlers[len(r.Handlers)-1])
		if handlerKey == "" {
			continue
		}
		enforced := graph.literalActionsFor(handlerKey)
		if len(enforced) == 0 {
			continue // fully dynamic action; cannot verify statically
		}

		// The declared permission may offer alternatives ("view:vm|view:node").
		// At least one declared action must be among those enforced.
		matched := false
		for _, alt := range strings.Split(declared, "|") {
			action, _, found := strings.Cut(strings.TrimSpace(alt), ":")
			if !found {
				continue
			}
			if enforced[action] {
				matched = true
				break
			}
		}
		if !matched {
			t.Errorf("%s: API docs promise permission %q but handlers.%s gates on action(s) %v — "+
				"update endpointMeta in internal/api/handlers/api_docs.go to match the code",
				key, declared, handlerKey, sortedKeys(enforced))
		}
	}
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
