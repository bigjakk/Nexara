package handlers

import (
	"go/ast"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// Static-analysis guard: no database, no running server. A guest is looked up by its Nexara uuid,
// which says nothing about its cluster, while the permission gate authorizes the cluster in the
// PATH, so a handler that loads a guest by uuid and acts on it WITHOUT checking the row belongs to
// the path's cluster serves cross-cluster. resolveVM always checked; GetVM and SetVMPool did not
// (SetVMPool sent another cluster's VMID to the path cluster's Proxmox). Both now go through
// guestInCluster, and this guard stops the next direct lookup reopening it. A tripwire, not a proof,
// with three limits. Limit 1: it checks that a ClusterID comparison APPEARS in the same function (not
// that it compares the right values, is reached, or returns on mismatch). Limit 2: it matches only the
// literal `x.queries.GetVM(...)` shape (hoisting the receiver walks past it). Limit 3: guestLookupCallees
// is hand-maintained (a rename is caught, a new by-uuid query under another name is not).
var guestLookupCallees = map[string]bool{
	"GetVM":        true,
	"GetContainer": true,
}

// clusterScopeChokePoints are the functions allowed to call a guest
// lookup without an adjacent ClusterID comparison, because they ARE the
// comparison. Every other caller has to make it themselves.
var clusterScopeChokePoints = map[string]bool{
	"VMHandler.resolveVM":        true,
	"VMHandler.guestInCluster":   true,
	"ContainerHandler.resolveCT": true,
}

// TestGuard_GuestLookupsAreClusterScoped walks every handler in this
// package for a `queries.GetVM` / `queries.GetContainer` call.
func TestGuard_GuestLookupsAreClusterScoped(t *testing.T) {
	paths, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("glob: %v", err)
	}

	var findings []string
	// lookupCallers counts functions that CALL a guest lookup, and only those.
	// The choke points count when they make the lookup themselves — which each
	// is required to — and not merely for existing: counted unconditionally,
	// they held this at three even when guestLookupCallees matched nothing at
	// all, so the floor below could never fire in the very case it names. A
	// rename of ONE lookup is the per-choke-point check's to catch, not the
	// floor's: the other lookup still keeps the count above zero.
	var lookupCallers int
	chokePointsSeen := map[string]bool{}

	for _, path := range paths {
		astFile, parseErr := guardParsed(path)
		if parseErr != nil {
			t.Fatalf("parse %s: %v", path, parseErr)
		}
		for _, decl := range astFile.Decls {
			fd, ok := decl.(*ast.FuncDecl)
			if !ok || fd.Body == nil {
				continue
			}
			name := guardFuncKey(fd)
			calls := callsGuestLookup(fd.Body)
			if calls {
				lookupCallers++
			}
			if clusterScopeChokePoints[name] {
				chokePointsSeen[name] = true
				// A choke point that no longer makes the lookup is not the
				// comparison for it any more, and a lookup renamed out from
				// under guestLookupCallees shows up here first: the choke
				// points are the three places certain to call one.
				if !calls {
					findings = append(findings, name+" is listed as a cluster-scope choke point but makes no guest "+
						"lookup — the lookup moved, or guestLookupCallees no longer names it")
				}
				if !comparesClusterID(fd.Body) {
					findings = append(findings, name+" is listed as a cluster-scope choke point but makes no ClusterID comparison")
				}
				continue
			}
			if !calls {
				continue
			}
			if !comparesClusterID(fd.Body) {
				findings = append(findings, name+" ("+path+") loads a guest by uuid but never compares its ClusterID "+
					"against the cluster in the request path — a caller with a grant on one cluster can reach a guest in another")
			}
		}
	}

	for name := range clusterScopeChokePoints {
		if !chokePointsSeen[name] {
			findings = append(findings, name+" is listed as a cluster-scope choke point but no longer exists")
		}
	}

	// Reported before the floor below, which is fatal: the choke-point
	// findings name the more specific cause, and a t.Fatal first would hide
	// them.
	sort.Strings(findings)
	for _, f := range findings {
		t.Error(f)
	}

	// The guard is worthless if it matched nothing: the lookups could have
	// been renamed out from under it.
	if lookupCallers == 0 {
		t.Fatal("no guest lookup was found at all: guestLookupCallees is stale, or the handlers' " +
			"queries field was renamed, and callsGuestLookup matches only x.queries.<lookup>")
	}
}

// guestKindCrossoverPairs maps each LXC-only Proxmox client method to its
// QEMU twin. A function that calls BOTH halves of a pair is branching on
// what the guest turned out to be — and that is the shape that needs a
// second permission check, because Nexara keeps both kinds in one table
// and the route it arrived through can only have gated one of them.
//
// Keying on the PAIR, rather than on the LXC method alone, is what keeps
// ContainerHandler out of it: those methods call only the CT half, and
// their routes gate manage:container up front.
var guestKindCrossoverPairs = map[string]string{
	"ConvertCTToTemplate": "ConvertVMToTemplate",
	"CloneCT":             "CloneVM",
	"StartCT":             "StartVM",
	"StopCT":              "StopVM",
	"ShutdownCT":          "ShutdownVM",
	"RebootCT":            "RebootVM",
	"SuspendCT":           "SuspendVM",
	"ResumeCT":            "ResumeVM",
	"DestroyCT":           "DestroyVM",
	"MigrateCT":           "MigrateVM",
	"ResizeCTDisk":        "ResizeDisk",
}

