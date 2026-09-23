package api

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"

	"github.com/bigjakk/nexara/internal/api/apischema"
	"github.com/bigjakk/nexara/internal/proxmox"
)

// sdnRouteCount is how many endpoints registerSDNEndpoints declares. See
// vmRouteCount in registry_vms_test.go for why the registry total is a sum
// of per-domain constants rather than one number.
const sdnRouteCount = 25

// sdnLegacyPermissions is what each handler checked with a hand-placed call
// BEFORE Phase 6e, transcribed from
// `git show HEAD:internal/api/handlers/networks.go` at commit eb888b6: one
// requireClusterPerm per handler, nothing else.
//
// This is the one slice of the four where the verb split is regular — read
// is view, create and change are manage, every delete is delete. The table
// is still written out rather than derived from the method, because the
// point of a tally is to be readable against `git show` rather than to be
// clever.
var sdnLegacyPermissions = map[string]string{
	"GET /api/v1/clusters/:cluster_id/sdn/zones":                          "view:network",
	"POST /api/v1/clusters/:cluster_id/sdn/zones":                         "manage:network",
	"PUT /api/v1/clusters/:cluster_id/sdn/zones/:zone":                    "manage:network",
	"DELETE /api/v1/clusters/:cluster_id/sdn/zones/:zone":                 "delete:network",
	"GET /api/v1/clusters/:cluster_id/sdn/vnets":                          "view:network",
	"POST /api/v1/clusters/:cluster_id/sdn/vnets":                         "manage:network",
	"PUT /api/v1/clusters/:cluster_id/sdn/vnets/:vnet":                    "manage:network",
	"DELETE /api/v1/clusters/:cluster_id/sdn/vnets/:vnet":                 "delete:network",
	"GET /api/v1/clusters/:cluster_id/sdn/vnets/:vnet/subnets":            "view:network",
	"POST /api/v1/clusters/:cluster_id/sdn/vnets/:vnet/subnets":           "manage:network",
	"PUT /api/v1/clusters/:cluster_id/sdn/vnets/:vnet/subnets/:subnet":    "manage:network",
	"DELETE /api/v1/clusters/:cluster_id/sdn/vnets/:vnet/subnets/:subnet": "delete:network",
	"PUT /api/v1/clusters/:cluster_id/sdn/apply":                          "manage:network",
	"GET /api/v1/clusters/:cluster_id/sdn/controllers":                    "view:network",
	"POST /api/v1/clusters/:cluster_id/sdn/controllers":                   "manage:network",
	"PUT /api/v1/clusters/:cluster_id/sdn/controllers/:controller":        "manage:network",
	"DELETE /api/v1/clusters/:cluster_id/sdn/controllers/:controller":     "delete:network",
	"GET /api/v1/clusters/:cluster_id/sdn/ipams":                          "view:network",
	"POST /api/v1/clusters/:cluster_id/sdn/ipams":                         "manage:network",
	"PUT /api/v1/clusters/:cluster_id/sdn/ipams/:ipam":                    "manage:network",
	"DELETE /api/v1/clusters/:cluster_id/sdn/ipams/:ipam":                 "delete:network",
	"GET /api/v1/clusters/:cluster_id/sdn/dns":                            "view:network",
	"POST /api/v1/clusters/:cluster_id/sdn/dns":                           "manage:network",
	"PUT /api/v1/clusters/:cluster_id/sdn/dns/:dns":                       "manage:network",
	"DELETE /api/v1/clusters/:cluster_id/sdn/dns/:dns":                    "delete:network",
}

func TestSDNRoutesDeclareTheSamePermissionTheyEnforced(t *testing.T) {
	assertNetworkTally(t, sdnLegacyPermissions, sdnRouteCount, ScopeCluster, map[string]int{
		"view:network":   6,
		"manage:network": 13,
		"delete:network": 6,
	})
}

