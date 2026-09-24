package proxmox

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// This is the counterpart to tracktask_guard_test.go over in internal/api/
// handlers, and it exists to close that guard's blind spot.
//
// The handlers guard starts from returnsStringError: it finds every *Client
// method declared `(string, error)`, forces someone to classify it, and forces
// the handler to TrackTask it. That enforces "if you have a UPID, track it" —
// but a method that SHOULD surface a UPID and declares plain `error` is
// invisible to it. There is nothing to classify, so nothing to track, and the
// handler falls back to AuditLog and reports success the instant the worker is
// queued. CreateCephPool and DeleteCephPool sat in exactly that hole: they
// passed nil as the response destination, threw both UPIDs away, and no guard
// on either side of the boundary had anything to say about it.
//
// So this one starts from the other end — the endpoint. PVE's dispatcher is the
// authority on which paths fork a worker, and that fact cannot be derived from
// Go, so it is recorded below by hand. What the test then enforces mechanically
// is the part that does drift: that a mutating request to one of those paths is
// written by a method that returns the UPID rather than discarding it.
//
// For an endpoint on the list below, the two guards then close the loop:
//
//	this test:      hits a worker endpoint  ⇒  must return (string, error)
//	handlers guard: returns (string, error) ⇒  must be classified, and if it is
//	                                           a UPID method, TrackTask'd
//
// Only for an endpoint on the list, though. This cannot catch the FIRST
// instance of the bug class at an unlisted path, because adding the entry
// requires already knowing the endpoint forks a worker — the very knowledge
// whose absence causes the bug. It is a regression guard, not a discovery tool.

// everyMutatingVerb lists the apiClient helpers that issue a state-changing
// request, and is the `verbs` value for the common path where all mutation
// forks a worker. A method that only calls `do` (GET) is reading, and a read of
// a worker-backed path — GetCephPools on /ceph/pool — returns data, not a UPID.
var everyMutatingVerb = []string{"doPost", "doPostRaw", "doPut", "doDelete"}

// mutatingHelpers is everyMutatingVerb as a set, derived rather than written
// out again so the two cannot drift apart.
var mutatingHelpers = func() map[string]bool {
	m := make(map[string]bool, len(everyMutatingVerb))
	for _, v := range everyMutatingVerb {
		m[v] = true
	}
	return m
}()

// upidEndpoint describes one PVE path whose work is handed to a fork_worker.
type upidEndpoint struct {
	// fragments are string literals that must ALL appear in a method body for
	// it to count as touching this path.
	//
	// A set rather than one string because most paths are assembled from
	// several literals with interpolated ids between them — "/nodes/" + node +
	// "/qemu/" + vmid + "/config" — so no single literal identifies the path.
	// Requiring the whole set is also what keeps a loose fragment honest:
	// "/config" alone would match /cluster/config and /nodes/{node}/config,
	// neither of which forks anything.
	fragments []string

	// verbs are the mutating helpers that fork a worker AT THIS PATH.
	//
	// Usually that is every one of them, but not always, and the exception is
	// not exotic: PVE registers PUT on /qemu/{vmid}/config as the synchronous
	// API and POST on the same path as the asynchronous one. A verb-blind list
	// cannot express that — it would flag the correct synchronous method and
	// the false positive would get the entry deleted, which is how a guard
	// stops guarding.
	verbs []string

	// why records the basis for the claim, so a future reader can check it
	// against pve-manager rather than trusting the list.
	why string
}

// The two signals — "calls a mutating helper" and "mentions the fragments" —
// are gathered over the whole method body rather than from the helper's own
// path argument, because most methods assemble the path into a local first
// (`path := "/nodes/" + … + "/ceph/pool"`) and the argument is then just an
// identifier. The looseness is one-directional: a method that reads a listed
// path and mutates some OTHER path in the same body would be flagged wrongly.
// No method is shaped that way today; if one appears, split it.