// guestKindCrossoverExemptions are the functions that branch on guest
// kind and legitimately do NOT re-check, each with the reason.
//
// A list of exceptions with no stated justification rots into a rubber
// stamp, so every entry says which authority covers both kinds and why —
// the same standard publicRoutes and selfServiceRoutes are held to.
var guestKindCrossoverExemptions = map[string]string{
	"VMHandler.convertCloneToTemplate": "a detached background goroutine with no request context: it cannot " +
		"check a permission at all, and the decision was already made at the dispatch site (CloneToTemplate)",
	"BackupHandler.RestoreBackup": "gated manage:backup, which is a different authority from manage:vm — " +
		"a backup covers both guest kinds by nature, so restoring either is what that grant is for",
	"NodeHandler.EvacuateNode": "gated manage:node: evacuating a node necessarily moves every guest on it, " +
		"and demanding manage:container would make evacuation fail on any node hosting one",
}

// TestGuard_GuestKindCrossoversRecheckThePermission is the call-site half of requireGuestKindPerm:
// TestRequireGuestKindPerm proves the helper refuses a container to a caller without
// manage:container, not that the two handlers that need it call it (deleting the call leaves every
// other test green: a check in the caller is a check the next caller skips). Walks the source: any
// function that calls both halves of a guestKindCrossoverPairs entry must call requireGuestKindPerm.
func TestGuard_GuestKindCrossoversRecheckThePermission(t *testing.T) {
	paths, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("glob: %v", err)
	}

	var crossovers int
	seenExempt := map[string]bool{}

	for _, path := range paths {
		astFile, parseErr := guardParsed(path)
		if parseErr != nil {
			t.Fatalf("parse %s: %v", path, parseErr)
		}
		for _, decl := range astFile.Decls {
			fd, ok := decl.(*ast.FuncDecl)
			if !ok || fd.Body == nil {
				continue
			}
			called := calledMethodNames(fd.Body)
			name := guardFuncKey(fd)

			for lxcMethod, qemuMethod := range guestKindCrossoverPairs {
				if !called[lxcMethod] || !called[qemuMethod] {
					continue
				}
				crossovers++
				if called["requireGuestKindPerm"] {
					continue
				}
				if reason, exempt := guestKindCrossoverExemptions[name]; exempt {
					seenExempt[name] = true
					if strings.TrimSpace(reason) == "" {
						t.Errorf("%s is exempt with no reason; every exemption must say which authority covers both kinds", name)
					}
					continue
				}
				t.Errorf("%s (%s) calls both %s and %s, so it acts on whichever kind the guest turned out "+
					"to be — but it never calls requireGuestKindPerm, so a caller holding only the VM "+
					"permission can drive the container branch. If a different grant legitimately covers "+
					"both kinds, add it to guestKindCrossoverExemptions with that reason",
					name, path, qemuMethod, lxcMethod)
			}
		}
	}

	// Several handlers have this shape today. If the count reaches zero
	// the pair table has gone stale and this guard is checking nothing.
	if crossovers == 0 {
		t.Fatal("no guest-kind crossover was found at all; guestKindCrossoverPairs is stale")
	}
	for name := range guestKindCrossoverExemptions {
		if !seenExempt[name] {
			t.Errorf("guestKindCrossoverExemptions lists %s, but it is no longer an unchecked crossover — "+
				"drop the stale entry, or the list stops being a review surface", name)
		}
	}
}

// calledMethodNames collects every function and method name called in
// body, by its final selector. It does not resolve receivers: this guard
// keys on names distinctive enough that a collision would be a function
// that ought to be making the check anyway.
func calledMethodNames(body *ast.BlockStmt) map[string]bool {
	out := map[string]bool{}
	ast.Inspect(body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		switch fn := call.Fun.(type) {
		case *ast.SelectorExpr:
			out[fn.Sel.Name] = true
		case *ast.Ident:
			out[fn.Name] = true
		}
		return true
	})
	return out
}

// callsGuestLookup reports whether body calls one of the guest-by-uuid
// queries, in the literal `x.queries.Name(…)` shape only — see limit 2 in
// the file comment.
//
// The receiver identifier is not matched: `h.queries.GetVM` and
// `e.queries.GetVM` are the same hazard, and the method names are
// distinctive enough that a false positive is a function that should be
// making the check anyway.
func callsGuestLookup(body *ast.BlockStmt) bool {
	found := false
	ast.Inspect(body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || !guestLookupCallees[sel.Sel.Name] {
			return true
		}
		// Only the queries.X form, so a Proxmox client call of the same
		// name (pxClient.GetContainers and friends) is not swept in.
		inner, ok := sel.X.(*ast.SelectorExpr)
		if ok && inner.Sel.Name == "queries" {
			found = true
			return false
		}
		return true
	})
	return found
}

// comparesClusterID reports whether body compares a .ClusterID field with
// anything — anywhere, in any direction, reached or not. See limit 1 in
// the file comment: this is presence, not enforcement.
func comparesClusterID(body *ast.BlockStmt) bool {
	found := false
	ast.Inspect(body, func(n ast.Node) bool {
		bin, ok := n.(*ast.BinaryExpr)
		if !ok {
			return true
		}
		for _, side := range []ast.Expr{bin.X, bin.Y} {
			if sel, isSel := side.(*ast.SelectorExpr); isSel && sel.Sel.Name == "ClusterID" {
				found = true
				return false
			}
		}
		return true
	})
	return found
}

// guardFuncKey renders a declaration as "Type.Method" or a bare name,
// matching the shape clusterScopeChokePoints is keyed by.
func guardFuncKey(fd *ast.FuncDecl) string {
	if fd.Recv == nil || len(fd.Recv.List) == 0 {
		return fd.Name.Name
	}
	typ := fd.Recv.List[0].Type
	if star, ok := typ.(*ast.StarExpr); ok {
		typ = star.X
	}
	if ident, ok := typ.(*ast.Ident); ok {
		return ident.Name + "." + fd.Name.Name
	}
	return fd.Name.Name
}