// TestEveryDeclaredSDNRouteIsInTheTally is the other direction, and a
// SEPARATE test for the reason its node counterpart spells out.
func TestEveryDeclaredSDNRouteIsInTheTally(t *testing.T) {
	s := newRouteStubServer(t)
	seen := 0
	for _, e := range s.registry.Endpoints() {
		if !strings.HasPrefix(e.Path, sdnScope+"/") {
			continue
		}
		seen++
		if _, listed := sdnLegacyPermissions[e.Method+" "+e.Path]; listed {
			continue
		}
		t.Errorf("%s %s is declared under the SDN scope but is not in sdnLegacyPermissions — "+
			"add it to the tally, or the tally stops being a review surface", e.Method, e.Path)
	}
	if seen == 0 {
		t.Fatal("no declared route matched the SDN scope; this guard would pass vacuously")
	}
}

// TestSDNBodiesDeclareEveryProxmoxParamField is this slice's half of the
// regression guard registry_networks_test.go explains at length: every JSON
// tag on the params struct a handler used to bind is a key that has always
// been accepted, and apischema rejects a key the schema does not declare.
//
// Twelve structs, six create/update pairs, reflected over rather than
// listed — so a field added to any of them in a later release fails here
// until it is declared, instead of quietly becoming unsendable.
func TestSDNBodiesDeclareEveryProxmoxParamField(t *testing.T) {
	for _, tt := range []struct {
		name   string
		method string
		path   string
		params any
	}{
		{"zone create", fiber.MethodPost, sdnScope + "/zones", proxmox.CreateSDNZoneParams{}},
		{"zone update", fiber.MethodPut, sdnScope + "/zones/:zone", proxmox.UpdateSDNZoneParams{}},
		{"vnet create", fiber.MethodPost, sdnScope + "/vnets", proxmox.CreateSDNVNetParams{}},
		{"vnet update", fiber.MethodPut, sdnScope + "/vnets/:vnet", proxmox.UpdateSDNVNetParams{}},
		{"subnet create", fiber.MethodPost, sdnScope + "/vnets/:vnet/subnets", proxmox.CreateSDNSubnetParams{}},
		{"subnet update", fiber.MethodPut, sdnScope + "/vnets/:vnet/subnets/:subnet", proxmox.UpdateSDNSubnetParams{}},
		{"controller create", fiber.MethodPost, sdnScope + "/controllers", proxmox.CreateSDNControllerParams{}},
		{"controller update", fiber.MethodPut, sdnScope + "/controllers/:controller", proxmox.UpdateSDNControllerParams{}},
		{"ipam create", fiber.MethodPost, sdnScope + "/ipams", proxmox.CreateSDNIPAMParams{}},
		{"ipam update", fiber.MethodPut, sdnScope + "/ipams/:ipam", proxmox.UpdateSDNIPAMParams{}},
		{"dns create", fiber.MethodPost, sdnScope + "/dns", proxmox.CreateSDNDNSParams{}},
		{"dns update", fiber.MethodPut, sdnScope + "/dns/:dns", proxmox.UpdateSDNDNSParams{}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			e := declaredEndpoint(t, tt.method, tt.path)
			tags := jsonFieldNames(reflect.TypeOf(tt.params))
			if len(tags) == 0 {
				t.Fatal("no JSON tags found; this guard would pass vacuously")
			}
			for _, tag := range tags {
				if _, ok := e.Parameters[tag]; !ok {
					t.Errorf("%s %s does not declare %q, which c.Bind().Body accepted before the "+
						"migration", tt.method, tt.path, tag)
				}
			}
		})
	}
}

