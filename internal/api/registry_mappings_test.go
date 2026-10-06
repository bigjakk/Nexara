package api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
)

// mappingKind is one of the two mirrored families of cluster resource mappings. The
// cluster-wide listing and the create share a path, as do the update and the delete.
type mappingKind struct {
	name string // "USB" or "PCI"
	word string // stem of the ids the fixtures use
	// list is the node listing, create the collection, item one mapping, usage its usage.
	list, create, item, usage string
	// updateBody is a body the update accepts.
	updateBody string
}

var mappingKinds = []mappingKind{
	{
		name: "USB", word: "usb",
		list: clusterScope + "/nodes/:node_name/usb-mappings", create: clusterScope + "/usb-mappings",
		item: clusterScope + "/usb-mappings/:mapping_id", usage: clusterScope + "/usb-mappings/:mapping_id/usage",
		updateBody: `{"map":["node=pve-01,id=1234:5678"],"digest":"0123456789abcdef"}`,
	},
	{
		name: "PCI", word: "gpu",
		list: clusterScope + "/nodes/:node_name/pci-mappings", create: clusterScope + "/pci-mappings",
		item: clusterScope + "/pci-mappings/:mapping_id", usage: clusterScope + "/pci-mappings/:mapping_id/usage",
		updateBody: `{"map":["id=1234:5678,node=pve-01,path=0000:01:00.0"],"digest":"0123456789abcdef"}`,
	},
}

const (
	usbMappingCreatePath = clusterScope + "/usb-mappings"
	usbMappingPath       = clusterScope + "/usb-mappings/:mapping_id"
	pciMappingCreatePath = clusterScope + "/pci-mappings"
	pciMappingPath       = clusterScope + "/pci-mappings/:mapping_id"
	validDigest          = "0123456789abcdef"
)

// mappingTarget fills a declared path in: the cluster, and the mapping id if it names one.
func mappingTarget(path, id string) string {
	return strings.NewReplacer(":cluster_id", testClusterID, ":mapping_id", id).Replace(path)
}

// mappingProbe mounts one declared route with a capturing handler, no permission gate
// and no route limiter (a shared declaration's limiter is shared state): these tests
// are about the parameters.
func mappingProbe(t *testing.T, method, path string) (*fiber.App, *capture) {
	t.Helper()
	cap := &capture{}
	e := sharedEndpoint(t, method, path)
	e.Handler = cap.handler()
	e.Permissions = Permissions{SelfService: "parameter fixture; authorization is exercised separately"}
	e.RateLimiter = nil
	return newRegistryApp(t, noAuth(), e), cap
}

// mappingBody is one request body to a route and whether the schema lets it through
// (204, handler reached) or refuses it (400, handler not reached).
type mappingBody struct {
	name, body string
	ok         bool
}

func accepted(rows ...mappingBody) []mappingBody {
	for i := range rows {
		rows[i].ok = true
	}
	return rows
}

// runMappingBodies sends each body to the route, one subtest each.
func runMappingBodies(t *testing.T, method, path, target string, rows ...[]mappingBody) {
	t.Helper()
	app, cap := mappingProbe(t, method, path)
	for _, group := range rows {
		for _, tt := range group {
			t.Run(tt.name, func(t *testing.T) {
				cap.called = false
				status, env := send(t, app, jsonRequest(method, target, tt.body))
				want := fiber.StatusBadRequest
				if tt.ok {
					want = fiber.StatusNoContent
				}
				if status != want {
					t.Errorf("status = %d (%q), want %d", status, env.Message, want)
				}
				if cap.called != tt.ok {
					t.Errorf("handler reached = %v, want %v", cap.called, tt.ok)
				}
			})
		}
	}
}

// requireParams holds what the handler read to want.
func requireParams(t *testing.T, cap *capture, want map[string]string) {
	t.Helper()
	for key, w := range want {
		if got := cap.params.String(key); got != w {
			t.Errorf("%s = %q, want %q", key, got, w)
		}
	}
}

