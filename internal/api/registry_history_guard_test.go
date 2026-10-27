package api

import (
	"go/ast"
	"go/token"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"testing"

	"github.com/gofiber/fiber/v3"
)

// clusterObjectAudits is the resource_type(s) each mutating route on one of the
// cluster page's objects files its audit row under; the id each kind uses is
// documented on AuditLog. An object's History reads its rows by that pair, so a
// route missing here, a handler that audits as another kind and one that audits
// as none all fail below.
var clusterObjectAudits = map[string][]string{
	"HAHandler.CreateResource": {"ha_resource"},
	"HAHandler.UpdateResource": {"ha_resource"},
	"HAHandler.DeleteResource": {"ha_resource"},
	"HAHandler.CreateGroup":    {"ha_group"},
	"HAHandler.UpdateGroup":    {"ha_group"},
	"HAHandler.DeleteGroup":    {"ha_group"},
	"HAHandler.CreateRule":     {"ha_rule"},
	"HAHandler.UpdateRule":     {"ha_rule"},
	"HAHandler.DeleteRule":     {"ha_rule"},
	"HAHandler.ArmHA":          {"ha"},
	"HAHandler.DisarmHA":       {"ha"},

	"CephHandler.CreatePool": {"ceph_pool"},
	"CephHandler.DeletePool": {"ceph_pool"},
	"CephHandler.SetOSDIn":   {"ceph_osd"},
	"CephHandler.SetOSDOut":  {"ceph_osd"},
	"CephHandler.StartOSD":   {"ceph_osd"},
	"CephHandler.StopOSD":    {"ceph_osd"},
	"CephHandler.RestartOSD": {"ceph_osd"},
	"PoolHandler.CreatePool": {"pool"},
	"PoolHandler.UpdatePool": {"pool"},
	"PoolHandler.DeletePool": {"pool"},

	"NetworkHandler.CreateSDNZone":       {"sdn_zone"},
	"NetworkHandler.UpdateSDNZone":       {"sdn_zone"},
	"NetworkHandler.DeleteSDNZone":       {"sdn_zone"},
	"NetworkHandler.CreateSDNVNet":       {"sdn_vnet"},
	"NetworkHandler.UpdateSDNVNet":       {"sdn_vnet"},
	"NetworkHandler.DeleteSDNVNet":       {"sdn_vnet"},
	"NetworkHandler.CreateSDNSubnet":     {"sdn_subnet"},
	"NetworkHandler.UpdateSDNSubnet":     {"sdn_subnet"},
	"NetworkHandler.DeleteSDNSubnet":     {"sdn_subnet"},
	"NetworkHandler.CreateSDNController": {"sdn_controller"},
	"NetworkHandler.UpdateSDNController": {"sdn_controller"},
	"NetworkHandler.DeleteSDNController": {"sdn_controller"},
	"NetworkHandler.CreateSDNIPAM":       {"sdn_ipam"},
	"NetworkHandler.UpdateSDNIPAM":       {"sdn_ipam"},
	"NetworkHandler.DeleteSDNIPAM":       {"sdn_ipam"},
	"NetworkHandler.CreateSDNDNS":        {"sdn_dns"},
	"NetworkHandler.UpdateSDNDNS":        {"sdn_dns"},
	"NetworkHandler.DeleteSDNDNS":        {"sdn_dns"},
	"NetworkHandler.ApplySDN":            {"sdn"},

	"NetworkHandler.CreateNetworkInterface": {"network"},
	"NetworkHandler.UpdateNetworkInterface": {"network"},
	"NetworkHandler.DeleteNetworkInterface": {"network"},
	"NetworkHandler.ApplyNetworkConfig":     {"network"},
	"NetworkHandler.RevertNetworkConfig":    {"network"},

	"NetworkHandler.CreateClusterFirewallRule": {"firewall_rule"},
	"NetworkHandler.UpdateClusterFirewallRule": {"firewall_rule"},
	"NetworkHandler.DeleteClusterFirewallRule": {"firewall_rule"},
	"NetworkHandler.CreateVMFirewallRule":      {"firewall_rule"},
	"NetworkHandler.UpdateVMFirewallRule":      {"firewall_rule"},
	"NetworkHandler.DeleteVMFirewallRule":      {"firewall_rule"},
	"NetworkHandler.SetFirewallOptions":        {"firewall_options"},
	"NetworkHandler.CreateFirewallAlias":       {"firewall_alias"},
	"NetworkHandler.UpdateFirewallAlias":       {"firewall_alias"},
	"NetworkHandler.DeleteFirewallAlias":       {"firewall_alias"},
	"NetworkHandler.CreateFirewallIPSet":       {"firewall_ipset"},
	"NetworkHandler.DeleteFirewallIPSet":       {"firewall_ipset"},
	"NetworkHandler.AddFirewallIPSetEntry":     {"firewall_ipset_entry"},
	"NetworkHandler.DeleteFirewallIPSetEntry":  {"firewall_ipset_entry"},
	"NetworkHandler.CreateSecurityGroup":       {"firewall_security_group"},
	"NetworkHandler.DeleteSecurityGroup":       {"firewall_security_group"},
	"NetworkHandler.CreateSecurityGroupRule":   {"firewall_security_group_rule"},
	"NetworkHandler.UpdateSecurityGroupRule":   {"firewall_security_group_rule"},
	"NetworkHandler.DeleteSecurityGroupRule":   {"firewall_security_group_rule"},
	"NetworkHandler.CreateTemplate":            {"firewall_template"},
	"NetworkHandler.UpdateTemplate":            {"firewall_template"},
	"NetworkHandler.ApplyTemplate":             {"firewall_template", "firewall_rule"},
	"NetworkHandler.DeleteTemplate":            {"firewall_template"},

	"ReplicationHandler.CreateJob":   {"replication"},
	"ReplicationHandler.UpdateJob":   {"replication"},
	"ReplicationHandler.DeleteJob":   {"replication"},
	"ReplicationHandler.TriggerSync": {"replication"},

	"VMHandler.CreateUSBMapping": {"usb_mapping"},
	"VMHandler.UpdateUSBMapping": {"usb_mapping"},
	"VMHandler.DeleteUSBMapping": {"usb_mapping"},
	"VMHandler.CreatePCIMapping": {"pci_mapping"},
	"VMHandler.UpdatePCIMapping": {"pci_mapping"},
	"VMHandler.DeletePCIMapping": {"pci_mapping"},

	"AccessHandler.CreateUser":  {"pve_user"},
	"AccessHandler.UpdateUser":  {"pve_user"},
	"AccessHandler.DeleteUser":  {"pve_user"},
	"AccessHandler.CreateToken": {"pve_token"},
	"AccessHandler.UpdateToken": {"pve_token"},
	"AccessHandler.DeleteToken": {"pve_token"},
	"AccessHandler.CreateGroup": {"pve_group"},
	"AccessHandler.UpdateGroup": {"pve_group"},
	"AccessHandler.DeleteGroup": {"pve_group"},
	"AccessHandler.CreateRole":  {"pve_role"},
	"AccessHandler.UpdateRole":  {"pve_role"},
	"AccessHandler.DeleteRole":  {"pve_role"},
	"AccessHandler.UpdateACL":   {"pve_acl"},

	"ACMEHandler.CreateAccount":         {"acme_account"},
	"ACMEHandler.UpdateAccount":         {"acme_account"},
	"ACMEHandler.DeleteAccount":         {"acme_account"},
	"ACMEHandler.CreatePlugin":          {"acme_plugin"},
	"ACMEHandler.UpdatePlugin":          {"acme_plugin"},
	"ACMEHandler.DeletePlugin":          {"acme_plugin"},
	"ACMEHandler.SetNodeACMEConfig":     {"acme_config"},
	"ACMEHandler.OrderNodeCertificate":  {"certificate"},
	"ACMEHandler.RenewNodeCertificate":  {"certificate"},
	"ACMEHandler.RevokeNodeCertificate": {"certificate"},

	"MetricServerHandler.CreateServer": {"metric_server"},
	"MetricServerHandler.UpdateServer": {"metric_server"},
	"MetricServerHandler.DeleteServer": {"metric_server"},

	"DRSHandler.UpdateConfig":    {"drs"},
	"DRSHandler.TriggerEvaluate": {"drs"},
	"DRSHandler.CreateRule":      {"drs_rule"},
	"DRSHandler.DeleteRule":      {"drs_rule"},
	"DRSHandler.CreateHARule":    {"ha_rule"},
	"DRSHandler.DeleteHARule":    {"ha_rule"},

	"ClusterOptionsHandler.UpdateOptions":     {"cluster_options"},
	"ClusterOptionsHandler.UpdateDescription": {"cluster_options"},
	"ClusterOptionsHandler.UpdateTags":        {"cluster_options"},
}