// TestSDNUpdateBodiesDropTheIdentifiersTheyNeverRead is the other side of
// the same coin, and it is the reason the update declarations are NOT just
// the create ones.
//
// proxmox.UpdateSDNZoneParams has no Type and UpdateSDNSubnetParams has
// neither Subnet nor Type, so a body carrying one was bound into nothing and
// silently discarded. Declaring them would document a parameter with no
// effect; leaving them out turns the same request into a 400 that says so.
func TestSDNUpdateBodiesDropTheIdentifiersTheyNeverRead(t *testing.T) {
	for _, tt := range []struct {
		name    string
		path    string
		absent  []string
		present []string
	}{
		{
			name:    "zone",
			path:    sdnScope + "/zones/:zone",
			absent:  []string{"type"},
			present: []string{"zone", "bridge", "mtu"},
		},
		{
			name:   "subnet",
			path:   sdnScope + "/vnets/:vnet/subnets/:subnet",
			absent: []string{"type"},
			// `subnet` IS declared, but as the path id rather than as the
			// CIDR the create body takes — the two are different values.
			present: []string{"subnet", "vnet", "gateway", "snat"},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			e := declaredEndpoint(t, fiber.MethodPut, tt.path)
			for _, name := range tt.absent {
				if _, declared := e.Parameters[name]; declared {
					t.Errorf("the update declares %q, which the Proxmox update params have no field for", name)
				}
			}
			for _, name := range tt.present {
				requireDeclaredProperty(t, e, name)
			}
		})
	}

	// The VNet update is the mirror image: zone IS a field of
	// UpdateSDNVNetParams, sent only when non-empty, so it is declared and
	// OPTIONAL — required on create, optional here.
	create := declaredEndpoint(t, fiber.MethodPost, sdnScope+"/vnets")
	update := declaredEndpoint(t, fiber.MethodPut, sdnScope+"/vnets/:vnet")
	if requireDeclaredProperty(t, create, "zone").Optional {
		t.Error("the VNet create makes zone optional; CreateSDNVNet refused an empty one")
	}
	if !requireDeclaredProperty(t, update, "zone").Optional {
		t.Error("the VNet update makes zone required; omitting it means \"keep the current zone\"")
	}
}

// TestSDNNumericSettingsKeepTheirZeroSentinel pins the shape every numeric
// SDN setting shares: the form builders in internal/proxmox/client.go test
// `if p.X != 0` before writing the field, so 0 means "do not send this" and
// a Minimum of 1 would 400 a caller spelling "unset" the way the endpoint
// has always accepted.
//
// The Minimum of 0 is not cosmetic either: a NEGATIVE value used to be
// rendered with strconv.Itoa and sent to Proxmox verbatim.
func TestSDNNumericSettingsKeepTheirZeroSentinel(t *testing.T) {
	for _, tt := range []struct {
		method string
		path   string
		params []string
	}{
		{fiber.MethodPost, sdnScope + "/zones", []string{"tag", "mtu", "vrf-vxlan", "advertise-subnets", "disable-arp-nd-suppression"}},
		{fiber.MethodPost, sdnScope + "/vnets", []string{"tag", "vlanaware", "isolate"}},
		{fiber.MethodPost, sdnScope + "/vnets/:vnet/subnets", []string{"snat"}},
		{fiber.MethodPost, sdnScope + "/controllers", []string{"asn", "ebgp-multihop"}},
		{fiber.MethodPost, sdnScope + "/ipams", []string{"section"}},
	} {
		e := declaredEndpoint(t, tt.method, tt.path)
		for _, name := range tt.params {
			t.Run(tt.path+" "+name, func(t *testing.T) {
				prop := requireDeclaredProperty(t, e, name)
				if !prop.Optional {
					t.Errorf("%s is required; no handler ever demanded it", name)
				}
				if prop.Default != 0 {
					t.Errorf("%s defaults to %v, want 0 — the \"not set\" sentinel", name, prop.Default)
				}
				if prop.Minimum == nil || *prop.Minimum != 0 {
					t.Errorf("%s has minimum %v, want 0", name, prop.Minimum)
				}
				if prop.Maximum == nil {
					t.Errorf("%s has no ceiling, so a value Proxmox cannot use still reaches it", name)
				}
				if _, err := e.Parameters.Validate(sdnParamsWith(t, e, map[string]any{name: -1})); err == nil {
					t.Errorf("%s accepted -1, which used to be relayed to Proxmox verbatim", name)
				}
			})
		}
	}
}

