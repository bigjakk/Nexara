package handlers

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"testing"
)

// TestGuard_MetricServerAuditOmitsTheToken is the secret-handling half of the
// metric-server migration, and it exists because the registry made a new way to
// get it wrong.
//
// A migrated handler receives an *apischema.Params, and Params.Raw() hands back
// every declared parameter in one map — which on these writes includes the
// InfluxDB API token. Marshalling that into an audit row would publish the
// credential to every Viewer on the instance: view:audit is a default Viewer
// grant, which is the same reason audit.go carries reservedSettingVisibility
// for the syslog destination.
//
// So the rule is not "do not log the token": it is that an audit payload here
// is built from a NAMED set of fields, because the token is only the field that
// happens to be sensitive today. Static analysis only, no DB.
func TestGuard_MetricServerAuditOmitsTheToken(t *testing.T) {
	const file = "metric_servers.go"

	// The fields a metric-server audit row may carry. Adding one is a
	// deliberate edit here, which is the point.
	allowed := map[string]bool{"id": true, "type": true}

	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, file, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", file, err)
	}

	// The three mutations: each takes a body that may carry a token, and each
	// records a row.
	audits := map[string]bool{
		"CreateServer": false,
		"UpdateServer": false,
		"DeleteServer": false,
	}

	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Recv == nil {
			continue
		}
		if _, tracked := audits[fn.Name.Name]; !tracked {
			continue
		}
		ast.Inspect(fn, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, isSel := call.Fun.(*ast.SelectorExpr)
			if isSel && sel.Sel.Name == "Raw" {
				t.Errorf("%s calls Params.Raw(); it carries the write-only InfluxDB token, and "+
					"view:audit is a default Viewer grant — name the audited fields instead", fn.Name.Name)
				return true
			}
			if id, isIdent := call.Fun.(*ast.Ident); isIdent && id.Name == "AuditLog" {
				audits[fn.Name.Name] = true
				return true
			}
			// Every audit payload in this file is built by json.Marshal over a
			// map literal. Check the keys of that literal against the allow-list.
			if !isSel || sel.Sel.Name != "Marshal" {
				return true
			}
			pkg, isPkg := sel.X.(*ast.Ident)
			if !isPkg || pkg.Name != "json" || len(call.Args) != 1 {
				return true
			}
			lit, isLit := call.Args[0].(*ast.CompositeLit)
			if !isLit {
				t.Errorf("%s marshals something other than a literal map into an audit row; the audited "+
					"fields have to be readable at the call site", fn.Name.Name)
				return true
			}
			for _, elt := range lit.Elts {
				kv, isKV := elt.(*ast.KeyValueExpr)
				if !isKV {
					continue
				}
				key, isStr := kv.Key.(*ast.BasicLit)
				if !isStr || key.Kind != token.STRING {
					continue
				}
				name, unquoteErr := strconv.Unquote(key.Value)
				if unquoteErr != nil {
					continue
				}
				if !allowed[name] {
					t.Errorf("%s records the audit field %q; only %v are allowed on a metric-server row, "+
						"because this body carries a credential and view:audit is a default Viewer grant",
						fn.Name.Name, name, sortedAllowedAuditFields(allowed))
				}
			}
			return true
		})
	}

	for name, recorded := range audits {
		if !recorded {
			t.Errorf("%s writes no audit row; every metric-server mutation has to leave one", name)
		}
	}
}

func sortedAllowedAuditFields(allowed map[string]bool) []string {
	out := make([]string, 0, len(allowed))
	for k := range allowed {
		out = append(out, k)
	}
	return out
}