// TestMappingRoutesDeclareTheirPermissions pins the gates. The node listing sits with
// the node hardware listings the same dialog reads, on view:node; the create, update
// and delete are cluster configuration, on manage:cluster (looser than Proxmox, which
// keeps Mapping.Modify out of PVEAdmin, by the operator's choice; see
// registry_mappings.go); the cluster-wide listing is cluster configuration to read,
// view:cluster; the usage answers with guests, so it is view:vm like every guest listing.
func TestMappingRoutesDeclareTheirPermissions(t *testing.T) {
	for _, k := range mappingKinds {
		for _, tt := range []struct{ method, path, want string }{
			{fiber.MethodGet, k.list, "view:node"},
			{fiber.MethodPost, k.create, "manage:cluster"},
			{fiber.MethodGet, k.create, "view:cluster"},
			{fiber.MethodPut, k.item, "manage:cluster"},
			{fiber.MethodDelete, k.item, "manage:cluster"},
			{fiber.MethodGet, k.usage, "view:vm"},
		} {
			e := sharedEndpoint(t, tt.method, tt.path)
			switch {
			case e.Permissions.Check == nil:
				t.Errorf("%s %s declares %q rather than a Check", tt.method, tt.path, e.Permissions.Describe())
			case e.Permissions.Describe() != tt.want:
				t.Errorf("%s %s declares %q, want %q", tt.method, tt.path, e.Permissions.Describe(), tt.want)
			case e.Permissions.Check.Scope != ScopeCluster:
				t.Errorf("%s %s is %s-scoped, want cluster", tt.method, tt.path, e.Permissions.Check.Scope)
			}
		}
	}
}

func TestEveryMappingEndpointIsDocumented(t *testing.T) {
	for _, k := range mappingKinds {
		for _, tt := range []struct{ method, path, group string }{
			{fiber.MethodGet, k.list, "Nodes"},
			{fiber.MethodPost, k.create, "Clusters"},
			{fiber.MethodGet, k.create, "Clusters"},
			{fiber.MethodPut, k.item, "Clusters"},
			{fiber.MethodDelete, k.item, "Clusters"},
			{fiber.MethodGet, k.usage, "Clusters"},
		} {
			e := sharedEndpoint(t, tt.method, tt.path)
			key := tt.method + " " + tt.path
			if err := e.Parameters.Compile(); err != nil {
				t.Errorf("%s: %v", key, err)
			}
			if strings.TrimSpace(e.Description) == "" {
				t.Errorf("%s has no description", key)
			}
			if e.Group != tt.group {
				t.Errorf("%s is in group %q, want %q", key, e.Group, tt.group)
			}
			if names := pathParamNames(e.Path); len(names) == 0 || names[0] != "cluster_id" {
				t.Errorf("%s has path parameters %v; :cluster_id must be the first", key, names)
			}
			for name, prop := range e.Parameters {
				if strings.TrimSpace(prop.Description) == "" {
					t.Errorf("%s: parameter %q has no description", key, name)
				}
			}
		}
	}
}