// TestSDNTagCeilingIsTheVXLANOne pins the one bound that had to be chosen
// rather than copied.
//
// A VNet's tag is a 12-bit VLAN id in a VLAN zone and a 24-bit VXLAN VNI in
// a VXLAN one. Which, depends on the zone — a cross-field rule the schema
// cannot express — so the ceiling has to be the LOOSER of the two or a
// legitimate VXLAN VNI would be refused.
func TestSDNTagCeilingIsTheVXLANOne(t *testing.T) {
	e := declaredEndpoint(t, fiber.MethodPost, sdnScope+"/vnets")
	if _, err := e.Parameters.Validate(sdnParamsWith(t, e, map[string]any{"tag": 4095})); err != nil {
		t.Errorf("tag refused 4095, which is a legal VXLAN VNI: %v", err)
	}
	if _, err := e.Parameters.Validate(sdnParamsWith(t, e, map[string]any{"tag": maxVXLANVNI})); err != nil {
		t.Errorf("tag refused the VXLAN ceiling: %v", err)
	}
	if _, err := e.Parameters.Validate(sdnParamsWith(t, e, map[string]any{"tag": maxVXLANVNI + 1})); err == nil {
		t.Error("tag accepted a value above the VXLAN ceiling")
	}
}

// TestSDNSubnetBodyIsACIDRAndThePathIsNot pins the one place in this domain
// where the same parameter NAME means two different things.
//
// The create body's `subnet` is a network in CIDR form; the update and
// delete routes' `:subnet` is the dashed id Proxmox DERIVES from it
// ("myzone-192.0.2.0-24"). Giving both the cidr format would make the update
// route undrivable, and giving neither would lose the one real format this
// domain has.
func TestSDNSubnetBodyIsACIDRAndThePathIsNot(t *testing.T) {
	create := declaredEndpoint(t, fiber.MethodPost, sdnScope+"/vnets/:vnet/subnets")
	if got := requireDeclaredProperty(t, create, "subnet").Format; got != "cidr" {
		t.Errorf("the create body's subnet declares format %q, want cidr", got)
	}
	for _, bad := range []string{"", "192.0.2.0", "not-a-network", "192.0.2.0/33"} {
		if _, err := create.Parameters.Validate(sdnParamsWith(t, create, map[string]any{"subnet": bad})); err == nil {
			t.Errorf("the create body accepted subnet=%q", bad)
		}
	}
	if _, err := create.Parameters.Validate(sdnParamsWith(t, create, map[string]any{"subnet": "192.0.2.0/24"})); err != nil {
		t.Errorf("the create body refused a CIDR: %v", err)
	}

	update := declaredEndpoint(t, fiber.MethodPut, sdnScope+"/vnets/:vnet/subnets/:subnet")
	if got := requireDeclaredProperty(t, update, "subnet").Format; got != "" {
		t.Errorf("the update path's subnet declares format %q; the derived id is not a CIDR", got)
	}
	// Both address families. The IPv6 case is the reason :subnet does not
	// share pveObjectNameParam: its derived id carries the address's COLONS,
	// and refusing them would make an existing IPv6 subnet un-editable and
	// un-deletable through this API — the exact failure the shared anchor's
	// own comment says it exists to avoid.
	for _, id := range []string{"storezone-192.0.2.0-24", "storezone-2001:db8::-64"} {
		if _, err := update.Parameters.Validate(sdnParamsWith(t, update, map[string]any{"subnet": id})); err != nil {
			t.Errorf("the update path refused the derived subnet id %q: %v", id, err)
		}
	}
}

