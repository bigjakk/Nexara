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

// mutatingHelpers are the apiClient helpers that issue a state-changing
// request. A method that only calls `do` (GET) is reading, and a read of a
// worker-backed path — GetCephPools on /ceph/pool — returns data, not a UPID.
var mutatingHelpers = map[string]bool{
	"doPost": true, "doPostRaw": true, "doPut": true, "doDelete": true,
}

// The two signals — "calls a mutating helper" and "mentions a listed path" —
// are gathered over the whole method body rather than from the helper's own
// path argument, because most methods assemble the path into a local first
// (`path := "/nodes/" + … + "/ceph/pool"`) and the argument is then just an
// identifier. The looseness is one-directional: a method that reads a listed
// path and mutates some OTHER path in the same body would be flagged wrongly.
// No method is shaped that way today; if one appears, split it.

// upidEndpoints are PVE API path fragments whose mutating verbs hand the work
// to a fork_worker and answer with a task UPID. The value is why, so that a
// future reader can check the claim against pve-manager rather than trusting
// the list.
//
// This list is not exhaustive over PVE and does not try to be — an entry earns
// its place when getting it wrong has already cost something, or when the path
// is spelled as a literal distinctive enough to match on. Add to it freely.
var upidEndpoints = map[string]string{
	// PVE::API2::Ceph registers PVE::API2::Ceph::Pool at path 'pool'; its
	// createpool and destroypool fork cephcreatepool / cephdestroypool.
	"/ceph/pool": "createpool/destroypool fork a worker",

	// PVE::API2::Disks and its subclasses run every mutating operation as a
	// task; Nexara has captured their UPIDs since the endpoints were added.
	"/disks/wipedisk":  "wiping a disk runs as a task",
	"/disks/initgpt":   "initialising a GPT runs as a task",
	"/disks/zfs":       "ZFS pool create/destroy run as tasks",
	"/disks/lvm":       "LVM and LVM-thin create/destroy run as tasks",
	"/disks/directory": "directory create/destroy run as tasks",
}

func TestGuard_WorkerEndpointsSurfaceTheUPID(t *testing.T) {
	fset := token.NewFileSet()
	matches, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("glob: %v", err)
	}

	covered := make(map[string]bool, len(upidEndpoints))
	for _, m := range matches {
		if strings.HasSuffix(m, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, m, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", m, err)
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil || !hasClientReceiver(fn.Recv) {
				continue
			}

			var mutates bool
			var endpoint, why string
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				switch node := n.(type) {
				case *ast.CallExpr:
					if sel, ok := node.Fun.(*ast.SelectorExpr); ok && mutatingHelpers[sel.Sel.Name] {
						mutates = true
					}
				case *ast.BasicLit:
					if node.Kind != token.STRING {
						return true
					}
					lit, err := strconv.Unquote(node.Value)
					if err != nil {
						return true
					}
					// Sorted, so a literal matching two fragments always
					// reports the same one rather than whichever map order won.
					for _, frag := range sortedEndpoints() {
						if strings.Contains(lit, frag) {
							endpoint, why = frag, upidEndpoints[frag]
							break
						}
					}
				}
				return true
			})

			if !mutates || endpoint == "" {
				continue
			}
			covered[endpoint] = true
			if returnsUPIDAndError(fn.Type) {
				continue
			}
			t.Errorf("%s: %s sends a mutating request to %s, where %s, but returns error alone.\n"+
				"\tPVE answers with a task UPID and the operation has NOT finished when the "+
				"request returns. Return (string, error), pass the UPID as the response "+
				"destination instead of nil, and TrackTask it in the handler — otherwise the "+
				"caller reports success for work that may still fail.",
				fset.Position(fn.Pos()), fn.Name.Name, endpoint, why)
		}
	}

	// Per entry, not "did anything match at all". A count over the whole list
	// is a check that cannot express the failure it is meant to catch: misspell
	// one fragment and the other five still match, the count stays healthy, and
	// the entry that this change exists to protect is silently guarding nothing.
	for _, frag := range sortedEndpoints() {
		if !covered[frag] {
			t.Errorf("upidEndpoints[%q] (%s) matched no mutating client method.\n"+
				"\tEither the fragment is misspelled — in which case it guards nothing — or the "+
				"method that used it is gone and the entry should go with it. An entry that "+
				"matches nothing is worse than no entry: it reads as coverage.", frag, upidEndpoints[frag])
		}
	}
}

// sortedEndpoints returns the upidEndpoints keys in a stable order.
func sortedEndpoints() []string {
	frags := make([]string, 0, len(upidEndpoints))
	for frag := range upidEndpoints {
		frags = append(frags, frag)
	}
	sort.Strings(frags)
	return frags
}

// hasClientReceiver reports whether a method hangs off Client or *Client.
func hasClientReceiver(recv *ast.FieldList) bool {
	if recv == nil || len(recv.List) == 0 {
		return false
	}
	expr := recv.List[0].Type
	if star, ok := expr.(*ast.StarExpr); ok {
		expr = star.X
	}
	id, ok := expr.(*ast.Ident)
	return ok && id.Name == "Client"
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