// clusterObjectIDKeys is the auditkey function each name-spelled kind's id must
// be built with: Proxmox spells these names its own way (lower case, a normalised
// path, the id it derived), and a handler that audits what was typed files one
// object under as many keys as it has spellings.
var clusterObjectIDKeys = map[string]string{
	"firewall_alias":               "FirewallName",
	"firewall_ipset":               "FirewallName",
	"firewall_ipset_entry":         "IPSetEntry",
	"firewall_security_group":      "FirewallName",
	"firewall_security_group_rule": "FirewallName",
	"pve_acl":                      "ACLPath",
	"sdn_subnet":                   "SDNSubnet",
}

// clusterObjectLegacyHandlers are the cluster page's mutating routes still wired
// by hand in router.go (legacy_route_ratchet_test.go): the registry walk cannot
// see them, so the guard reads them by name.
var clusterObjectLegacyHandlers = []string{"NetworkHandler.CreateTemplate", "NetworkHandler.UpdateTemplate"}

// clusterObjectRoute matches the routes of the objects the table covers: the
// cluster page's tabs (HA, Ceph, pools, mappings, access control, replication,
// networks and SDN, firewall, certificates, metric servers, DRS, options), a
// guest's firewall rules, and the firewall templates. Node-scoped objects other
// than the ACME ones are a node's History, not this table's.
var clusterObjectRoute = regexp.MustCompile(`^/api/v1/(` +
	`clusters/:cluster_id/(ha|ceph|pools|sdn|networks|firewall|firewall-templates|replication|usb-mappings|` +
	`pci-mappings|access|acme|metric-servers|drs|options|description|tags)(/|$)` +
	`|clusters/:cluster_id/vms/:vm_id/firewall/` +
	`|clusters/:cluster_id/nodes/:node/(acme-config|certificates)` +
	`|firewall-templates/)`)