// TestSDNSubnetTypeDefaultMovedIntoTheDeclaration pins the one handler
// default that became a schema default: CreateSDNSubnet filled in "subnet"
// when the body left type empty, so the declaration states it instead and
// the docs answer what omitting it does.
func TestSDNSubnetTypeDefaultMovedIntoTheDeclaration(t *testing.T) {
	e := declaredEndpoint(t, fiber.MethodPost, sdnScope+"/vnets/:vnet/subnets")
	prop := requireDeclaredProperty(t, e, "type")
	if !prop.Optional {
		t.Error("type is required; the handler filled it in when it was empty")
	}
	if prop.Default != "subnet" {
		t.Errorf("type defaults to %v, want \"subnet\" — the value the handler substituted", prop.Default)
	}
	params, err := e.Parameters.Validate(sdnParamsWith(t, e, nil))
	if err != nil {
		t.Fatalf("the known-good parameter set was rejected: %v", err)
	}
	if got := params.String("type"); got != "subnet" {
		t.Errorf("an omitted type reads back as %q, want \"subnet\"", got)
	}
	// An EMPTY type meant "subnet" too, but a Default never applies to a key
	// the caller sent — so the schema must let "" through, and
	// CreateSDNSubnet substitutes it (TestSDNSubnetCreateSendsSubnetForAnEmptyType
	// in the handlers package pins that half).
	if _, err := e.Parameters.Validate(sdnParamsWith(t, e, map[string]any{"type": ""})); err != nil {
		t.Errorf("an empty type was refused (%v); the handler has always read it as \"subnet\"", err)
	}
}

// TestSDNVNetUpdateZoneKeepsTheEmptySentinel pins that the VNet update's
// schema admits the empty zone it always took: sdnVNetUpdateToForm drops an
// empty zone, so "" has always kept the VNet where it is. The bare object-name
// rule refuses "", which turned that request into a 400; the declaration
// carries the -or-empty variant, and still refuses a traversal. The meaning
// half — that "" really leaves the zone alone — is the client's, and
// TestUpdateSDNVNetOmitsAnEmptyZone in internal/proxmox pins it.
//
// It also holds this call site of pveObjectNameOrEmptyParam: the pattern must
// be the catalogued rule and the MaxLength must survive, because a Pattern
// reassigned to a literal after the helper returns is invisible to the guards
// that read the source (see registry_rule_reference_ratchet_test.go).
func TestSDNVNetUpdateZoneKeepsTheEmptySentinel(t *testing.T) {
	e := declaredEndpoint(t, fiber.MethodPut, sdnScope+"/vnets/:vnet")
	prop := e.Parameters["zone"]
	if want := apischema.Rule("pve-object-id-or-empty"); prop.Pattern != want {
		t.Errorf("zone declares pattern %q, want the catalogued pve-object-id-or-empty %q", prop.Pattern, want)
	}
	if prop.MaxLength == nil {
		t.Error("zone declares no MaxLength, want 64 — the bound pveObjectNameParam sets")
	} else if *prop.MaxLength != 64 {
		t.Errorf("zone declares MaxLength %d, want 64 — the bound pveObjectNameParam sets", *prop.MaxLength)
	}
	for _, tt := range []struct {
		zone string
		ok   bool
	}{
		{"", true},
		{"storezone", true},
		{"..", false},
	} {
		_, err := e.Parameters.Validate(sdnParamsWith(t, e, map[string]any{"zone": tt.zone}))
		if (err == nil) != tt.ok {
			t.Errorf("zone %q: err = %v, want accepted=%v", tt.zone, err, tt.ok)
		}
	}
}