// TestCreateUSBMappingRefusesMalformedBodies drives the declaration with real
// requests: a well-formed body reaches the handler with every value intact, and each
// malformed one is a 400 before it does.
func TestCreateUSBMappingRefusesMalformedBodies(t *testing.T) {
	const valid = `"mapping_id":"usbdev01","node":"pve-01","device_id":"1234:5678","path":"1-2.3","description":"Example serial adapter"`
	target := mappingTarget(usbMappingCreatePath, "")
	with := func(old, new string) string { return `{` + strings.Replace(valid, old, new, 1) + `}` }

	t.Run("a valid body reaches the handler intact", func(t *testing.T) {
		app, cap := mappingProbe(t, fiber.MethodPost, usbMappingCreatePath)
		if status, env := send(t, app, jsonRequest(http.MethodPost, target, "{"+valid+"}")); status != fiber.StatusNoContent {
			t.Fatalf("status = %d (%q), want 204", status, env.Message)
		}
		requireParams(t, cap, map[string]string{
			"mapping_id": "usbdev01", "node": "pve-01", "device_id": "1234:5678", "path": "1-2.3", "description": "Example serial adapter",
		})
	})

	runMappingBodies(t, fiber.MethodPost, usbMappingCreatePath, target,
		accepted(mappingBody{name: "the path is optional", body: `{"mapping_id":"usbdev01","node":"pve-01","device_id":"1234:5678"}`}),
		[]mappingBody{
			{name: "no device id", body: `{"mapping_id":"usbdev01","node":"pve-01"}`},
			{name: "no mapping id", body: `{"node":"pve-01","device_id":"1234:5678"}`},
			{name: "no node", body: `{"mapping_id":"usbdev01","device_id":"1234:5678"}`},
			{name: "a mapping id starting with a digit", body: with(`"usbdev01"`, `"1usb"`)},
			{name: "a device id with 0x", body: with(`"1234:5678"`, `"0x1234:0x5678"`)},
			{name: "a device id smuggling a key", body: with(`"1234:5678"`, `"1234:5678,path=9-9"`)},
			{name: "a port with no port", body: with(`"1-2.3"`, `"1-"`)},
			{name: "a port over 64 characters", body: with(`"1-2.3"`, `"1-`+strings.Repeat("1.", 31)+`1"`)},
			{name: "a node smuggling a key", body: with(`"pve-01"`, `"pve-01,path=9-9"`)},
			{name: "a description over 4096 characters", body: with(`"Example serial adapter"`, fmt.Sprintf("%q", strings.Repeat("d", 4097)))},
			// "id" is what Proxmox calls it, and the middleware resolves a cluster from;
			// the parameter set is closed, so it is refused and not silently dropped.
			{name: "the Proxmox spelling of the id", body: `{"id":"usbdev01","node":"pve-01","device_id":"1234:5678"}`},
		})
}

// TestCreatePCIMappingRefusesMalformedBodies drives the declaration with real
// requests. The body names only the node and the device's address: the rest of the
// entry is the node's own report of the device, so an id, a subsystem id, an IOMMU
// group or an mdev flag in the body is refused, not used.
func TestCreatePCIMappingRefusesMalformedBodies(t *testing.T) {
	const valid = `"mapping_id":"gpu01","node":"pve-01","path":"0000:01:00.0","description":"Example GPU"`
	target := mappingTarget(pciMappingCreatePath, "")
	with := func(old, new string) string { return `{` + strings.Replace(valid, old, new, 1) + `}` }

	t.Run("a valid body reaches the handler intact", func(t *testing.T) {
		app, cap := mappingProbe(t, fiber.MethodPost, pciMappingCreatePath)
		if status, env := send(t, app, jsonRequest(http.MethodPost, target, "{"+valid+"}")); status != fiber.StatusNoContent {
			t.Fatalf("status = %d (%q), want 204", status, env.Message)
		}
		requireParams(t, cap, map[string]string{"mapping_id": "gpu01", "node": "pve-01", "path": "0000:01:00.0", "description": "Example GPU"})
	})

	runMappingBodies(t, fiber.MethodPost, pciMappingCreatePath, target,
		accepted(
			mappingBody{name: "the whole device, without a function", body: `{"mapping_id":"gpu01","node":"pve-01","path":"0000:01:00"}`},
			mappingBody{name: "a domain wider than four digits", body: `{"mapping_id":"gpu01","node":"pve-01","path":"10000:01:00.0"}`},
			mappingBody{name: "a path of 64 characters", body: `{"mapping_id":"gpu01","node":"pve-01","path":"` + strings.Repeat("0", 56) + `:01:00.0"}`},
		),
		[]mappingBody{
			{name: "no path", body: `{"mapping_id":"gpu01","node":"pve-01"}`},
			{name: "no node", body: `{"mapping_id":"gpu01","path":"0000:01:00.0"}`},
			{name: "no mapping id", body: `{"node":"pve-01","path":"0000:01:00.0"}`},
			{name: "a mapping id starting with a digit", body: with(`"gpu01"`, `"1gpu"`)},
			{name: "a path without the domain", body: with(`"0000:01:00.0"`, `"01:00.0"`)},
			{name: "a path in uppercase", body: with(`"0000:01:00.0"`, `"0000:0A:00.0"`)},
			{name: "a list of paths", body: with(`"0000:01:00.0"`, `"0000:01:00.0;0000:02:00.0"`)},
			{name: "a path smuggling a key", body: with(`"0000:01:00.0"`, `"0000:01:00.0,node=pve-02"`)},
			{name: "a path over 64 characters", body: with(`"0000:01:00.0"`, `"`+strings.Repeat("0", 57)+`:01:00.0"`)},
			{name: "a node smuggling a key", body: with(`"pve-01"`, `"pve-01,path=0000:02:00.0"`)},
			{name: "a description over 4096 characters", body: with(`"Example GPU"`, fmt.Sprintf("%q", strings.Repeat("d", 4097)))},
			{name: "a device id the node did not report", body: `{` + valid + `,"device_id":"1234:5678"}`},
			{name: "an IOMMU group the node did not report", body: `{` + valid + `,"iommugroup":1}`},
			{name: "an mdev flag the node did not report", body: `{` + valid + `,"mdev":true}`},
			{name: "the Proxmox spelling of the id", body: `{"id":"gpu01","node":"pve-01","path":"0000:01:00.0"}`},
		})
}

