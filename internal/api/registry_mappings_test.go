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

const (
	usbMappingListPath    = clusterScope + "/nodes/:node_name/usb-mappings"
	usbMappingCreatePath  = clusterScope + "/usb-mappings"
	usbMappingClusterPath = clusterScope + "/usb-mappings"
	usbMappingPath        = clusterScope + "/usb-mappings/:mapping_id"
	usbMappingUsagePath   = clusterScope + "/usb-mappings/:mapping_id/usage"
	pciMappingListPath    = clusterScope + "/nodes/:node_name/pci-mappings"
	pciMappingCreatePath  = clusterScope + "/pci-mappings"
)

// TestUSBMappingRoutesDeclareTheirPermissions pins the gates. The node
// listing sits with the node hardware listings the same dialog reads, on
// view:node; the create, update and delete are cluster configuration, on
// manage:cluster — looser than Proxmox, which keeps Mapping.Modify out of
// PVEAdmin, by the operator's choice (see registry_mappings.go). The
// cluster-wide listing is cluster configuration to read, on view:cluster; the
// usage answers with guests, so it is view:vm like every guest listing.
func TestUSBMappingRoutesDeclareTheirPermissions(t *testing.T) {
	for _, tt := range []struct{ method, path, want string }{
		{fiber.MethodGet, usbMappingListPath, "view:node"},
		{fiber.MethodPost, usbMappingCreatePath, "manage:cluster"},
		{fiber.MethodGet, usbMappingClusterPath, "view:cluster"},
		{fiber.MethodPut, usbMappingPath, "manage:cluster"},
		{fiber.MethodDelete, usbMappingPath, "manage:cluster"},
		{fiber.MethodGet, usbMappingUsagePath, "view:vm"},
	} {
		e := declaredEndpoint(t, tt.method, tt.path)
		if e.Permissions.Check == nil {
			t.Errorf("%s %s declares %q rather than a Check", tt.method, tt.path, e.Permissions.Describe())
			continue
		}
		if got := e.Permissions.Describe(); got != tt.want {
			t.Errorf("%s %s declares %q, want %q", tt.method, tt.path, got, tt.want)
		}
		if e.Permissions.Check.Scope != ScopeCluster {
			t.Errorf("%s %s is %s-scoped, want cluster", tt.method, tt.path, e.Permissions.Check.Scope)
		}
	}
}