// auditCall is one AuditLog or TrackTask call the walk found.
type auditCall struct {
	resourceType string // "" when the argument is not a string literal
	idMissing    bool   // the resource id is an empty literal
	idFunc       string // the auditkey function the id is built with, "" if it is not a call to one
	pos          token.Pos
}

// auditkeyCall is the name of the auditkey function e calls, "" if it is not a
// call to one.
func auditkeyCall(e ast.Expr) string {
	call, ok := e.(*ast.CallExpr)
	if !ok {
		return ""
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return ""
	}
	if pkg, ok := sel.X.(*ast.Ident); !ok || pkg.Name != "auditkey" {
		return ""
	}
	return sel.Sel.Name
}

// auditCallsIn collects every AuditLog and TrackTask call reachable from fd
// through calls to its own receiver's methods and to package functions, which is
// how a handler that delegates its audit row (osdMembershipAction) is still read.
func auditCallsIn(fd *ast.FuncDecl, decls map[string]*ast.FuncDecl, seen map[string]bool) []auditCall {
	if seen[funcKey(fd)] {
		return nil
	}
	seen[funcKey(fd)] = true

	var out []auditCall
	recv := receiverType(fd)
	ast.Inspect(fd.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		name := calleeName(call)
		switch name {
		case "AuditLog":
			// AuditLog(c, queries, pub, clusterID, resourceType, resourceID, action, details)
			if len(call.Args) >= 8 {
				out = append(out, auditCall{
					resourceType: stringLiteral(call.Args[4]),
					idMissing:    stringLiteral(call.Args[5]) == "" && isStringLiteral(call.Args[5]),
					idFunc:       auditkeyCall(call.Args[5]),
					pos:          call.Pos(),
				})
			}
		case "TrackTask":
			// TrackTask(c, queries, pub, TrackTaskParams{... ResourceType, ResourceID ...})
			if len(call.Args) >= 4 {
				ac := auditCall{pos: call.Pos(), idMissing: true}
				if lit, ok := call.Args[3].(*ast.CompositeLit); ok {
					for _, elt := range lit.Elts {
						kv, ok := elt.(*ast.KeyValueExpr)
						if !ok {
							continue
						}
						key, _ := kv.Key.(*ast.Ident)
						switch {
						case key != nil && key.Name == "ResourceType":
							ac.resourceType = stringLiteral(kv.Value)
						case key != nil && key.Name == "ResourceID":
							ac.idMissing = isStringLiteral(kv.Value) && stringLiteral(kv.Value) == ""
							ac.idFunc = auditkeyCall(kv.Value)
						}
					}
				}
				out = append(out, ac)
			}
		}
		// The entry points are the leaves: reading into them would find the
		// non-literal arguments they forward.
		if name == "AuditLog" || name == "TrackTask" {
			return true
		}
		if callee, ok := decls[calleeKey(call.Fun, recv)]; ok {
			out = append(out, auditCallsIn(callee, decls, seen)...)
		}
		return true
	})
	return out
}