// TestUpdateUSBMappingRefusesMalformedBodies drives the update's declaration with real requests.
func TestUpdateUSBMappingRefusesMalformedBodies(t *testing.T) {
	target := mappingTarget(usbMappingPath, "usbdev01")
	const entry = `"node=pve-01,id=1234:5678"`

	t.Run("a valid body reaches the handler intact", func(t *testing.T) {
		app, cap := mappingProbe(t, fiber.MethodPut, usbMappingPath)
		body := `{"map":["node=pve-01,id=1234:5678,path=1-2","node=pve-02,id=abcd:ef01"],` +
			`"description":"Example serial adapter","digest":"0123456789abcdef"}`
		if status, env := send(t, app, jsonRequest(http.MethodPut, target, body)); status != fiber.StatusNoContent {
			t.Fatalf("status = %d (%q), want 204", status, env.Message)
		}
		requireParams(t, cap, map[string]string{"mapping_id": "usbdev01", "digest": validDigest})
		if got, want := cap.params.Strings("map"), []string{"node=pve-01,id=1234:5678,path=1-2", "node=pve-02,id=abcd:ef01"}; !slices.Equal(got, want) {
			t.Errorf("map = %q, want %q", got, want)
		}
		if got, set := cap.params.OptString("description"); !set || got != "Example serial adapter" {
			t.Errorf("description = %q (set %v)", got, set)
		}
	})

	// The description's three states reach the handler apart: omitted leaves it, "" removes it.
	for _, tt := range []struct {
		name, body  string
		wantDesc    string
		wantDescSet bool
		wantMap     []string
	}{
		{"an omitted description is not set", `{"map":[` + entry + `],"digest":"` + validDigest + `"}`, "", false, []string{"node=pve-01,id=1234:5678"}},
		{"an empty description is set, and empty", `{"map":[` + entry + `],"description":"","digest":"` + validDigest + `"}`, "", true, []string{"node=pve-01,id=1234:5678"}},
		// apischema reads a lone string as a one-element array (toSlice) and never
		// splits it: an entry is itself comma-separated, so splitting would tear it into keys.
		{"a lone string is one whole entry", `{"map":"node=pve-01,id=1234:5678","digest":"` + validDigest + `"}`, "", false, []string{"node=pve-01,id=1234:5678"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			app, cap := mappingProbe(t, fiber.MethodPut, usbMappingPath)
			if status, env := send(t, app, jsonRequest(http.MethodPut, target, tt.body)); status != fiber.StatusNoContent {
				t.Fatalf("status = %d (%q), want 204", status, env.Message)
			}
			if got, set := cap.params.OptString("description"); set != tt.wantDescSet || got != tt.wantDesc {
				t.Errorf("description = %q (set %v), want %q (set %v)", got, set, tt.wantDesc, tt.wantDescSet)
			}
			if got := cap.params.Strings("map"); !slices.Equal(got, tt.wantMap) {
				t.Errorf("map = %q, want %q", got, tt.wantMap)
			}
		})
	}

	runMappingBodies(t, fiber.MethodPut, usbMappingPath, target,
		accepted(mappingBody{name: "a 64-character digest", body: `{"map":[` + entry + `],"digest":"` + strings.Repeat("a", 64) + `"}`}),
		[]mappingBody{
			{name: "no map", body: `{"digest":"` + validDigest + `"}`},
			// Remove the last entry by deleting the mapping.
			{name: "an empty map", body: `{"map":[],"digest":"` + validDigest + `"}`},
			{name: "a map entry that is not a string", body: `{"map":[{"node":"pve-01"}],"digest":"` + validDigest + `"}`},
			// The compare-and-swap is the route's reason to exist.
			{name: "no digest", body: `{"map":[` + entry + `]}`},
			{name: "an empty digest", body: `{"map":[` + entry + `],"digest":""}`},
			{name: "a digest over 64 characters", body: `{"map":[` + entry + `],"digest":"` + strings.Repeat("a", 65) + `"}`},
			{name: "a description over 4096 characters", body: `{"map":[` + entry + `],"digest":"` + validDigest + `","description":` + fmt.Sprintf("%q", strings.Repeat("d", 4097)) + `}`},
			// The id comes off the path; the parameter set is closed.
			{name: "an id in the body", body: `{"id":"usbdev02","map":[` + entry + `],"digest":"` + validDigest + `"}`},
		})
}