// TestCreateUSBMappingRefusesMalformedBodies drives the declaration with real
// requests: a well-formed body reaches the handler with every value intact,
// and each malformed one is a 400 before it does.
func TestCreateUSBMappingRefusesMalformedBodies(t *testing.T) {
	const valid = `"mapping_id":"usbdev01","node":"pve-01","device_id":"1234:5678","path":"1-2.3","description":"Example serial adapter"`
	target := strings.Replace(usbMappingCreatePath, ":cluster_id", testClusterID, 1)

	t.Run("a valid body reaches the handler intact", func(t *testing.T) {
		cap := &capture{}
		e := declaredEndpoint(t, fiber.MethodPost, usbMappingCreatePath)
		e.Handler = cap.handler()
		e.Permissions = Permissions{SelfService: "parameter fixture; authorization is exercised separately"}
		app := newRegistryApp(t, noAuth(), e)

		status, env := send(t, app, jsonRequest(http.MethodPost, target, "{"+valid+"}"))
		if status != fiber.StatusNoContent {
			t.Fatalf("status = %d (%q), want 204", status, env.Message)
		}
		for key, want := range map[string]string{
			"mapping_id":  "usbdev01",
			"node":        "pve-01",
			"device_id":   "1234:5678",
			"path":        "1-2.3",
			"description": "Example serial adapter",
		} {
			if got := cap.params.String(key); got != want {
				t.Errorf("%s = %q, want %q", key, got, want)
			}
		}
	})

	for _, tt := range []struct{ name, body string }{
		{"the path is optional", `{"mapping_id":"usbdev01","node":"pve-01","device_id":"1234:5678"}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cap := &capture{}
			e := declaredEndpoint(t, fiber.MethodPost, usbMappingCreatePath)
			e.Handler = cap.handler()
			e.Permissions = Permissions{SelfService: "parameter fixture; authorization is exercised separately"}
			app := newRegistryApp(t, noAuth(), e)

			if status, env := send(t, app, jsonRequest(http.MethodPost, target, tt.body)); status != fiber.StatusNoContent {
				t.Errorf("status = %d (%q), want 204", status, env.Message)
			}
		})
	}

	for _, tt := range []struct{ name, body string }{
		{"no device id", `{"mapping_id":"usbdev01","node":"pve-01"}`},
		{"no mapping id", `{"node":"pve-01","device_id":"1234:5678"}`},
		{"no node", `{"mapping_id":"usbdev01","device_id":"1234:5678"}`},
		{"a mapping id starting with a digit", `{` + strings.Replace(valid, `"usbdev01"`, `"1usb"`, 1) + `}`},
		{"a device id with 0x", `{` + strings.Replace(valid, `"1234:5678"`, `"0x1234:0x5678"`, 1) + `}`},
		{"a device id smuggling a key", `{` + strings.Replace(valid, `"1234:5678"`, `"1234:5678,path=9-9"`, 1) + `}`},
		{"a port with no port", `{` + strings.Replace(valid, `"1-2.3"`, `"1-"`, 1) + `}`},
		{"a port over 64 characters", `{` + strings.Replace(valid, `"1-2.3"`,
			`"1-`+strings.Repeat("1.", 31)+`1"`, 1) + `}`},
		{"a node smuggling a key", `{` + strings.Replace(valid, `"pve-01"`, `"pve-01,path=9-9"`, 1) + `}`},
		{"a description over 4096 characters", `{` + strings.Replace(valid, `"Example serial adapter"`,
			fmt.Sprintf("%q", strings.Repeat("d", 4097)), 1) + `}`},
		// "id" is what Proxmox calls it, and what the middleware resolves a
		// cluster from; the parameter set is closed, so it is refused rather
		// than silently dropped.
		{"the Proxmox spelling of the id", `{"id":"usbdev01","node":"pve-01","device_id":"1234:5678"}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cap := &capture{}
			e := declaredEndpoint(t, fiber.MethodPost, usbMappingCreatePath)
			e.Handler = cap.handler()
			e.Permissions = Permissions{SelfService: "parameter fixture; authorization is exercised separately"}
			app := newRegistryApp(t, noAuth(), e)

			status, env := send(t, app, jsonRequest(http.MethodPost, target, tt.body))
			if status != fiber.StatusBadRequest {
				t.Errorf("status = %d (%q), want 400", status, env.Message)
			}
			if cap.called {
				t.Error("the handler ran for a malformed body")
			}
		})
	}
}

func TestEveryUSBMappingEndpointIsDocumented(t *testing.T) {
	for _, tt := range []struct{ method, path, group string }{
		{fiber.MethodGet, usbMappingListPath, "Nodes"},
		{fiber.MethodPost, usbMappingCreatePath, "Clusters"},
		{fiber.MethodGet, usbMappingClusterPath, "Clusters"},
		{fiber.MethodPut, usbMappingPath, "Clusters"},
		{fiber.MethodDelete, usbMappingPath, "Clusters"},
		{fiber.MethodGet, usbMappingUsagePath, "Clusters"},
	} {
		e := declaredEndpoint(t, tt.method, tt.path)
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
		names := pathParamNames(e.Path)
		if len(names) == 0 || names[0] != "cluster_id" {
			t.Errorf("%s has path parameters %v; :cluster_id must be the first", key, names)
		}
		for name, prop := range e.Parameters {
			if strings.TrimSpace(prop.Description) == "" {
				t.Errorf("%s: parameter %q has no description", key, name)
			}
		}
	}
}

// TestPCIMappingRoutesDeclareTheirPermissions pins the PCI gates, which are
// the USB ones for the same reasons: the node listing sits with the node
// hardware listings the Add PCI Device dialog also reads, and the create is
// cluster configuration.
func TestPCIMappingRoutesDeclareTheirPermissions(t *testing.T) {
	for _, tt := range []struct{ method, path, want string }{
		{fiber.MethodGet, pciMappingListPath, "view:node"},
		{fiber.MethodPost, pciMappingCreatePath, "manage:cluster"},
	} {
		e := declaredEndpoint(t, tt.method, tt.path)
		if e.Permissions.Check == nil {
			t.Errorf("%s %s declares %q rather than a Check", tt.method, tt.path, e.Permissions.Describe())
			continue
		}
		if got := e.Permissions.Describe(); got != tt.want {
			t.Errorf("%s %s declares %q, want %q", tt.method, tt.path, got, tt.want)
		}
		if e.Permissions.Check.Scope != ScopeCluster {
			t.Errorf("%s %s is %s-scoped, want cluster", tt.method, tt.path, e.Permissions.Check.Scope)
		}
	}
}

