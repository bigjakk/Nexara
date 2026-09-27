package api

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
)

const (
	usbMappingListPath   = clusterScope + "/nodes/:node_name/usb-mappings"
	usbMappingCreatePath = clusterScope + "/usb-mappings"
)

// TestUSBMappingRoutesDeclareTheirPermissions pins the two gates. The listing
// sits with the node hardware listings the same dialog reads, on view:node;
// the create is cluster configuration, on manage:cluster — looser than
// Proxmox, which keeps Mapping.Modify out of PVEAdmin, by the operator's
// choice (see registry_mappings.go).
func TestUSBMappingRoutesDeclareTheirPermissions(t *testing.T) {
	for _, tt := range []struct{ method, path, want string }{
		{fiber.MethodGet, usbMappingListPath, "view:node"},
		{fiber.MethodPost, usbMappingCreatePath, "manage:cluster"},
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