// TestUpdatePCIMappingRefusesMalformedBodies drives the PCI update's declaration with
// real requests. The body names entries only as the listing returned them, and a
// device only by its node and address: an id, a group or a flag of the caller's is
// refused, not used.
func TestUpdatePCIMappingRefusesMalformedBodies(t *testing.T) {
	target := mappingTarget(pciMappingPath, "gpu01")
	const entry = `"id=1234:5678,node=pve-01,path=0000:01:00.0"`
	const d = `"digest":"` + validDigest + `"`
	m := `"map":[` + entry + `]`

	t.Run("a valid body reaches the handler intact", func(t *testing.T) {
		app, cap := mappingProbe(t, fiber.MethodPut, pciMappingPath)
		body := `{` + m + `,"add_node":"pve-01","add_path":"0000:02:00.0","replace":` + entry + `,"description":"Example GPU",` + d + `}`
		if status, env := send(t, app, jsonRequest(http.MethodPut, target, body)); status != fiber.StatusNoContent {
			t.Fatalf("status = %d (%q), want 204", status, env.Message)
		}
		requireParams(t, cap, map[string]string{
			"mapping_id": "gpu01", "add_node": "pve-01", "add_path": "0000:02:00.0",
			"replace": "id=1234:5678,node=pve-01,path=0000:01:00.0", "description": "Example GPU", "digest": validDigest,
		})
		if got := cap.params.Strings("map"); !slices.Equal(got, []string{"id=1234:5678,node=pve-01,path=0000:01:00.0"}) {
			t.Errorf("map = %q", got)
		}
	})

	add := func(extra string) string {
		return `{` + m + `,"add_node":"pve-01","add_path":"0000:02:00.0",` + extra + d + `}`
	}
	runMappingBodies(t, fiber.MethodPut, pciMappingPath, target,
		accepted(
			mappingBody{name: "entries kept, nothing added", body: `{` + m + `,` + d + `}`},
			mappingBody{name: "a device added", body: `{` + m + `,"add_node":"pve-02","add_path":"0000:02:00.0",` + d + `}`},
			mappingBody{name: "the whole device added", body: `{` + m + `,"add_node":"pve-02","add_path":"0000:02:00",` + d + `}`},
			mappingBody{name: "an entry replaced", body: `{` + m + `,"add_node":"pve-01","add_path":"0000:02:00.0","replace":` + entry + `,` + d + `}`},
			mappingBody{name: "the description removed", body: `{` + m + `,"description":"",` + d + `}`},
		),
		[]mappingBody{
			{name: "no map", body: `{` + d + `}`},
			// The last entry goes by deleting the mapping.
			{name: "an empty map", body: `{"map":[],` + d + `}`},
			{name: "no digest", body: `{` + m + `}`},
			{name: "an empty digest", body: `{` + m + `,"digest":""}`},
			// Requires, both ways: a device is its node and its address.
			{name: "a node without an address", body: `{` + m + `,"add_node":"pve-01",` + d + `}`},
			{name: "an address without a node", body: `{` + m + `,"add_path":"0000:02:00.0",` + d + `}`},
			{name: "a replace without a device", body: `{` + m + `,"replace":` + entry + `,` + d + `}`},
			{name: "a replace with a node only", body: `{` + m + `,"add_node":"pve-01","replace":` + entry + `,` + d + `}`},
			{name: "an empty replace", body: add(`"replace":"",`)},
			{name: "an address in uppercase", body: `{` + m + `,"add_node":"pve-01","add_path":"0000:0A:00.0",` + d + `}`},
			{name: "an address smuggling a key", body: `{` + m + `,"add_node":"pve-01","add_path":"0000:02:00.0,node=pve-02",` + d + `}`},
			{name: "a list of addresses", body: `{` + m + `,"add_node":"pve-01","add_path":"0000:02:00.0;0000:03:00.0",` + d + `}`},
			{name: "a node smuggling a key", body: `{` + m + `,"add_node":"pve-01,path=0000:02:00.0","add_path":"0000:02:00.0",` + d + `}`},
			// The entry is the node's own report of the device: nothing of it comes from the caller.
			{name: "a device id", body: add(`"id":"1234:5678",`)},
			{name: "an IOMMU group", body: add(`"iommugroup":1,`)},
			{name: "a subsystem id", body: add(`"subsystem_id":"abcd:ef01",`)},
			{name: "an mdev flag", body: `{` + m + `,"mdev":true,` + d + `}`},
			{name: "a node and path of Proxmox's spelling", body: `{` + m + `,"node":"pve-01","path":"0000:02:00.0",` + d + `}`},
			{name: "a map entry that is not a string", body: `{"map":[{"node":"pve-01"}],` + d + `}`},
			{name: "a digest over 64 characters", body: `{` + m + `,"digest":"` + strings.Repeat("a", 65) + `"}`},
		})
}

