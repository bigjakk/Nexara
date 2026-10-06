package handlers

import (
	"go/ast"
	"testing"
)

// The query half was a cross-cluster read; this is the handler half. ListCVEScanVulnsByNode takes a
// ScanID as well as a ScanNodeID, and TestCVEVulnReadsAreScanScoped (internal/db) holds the query to
// filtering on both, but cannot see WHICH scan id the handler passes: `ScanID: uuid.Nil` or
// `ScanID: nid` compiles and reopens the hole. scanID is the local ListVulnerabilities parses from the
// PATH and checks against the path's cluster (GetCVEScan, then scan.ClusterID != clusterID is a 404);
// nid is whatever ?node_id= carried and is checked against nothing. This pins the first. It keys on the
// identifier, not dataflow: a reassignment between the parse and the call would pass.
func TestGuard_CVENodeFilterPassesTheValidatedScanID(t *testing.T) {
	const (
		queryName   = "ListCVEScanVulnsByNode"
		paramsName  = "ListCVEScanVulnsByNodeParams"
		wantScanID  = "scanID"
		wantNodeID  = "nid"
		wantCallers = 1
		scanIDField = "ScanID"
		nodeIDField = "ScanNodeID"
	)

	_, files := parseGoFiles(t, ".")

	callers := 0
	for _, file := range files {
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok || sel.Sel.Name != queryName {
					return true
				}
				callers++
				name := qualifiedFuncName(fn)

				lit := compositeLitArg(call, paramsName)
				if lit == nil {
					t.Errorf("%s calls %s without an inline %s literal; this guard reads the "+
						"field values at the call site, so it cannot see a struct built elsewhere. "+
						"Build it inline, or extend the guard.", name, queryName, paramsName)
					return true
				}
				fields := litFieldIdents(lit)
				if got := fields[scanIDField]; got != wantScanID {
					t.Errorf("%s passes %s: %q, want %q — the scan id the handler checked against "+
						"the cluster in the path is the only value that confines this read. Anything "+
						"else reopens the cross-cluster read that ?node_id= used to allow.",
						name, scanIDField, got, wantScanID)
				}
				if got := fields[nodeIDField]; got != wantNodeID {
					t.Errorf("%s passes %s: %q, want %q — the parsed ?node_id=.",
						name, nodeIDField, got, wantNodeID)
				}
				return true
			})
		}
	}

	// Without this the guard passes vacuously the moment the call is renamed,
	// moved to another package, or deleted — and "no callers" would read
	// exactly like "every caller is correct".
	if callers != wantCallers {
		t.Errorf("found %d call(s) to %s in this package, want %d — if the call moved, move this "+
			"guard with it; if it gained a caller, the new one needs the same check",
			callers, queryName, wantCallers)
	}
}

// compositeLitArg returns the call's sole composite-literal argument whose
// type name matches want, or nil. It looks through both `Params{…}` and
// `db.Params{…}`, since the handlers package qualifies the generated types.
func compositeLitArg(call *ast.CallExpr, want string) *ast.CompositeLit {
	for _, arg := range call.Args {
		lit, ok := arg.(*ast.CompositeLit)
		if !ok {
			continue
		}
		switch typ := lit.Type.(type) {
		case *ast.Ident:
			if typ.Name == want {
				return lit
			}
		case *ast.SelectorExpr:
			if typ.Sel.Name == want {
				return lit
			}
		}
	}
	return nil
}

// litFieldIdents maps each keyed field of a composite literal to the NAME of
// the identifier assigned to it, or "" when the value is not a bare
// identifier — which is itself a finding, since both fields here should be
// locals the handler already validated.
func litFieldIdents(lit *ast.CompositeLit) map[string]string {
	out := map[string]string{}
	for _, elt := range lit.Elts {
		kv, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		key, ok := kv.Key.(*ast.Ident)
		if !ok {
			continue
		}
		if ident, ok := kv.Value.(*ast.Ident); ok {
			out[key.Name] = ident.Name
			continue
		}
		out[key.Name] = ""
	}
	return out
}