func TestEveryPCIMappingEndpointIsDocumented(t *testing.T) {
	for _, tt := range []struct{ method, path, group string }{
		{fiber.MethodGet, pciMappingListPath, "Nodes"},
		{fiber.MethodPost, pciMappingCreatePath, "Clusters"},
	} {
		e := declaredEndpoint(t, tt.method, tt.path)
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
		names := pathParamNames(e.Path)
		if len(names) == 0 || names[0] != "cluster_id" {
			t.Errorf("%s has path parameters %v; :cluster_id must be the first", key, names)
		}
		for name, prop := range e.Parameters {
			if strings.TrimSpace(prop.Description) == "" {
				t.Errorf("%s: parameter %q has no description", key, name)
			}
		}
	}
}

// TestCreatePCIMappingRefusesMalformedBodies drives the declaration with real
// requests. The body names only the node and the device's address: the rest
// of the entry is the node's own report of the device, so an id, a subsystem
// id, an IOMMU group or an mdev flag in the body is refused, not used.
func TestCreatePCIMappingRefusesMalformedBodies(t *testing.T) {
	const valid = `"mapping_id":"gpu01","node":"pve-01","path":"0000:01:00.0","description":"Example GPU"`
	target := strings.Replace(pciMappingCreatePath, ":cluster_id", testClusterID, 1)

	t.Run("a valid body reaches the handler intact", func(t *testing.T) {
		app, cap := mountForParams(t, fiber.MethodPost, pciMappingCreatePath)
		status, env := send(t, app, jsonRequest(http.MethodPost, target, "{"+valid+"}"))
		if status != fiber.StatusNoContent {
			t.Fatalf("status = %d (%q), want 204", status, env.Message)
		}
		for key, want := range map[string]string{
			"mapping_id":  "gpu01",
			"node":        "pve-01",
			"path":        "0000:01:00.0",
			"description": "Example GPU",
		} {
			if got := cap.params.String(key); got != want {
				t.Errorf("%s = %q, want %q", key, got, want)
			}
		}
	})

	for _, tt := range []struct{ name, body string }{
		{"the whole device, without a function", `{"mapping_id":"gpu01","node":"pve-01","path":"0000:01:00"}`},
		{"a domain wider than four digits", `{"mapping_id":"gpu01","node":"pve-01","path":"10000:01:00.0"}`},
		{"a path of 64 characters", `{"mapping_id":"gpu01","node":"pve-01","path":"` + strings.Repeat("0", 56) + `:01:00.0"}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			app, _ := mountForParams(t, fiber.MethodPost, pciMappingCreatePath)
			if status, env := send(t, app, jsonRequest(http.MethodPost, target, tt.body)); status != fiber.StatusNoContent {
				t.Errorf("status = %d (%q), want 204", status, env.Message)
			}
		})
	}

	for _, tt := range []struct{ name, body string }{
		{"no path", `{"mapping_id":"gpu01","node":"pve-01"}`},
		{"no node", `{"mapping_id":"gpu01","path":"0000:01:00.0"}`},
		{"no mapping id", `{"node":"pve-01","path":"0000:01:00.0"}`},
		{"a mapping id starting with a digit", `{` + strings.Replace(valid, `"gpu01"`, `"1gpu"`, 1) + `}`},
		{"a path without the domain", `{` + strings.Replace(valid, `"0000:01:00.0"`, `"01:00.0"`, 1) + `}`},
		{"a path in uppercase", `{` + strings.Replace(valid, `"0000:01:00.0"`, `"0000:0A:00.0"`, 1) + `}`},
		{"a list of paths", `{` + strings.Replace(valid, `"0000:01:00.0"`, `"0000:01:00.0;0000:02:00.0"`, 1) + `}`},
		{"a path smuggling a key", `{` + strings.Replace(valid, `"0000:01:00.0"`, `"0000:01:00.0,node=pve-02"`, 1) + `}`},
		{"a path over 64 characters", `{` + strings.Replace(valid, `"0000:01:00.0"`, `"`+strings.Repeat("0", 57)+`:01:00.0"`, 1) + `}`},
		{"a node smuggling a key", `{` + strings.Replace(valid, `"pve-01"`, `"pve-01,path=0000:02:00.0"`, 1) + `}`},
		{"a description over 4096 characters", `{` + strings.Replace(valid, `"Example GPU"`,
			fmt.Sprintf("%q", strings.Repeat("d", 4097)), 1) + `}`},
		{"a device id the node did not report", `{` + valid + `,"device_id":"1234:5678"}`},
		{"an IOMMU group the node did not report", `{` + valid + `,"iommugroup":1}`},
		{"an mdev flag the node did not report", `{` + valid + `,"mdev":true}`},
		{"the Proxmox spelling of the id", `{"id":"gpu01","node":"pve-01","path":"0000:01:00.0"}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			app, cap := mountForParams(t, fiber.MethodPost, pciMappingCreatePath)
			status, env := send(t, app, jsonRequest(http.MethodPost, target, tt.body))
			if status != fiber.StatusBadRequest {
				t.Errorf("status = %d (%q), want 400", status, env.Message)
			}
			if cap.called {
				t.Error("the handler ran for a malformed body")
			}
		})
	}
}