// TestMappingIDParam holds the path parameter every per-mapping route takes: it
// addresses any id the create mints, down to one character and up to the create's
// 128, and nothing that could leave the mapping's own path.
func TestMappingIDParam(t *testing.T) {
	for _, k := range mappingKinds {
		l := k.word[:1]
		for _, r := range []struct{ method, path, body string }{
			{fiber.MethodPut, k.item, k.updateBody},
			{fiber.MethodDelete, k.item, ""},
			{fiber.MethodGet, k.usage, ""},
		} {
			app, cap := mappingProbe(t, r.method, r.path)
			for _, id := range []string{l, k.word + "01", k.word + "_dev-01", strings.Repeat(l, 128)} {
				t.Run(k.name+" "+r.method+" accepts "+id[:min(len(id), 16)], func(t *testing.T) {
					cap.called = false
					status, env := send(t, app, jsonRequest(r.method, mappingTarget(r.path, id), r.body))
					if status != fiber.StatusNoContent || !cap.called {
						t.Fatalf("status = %d (%q), want 204", status, env.Message)
					}
					if got := cap.params.String("mapping_id"); got != id {
						t.Errorf("mapping_id = %q, want %q", got, id)
					}
				})
			}
			for _, id := range []string{"1" + k.word, "-" + k.word, k.word + ".dev", "%2e%2e", k.word + "%2Fdev", strings.Repeat(l, 129)} {
				t.Run(k.name+" "+r.method+" refuses "+id[:min(len(id), 16)], func(t *testing.T) {
					cap.called = false
					status, env := send(t, app, jsonRequest(r.method, mappingTarget(r.path, id), r.body))
					if status != fiber.StatusBadRequest || cap.called {
						t.Errorf("status = %d (%q), handler reached = %v, want a 400 before it", status, env.Message, cap.called)
					}
				})
			}
		}
	}
}