// calleeName is the bare function or method name of a call.
func calleeName(call *ast.CallExpr) string {
	switch f := call.Fun.(type) {
	case *ast.Ident:
		return f.Name
	case *ast.SelectorExpr:
		return f.Sel.Name
	}
	return ""
}

func isStringLiteral(e ast.Expr) bool {
	lit, ok := e.(*ast.BasicLit)
	return ok && lit.Kind == token.STRING
}

// stringLiteral is the value of a string literal, "" for anything else.
func stringLiteral(e ast.Expr) string {
	if !isStringLiteral(e) {
		return ""
	}
	v, err := strconv.Unquote(e.(*ast.BasicLit).Value)
	if err != nil {
		return ""
	}
	return v
}

// TestGuard_ClusterObjectMutationsFileUnderTheirKind fails a mutating route on
// one of the cluster page's objects whose handler files no audit row, files it
// under a kind other than clusterObjectAudits names, or files it with an
// unreadable type or an empty id — and a route, or a table row, with no
// counterpart. Static, as TestGuard_AuditCallsCarryResourceCluster is: handlers
// hold a concrete *db.Queries, so the row a handler writes can only be read in
// the source.
func TestGuard_ClusterObjectMutationsFileUnderTheirKind(t *testing.T) {
	routed := map[string]bool{}
	check := func(route, key string, fd *ast.FuncDecl, decls map[string]*ast.FuncDecl) {
		routed[key] = true
		want, known := clusterObjectAudits[key]
		if !known {
			t.Errorf("%s is a mutation on one of the cluster page's objects with no entry in "+
				"clusterObjectAudits: add the resource_type its audit row files under, so its History can find it",
				route)
			return
		}
		calls := auditCallsIn(fd, decls, map[string]bool{})
		if len(calls) == 0 {
			t.Errorf("%s writes no audit row (AuditLog or TrackTask)", route)
		}
		filed := map[string]bool{}
		for _, c := range calls {
			switch {
			case c.resourceType == "":
				t.Errorf("%s files an audit row whose resource_type is not a string literal; the guard "+
					"cannot read it", key)
			case !slices.Contains(want, c.resourceType):
				t.Errorf("%s files an audit row under %q, want one of %v", key, c.resourceType, want)
			}
			if c.idMissing {
				t.Errorf("%s files an audit row with an empty resource_id", key)
			}
			if want, named := clusterObjectIDKeys[c.resourceType]; named && c.idFunc != want {
				t.Errorf("%s files a %s row whose id is not built with auditkey.%s", key, c.resourceType, want)
			}
			filed[c.resourceType] = true
		}
		for _, wantType := range want {
			if len(calls) > 0 && !filed[wantType] {
				t.Errorf("%s is expected to file under %q and never does; update clusterObjectAudits", key, wantType)
			}
		}
	}

	for _, e := range sharedRouteStub(t).registry.Endpoints() {
		if e.Method == fiber.MethodGet || !clusterObjectRoute.MatchString(e.Path) {
			continue
		}
		fd, _, decls, _, err := resolveHandlerDecl(e.Handler)
		if err != nil {
			t.Errorf("%s %s: %v", e.Method, e.Path, err)
			continue
		}
		check(e.Method+" "+e.Path+" ("+funcKey(fd)+")", funcKey(fd), fd, decls)
	}

	handlersDir, _ := relPackageDir(apiPackageImportPath + "/handlers")
	handlerDecls, err := packageFuncDecls(handlersDir)
	if err != nil {
		t.Fatalf("parse the handlers: %v", err)
	}
	for _, key := range clusterObjectLegacyHandlers {
		fd, ok := handlerDecls[key]
		switch {
		case routed[key]:
			t.Errorf("%s is routed through the registry now: delete it from clusterObjectLegacyHandlers", key)
		case !ok:
			t.Errorf("clusterObjectLegacyHandlers names %s, which no handler declares", key)
		default:
			check("legacy route "+key, key, fd, handlerDecls)
		}
	}

	// The anti-vacuity floor: a registry change or a path rename that stops the
	// matcher seeing the routes would otherwise pass an empty loop.
	if len(routed) < len(clusterObjectAudits)/2 {
		t.Fatalf("the guard matched %d of the %d handlers clusterObjectAudits names; the route matcher "+
			"has stopped seeing the cluster page's routes", len(routed), len(clusterObjectAudits))
	}
	var stale []string
	for key := range clusterObjectAudits {
		if !routed[key] {
			stale = append(stale, key)
		}
	}
	sort.Strings(stale)
	for _, key := range stale {
		t.Errorf("clusterObjectAudits has %q, which no mutating route on the cluster page reaches — "+
			"delete the entry", key)
	}
}