// mountForParams mounts one declared route with a capturing handler and no
// permission gate: these tests are about the parameters.
func mountForParams(t *testing.T, method, path string) (*fiber.App, *capture) {
	t.Helper()
	cap := &capture{}
	e := declaredEndpoint(t, method, path)
	e.Handler = cap.handler()
	e.Permissions = Permissions{SelfService: "parameter fixture; authorization is exercised separately"}
	return newRegistryApp(t, noAuth(), e), cap
}

// TestUpdateUSBMappingRefusesMalformedBodies drives the update's declaration
// with real requests.
func TestUpdateUSBMappingRefusesMalformedBodies(t *testing.T) {
	target := strings.Replace(strings.Replace(usbMappingPath, ":cluster_id", testClusterID, 1), ":mapping_id", "usbdev01", 1)

	t.Run("a valid body reaches the handler intact", func(t *testing.T) {
		app, cap := mountForParams(t, fiber.MethodPut, usbMappingPath)
		body := `{"map":["node=pve-01,id=1234:5678,path=1-2","node=pve-02,id=abcd:ef01"],` +
			`"description":"Example serial adapter","digest":"0123456789abcdef"}`
		if status, env := send(t, app, jsonRequest(http.MethodPut, target, body)); status != fiber.StatusNoContent {
			t.Fatalf("status = %d (%q), want 204", status, env.Message)
		}
		if got := cap.params.String("mapping_id"); got != "usbdev01" {
			t.Errorf("mapping_id = %q", got)
		}
		if got, want := cap.params.Strings("map"), []string{"node=pve-01,id=1234:5678,path=1-2", "node=pve-02,id=abcd:ef01"}; !slices.Equal(got, want) {
			t.Errorf("map = %q, want %q", got, want)
		}
		if got, set := cap.params.OptString("description"); !set || got != "Example serial adapter" {
			t.Errorf("description = %q (set %v)", got, set)
		}
		if got := cap.params.String("digest"); got != "0123456789abcdef" {
			t.Errorf("digest = %q", got)
		}
	})

	// The description's three states reach the handler apart: omitted leaves
	// it, "" removes it.
	t.Run("an omitted description is not set", func(t *testing.T) {
		app, cap := mountForParams(t, fiber.MethodPut, usbMappingPath)
		body := `{"map":["node=pve-01,id=1234:5678"],"digest":"0123456789abcdef"}`
		if status, env := send(t, app, jsonRequest(http.MethodPut, target, body)); status != fiber.StatusNoContent {
			t.Fatalf("status = %d (%q), want 204", status, env.Message)
		}
		if _, set := cap.params.OptString("description"); set {
			t.Error("an omitted description reads as set")
		}
	})
	t.Run("an empty description is set, and empty", func(t *testing.T) {
		app, cap := mountForParams(t, fiber.MethodPut, usbMappingPath)
		body := `{"map":["node=pve-01,id=1234:5678"],"description":"","digest":"0123456789abcdef"}`
		if status, env := send(t, app, jsonRequest(http.MethodPut, target, body)); status != fiber.StatusNoContent {
			t.Fatalf("status = %d (%q), want 204", status, env.Message)
		}
		if got, set := cap.params.OptString("description"); !set || got != "" {
			t.Errorf("description = %q (set %v), want an explicit empty string", got, set)
		}
	})
	// apischema reads a lone string as a one-element array (toSlice), and
	// never splits it: an entry is itself comma-separated, so splitting would
	// tear it into keys.
	t.Run("a lone string is one whole entry", func(t *testing.T) {
		app, cap := mountForParams(t, fiber.MethodPut, usbMappingPath)
		body := `{"map":"node=pve-01,id=1234:5678","digest":"0123456789abcdef"}`
		if status, env := send(t, app, jsonRequest(http.MethodPut, target, body)); status != fiber.StatusNoContent {
			t.Fatalf("status = %d (%q), want 204", status, env.Message)
		}
		if got, want := cap.params.Strings("map"), []string{"node=pve-01,id=1234:5678"}; !slices.Equal(got, want) {
			t.Errorf("map = %q, want %q", got, want)
		}
	})
	t.Run("a 64-character digest", func(t *testing.T) {
		app, _ := mountForParams(t, fiber.MethodPut, usbMappingPath)
		body := `{"map":["node=pve-01,id=1234:5678"],"digest":"` + strings.Repeat("a", 64) + `"}`
		if status, env := send(t, app, jsonRequest(http.MethodPut, target, body)); status != fiber.StatusNoContent {
			t.Errorf("status = %d (%q), want 204", status, env.Message)
		}
	})

	for _, tt := range []struct{ name, body string }{
		{"no map", `{"digest":"0123456789abcdef"}`},
		// Remove the last entry by deleting the mapping.
		{"an empty map", `{"map":[],"digest":"0123456789abcdef"}`},
		{"a map entry that is not a string", `{"map":[{"node":"pve-01"}],"digest":"0123456789abcdef"}`},
		// The compare-and-swap is the route's reason to exist.
		{"no digest", `{"map":["node=pve-01,id=1234:5678"]}`},
		{"an empty digest", `{"map":["node=pve-01,id=1234:5678"],"digest":""}`},
		{"a digest over 64 characters", `{"map":["node=pve-01,id=1234:5678"],"digest":"` + strings.Repeat("a", 65) + `"}`},
		{"a description over 4096 characters", `{"map":["node=pve-01,id=1234:5678"],"digest":"0123456789abcdef",` +
			`"description":` + fmt.Sprintf("%q", strings.Repeat("d", 4097)) + `}`},
		// The id comes off the path; the parameter set is closed.
		{"an id in the body", `{"id":"usbdev02","map":["node=pve-01,id=1234:5678"],"digest":"0123456789abcdef"}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			app, cap := mountForParams(t, fiber.MethodPut, usbMappingPath)
			status, env := send(t, app, jsonRequest(http.MethodPut, target, tt.body))
			if status != fiber.StatusBadRequest {
				t.Errorf("status = %d (%q), want 400", status, env.Message)
			}
			if cap.called {
				t.Error("the handler ran for a malformed body")
			}
		})
	}
}

// TestUSBMappingIDParam holds the path parameter every per-mapping route
// takes: it addresses any id the create mints — down to one character, up to
// the create's 128 — and nothing that could leave the mapping's own path.
func TestUSBMappingIDParam(t *testing.T) {
	routes := []struct{ method, path, body string }{
		{fiber.MethodPut, usbMappingPath, `{"map":["node=pve-01,id=1234:5678"],"digest":"0123456789abcdef"}`},
		{fiber.MethodDelete, usbMappingPath, ""},
		{fiber.MethodGet, usbMappingUsagePath, ""},
	}
	build := func(path, id string) string {
		return strings.Replace(strings.Replace(path, ":cluster_id", testClusterID, 1), ":mapping_id", id, 1)
	}
	for _, r := range routes {
		for _, id := range []string{"u", "usbdev01", "usb_dev-01", strings.Repeat("u", 128)} {
			t.Run(r.method+" accepts "+id[:min(len(id), 16)], func(t *testing.T) {
				app, cap := mountForParams(t, r.method, r.path)
				status, env := send(t, app, jsonRequest(r.method, build(r.path, id), r.body))
				if status != fiber.StatusNoContent || !cap.called {
					t.Fatalf("status = %d (%q), want 204", status, env.Message)
				}
				if got := cap.params.String("mapping_id"); got != id {
					t.Errorf("mapping_id = %q, want %q", got, id)
				}
			})
		}
		for _, id := range []string{"1usb", "-usb", "usb.dev", "%2e%2e", "usb%2Fdev", strings.Repeat("u", 129)} {
			t.Run(r.method+" refuses "+id[:min(len(id), 16)], func(t *testing.T) {
				app, cap := mountForParams(t, r.method, r.path)
				status, env := send(t, app, jsonRequest(r.method, build(r.path, id), r.body))
				if status != fiber.StatusBadRequest {
					t.Errorf("status = %d (%q), want 400", status, env.Message)
				}
				if cap.called {
					t.Error("the handler ran")
				}
			})
		}
	}
}

// The delete's digest is optional — Proxmox's delete takes none — and held to
// pve-config-digest's 64 characters when sent.
func TestDeleteUSBMappingDigestParam(t *testing.T) {
	base := strings.Replace(strings.Replace(usbMappingPath, ":cluster_id", testClusterID, 1), ":mapping_id", "usbdev01", 1)
	for _, tt := range []struct {
		name, query string
		want        int
	}{
		{"no digest", "", fiber.StatusNoContent},
		{"a digest", "?digest=0123456789abcdef", fiber.StatusNoContent},
		{"a 64-character digest", "?digest=" + strings.Repeat("a", 64), fiber.StatusNoContent},
		{"a digest over 64 characters", "?digest=" + strings.Repeat("a", 65), fiber.StatusBadRequest},
		// Sent means compared: an empty one would read as "not sent".
		{"an empty digest", "?digest=", fiber.StatusBadRequest},
	} {
		t.Run(tt.name, func(t *testing.T) {
			app, cap := mountForParams(t, fiber.MethodDelete, usbMappingPath)
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

// Each usage call reads every VM's configuration, and the route is open to
// every Viewer: it carries its own per-IP limiter, attached to the route
// (usbMappingUsageLimiter in middleware.go) — and only that route does.
func TestUSBMappingUsageIsRateLimited(t *testing.T) {
	for _, tt := range []struct{ method, path string }{
		{fiber.MethodGet, usbMappingClusterPath},
		{fiber.MethodPut, usbMappingPath},
		{fiber.MethodDelete, usbMappingPath},
	} {
		if e := declaredEndpoint(t, tt.method, tt.path); e.RateLimiter != nil {
			t.Errorf("%s %s carries a route limiter; only the usage route should", tt.method, tt.path)
		}
	}
	e := declaredEndpoint(t, fiber.MethodGet, usbMappingUsagePath)
	if e.RateLimiter == nil {
		t.Fatal("the usage route carries no limiter of its own")
	}
	// Stands in for authRequired, which runs before a route limiter and sets
	// user_id; every request here comes from the same IP.
	alice, bob := uuid.New(), uuid.New()
	app := fiber.New()
	app.Get("/probe", func(c fiber.Ctx) error {
		if c.Get("X-Test-User") == "bob" {
			c.Locals("user_id", bob)
		} else {
			c.Locals("user_id", alice)
		}
		return c.Next()
	}, e.RateLimiter, func(c fiber.Ctx) error { return c.SendStatus(fiber.StatusOK) })
	get := func(user string) int {
		req := httptest.NewRequest(http.MethodGet, "/probe", nil)
		req.Header.Set("X-Test-User", user)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test: %v", err)
		}
		_ = resp.Body.Close()
		return resp.StatusCode
	}
	got := make([]int, 0, 12)
	for range 12 {
		got = append(got, get("alice"))
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
	if status := get("bob"); status != fiber.StatusOK {
		t.Errorf("bob's first request = %d, want 200", status)
	}
}