// TestDeleteMappingDigestParam: the delete's digest is optional (Proxmox's delete
// takes none) and held to pve-config-digest's 64 characters when sent.
func TestDeleteMappingDigestParam(t *testing.T) {
	for _, k := range mappingKinds {
		app, cap := mappingProbe(t, fiber.MethodDelete, k.item)
		base := mappingTarget(k.item, k.word+"dev01")
		for _, tt := range []struct {
			name, query string
			want        int
		}{
			{"no digest", "", fiber.StatusNoContent},
			{"a digest", "?digest=" + validDigest, fiber.StatusNoContent},
			{"a 64-character digest", "?digest=" + strings.Repeat("a", 64), fiber.StatusNoContent},
			{"a digest over 64 characters", "?digest=" + strings.Repeat("a", 65), fiber.StatusBadRequest},
			// Sent means compared: an empty one would read as "not sent".
			{"an empty digest", "?digest=", fiber.StatusBadRequest},
		} {
			t.Run(k.name+": "+tt.name, func(t *testing.T) {
				cap.called, cap.params = false, nil
				status, env := send(t, app, jsonRequest(http.MethodDelete, base+tt.query, ""))
				if status != tt.want {
					t.Fatalf("status = %d (%q), want %d", status, env.Message, tt.want)
				}
				if tt.want == fiber.StatusNoContent && tt.query != "" {
					if got, _ := cap.params.OptString("digest"); got != strings.TrimPrefix(tt.query, "?digest=") {
						t.Errorf("digest = %q", got)
					}
				}
			})
		}
	}
}

// usageProbe mounts limiters on routes, each behind a stand-in for authRequired (which
// runs before a route limiter and sets user_id) that makes the caller named in
// X-Test-User a user of its own; every request comes from the same IP. It returns the
// status of a GET.
func usageProbe(t *testing.T, users []string, routes map[string]fiber.Handler) func(path, user string) int {
	t.Helper()
	ids := map[string]uuid.UUID{}
	for _, u := range users {
		ids[u] = uuid.New()
	}
	app := fiber.New()
	for path, limiter := range routes {
		app.Get(path, func(c fiber.Ctx) error {
			c.Locals("user_id", ids[c.Get("X-Test-User")])
			return c.Next()
		}, limiter, func(c fiber.Ctx) error { return c.SendStatus(fiber.StatusOK) })
	}
	return func(path, user string) int {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("X-Test-User", user)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test: %v", err)
		}
		_ = resp.Body.Close()
		return resp.StatusCode
	}
}