// upidEndpoints are the PVE API paths whose mutating verbs hand the work to a
// fork_worker and answer with a task UPID, keyed by the path as PVE spells it.
//
// This list is not exhaustive over PVE and does not try to be — an entry earns
// its place when getting it wrong has already cost something, or when the path
// is spelled as literals distinctive enough to match on. Add to it freely.
var upidEndpoints = map[string]upidEndpoint{
	// PVE::API2::Ceph registers PVE::API2::Ceph::Pool at path 'pool'; its
	// createpool and destroypool fork cephcreatepool / cephdestroypool.
	"/nodes/{node}/ceph/pool": {
		fragments: []string{"/ceph/pool"},
		verbs:     everyMutatingVerb,
		why:       "createpool/destroypool fork a worker",
	},

	// PVE::API2::Qemu registers BOTH verbs on this one path: update_vm (PUT) is
	// the synchronous API and runs the update inline, update_vm_async (POST)
	// forks a qmconfig worker and returns its UPID. They share one body, so
	// hotplug behaves identically — the verb chooses only whether the caller
	// waits. That makes POST-with-a-discarded-UPID the whole bug class in a
	// single method, which is what this entry exists to keep out.
	"/nodes/{node}/qemu/{vmid}/config": {
		fragments: []string{"/qemu/", "/config"},
		verbs:     []string{"doPost", "doPostRaw"},
		why:       "POST is update_vm_async and forks a worker; PUT is the synchronous update_vm",
	},

	// PVE::API2::Disks and its subclasses run every mutating operation as a
	// task; Nexara has captured their UPIDs since the endpoints were added.
	"/nodes/{node}/disks/wipedisk": {
		fragments: []string{"/disks/wipedisk"},
		verbs:     everyMutatingVerb,
		why:       "wiping a disk runs as a task",
	},
	"/nodes/{node}/disks/initgpt": {
		fragments: []string{"/disks/initgpt"},
		verbs:     everyMutatingVerb,
		why:       "initialising a GPT runs as a task",
	},
	"/nodes/{node}/disks/zfs": {
		fragments: []string{"/disks/zfs"},
		verbs:     everyMutatingVerb,
		why:       "ZFS pool create/destroy run as tasks",
	},
	"/nodes/{node}/disks/lvm": {
		fragments: []string{"/disks/lvm"},
		verbs:     everyMutatingVerb,
		why:       "LVM and LVM-thin create/destroy run as tasks",
	},
	"/nodes/{node}/disks/directory": {
		fragments: []string{"/disks/directory"},
		verbs:     everyMutatingVerb,
		why:       "directory create/destroy run as tasks",
	},

	// PVE::API2::VZDump's vzdump forks a 'vzdump' worker — or answers "OK"
	// when the node holds none of the guests it was asked for, which is why
	// the reply has to be read rather than dropped: it is the only thing that
	// says which of the two happened. TriggerBackup and startVzdump (behind
	// RunBackupJob) both post here.
	"/nodes/{node}/vzdump": {
		fragments: []string{"/vzdump"},
		verbs:     everyMutatingVerb,
		why:       "vzdump forks a backup worker, or answers \"OK\" when it has nothing to back up",
	},
}

func TestGuard_WorkerEndpointsSurfaceTheUPID(t *testing.T) {
	fset, files := parsePackage(t)

	// Anchor the vocabulary before anything is checked against it. The entry
	// precheck below rejects a verb that is not in everyMutatingVerb, but that
	// only pushes the same silent-disarm problem up one level: a typo in the
	// LIST is checked by nothing, and every entry naming the misspelt verb goes
	// inert while its coverage stays green off a sibling method. "doDeleet" is
	// the dangerous one in practice — each DELETE-side entry here is covered by
	// its create counterpart, so nothing anywhere would go red.
	//
	// The package's own AST is the authority, so ask it rather than restating
	// the list a third time.
	declared := methodsOn(files, "apiClient")
	for _, v := range everyMutatingVerb {
		if !declared[v] {
			t.Fatalf("everyMutatingVerb names %q, which apiClient does not declare. Every entry "+
				"using it is inert, and nothing downstream can tell that from \"no violations\".", v)
		}
	}

	// An entry with no fragments would match every method in the package and
	// bury the real output, so refuse it up front rather than diagnose it later.
	for _, name := range sortedEndpoints() {
		if len(upidEndpoints[name].fragments) == 0 {
			t.Fatalf("upidEndpoints[%q] lists no fragments, so it would match every method", name)
		}
		if len(upidEndpoints[name].verbs) == 0 {
			t.Fatalf("upidEndpoints[%q] lists no verbs, so it can never flag anything", name)
		}
		// A verb is matched by name against the helpers a body calls, so a typo
		// in one does not fail — it just never matches, and the entry goes
		// quietly inert while a sibling method on a non-forking verb keeps the
		// coverage check below green. That is the same silent-disarm shape the
		// per-entry coverage check exists to prevent, one level down, and it is
		// only catchable here: nothing downstream can tell "no violations" from
		// "no longer looking".
		for _, v := range upidEndpoints[name].verbs {
			if !mutatingHelpers[v] {
				t.Fatalf("upidEndpoints[%q] lists verb %q, which is not an apiClient mutating "+
					"helper (%s). It can never match, so the entry guards nothing.",
					name, v, strings.Join(everyMutatingVerb, ", "))
			}
		}
	}

	covered := make(map[string]bool, len(upidEndpoints))
	for _, file := range files {
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil || !receiverIs(fn.Recv, "Client") {
				continue
			}

			verbs, literals := scanBody(fn.Body)
			if len(verbs) == 0 {
				continue // a pure read; a GET of a worker path returns data, not a UPID
			}

			// Every match, not just the first: one method can legitimately sit
			// under two entries, and stopping early would leave the second
			// looking uncovered.
			for _, name := range sortedEndpoints() {
				ep := upidEndpoints[name]
				if !mentionsAll(literals, ep.fragments) {
					continue
				}

				// Coverage is "the fragments reach a real mutating method",
				// deliberately not "…reach a forking one". Since verbs became
				// per-entry, an entry can correctly have zero forking callers —
				// /qemu/{vmid}/config is exactly that once the async variant is
				// gone — and that state is the goal, not a misspelling. What a
				// sibling PUT method still proves is the only thing this check
				// can honestly assert: that the fragments match live code.
				covered[name] = true

				forking := forkingVerbsUsed(verbs, ep.verbs)
				if len(forking) == 0 || returnsUPIDAndError(fn.Type) {
					continue
				}
				t.Errorf("%s: %s calls %s against %s, where %s, but returns error alone.\n"+
					"\tPVE answers with a task UPID and the operation has NOT finished when the "+
					"request returns. Either return (string, error), pass the UPID as the response "+
					"destination instead of nil, and TrackTask it in the handler — or, if the "+
					"caller needs the change to have happened, use the synchronous verb.",
					fset.Position(fn.Pos()), fn.Name.Name, strings.Join(forking, "/"), name, ep.why)
			}
		}
	}

	// Per entry, not "did anything match at all". A count over the whole list
	// is a check that cannot express the failure it is meant to catch: misspell
	// one fragment and the other six still match, the count stays healthy, and
	// the entry that this change exists to protect is silently guarding nothing.
	for _, name := range sortedEndpoints() {
		if !covered[name] {
			t.Errorf("upidEndpoints[%q] (%s) matched no mutating client method.\n"+
				"\tEither a fragment is misspelled — in which case it guards nothing — or the "+
				"method that used it is gone and the entry should go with it. An entry that "+
				"matches nothing is worse than no entry: it reads as coverage.",
				name, upidEndpoints[name].why)
		}
	}
}

