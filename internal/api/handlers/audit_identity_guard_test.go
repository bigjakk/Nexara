package handlers

import (
	"go/ast"
	"go/token"
	"go/types"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// An audit row is never rewritten, so what it says about WHICH storage, PBS
// server or backup job it concerns is all a History will ever find it by. These
// guards fail code that writes a row no History could find; handlers hold a
// concrete *db.Queries, so the source is all there is to read, as in
// audit_cluster_guard_test.go.

// auditSite is one place that names an audit row's resource and action: an
// AuditLog/AuditLogAs call, or a composite literal with ResourceType and
// ResourceID (TrackTaskParams, the scheduler's trackTaskParams, sqlc's
// InsertAuditLog*Params). typ and action are set when spelled as literals.
type auditSite struct {
	pos         token.Pos
	fn          string
	typ, action string
	id          string // the resource id expression, as written
	literal     bool   // ... and it is a basic literal
	call        bool   // an AuditLog/AuditLogAs call, rather than a composite literal
}

func auditSites(file *ast.File) []auditSite {
	str := func(e ast.Expr) string { s, _ := stringLiteral(e); return s }
	isLit := func(e ast.Expr) bool { _, ok := e.(*ast.BasicLit); return ok }
	var sites []auditSite
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			switch node := n.(type) {
			case *ast.CallExpr:
				if at, ok := auditCallArgs[callName(node)]; ok && len(node.Args) > at.action {
					sites = append(sites, auditSite{
						pos: node.Pos(), fn: fn.Name.Name, typ: str(node.Args[at.typ]), action: str(node.Args[at.action]),
						id: types.ExprString(node.Args[at.id]), literal: isLit(node.Args[at.id]), call: true,
					})
				}
			case *ast.CompositeLit:
				site, hasID := auditSite{pos: node.Pos(), fn: fn.Name.Name}, false
				for _, elt := range node.Elts {
					kv, ok := elt.(*ast.KeyValueExpr)
					if !ok {
						continue
					}
					switch key, _ := kv.Key.(*ast.Ident); {
					case key == nil:
					case key.Name == "ResourceType":
						site.typ = str(kv.Value)
					case key.Name == "Action":
						site.action = str(kv.Value)
					case key.Name == "ResourceID":
						site.id, site.literal, hasID = types.ExprString(kv.Value), isLit(kv.Value), true
					}
				}
				if hasID {
					sites = append(sites, site)
				}
			}
			return true
		})
	}
	return sites
}

// storageNameExpr matches an expression that is a storage's NAME: pool.Storage,
// req.Storage, a variable called storage or storageName.
var storageNameExpr = regexp.MustCompile(`^([A-Za-z_]\w*\.)*[sS]torage(Name)?$`)

// identityRules are the object kinds whose audit rows must carry a stable
// identity, judged on the resource id each site passes.
var identityRules = []struct {
	name    string
	applies func(typ, action string) bool
	ok      func(s auditSite) bool
	why     string
}{
	{
		name:    "a storage row is keyed by the storage's name",
		applies: func(typ, _ string) bool { return typ == "storage" },
		ok:      func(s auditSite) bool { return storageNameExpr.MatchString(s.id) },
		why: "A storage's audit rows are keyed by its NAME, not by the storage_pools row the request\n" +
			"\taddressed: that row is one node's view of the storage and the collector prunes and\n" +
			"\tre-inserts it, so its id stops resolving and the storage's History can no longer find the\n" +
			"\trow. Pass pool.Storage (or the name you have); put the node in details when one ran it.",
	},
	{
		name:    "a backup job row is keyed by the job's id, not a literal",
		applies: func(_, action string) bool { return strings.HasPrefix(action, "backup_job_") },
		ok:      func(s auditSite) bool { return !s.literal },
		why: "A backup job's audit rows are keyed by the id Proxmox gave the job. A literal names no\n" +
			"\tjob (it is what backup_job_created carried until the id was looked up). Use the job's id,\n" +
			"\tor backupJobIDUnknown when it cannot be found.",
	},
}

// TestGuard_AuditRowsCarryTheirObjectsIdentity runs identityRules over every
// audit site in the repository, the background packages included. A rule that
// meets no call and no literal fails too: an AuditLog that gained a parameter
// would otherwise be misread while the literals kept every rule "met".
func TestGuard_AuditRowsCarryTheirObjectsIdentity(t *testing.T) {
	t.Parallel()

	met := make([][2]int, len(identityRules)) // [calls, literals]
	var findings []string
	for _, path := range goSourceFiles(t) {
		file, err := guardParsed(path)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		for _, site := range auditSites(file) {
			for i, rule := range identityRules {
				if !rule.applies(site.typ, site.action) {
					continue
				}
				if site.call {
					met[i][0]++
				} else {
					met[i][1]++
				}
				if !rule.ok(site) {
					findings = append(findings, guardFset.Position(site.pos).String()+": "+site.fn+
						" files a row under the id "+site.id+".\n\t"+rule.why)
				}
			}
		}
	}

	sort.Strings(findings)
	for _, f := range findings {
		t.Error(f)
	}
	for i, rule := range identityRules {
		if met[i][0] == 0 || met[i][1] == 0 {
			t.Errorf("rule %q met %d audit calls and %d literals: the scan no longer sees what it guards",
				rule.name, met[i][0], met[i][1])
		}
	}
}

// TestGuard_PBSHandlersAuditThroughAuditPBS fails a handler that resolves a PBS
// server from its :pbs_id and then audits with AuditLog or TrackTask itself.
// Those rows were filed as "backup" under the datastore's name, which named no
// server; auditPBS gives a datastore action its server's id and details.datastore.
func TestGuard_PBSHandlersAuditThroughAuditPBS(t *testing.T) {
	t.Parallel()

	fset, files := parseGoFiles(t, ".")
	resolvers, throughHelper := 0, 0
	for _, file := range files {
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			var resolves, helper bool
			direct := map[token.Pos]string{}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				if call, ok := n.(*ast.CallExpr); ok {
					switch name := callName(call); name {
					case "pbsIDFromParams":
						resolves = true
					case "auditPBS":
						helper = true
					case "AuditLog", "AuditLogAs", "TrackTask":
						direct[call.Pos()] = name
					}
				}
				return true
			})
			if !resolves {
				continue
			}
			resolvers++
			if helper {
				throughHelper++
			}
			for pos, name := range direct {
				t.Errorf("%s: %s resolves a PBS server and audits with %s directly; audit through "+
					"h.auditPBS, which files the row under the server's id (details.datastore names the datastore).",
					fset.Position(pos), fn.Name.Name, name)
			}
		}
	}
	if resolvers == 0 || throughHelper == 0 {
		t.Errorf("saw %d handlers that resolve a PBS server and %d that audit through auditPBS: "+
			"the scan no longer sees the calls it guards", resolvers, throughHelper)
	}
}