// TestUSBMappingUsageIsRateLimited: each usage call reads every VM's configuration
// and the route is open to every Viewer, so it carries its own per-user limiter
// (mappingUsageLimiter in middleware.go), attached to the route, and only the two
// usage routes do.
func TestUSBMappingUsageIsRateLimited(t *testing.T) {
	for _, k := range mappingKinds {
		for _, tt := range []struct{ method, path string }{{fiber.MethodGet, k.create}, {fiber.MethodPut, k.item}, {fiber.MethodDelete, k.item}} {
			if e := sharedEndpoint(t, tt.method, tt.path); e.RateLimiter != nil {
				t.Errorf("%s %s carries a route limiter; only the usage routes should", tt.method, tt.path)
			}
		}
	}
	// This route's limiter is shared with every test that mounts the shared stub's
	// declaration, but its buckets are per user and these users are fresh.
	e := sharedEndpoint(t, fiber.MethodGet, mappingKinds[0].usage)
	if e.RateLimiter == nil {
		t.Fatal("the usage route carries no limiter of its own")
	}
	get := usageProbe(t, []string{"alice", "bob"}, map[string]fiber.Handler{"/probe": e.RateLimiter})
	got := make([]int, 0, 12)
	for range 12 {
		got = append(got, get("/probe", "alice"))
	}
	for i, status := range got {
		want := fiber.StatusOK
		if i >= 10 {
			want = fiber.StatusTooManyRequests
		}
		if status != want {
			t.Fatalf("alice's statuses = %v, want 10 × 200 then 429", got)
		}
	}
	// Per user, not per IP: bob, behind the same address, still has his ten.
	if status := get("/probe", "bob"); status != fiber.StatusOK {
		t.Errorf("bob's first request = %d, want 200", status)
	}
}

// TestMappingUsageRoutesShareOneLimiter: the two usage routes share ONE limiter, so a
// user's checks of either kind draw on the same ten a minute. Proved by what it does
// (six USB and four PCI requests spend it, the eleventh is refused on either route),
// since two handlers compared by code pointer look equal even with separate state.
func TestMappingUsageRoutesShareOneLimiter(t *testing.T) {
	usb, pci := sharedEndpoint(t, fiber.MethodGet, mappingKinds[0].usage).RateLimiter, sharedEndpoint(t, fiber.MethodGet, mappingKinds[1].usage).RateLimiter
	if usb == nil || pci == nil {
		t.Fatalf("usage limiters: usb %v, pci %v; want both", usb != nil, pci != nil)
	}
	get := usageProbe(t, []string{"alice"}, map[string]fiber.Handler{"/usb": usb, "/pci": pci})
	for i := range 10 {
		path := "/usb"
		if i >= 6 {
			path = "/pci"
		}
		if status := get(path, "alice"); status != fiber.StatusOK {
			t.Fatalf("request %d on %s = %d, want 200", i+1, path, status)
		}
	}
	for _, path := range []string{"/usb", "/pci"} {
		if status := get(path, "alice"); status != fiber.StatusTooManyRequests {
			t.Errorf("the eleventh request on %s = %d, want 429", path, status)
		}
	}
}

// The pci-address rule's catalogue entry says each site holds it to 64 characters
// (proxmox.pciMappingPathMax): a site that did not would take an address the client
// then refuses.
func TestPCIAddressSitesAreHeldTo64Characters(t *testing.T) {
	var sites []ruleSite
	for _, s := range declaredRuleSites(t) {
		if s.rule == "pci-address" {
			sites = append(sites, s)
		}
	}
	if len(sites) < 2 {
		t.Fatalf("pci-address sites = %v, want at least the PCI create's path and the update's add_path", sites)
	}
	for _, s := range sites {
		if s.prop.MaxLength == nil || *s.prop.MaxLength != 64 {
			got := "none"
			if s.prop.MaxLength != nil {
				got = fmt.Sprint(*s.prop.MaxLength)
			}
			t.Errorf("%s: MaxLength = %s, want 64, the ceiling the catalogue states for every site", s, got)
		}
	}
}