// scanBody collects the mutating helpers a method calls and every string
// literal it contains, in one pass over the body.
func scanBody(body *ast.BlockStmt) (map[string]bool, []string) {
	verbs := map[string]bool{}
	var literals []string
	ast.Inspect(body, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.CallExpr:
			if sel, ok := node.Fun.(*ast.SelectorExpr); ok && mutatingHelpers[sel.Sel.Name] {
				verbs[sel.Sel.Name] = true
			}
		case *ast.BasicLit:
			if node.Kind != token.STRING {
				return true
			}
			if lit, err := strconv.Unquote(node.Value); err == nil {
				literals = append(literals, lit)
			}
		}
		return true
	})
	return verbs, literals
}

// mentionsAll reports whether every fragment appears in at least one literal.
func mentionsAll(literals, fragments []string) bool {
	for _, frag := range fragments {
		found := false
		for _, lit := range literals {
			if strings.Contains(lit, frag) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// forkingVerbsUsed returns the entry's worker-forking verbs that the method
// actually calls, in the entry's own order so the message is deterministic.
func forkingVerbsUsed(called map[string]bool, forking []string) []string {
	var used []string
	for _, v := range forking {
		if called[v] {
			used = append(used, v)
		}
	}
	return used
}

// sortedEndpoints returns the upidEndpoints keys in a stable order.
func sortedEndpoints() []string {
	names := make([]string, 0, len(upidEndpoints))
	for name := range upidEndpoints {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// parsePackage parses every non-test file in this package once, so the verb
// vocabulary and the endpoint scan read the same AST.
func parsePackage(t *testing.T) (*token.FileSet, []*ast.File) {
	t.Helper()
	fset := token.NewFileSet()
	matches, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	var files []*ast.File
	for _, m := range matches {
		if strings.HasSuffix(m, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, m, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", m, err)
		}
		files = append(files, f)
	}
	if len(files) == 0 {
		t.Fatalf("no source files parsed")
	}
	return fset, files
}

// methodsOn returns the names of every method declared on the named type.
func methodsOn(files []*ast.File, typeName string) map[string]bool {
	names := map[string]bool{}
	for _, file := range files {
		for _, decl := range file.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok && receiverIs(fn.Recv, typeName) {
				names[fn.Name.Name] = true
			}
		}
	}
	return names
}

// receiverIs reports whether a method hangs off T or *T.
func receiverIs(recv *ast.FieldList, typeName string) bool {
	if recv == nil || len(recv.List) == 0 {
		return false
	}
	expr := recv.List[0].Type
	if star, ok := expr.(*ast.StarExpr); ok {
		expr = star.X
	}
	id, ok := expr.(*ast.Ident)
	return ok && id.Name == typeName
}

// returnsUPIDAndError reports whether the results are exactly (string, error),
// named or not. It is the same shape internal/api/handlers keys its own guard
// on, which is what lets the two tests meet in the middle.
func returnsUPIDAndError(ft *ast.FuncType) bool {
	if ft.Results == nil || len(ft.Results.List) != 2 {
		return false
	}
	isIdent := func(e ast.Expr, name string) bool {
		id, ok := e.(*ast.Ident)
		return ok && id.Name == name
	}
	return isIdent(ft.Results.List[0].Type, "string") && isIdent(ft.Results.List[1].Type, "error")
}