// TestSDNPathIDsRefuseATraversalSegment is the reason pveObjectNameParam
// exists.
//
// None of the SDN client methods runs proxmox.validatePathSegment, and
// url.PathEscape leaves "." and ".." alone — so the request resolves onto
// the PARENT collection once pveproxy normalises it. The worst of them is
// PUT /cluster/sdn/zones/.., which normalises to PUT /cluster/sdn: the SDN
// APPLY endpoint. Same permission, so not an escalation, but "change this
// zone" silently applying the whole pending configuration is not a thing to
// leave declarable.
func TestSDNPathIDsRefuseATraversalSegment(t *testing.T) {
	for _, tt := range []struct {
		method string
		path   string
		param  string
	}{
		{fiber.MethodPut, sdnScope + "/zones/:zone", "zone"},
		{fiber.MethodDelete, sdnScope + "/zones/:zone", "zone"},
		{fiber.MethodPut, sdnScope + "/vnets/:vnet", "vnet"},
		{fiber.MethodDelete, sdnScope + "/vnets/:vnet", "vnet"},
		{fiber.MethodGet, sdnScope + "/vnets/:vnet/subnets", "vnet"},
		{fiber.MethodPost, sdnScope + "/vnets/:vnet/subnets", "vnet"},
		{fiber.MethodPut, sdnScope + "/vnets/:vnet/subnets/:subnet", "subnet"},
		{fiber.MethodDelete, sdnScope + "/vnets/:vnet/subnets/:subnet", "subnet"},
		{fiber.MethodPut, sdnScope + "/controllers/:controller", "controller"},
		{fiber.MethodDelete, sdnScope + "/controllers/:controller", "controller"},
		{fiber.MethodPut, sdnScope + "/ipams/:ipam", "ipam"},
		{fiber.MethodDelete, sdnScope + "/ipams/:ipam", "ipam"},
		{fiber.MethodPut, sdnScope + "/dns/:dns", "dns"},
		{fiber.MethodDelete, sdnScope + "/dns/:dns", "dns"},
	} {
		t.Run(tt.method+" "+tt.path+" "+tt.param, func(t *testing.T) {
			e := declaredEndpoint(t, tt.method, tt.path)
			prop := requireDeclaredProperty(t, e, tt.param)
			if prop.Pattern == "" {
				t.Fatalf("%q carries no pattern, so nothing keeps \"..\" out of a Proxmox path segment", tt.param)
			}
			if _, err := e.Parameters.Validate(sdnParamsWith(t, e, nil)); err != nil {
				t.Fatalf("the known-good parameter set was rejected: %v", err)
			}
			for _, bad := range []string{".", "..", "a/b", `a\b`, "-leading", ""} {
				_, err := e.Parameters.Validate(sdnParamsWith(t, e, map[string]any{tt.param: bad}))
				if err == nil {
					t.Errorf("%q accepted %q", tt.param, bad)
					continue
				}
				if !strings.HasPrefix(err.Error(), tt.param+":") {
					t.Errorf("%q = %q was rejected, but the message blames something else: %v", tt.param, bad, err)
				}
			}
		})
	}
}

// sdnValidValues is one acceptable value per REQUIRED parameter across this
// slice. See networkValidValues for why a helper like this has to fail
// rather than guess.
//
// The ids are the placeholder scheme's, not textbook "tank"/"zone1": these
// are route substitutions and a value that could collide with a real object
// buys nothing and has to be argued about in every review.
var sdnValidValues = map[string]any{
	"cluster_id": testClusterID,
	"zone":       "storezone",
	"vnet":       "storevnet",
	"subnet":     "192.0.2.0/24",
	"controller": "storectl",
	"ipam":       "storeipam",
	"dns":        "storedns",
	"type":       "simple",
}

// sdnParamsWith fills every required parameter of e with a known-good value,
// then applies overrides.
//
// `subnet` needs the same two-spelling care the firewall helper gives
// `cidr`: the create body wants a CIDR and the update/delete path wants the
// derived id, so the path spelling is substituted when the route's path
// names it.
func sdnParamsWith(t *testing.T, e Endpoint, overrides map[string]any) map[string]any {
	t.Helper()
	pathParams := pathParamNames(e.Path)
	out := map[string]any{}
	for name, prop := range e.Parameters {
		if prop.Optional {
			continue
		}
		v, ok := sdnValidValues[name]
		if !ok {
			t.Fatalf("%s %s: no known-good value for required parameter %q; add one to sdnValidValues",
				e.Method, e.Path, name)
		}
		if name == "subnet" && slices.Contains(pathParams, name) {
			v = "storezone-192.0.2.0-24"
		}
		out[name] = v
	}
	for k, v := range overrides {
		out[k] = v
	}
	return out
}
