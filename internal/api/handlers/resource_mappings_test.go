package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"

	"github.com/bigjakk/nexara/internal/api/apischema"
	"github.com/bigjakk/nexara/internal/proxmox"
)

func mappingAPIError(message string) error {
	return &proxmox.APIError{StatusCode: 500, Message: message}
}

func TestMapMappingCreateError(t *testing.T) {
	for _, tt := range []struct {
		name string
		err  error
		want int
	}{
		// What pve-manager's create dies with, re-died by lock_usb_config, in the
		// JSON envelope checkStatus keeps as the message; PCI.pm's dies the same way.
		{"a taken id is a conflict",
			mappingAPIError(`{"data":null,"message":"create hardware mapping failed: usb ID 'usbdev01' already defined\n"}`), fiber.StatusConflict},
		{"a taken id is a conflict without the envelope too",
			mappingAPIError("create hardware mapping failed: usb ID 'usbdev01' already defined"), fiber.StatusConflict},
		{"a taken PCI id is a conflict",
			mappingAPIError(`{"data":null,"message":"create hardware mapping failed: pci ID 'gpu01' already defined\n"}`), fiber.StatusConflict},
		{"a malformed device id stays a 400",
			fmt.Errorf("%w: USB device id %q must be vendor:product", proxmox.ErrInvalidInput, "12345678"), fiber.StatusBadRequest},
		{"a token without Mapping.Modify stays a 403", proxmox.ErrForbidden, fiber.StatusForbidden},
		{"an unreachable cluster stays a 502", proxmox.ErrConnectionFailed, fiber.StatusBadGateway},
		{"an unrelated create failure stays a 502",
			mappingAPIError("create hardware mapping failed: can't lock file '/var/lock/pve-manager/pve-mapping-usb.lck' - got timeout"), fiber.StatusBadGateway},
		// The phrase in the client's wrap is the caller's own input: only Proxmox's words are read.
		{"the phrase in the client's wrap is not Proxmox's",
			fmt.Errorf("create USB mapping %s: %w", "already defined", mappingAPIError("unable to read usb.cfg")), fiber.StatusBadGateway},
	} {
		t.Run(tt.name, func(t *testing.T) {
			wantStatus(t, mapMappingCreateError("USB", tt.err), tt.want)
		})
	}
	if mapMappingCreateError("USB", nil) != nil {
		t.Error("mapMappingCreateError(nil) should stay nil")
	}
	// The conflict names the kind the caller was creating.
	var fe *fiber.Error
	err := mapMappingCreateError("PCI", mappingAPIError("pci ID 'gpu01' already defined"))
	if !errors.As(err, &fe) || fe.Message != "A PCI mapping with that ID already exists" {
		t.Errorf("PCI conflict = %v, want \"A PCI mapping with that ID already exists\"", fe)
	}
}

// Both kinds' per-node listings are proxied to the node by name, so the node they
// are handed has already been checked — by the registry, for the node in the
// route's path (RequireNodesInCluster; the route-level proof is
// TestGuard_EveryRouteNamingANodeRefusesOneTheClusterDoesNotHold).
func TestListNodeMappings(t *testing.T) {
	var asked []string
	list := func(_ context.Context, node string) ([]proxmox.PCIMapping, error) {
		asked = append(asked, node)
		return []proxmox.PCIMapping{{ID: "gpu01"}}, nil
	}
	got, err := listNodeMappings(context.Background(), "pve-01", list)
	if err != nil || len(got) != 1 || !slices.Equal(asked, []string{"pve-01"}) {
		t.Fatalf("got %v, %v, asked %v", got, err, asked)
	}
	failing := func(context.Context, string) ([]proxmox.PCIMapping, error) { return nil, proxmox.ErrConnectionFailed }
	_, err = listNodeMappings(context.Background(), "pve-01", failing)
	wantStatus(t, err, fiber.StatusBadGateway)
}

// Reading the node's devices is a /nodes/{node} call, which pveproxy forwards to
// wherever the name resolves; a node that is no member of the cluster is refused
// before anything is sent to Proxmox. A failed lookup is a failure, not a member.
func TestCreatePCIMapping_AsksNothingOfANodeOutsideTheCluster(t *testing.T) {
	fake := &mappingFakeClient{devices: map[string][]proxmox.NodePCIDevice{
		"pve-01": {{ID: "0000:01:00.0", Vendor: "0x1234", Device: "0x5678", IOMMUGroup: -1}}}}
	_, err := createPCIMapping(context.Background(), fake, memberOf("pve-01"), "gpu01", "pve-09", "0000:01:00.0", "")
	wantStatus(t, err, fiber.StatusNotFound)

	lookupFails := func(context.Context, string) (bool, error) {
		return false, fiber.NewError(fiber.StatusInternalServerError, "Failed to look up the node")
	}
	_, err = createPCIMapping(context.Background(), fake, lookupFails, "gpu01", "pve-01", "0000:01:00.0", "")
	wantStatus(t, err, fiber.StatusInternalServerError)
	fake.requireCalls(t)
}

func TestCreatePCIMapping_BuildsTheEntryFromTheNodesDevice(t *testing.T) {
	group := 14
	fake := &mappingFakeClient{devices: map[string][]proxmox.NodePCIDevice{"pve-01": {
		{ID: "0000:01:00.0", Vendor: "0x1234", Device: "0x5678", SubsystemVendor: "0xabcd", SubsystemDevice: "0xef01", IOMMUGroup: group, MDev: true},
	}}}
	got, err := createPCIMapping(context.Background(), fake, memberOf("pve-01"), "gpu01", "pve-01", "0000:01:00", "Example GPU")
	if err != nil {
		t.Fatalf("createPCIMapping: %v", err)
	}
	want := proxmox.CreatePCIMappingParams{
		ID:          "gpu01",
		Description: "Example GPU",
		Entry:       proxmox.PCIMapEntry{Node: "pve-01", Path: "0000:01:00", ID: "1234:5678", SubsystemID: "abcd:ef01", IOMMUGroup: &group},
		MDev:        true,
	}
	fake.requireCalls(t, "devices:pve-01", "create:gpu01")
	if len(fake.created) != 1 || !reflect.DeepEqual(fake.created[0], want) || !reflect.DeepEqual(got, want) {
		t.Errorf("created %+v, returned %+v, want %+v", fake.created, got, want)
	}
}

func TestCreatePCIMapping_Refusals(t *testing.T) {
	devices := map[string][]proxmox.NodePCIDevice{"pve-01": {{ID: "0000:01:00.0", Vendor: "0x1234", Device: "0x5678", IOMMUGroup: -1}}}
	for _, tt := range []struct {
		name        string
		fake        *mappingFakeClient
		path        string
		want        int
		wantMessage string
		wantCreated bool
	}{
		// The node's own listing is the only source of the entry, so a device it does not report is refused before anything is written.
		{"a device the node does not have", &mappingFakeClient{devices: devices}, "0000:02:00.0", fiber.StatusBadRequest, "", false},
		{"the listing failing", &mappingFakeClient{devices: devices, devicesErr: proxmox.ErrConnectionFailed}, "0000:01:00.0", fiber.StatusBadGateway, "", false},
		// An id Nexara cannot read is Proxmox's report, not the caller's input: a server error, not a 400.
		{"a device the node reports oddly", &mappingFakeClient{devices: map[string][]proxmox.NodePCIDevice{
			"pve-01": {{ID: "0000:01:00.0", Vendor: "0x12345", Device: "0x5678"}}}}, "0000:01:00.0", fiber.StatusInternalServerError, "", false},
		{"a taken id", &mappingFakeClient{devices: devices, createErr: mappingAPIError("create hardware mapping failed: pci ID 'gpu01' already defined")},
			"0000:01:00.0", fiber.StatusConflict, "A PCI mapping with that ID already exists", true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := createPCIMapping(context.Background(), tt.fake, memberOf("pve-01"), "gpu01", "pve-01", tt.path, "")
			wantStatus(t, err, tt.want)
			var fe *fiber.Error
			if errors.As(err, &fe) && tt.wantMessage != "" && fe.Message != tt.wantMessage {
				t.Errorf("message = %q, want %q", fe.Message, tt.wantMessage)
			}
			if created := len(tt.fake.created) > 0; created != tt.wantCreated {
				t.Errorf("create called = %v, want %v", created, tt.wantCreated)
			}
		})
	}
}

func TestPCIMappingCreateDetails(t *testing.T) {
	group := 0
	for _, tt := range []struct {
		name   string
		params proxmox.CreatePCIMappingParams
		want   string
	}{
		// Group 0 is a group, recorded like any other.
		{"every value", proxmox.CreatePCIMappingParams{ID: "gpu01", MDev: true, Entry: proxmox.PCIMapEntry{
			Node: "pve-01", Path: "0000:01:00.0", ID: "1234:5678", SubsystemID: "abcd:ef01", IOMMUGroup: &group}},
			`{"device_id":"1234:5678","iommugroup":0,"mapping_id":"gpu01","mdev":true,"node":"pve-01","path":"0000:01:00.0","subsystem_id":"abcd:ef01"}`},
		{"only what is there", proxmox.CreatePCIMappingParams{ID: "nic01", Entry: proxmox.PCIMapEntry{Node: "pve-02", Path: "0000:02:00", ID: "1234:0002"}},
			`{"device_id":"1234:0002","mapping_id":"nic01","node":"pve-02","path":"0000:02:00"}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := string(pciMappingCreateDetails(tt.params)); got != tt.want {
				t.Errorf("details = %s, want %s", got, tt.want)
			}
		})
	}
}

// TestMapMappingUpdateError: an update finds its view stale two ways, on top of
// mapProxmoxError, for either kind. A digest that no longer matches is 409
// (assert_if_modified's "detected modified configuration"), a mapping that is gone
// is 404, and the digest is compared first, as the update does, so a stale request
// never reaches the 404.
func TestMapMappingUpdateError(t *testing.T) {
	for _, kind := range []struct {
		name  string
		mapIt func(error) error
		stale string
		id    string
	}{
		{"USB", mapUSBMappingUpdateError, usbMappingStaleMessage, "usbdev01"},
		{"PCI", mapPCIMappingUpdateError, pciMappingStaleMessage, "gpu01"},
	} {
		lower := strings.ToLower(kind.name)
		for _, tt := range []struct {
			name  string
			err   error
			want  int
			stale bool // the 409 carries the kind's stale-digest message
		}{
			{"a stale digest is a conflict", mappingAPIError(`{"data":null,"message":"update hardware mapping failed: detected modified configuration - file changed by other user? Try again.\n"}`), fiber.StatusConflict, true},
			{"a stale digest is a conflict without the envelope too", mappingAPIError("update hardware mapping failed: detected modified configuration - file changed by other user? Try again."), fiber.StatusConflict, true},
			{"a missing mapping is not found", mappingAPIError(`{"data":null,"message":"update hardware mapping failed: ` + lower + ` ID '` + kind.id + `' does not exist\n"}`), fiber.StatusNotFound, false},
			{"the digest wins over the missing mapping", mappingAPIError("detected modified configuration; " + lower + " ID '" + kind.id + "' does not exist"), fiber.StatusConflict, true},
			{"a malformed entry refused by the client stays a 400", fmt.Errorf("%w: mapping entry %q needs id=<vendor:product>", proxmox.ErrInvalidInput, "node=pve-01"), fiber.StatusBadRequest, false},
			{"a token without Mapping.Modify stays a 403", proxmox.ErrForbidden, fiber.StatusForbidden, false},
			{"an unreachable cluster stays a 502", proxmox.ErrConnectionFailed, fiber.StatusBadGateway, false},
			{"a lock timeout stays a 502", mappingAPIError("update hardware mapping failed: can't lock file '/var/lock/pve-manager/pve-mapping-" + lower + ".lck' - got timeout"), fiber.StatusBadGateway, false},
			// Only Proxmox's words decide the status, never the client's wrap, which carries the caller's id.
			{"a phrase in the client's wrap is not Proxmox's", fmt.Errorf("update mapping %s: %w", "does not exist detected modified configuration", mappingAPIError("unable to read "+lower+".cfg")), fiber.StatusBadGateway, false},
		} {
			t.Run(kind.name+"/"+tt.name, func(t *testing.T) {
				err := kind.mapIt(tt.err)
				wantStatus(t, err, tt.want)
				var fe *fiber.Error
				if errors.As(err, &fe) && tt.stale && fe.Message != kind.stale {
					t.Errorf("message = %q, want %q", fe.Message, kind.stale)
				}
			})
		}
		if kind.mapIt(nil) != nil {
			t.Errorf("the %s mapper should leave nil nil", kind.name)
		}
	}
}

func TestClassifyUSBMappingDelete(t *testing.T) {
	snapshot := &proxmox.USBMapping{ID: "usbdev01", Map: []string{"node=pve-01,id=1234:5678"}}
	for _, tt := range []struct {
		name        string
		snapshot    *proxmox.USBMapping
		snapErr     error
		wantAction  string
		wantUnknown bool
	}{
		{"seen before the delete", snapshot, nil, "deleted", false},
		// Could not look: recorded as a deletion, with the doubt marked.
		{"the listing could not be read", nil, proxmox.ErrConnectionFailed, "deleted", true},
		// Confirmed absent: Proxmox's 200 removed nothing.
		{"absent before the delete", nil, nil, "already_deleted", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			action, unknown := classifyUSBMappingDelete(tt.snapshot, tt.snapErr)
			if action != tt.wantAction || unknown != tt.wantUnknown {
				t.Errorf("= (%q, %v), want (%q, %v)", action, unknown, tt.wantAction, tt.wantUnknown)
			}
		})
	}
}

// The row keeps what it takes to create the mapping again, and marks a row whose
// snapshot could not be taken rather than leaving it looking empty.
func TestUSBMappingDeleteDetails(t *testing.T) {
	for _, tt := range []struct {
		name     string
		snapshot *proxmox.USBMapping
		unknown  bool
		want     string
	}{
		{"entries and description",
			&proxmox.USBMapping{ID: "usbdev01", Description: "Example serial adapter", Map: []string{"node=pve-01,id=1234:5678,path=1-2", "node=pve-02,id=1234:5678"}},
			false, `{"description":"Example serial adapter","map":["node=pve-01,id=1234:5678,path=1-2","node=pve-02,id=1234:5678"],"map_count":2}`},
		// Recorded as Proxmox held it — an uppercase id included, which is what kept VMs from starting.
		{"an entry is recorded as it was", &proxmox.USBMapping{ID: "usbdev01", Map: []string{"id=ABCD:EF01,node=pve-01"}},
			false, `{"map":["id=ABCD:EF01,node=pve-01"],"map_count":1}`},
		{"no description key when there is none", &proxmox.USBMapping{ID: "usbdev01", Map: []string{"node=pve-01,id=1234:5678"}},
			false, `{"map":["node=pve-01,id=1234:5678"],"map_count":1}`},
		{"prior state unknown", nil, true, `{"prior_state_unknown":true}`},
		{"already deleted", nil, false, `{}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := string(usbMappingDeleteDetails(tt.snapshot, tt.unknown)); got != tt.want {
				t.Errorf("details = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestUSBMappingUpdateDetails(t *testing.T) {
	written := []string{"node=pve-01,id=1234:5678", "node=pve-02,id=1234:5678,path=1-2"}
	const maps = `"map":["node=pve-01,id=1234:5678","node=pve-02,id=1234:5678,path=1-2"],"map_count":2}`
	desc, empty := "Example serial adapter", ""
	for _, tt := range []struct {
		name        string
		description *string
		want        string
	}{
		{"the description left alone", nil, `{` + maps},
		{"a new description", &desc, `{"description":"Example serial adapter",` + maps},
		{"the description removed", &empty, `{"description_removed":true,` + maps},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := string(usbMappingUpdateDetails(written, tt.description)); got != tt.want {
				t.Errorf("details = %s, want %s", got, tt.want)
			}
		})
	}
}

// audit_log is never trimmed and these values are the caller's, so a row holds at
// most usbMappingAuditEntries entries of at most usbMappingAuditTextMax characters
// each, and says how many there were.
func TestUSBMappingAuditDetailsAreCapped(t *testing.T) {
	long := strings.Repeat("é", 4096)
	entries := make([]string, 0, 70)
	for i := range 70 {
		entries = append(entries, fmt.Sprintf("description=%s,id=1234:5678,node=pve-%02d", long, i))
	}
	mapOf := func(t *testing.T, raw json.RawMessage) []string {
		t.Helper()
		var detail struct {
			Map []string `json:"map"`
		}
		if err := json.Unmarshal(raw, &detail); err != nil {
			t.Fatalf("details are not JSON: %v", err)
		}
		return detail.Map
	}
	check := func(t *testing.T, raw json.RawMessage) {
		t.Helper()
		var detail struct {
			Map         []string `json:"map"`
			MapCount    int      `json:"map_count"`
			Description string   `json:"description"`
		}
		if err := json.Unmarshal(raw, &detail); err != nil {
			t.Fatalf("details are not JSON: %v", err)
		}
		if len(detail.Map) != usbMappingAuditEntries || detail.MapCount != 70 {
			t.Errorf("recorded %d entries with map_count %d, want %d and 70", len(detail.Map), detail.MapCount, usbMappingAuditEntries)
		}
		for i, e := range detail.Map {
			// The cut is the description's: the entry keeps what makes it one.
			parsed, err := proxmox.ParseUSBMapEntry(e)
			if err != nil {
				t.Fatalf("recorded entry %d does not parse: %v", i, err)
			}
			if parsed.Node != fmt.Sprintf("pve-%02d", i) || parsed.ID != "1234:5678" {
				t.Errorf("recorded entry %d = %+v, want its node and id kept", i, parsed)
			}
			if n := utf8.RuneCountInString(parsed.Description); n > usbMappingAuditTextMax+1 {
				t.Fatalf("a description of %d characters was recorded, want at most %d and the ellipsis", n, usbMappingAuditTextMax)
			}
		}
		if n := utf8.RuneCountInString(detail.Description); n == 0 || n > usbMappingAuditTextMax+1 {
			t.Errorf("description of %d characters recorded, want 1..%d and the ellipsis", n, usbMappingAuditTextMax)
		}
	}
	t.Run("update", func(t *testing.T) { check(t, usbMappingUpdateDetails(entries, &long)) })
	t.Run("delete", func(t *testing.T) {
		check(t, usbMappingDeleteDetails(&proxmox.USBMapping{ID: "usbdev01", Description: long, Map: entries}, false))
	})
	// An entry Proxmox holds that does not parse — written by hand, say — is cut whole, not dropped.
	t.Run("an entry that does not parse", func(t *testing.T) {
		got := mapOf(t, usbMappingDeleteDetails(&proxmox.USBMapping{Map: []string{"odd=" + strings.Repeat("x", 600)}}, false))
		if len(got) != 1 || utf8.RuneCountInString(got[0]) != usbMappingAuditTextMax+1 || !strings.HasPrefix(got[0], "odd=xxx") {
			t.Errorf("map = %q, want the entry cut to %d and the ellipsis", got, usbMappingAuditTextMax)
		}
	})
	// Nothing but the port holds a node name to a length, so the rewritten entry is bounded as a whole too.
	t.Run("an entry long in its node name", func(t *testing.T) {
		raw := "description=" + strings.Repeat("é", 600) + ",id=1234:5678,node=" + strings.Repeat("n", 2000)
		got := mapOf(t, usbMappingDeleteDetails(&proxmox.USBMapping{Map: []string{raw}}, false))
		if len(got) != 1 || utf8.RuneCountInString(got[0]) > 2*usbMappingAuditTextMax+1 {
			t.Errorf("recorded %q, want at most %d characters and the ellipsis", got, 2*usbMappingAuditTextMax)
		}
	})
}

// Every node any entry names, each once, in the order first named — across
// mappings, since one node's check answers for all of them.
func TestUSBMappingEntryNodes(t *testing.T) {
	got := usbMappingEntryNodes([]proxmox.USBMapping{
		{ID: "usbdev01", Map: []string{"node=pve-02,id=1234:5678", "node=pve-01,id=1234:5678"}},
		{ID: "usbdev02", Map: []string{"id=1234:5678", "node=pve-01,id=abcd:ef01", "node=pve-03,id=abcd:ef01"}},
		{ID: "usbdev03"},
	})
	if want := []string{"pve-02", "pve-01", "pve-03"}; !slices.Equal(got, want) {
		t.Errorf("nodes = %v, want %v", got, want)
	}
}

func TestUSBMapEntryNode(t *testing.T) {
	for raw, want := range map[string]string{
		"node=pve-01,id=1234:5678":          "pve-01",
		"id=1234:5678,path=1-2,node=pve-02": "pve-02",
		"id=1234:5678":                      "",
		// The first one: Proxmox's own parse refuses the entry, and its check on that
		// node then reports no entry there, which is what is shown.
		"node=pve-01,node=pve-02,id=1234:5678": "pve-01",
		// Not trimmed, as Proxmox does not trim: this is no node key.
		" node=pve-01,id=1234:5678": "",
		"":                          "",
	} {
		if got := usbMapEntryNode(raw); got != want {
			t.Errorf("usbMapEntryNode(%q) = %q, want %q", raw, got, want)
		}
	}
}

func TestCheckUSBMappingNodes(t *testing.T) {
	old := usbMappingCheckTimeout
	usbMappingCheckTimeout = 200 * time.Millisecond
	t.Cleanup(func() { usbMappingCheckTimeout = old })

	listing := []proxmox.USBMapping{{ID: "usbdev01", Digest: "d1", Errors: []proxmox.MappingCheck{}}}
	// More online nodes than checks run at once, all of which must come back answered.
	many := make([]string, 0, 2*usbMappingCheckConcurrency)
	for i := range 2 * usbMappingCheckConcurrency {
		many = append(many, fmt.Sprintf("pve-%02d", 10+i))
	}
	f := &mappingFakeClient{
		nodes: append(onlineNodes(append([]string{"pve-01", "pve-04", "pve-05"}, many...)...),
			proxmox.NodeListEntry{Node: "pve-02", Status: "offline"},
			proxmox.NodeListEntry{Node: "pve-03", Status: "unknown"}),
		listings: map[string][]proxmox.USBMapping{"pve-01": listing},
		listErrs: map[string]error{
			"pve-04": mappingAPIError(`{"data":null,"message":"hostname lookup 'pve-04' failed\n"}`),
		},
		hangNodes: map[string]bool{"pve-05": true},
	}

	got := checkUSBMappingNodes(context.Background(), f, append([]string{"pve-01", "pve-02", "pve-03", "pve-04", "pve-05", "pve-09"}, many...))

	if c := got["pve-01"]; c.reason != "" || len(c.mappings) != 1 || c.mappings[0].ID != "usbdev01" {
		t.Errorf("pve-01 = %+v, want its listing", c)
	}
	for node, want := range map[string]string{
		"pve-02": "The node is offline.",
		"pve-03": `The node is not online (Proxmox reports it as "unknown").`,
		"pve-04": "hostname lookup 'pve-04' failed",
		"pve-05": "No answer within 200ms.",
		"pve-09": "This cluster has no node of that name.",
	} {
		if got[node].reason != want {
			t.Errorf("%s reason = %q, want %q", node, got[node].reason, want)
		}
	}
	for _, node := range many {
		if c, ok := got[node]; !ok || c.reason != "" {
			t.Errorf("%s = %+v, want an answered check", node, c)
		}
	}
	// Only the online members are asked.
	slices.Sort(f.asked)
	want := append([]string{"pve-01", "pve-04", "pve-05"}, many...)
	slices.Sort(want)
	if !slices.Equal(f.asked, want) {
		t.Errorf("asked %v, want %v", f.asked, want)
	}
}

// Without the cluster's node list no node is asked: an entry may name any host, and
// Proxmox would resolve a non-member through DNS and connect to it. Every node still
// gets its reason — none reads as checked.
func TestCheckUSBMappingNodes_WithoutTheNodeListAsksNone(t *testing.T) {
	f := &mappingFakeClient{nodesErr: proxmox.ErrConnectionFailed, listings: map[string][]proxmox.USBMapping{"pve-01": {}, "pve-02": {}}}
	got := checkUSBMappingNodes(context.Background(), f, []string{"pve-01", "pve-02", "host-09"})
	if len(f.asked) != 0 {
		t.Errorf("asked %v, want none", f.asked)
	}
	for _, node := range []string{"pve-01", "pve-02", "host-09"} {
		if want := "Could not read the cluster's nodes, so no node was checked."; got[node].reason != want {
			t.Errorf("%s reason = %q, want %q", node, got[node].reason, want)
		}
	}
}

// The listing's checks share one budget: a node not answered, or not even asked, by
// then is reported out of time — never left out, never clean. Every call hangs, so
// the first wave is exactly the checks that run at once: the bound is asked here too.
func TestCheckUSBMappingNodes_BudgetRunsOut(t *testing.T) {
	oldTimeout, oldBudget := usbMappingCheckTimeout, usbMappingCheckBudget
	usbMappingCheckTimeout, usbMappingCheckBudget = 5*time.Second, 150*time.Millisecond
	t.Cleanup(func() { usbMappingCheckTimeout, usbMappingCheckBudget = oldTimeout, oldBudget })

	nodes := make([]string, 0, 3*usbMappingCheckConcurrency)
	f := &mappingFakeClient{hangNodes: map[string]bool{}}
	for i := range 3 * usbMappingCheckConcurrency {
		node := fmt.Sprintf("pve-%02d", i+1)
		nodes = append(nodes, node)
		f.hangNodes[node] = true
	}
	f.nodes = onlineNodes(nodes...)

	start := time.Now()
	got := checkUSBMappingNodes(context.Background(), f, nodes)
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Errorf("the checks took %s; the budget is %s", elapsed, usbMappingCheckBudget)
	}
	want := "The checks ran out of their 150ms before this one answered."
	for _, node := range nodes {
		if got[node].reason != want {
			t.Errorf("%s reason = %q, want %q", node, got[node].reason, want)
		}
	}
	// Only the first wave was ever asked: the rest waited for a worker, and the budget was gone by the time one was free.
	if len(f.asked) != usbMappingCheckConcurrency {
		t.Errorf("asked %d nodes, want %d", len(f.asked), usbMappingCheckConcurrency)
	}
}

func TestMergeUSBMappingChecks(t *testing.T) {
	warn := proxmox.MappingCheck{Severity: "error", Message: "Invalid configuration: no usb device found for 'usbdev01' (1234:5678)"}
	plain := []proxmox.USBMapping{
		{ID: "usbdev01", Description: "Example serial adapter", Digest: "d1", Map: []string{
			"node=pve-01,id=1234:5678",
			"node=pve-02,id=1234:5678",
			"node=pve-03,id=1234:5678",
			"node=pve-04,id=1234:5678",
			"node=pve-05,id=1234:5678",
			"node=pve-06,id=1234:5678",
			// A second entry for pve-01, and one naming no node.
			"node=pve-01,id=abcd:ef01",
			"id=1234:5678",
		}},
		{ID: "usbdev02", Digest: "d1"},
	}
	checks := map[string]usbMappingNodeCheck{
		// Clean: Proxmox reports nothing. Its nil Errors must still read as a clean check, not a missing one.
		"pve-01": {mappings: []proxmox.USBMapping{{ID: "usbdev01", Digest: "d1"}}},
		"pve-02": {mappings: []proxmox.USBMapping{{ID: "usbdev01", Digest: "d1", Errors: []proxmox.MappingCheck{warn}}}},
		"pve-03": {reason: "The node is offline."},
		// Checked against a usb.cfg the page is not showing.
		"pve-04": {mappings: []proxmox.USBMapping{{ID: "usbdev01", Digest: "d2", Errors: []proxmox.MappingCheck{}}}},
		// The node's listing does not hold the mapping.
		"pve-05": {mappings: []proxmox.USBMapping{{ID: "usbdev02", Digest: "d1"}}},
		// pve-06 is missing from checks altogether.
	}

	got := mergeUSBMappingChecks(plain, checks)
	if len(got) != 2 {
		t.Fatalf("got %d mappings, want 2", len(got))
	}
	m := got[0]
	if m.ID != "usbdev01" || m.Description != "Example serial adapter" || m.Digest != "d1" || len(m.Map) != 8 {
		t.Errorf("mapping = %+v, want the plain listing's fields", m)
	}
	if c, ok := m.NodeChecks["pve-01"]; !ok || c == nil || len(c) != 0 {
		t.Errorf("pve-01 check = %#v (present %v), want an empty, non-nil list", c, ok)
	}
	if c := m.NodeChecks["pve-02"]; len(c) != 1 || c[0] != warn {
		t.Errorf("pve-02 check = %#v, want the error", c)
	}
	for node, want := range map[string]string{
		"pve-03": "The node is offline.",
		"pve-04": "The USB mappings changed while they were being checked. Reload to check again.",
		"pve-05": "The node's check did not list this mapping.",
		"pve-06": "Nexara did not check this node.",
	} {
		if m.Unchecked[node] != want {
			t.Errorf("%s unchecked = %q, want %q", node, m.Unchecked[node], want)
		}
	}
	// Every node an entry names is in exactly one of the two maps.
	for _, node := range []string{"pve-01", "pve-02", "pve-03", "pve-04", "pve-05", "pve-06"} {
		_, checked := m.NodeChecks[node]
		_, unchecked := m.Unchecked[node]
		if checked == unchecked {
			t.Errorf("%s: checked %v, unchecked %v — want exactly one", node, checked, unchecked)
		}
	}
	if n := len(m.NodeChecks) + len(m.Unchecked); n != 6 {
		t.Errorf("%d nodes reported, want 6", n)
	}
	if empty := got[1]; empty.Map == nil || empty.NodeChecks == nil || empty.Unchecked == nil {
		t.Errorf("mapping with no entries = %#v, want empty, non-nil collections", empty)
	}
}

func TestScanUSBMappingUsage(t *testing.T) {
	oldTimeout := usbMappingUsageTimeout
	usbMappingUsageTimeout = 200 * time.Millisecond
	t.Cleanup(func() { usbMappingUsageTimeout = oldTimeout })

	f := &mappingFakeClient{
		nodes: append(onlineNodes("pve-01", "pve-02"), proxmox.NodeListEntry{Node: "pve-03", Status: "offline"}),
		// Out of order, so the sort is tested; and the guest on the offline node after
		// two whose reads fail, so its unchecked entry is written while theirs are —
		// which -race reports if the two are not synchronised.
		resources: []proxmox.ClusterResource{
			usageGuest(105, "pve-02", "linux05", "qemu"),
			usageGuest(106, "pve-02", "win06", "qemu"),
			usageGuest(107, "pve-02", "linux07", "qemu"),
			usageGuest(101, "pve-01", "linux01", "qemu"),
			usageGuest(102, "pve-01", "linux02", "qemu"),
			usageGuest(103, "pve-01", "linux03", "qemu"),
			usageGuest(104, "pve-03", "win04", "qemu"),
			usageGuest(200, "pve-01", "linux20", "lxc"),
		},
		configs: map[int]proxmox.VMConfig{
			// Two keys, found wherever the mapping key sits in the value.
			105: {"usb10": "usb3=1,mapping=usbdev01", "usb2": "mapping=usbdev01", "name": "linux05"},
			101: {"usb0": "mapping=usbdev01,usb3=1"},
			// Another mapping, a raw device, a mapping only in a PCI key, and the id in another case: none uses it.
			102: {"usb0": "mapping=usbdev011", "usb1": "host=1-2", "hostpci0": "mapping=usbdev01", "usb2": "mapping=USBDEV01"},
			103: {},
		},
		configErrs: map[int]error{
			106: mappingAPIError(`{"data":null,"message":"Configuration file 'nodes/pve-02/qemu-server/106.conf' does not exist\n"}`),
		},
		hangVMIDs: map[int]bool{107: true},
	}
	// More guests than reads run at once, all of which must be read and counted.
	wantRead := []int{101, 102, 103, 105, 106, 107}
	for vmid := 301; vmid < 301+2*usbMappingUsageConcurrency; vmid++ {
		f.resources = append(f.resources, usageGuest(vmid, "pve-01", "linux01", "qemu"))
		wantRead = append(wantRead, vmid)
	}

	usage, err := scanUSBMappingUsage(context.Background(), f, "usbdev01")
	if err != nil {
		t.Fatalf("scanUSBMappingUsage: %v", err)
	}
	if usage.MappingID != "usbdev01" {
		t.Errorf("mapping_id = %q", usage.MappingID)
	}
	wantUsers := []usbMappingGuest{
		{VMID: 101, Name: "linux01", Node: "pve-01", Keys: []string{"usb0"}},
		{VMID: 105, Name: "linux05", Node: "pve-02", Keys: []string{"usb2", "usb10"}},
	}
	if !reflect.DeepEqual(usage.Users, wantUsers) {
		t.Errorf("users = %+v, want %+v", usage.Users, wantUsers)
	}
	wantUnchecked := []usbMappingGuest{
		{VMID: 104, Name: "win04", Node: "pve-03", Reason: "The node is offline."},
		{VMID: 106, Name: "win06", Node: "pve-02", Reason: "Configuration file 'nodes/pve-02/qemu-server/106.conf' does not exist"},
		{VMID: 107, Name: "linux07", Node: "pve-02", Reason: "No answer within 200ms."},
	}
	if !reflect.DeepEqual(usage.Unchecked, wantUnchecked) {
		t.Errorf("unchecked = %+v, want %+v", usage.Unchecked, wantUnchecked)
	}
	if want := 4 + 2*usbMappingUsageConcurrency; usage.Checked != want {
		t.Errorf("checked = %d, want %d (101, 102, 103, 105 and the %d beyond the first wave)", usage.Checked, want, 2*usbMappingUsageConcurrency)
	}
	// Neither the container nor the guest on the offline node is read.
	slices.Sort(f.read)
	if !slices.Equal(f.read, wantRead) {
		t.Errorf("read %v, want %v", f.read, wantRead)
	}
}

// The guest list is the scan: without it there is nothing to say, so the whole
// request fails rather than answering "no users".
func TestScanUSBMappingUsage_FailsWithoutTheGuestList(t *testing.T) {
	f := &mappingFakeClient{resourcesErr: proxmox.ErrConnectionFailed}
	if _, err := scanUSBMappingUsage(context.Background(), f, "usbdev01"); !errors.Is(err, proxmox.ErrConnectionFailed) {
		t.Errorf("err = %v, want the listing's error", err)
	}
}

func TestScanUSBMappingUsage_WithoutNodeStatusReadsEveryGuest(t *testing.T) {
	f := &mappingFakeClient{
		nodesErr:  proxmox.ErrConnectionFailed,
		resources: []proxmox.ClusterResource{usageGuest(101, "pve-01", "linux01", "qemu")},
		configs:   map[int]proxmox.VMConfig{101: {"usb0": "mapping=usbdev01"}},
	}
	usage, err := scanUSBMappingUsage(context.Background(), f, "usbdev01")
	if err != nil {
		t.Fatalf("scanUSBMappingUsage: %v", err)
	}
	if len(usage.Users) != 1 || len(usage.Unchecked) != 0 {
		t.Errorf("usage = %+v, want the one user", usage)
	}
}

// A guest not read within the scan's budget is reported unchecked with the budget as
// its reason — never as a guest that does not use the mapping, and never blamed on
// its own timeout. Every read hangs, so the reads in flight are exactly the first
// wave: the bound is asked here too.
func TestScanUSBMappingUsage_BudgetRunsOut(t *testing.T) {
	oldTimeout, oldBudget := usbMappingUsageTimeout, usbMappingUsageBudget
	usbMappingUsageTimeout, usbMappingUsageBudget = 5*time.Second, 150*time.Millisecond
	t.Cleanup(func() { usbMappingUsageTimeout, usbMappingUsageBudget = oldTimeout, oldBudget })

	f := &mappingFakeClient{nodes: onlineNodes("pve-01"), hangVMIDs: map[int]bool{}}
	var want []int
	for vmid := 101; vmid <= 101+2*usbMappingUsageConcurrency; vmid++ {
		f.resources = append(f.resources, usageGuest(vmid, "pve-01", fmt.Sprintf("linux%d", vmid), "qemu"))
		f.hangVMIDs[vmid] = true
		want = append(want, vmid)
	}

	start := time.Now()
	usage, err := scanUSBMappingUsage(context.Background(), f, "usbdev01")
	if err != nil {
		t.Fatalf("scanUSBMappingUsage: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Errorf("the scan took %s; the budget is %s", elapsed, usbMappingUsageBudget)
	}
	got := make([]int, 0, len(usage.Unchecked))
	for _, g := range usage.Unchecked {
		got = append(got, g.VMID)
		if g.Reason != "Not read: the scan ran out of its 150ms." {
			t.Errorf("guest %d reason = %q, want the budget", g.VMID, g.Reason)
		}
	}
	if !slices.Equal(got, want) {
		t.Errorf("unchecked %v, want every guest %v", got, want)
	}
	if usage.Checked != 0 || len(usage.Users) != 0 {
		t.Errorf("usage = %+v, want nothing checked", usage)
	}
	if f.maxInFlight > usbMappingUsageConcurrency {
		t.Errorf("%d reads ran at once, want at most %d", f.maxInFlight, usbMappingUsageConcurrency)
	}
}

// The guest list is read within the scan's budget too, so a Proxmox that never
// answers it cannot hold the delete dialog for the client's timeout.
func TestScanUSBMappingUsage_GuestListIsInsideTheBudget(t *testing.T) {
	oldBudget := usbMappingUsageBudget
	usbMappingUsageBudget = 100 * time.Millisecond
	t.Cleanup(func() { usbMappingUsageBudget = oldBudget })

	f := &mappingFakeClient{hangResources: true}
	start := time.Now()
	if _, err := scanUSBMappingUsage(context.Background(), f, "usbdev01"); !errors.Is(err, proxmox.ErrConnectionFailed) {
		t.Errorf("err = %v, want the guest list's failure", err)
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Errorf("the scan took %s; the budget is %s", elapsed, usbMappingUsageBudget)
	}
}

func TestEachWithin(t *testing.T) {
	// Clamped to one: with none, the first send would wait forever.
	t.Run("no workers still runs every item", func(t *testing.T) {
		done := make(chan int, 1)
		go func() {
			ran := 0
			eachWithin(context.Background(), 0, []int{1, 2, 3}, func(int) { ran++ }, func(int) {})
			done <- ran
		}()
		select {
		case ran := <-done:
			if ran != 3 {
				t.Errorf("ran %d items, want 3", ran)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("eachWithin with no workers never returned")
		}
	})
	// Calls are held until the pool is as wide as it will go: the width is reached
	// (the pool really overlaps work), and a wider pool shows itself. Proving that
	// no more arrive needs a wait, which can only catch a wrong pool, never fail a
	// right one.
	t.Run("every item once, at most workers at a time", func(t *testing.T) {
		const workers = 4
		var mu sync.Mutex
		var inFlight, maxInFlight int
		seen := map[int]int{}
		atBound, over, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
		var boundOnce, overOnce sync.Once
		items := make([]int, 0, 30)
		for i := range 30 {
			items = append(items, i)
		}
		done := make(chan struct{})
		go func() {
			defer close(done)
			eachWithin(context.Background(), workers, items, func(i int) {
				mu.Lock()
				inFlight++
				maxInFlight = max(maxInFlight, inFlight)
				seen[i]++
				n := inFlight
				mu.Unlock()
				if n == workers {
					boundOnce.Do(func() { close(atBound) })
				}
				if n > workers {
					overOnce.Do(func() { close(over) })
				}
				<-release
				mu.Lock()
				inFlight--
				mu.Unlock()
			}, func(int) { t.Error("an item was skipped with the budget unspent") })
		}()
		select {
		case <-atBound:
		case <-time.After(5 * time.Second):
			t.Fatal("the pool never ran its workers side by side")
		}
		select {
		case <-over:
			t.Error("more items ran at once than there are workers")
		case <-time.After(25 * time.Millisecond):
		}
		close(release)
		<-done
		if maxInFlight != workers {
			t.Errorf("%d ran at once, want exactly %d", maxInFlight, workers)
		}
		for _, i := range items {
			if seen[i] != 1 {
				t.Errorf("item %d ran %d times, want once", i, seen[i])
			}
		}
	})
	t.Run("items not started when the budget ends are skipped", func(t *testing.T) {
		budget, cancel := context.WithCancel(context.Background())
		var mu sync.Mutex
		ran, skipped := 0, 0
		eachWithin(budget, 2, []int{1, 2, 3, 4, 5, 6}, func(int) {
			mu.Lock()
			ran++
			if ran == 2 {
				cancel()
			}
			mu.Unlock()
		}, func(int) {
			mu.Lock()
			skipped++
			mu.Unlock()
		})
		if ran+skipped != 6 || skipped == 0 || ran > 3 {
			t.Errorf("ran %d, skipped %d; want every item accounted for and the rest skipped", ran, skipped)
		}
	})
}

// mappingUsageMirror is a usage route's declared parameters, as withRequestParams
// needs them; registry_mappings.go holds the declaration.
func mappingUsageMirror(t *testing.T) apischema.Properties {
	t.Helper()
	return compiledMirror(t, apischema.Properties{
		"cluster_id": apischema.StdOption("cluster-id"),
		"mapping_id": {Type: apischema.String, MaxLength: apischema.Ptr(128)},
	})
}

// A caller over a cap on scans in flight gets 429 at once — before any Proxmox
// client is made (these handlers have none to make: it would panic), so nothing is
// read on its behalf. The PCI route shares the USB route's pool, so slots taken
// through it leave the PCI check no room either.
func TestMappingUsageRoutes_CapScansInFlight(t *testing.T) {
	cluster := uuid.New()
	for range usbMappingUsageScansPerCluster {
		_, release, err := usbMappingUsageScans.acquire(context.Background(), cluster, uuid.New())
		if err != nil {
			t.Fatalf("acquire: %v", err)
		}
		t.Cleanup(release)
	}
	app := fiber.New(fiber.Config{ErrorHandler: testErrorHandler})
	h := &VMHandler{}
	for kind, handler := range map[string]fiber.Handler{
		"usb": withRequestParams(t, mappingUsageMirror(t), []string{"cluster_id", "mapping_id"}, h.GetUSBMappingUsage),
		"pci": withRequestParams(t, mappingUsageMirror(t), []string{"cluster_id", "mapping_id"}, h.GetPCIMappingUsage),
	} {
		app.Get("/api/v1/clusters/:cluster_id/"+kind+"-mappings/:mapping_id/usage", handler)
	}
	for _, kind := range []string{"usb", "pci"} {
		resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/api/v1/clusters/"+cluster.String()+"/"+kind+"-mappings/id01/usage", nil))
		if err != nil {
			t.Fatalf("app.Test: %v", err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != fiber.StatusTooManyRequests {
			t.Errorf("%s: status = %d, want 429", kind, resp.StatusCode)
		}
	}
}

// mappingAcquireStopping takes a scan slot for a scan that stops, and gives the
// slot up, the moment it is cancelled — as a real one does.
func mappingAcquireStopping(slots *usageScanSlots, cluster, user uuid.UUID) (context.Context, func(), error) {
	ctx, release, err := slots.acquire(context.Background(), cluster, user)
	if err != nil {
		return nil, nil, err
	}
	go func() {
		<-ctx.Done()
		release()
	}()
	return ctx, release, nil
}

// Per cluster, so no caller holds what another cluster's check needs; per user, so
// no one account holds both of a cluster's — and a user's newer scan replaces their
// older one instead of being refused behind it.
func TestUsageScanSlots(t *testing.T) {
	bg := context.Background()
	refused := func(t *testing.T, err error) {
		t.Helper()
		wantStatus(t, err, fiber.StatusTooManyRequests)
	}

	t.Run("two per cluster, whoever asks", func(t *testing.T) {
		slots := newUsageScanSlots()
		clusterA, clusterB := uuid.New(), uuid.New()
		_, releaseAlice, err := slots.acquire(bg, clusterA, uuid.New())
		if err != nil {
			t.Fatalf("alice: %v", err)
		}
		_, releaseBob, err := slots.acquire(bg, clusterA, uuid.New())
		if err != nil {
			t.Fatalf("bob: %v", err)
		}
		_, _, err = slots.acquire(bg, clusterA, uuid.New())
		refused(t, err)
		// Another cluster's slots are its own.
		_, releaseCarol, err := slots.acquire(bg, clusterB, uuid.New())
		if err != nil {
			t.Fatalf("carol on cluster B: %v", err)
		}
		releaseAlice()
		releaseBob()
		releaseCarol()
		if len(slots.byUser) != 0 || len(slots.byCluster) != 0 {
			t.Errorf("after every release: users %v, clusters %v; want both empty", slots.byUser, slots.byCluster)
		}
	})

	t.Run("a release twice frees one slot, not two", func(t *testing.T) {
		slots := newUsageScanSlots()
		cluster := uuid.New()
		_, releaseAlice, err := slots.acquire(bg, cluster, uuid.New())
		if err != nil {
			t.Fatalf("alice: %v", err)
		}
		if _, _, err := slots.acquire(bg, cluster, uuid.New()); err != nil {
			t.Fatalf("bob: %v", err)
		}
		releaseAlice()
		releaseAlice()
		if _, _, err := slots.acquire(bg, cluster, uuid.New()); err != nil {
			t.Fatalf("carol, into alice's slot: %v", err)
		}
		// Bob and carol hold both slots: a double release must not have counted a third free.
		_, _, err = slots.acquire(bg, cluster, uuid.New())
		refused(t, err)
	})

	t.Run("a user's newer scan replaces their older one", func(t *testing.T) {
		slots := newUsageScanSlots()
		cluster, alice := uuid.New(), uuid.New()
		olderCtx, releaseOlder, err := mappingAcquireStopping(slots, cluster, alice)
		if err != nil {
			t.Fatalf("older: %v", err)
		}
		newerCtx, releaseNewer, err := slots.acquire(bg, cluster, alice)
		if err != nil {
			t.Fatalf("newer: %v", err)
		}
		defer releaseNewer()
		if !errors.Is(olderCtx.Err(), context.Canceled) {
			t.Errorf("older scan's context = %v, want cancelled", olderCtx.Err())
		}
		if newerCtx.Err() != nil {
			t.Errorf("newer scan's context = %v, want live", newerCtx.Err())
		}
		// A second release of the older scan — by a caller's defer, say — must not free the newer one's slot.
		releaseOlder()
		if slots.byUser[alice] == nil || slots.byCluster[cluster] != 1 {
			t.Errorf("users %v, clusters %v; want the newer scan still counted", slots.byUser, slots.byCluster)
		}
	})

	// The re-check after the wait: two newer requests of one user racing for the slot
	// the older scan gives up. Whether they arrive together (one gets the slot, the
	// other a 429) or one after the other (the later replaces the earlier), the user
	// ends with exactly ONE scan running and one slot held — never two side by side.
	t.Run("two newer requests at once: one scan runs", func(t *testing.T) {
		type held struct {
			ctx     context.Context
			release func()
		}
		for range 50 {
			slots := newUsageScanSlots()
			cluster, alice := uuid.New(), uuid.New()
			if _, _, err := mappingAcquireStopping(slots, cluster, alice); err != nil {
				t.Fatalf("older: %v", err)
			}
			results := make(chan held, 2)
			for range 2 {
				go func() {
					ctx, release, _ := mappingAcquireStopping(slots, cluster, alice)
					results <- held{ctx, release}
				}()
			}
			// Both answered before any is judged: the later of two that both got the
			// slot cancels the earlier one's scan as it takes over.
			got := []held{<-results, <-results}
			var running []held
			for _, h := range got {
				if h.ctx != nil && h.ctx.Err() == nil {
					running = append(running, h)
				}
			}
			slots.mu.Lock()
			count := slots.byCluster[cluster]
			slots.mu.Unlock()
			if len(running) != 1 || count != 1 {
				t.Fatalf("%d scans still running and %d slots held for one user, want 1 and 1", len(running), count)
			}
			running[0].release()
			slots.mu.Lock()
			users, clusters := len(slots.byUser), len(slots.byCluster)
			slots.mu.Unlock()
			if users != 0 || clusters != 0 {
				t.Fatalf("after the last release: %d users, %d clusters; want none", users, clusters)
			}
		}
	})

	// A request the cluster has no room for is refused BEFORE it stops the user's
	// older scan elsewhere: otherwise it would cost them both checks.
	t.Run("a full cluster refuses without stopping the user's other scan", func(t *testing.T) {
		slots := newUsageScanSlots()
		clusterA, clusterB, alice := uuid.New(), uuid.New(), uuid.New()
		aliceCtx, releaseAlice, err := slots.acquire(bg, clusterA, alice)
		if err != nil {
			t.Fatalf("alice on A: %v", err)
		}
		defer releaseAlice()
		for range usbMappingUsageScansPerCluster {
			if _, _, err := slots.acquire(bg, clusterB, uuid.New()); err != nil {
				t.Fatalf("filling B: %v", err)
			}
		}
		_, _, err = slots.acquire(bg, clusterB, alice)
		refused(t, err)
		if aliceCtx.Err() != nil {
			t.Errorf("alice's scan of A was stopped (%v) by a request that was refused anyway", aliceCtx.Err())
		}
	})

	// On a busy cluster — the user's older scan and someone else's — the user's newer
	// check replaces the older one: its slot is room.
	t.Run("the user's own older scan on the cluster counts as room", func(t *testing.T) {
		slots := newUsageScanSlots()
		cluster, alice := uuid.New(), uuid.New()
		if _, _, err := mappingAcquireStopping(slots, cluster, alice); err != nil {
			t.Fatalf("older: %v", err)
		}
		for range usbMappingUsageScansPerCluster - 1 {
			if _, _, err := slots.acquire(bg, cluster, uuid.New()); err != nil {
				t.Fatalf("others fill the cluster: %v", err)
			}
		}
		_, releaseNewer, err := slots.acquire(bg, cluster, alice)
		if err != nil {
			t.Fatalf("alice's newer check on a cluster her older one fills: %v", err)
		}
		releaseNewer()
	})

	// The room is looked at again after the wait: another user may have taken the last slot meanwhile.
	t.Run("a cluster that filled during the wait refuses", func(t *testing.T) {
		slots := newUsageScanSlots()
		clusterX, clusterY, alice := uuid.New(), uuid.New(), uuid.New()
		olderCtx, releaseOlder, err := slots.acquire(bg, clusterX, alice)
		if err != nil {
			t.Fatalf("alice's older scan on X: %v", err)
		}
		if _, _, err := slots.acquire(bg, clusterY, uuid.New()); err != nil {
			t.Fatalf("dave on Y: %v", err)
		}
		carol := make(chan error, 1)
		go func() {
			// Carol takes Y's last slot while alice's newer check is still waiting for her older scan to stop.
			<-olderCtx.Done()
			_, _, err := slots.acquire(bg, clusterY, uuid.New())
			carol <- err
			releaseOlder()
		}()
		// Refused for want of room — not with one of the other two 429s acquire can answer.
		_, _, err = slots.acquire(bg, clusterY, alice)
		if !errors.Is(err, errUSBMappingUsageClusterFull) {
			t.Errorf("alice's newer check on Y = %v, want %v", err, errUSBMappingUsageClusterFull)
		}
		select {
		case err := <-carol:
			if err != nil {
				t.Fatalf("carol on Y: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("alice's older scan was never stopped, so carol never asked")
		}
		slots.mu.Lock()
		defer slots.mu.Unlock()
		if slots.byCluster[clusterY] != usbMappingUsageScansPerCluster {
			t.Errorf("Y holds %d scans, want %d", slots.byCluster[clusterY], usbMappingUsageScansPerCluster)
		}
	})

	t.Run("an older scan that will not stop keeps its slot", func(t *testing.T) {
		old := usbMappingUsageSupersedeWait
		usbMappingUsageSupersedeWait = 50 * time.Millisecond
		t.Cleanup(func() { usbMappingUsageSupersedeWait = old })

		slots := newUsageScanSlots()
		cluster, alice := uuid.New(), uuid.New()
		_, releaseOlder, err := slots.acquire(bg, cluster, alice)
		if err != nil {
			t.Fatalf("older: %v", err)
		}
		defer releaseOlder()
		_, _, err = slots.acquire(bg, cluster, alice)
		refused(t, err)
	})
}

// mappingHoldScan starts a USB usage check that cannot finish on its own — guests
// guests, none of which answers — and returns once it is reading, with the channel
// its error will arrive on when something supersedes it.
func mappingHoldScan(t *testing.T, slots *usageScanSlots, cluster, user uuid.UUID, guests int) <-chan error {
	t.Helper()
	stuck := &mappingFakeClient{nodes: onlineNodes("pve-01"), hangVMIDs: map[int]bool{}}
	for vmid := 101; vmid < 101+guests; vmid++ {
		stuck.resources = append(stuck.resources, usageGuest(vmid, "pve-01", "linux01", "qemu"))
		stuck.hangVMIDs[vmid] = true
	}
	reading := stuck.firstRead()
	errc := make(chan error, 1)
	go func() {
		_, err := checkUSBMappingUsage(context.Background(), slots, cluster, user, "usbdev01",
			func() (usbMappingReader, error) { return stuck, nil })
		errc <- err
	}()
	select {
	case <-reading:
	case <-time.After(5 * time.Second):
		t.Fatal("the held scan never started reading")
	}
	return errc
}

// The handler's whole path: the scan runs under the slot's context, so a user's
// newer check stops their older one — here a USB scan stuck on guests that never
// answer, replaced by a USB check and by a PCI check, which share one pool — and
// goes ahead at once.
func TestCheckMappingUsage_NewerReplacesOlder(t *testing.T) {
	cluster, alice := uuid.New(), uuid.New()
	usbQuick := &mappingFakeClient{
		nodes:     onlineNodes("pve-01"),
		resources: []proxmox.ClusterResource{usageGuest(101, "pve-01", "linux01", "qemu")},
		configs:   map[int]proxmox.VMConfig{101: {"usb0": "mapping=usbdev01", "hostpci0": "mapping=gpu01"}},
	}
	for _, tt := range []struct {
		name  string
		check func(slots *usageScanSlots) (mappingUsage, error)
	}{
		{"a USB check", func(slots *usageScanSlots) (mappingUsage, error) {
			return checkUSBMappingUsage(context.Background(), slots, cluster, alice, "usbdev01",
				func() (usbMappingReader, error) { return usbQuick, nil })
		}},
		{"a PCI check", func(slots *usageScanSlots) (mappingUsage, error) {
			return checkPCIMappingUsage(context.Background(), slots, cluster, alice, "gpu01",
				func() (guestConfigReader, error) { return usbQuick, nil })
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			slots := newUsageScanSlots()
			older := mappingHoldScan(t, slots, cluster, alice, 20)
			usage, err := tt.check(slots)
			if err != nil || len(usage.Users) != 1 || usage.Users[0].VMID != 101 {
				t.Fatalf("the newer check = %+v, %v; want its own answer", usage, err)
			}
			select {
			case err := <-older:
				if !errors.Is(err, errUSBMappingUsageSuperseded) {
					t.Errorf("the older check = %v, want errUSBMappingUsageSuperseded", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("the older check never answered")
			}
		})
	}
}

// A check that gets a slot and then cannot make its Proxmox client — a cluster gone
// from the database, say — still gives the slot back: a leaked one would hold the
// user at "still stopping" and, twice over, the cluster at 429 until a restart.
func TestCheckUSBMappingUsage_ReleasesWhenTheClientFails(t *testing.T) {
	slots := newUsageScanSlots()
	noClient := fiber.NewError(fiber.StatusNotFound, "Cluster not found")
	_, err := checkUSBMappingUsage(context.Background(), slots, uuid.New(), uuid.New(), "usbdev01",
		func() (usbMappingReader, error) { return nil, noClient })
	if !errors.Is(err, noClient) {
		t.Errorf("err = %v, want the client's", err)
	}
	slots.mu.Lock()
	defer slots.mu.Unlock()
	if len(slots.byUser) != 0 || len(slots.byCluster) != 0 {
		t.Errorf("after the failed check: users %v, clusters %v; want both empty", slots.byUser, slots.byCluster)
	}
}

// A replaced scan answers that it was replaced — not its partial result, whose unread
// guests would carry untrue reasons, and not the error its cancelled calls produced.
func TestUSBMappingUsageAnswer(t *testing.T) {
	done := usbMappingUsage{MappingID: "usbdev01", Checked: 3, Users: []usbMappingGuest{}, Unchecked: []usbMappingGuest{}}

	live := context.Background()
	if got, err := usbMappingUsageAnswer(live, done, nil); err != nil || got.Checked != 3 {
		t.Errorf("a finished scan = %+v, %v; want its result", got, err)
	}
	_, err := usbMappingUsageAnswer(live, done, proxmox.ErrConnectionFailed)
	wantStatus(t, err, fiber.StatusBadGateway)

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	for _, scanErr := range []error{nil, proxmox.ErrConnectionFailed} {
		if _, err := usbMappingUsageAnswer(cancelled, done, scanErr); !errors.Is(err, errUSBMappingUsageSuperseded) {
			t.Errorf("a replaced scan (scan error %v) = %v, want errUSBMappingUsageSuperseded", scanErr, err)
		}
	}
}

// usbMappingUpdateMirror is the update route's declared parameters, as the request
// helpers read them; registry_mappings.go holds the declaration.
func usbMappingUpdateMirror(t *testing.T) apischema.Properties {
	t.Helper()
	return compiledMirror(t, apischema.Properties{
		"mapping_id":  {Type: apischema.String},
		"map":         {Type: apischema.Array, Items: &apischema.Property{Type: apischema.String}},
		"description": {Type: apischema.String, Optional: true},
		"digest":      {Type: apischema.String},
	})
}

// From the route's parameters to the client call, whole.
func TestUpdateUSBMappingRequest(t *testing.T) {
	p, err := usbMappingUpdateMirror(t).Validate(map[string]any{
		"mapping_id": "usbdev01", "map": []any{"node=pve-01,id=1234:5678"}, "digest": "d1",
	})
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	f := &mappingFakeClient{}
	id, params, err := updateUSBMappingRequest(context.Background(), f, p)
	if err != nil {
		t.Fatalf("updateUSBMappingRequest: %v", err)
	}
	if id != "usbdev01" || f.usbUpdateID != "usbdev01" || f.usbUpdate.Digest != "d1" ||
		!slices.Equal(f.usbUpdate.Map, []string{"node=pve-01,id=1234:5678"}) || f.usbUpdate.Description != nil {
		t.Errorf("sent %q %+v (returned %q %+v), want the route's values and no description", f.usbUpdateID, f.usbUpdate, id, params)
	}

	// Proxmox's stale-digest die comes back as the route's 409.
	f.updateErr = mappingAPIError("update hardware mapping failed: detected modified configuration")
	_, _, err = updateUSBMappingRequest(context.Background(), f, p)
	wantStatus(t, err, fiber.StatusConflict)
}

// The route's digest reaches the check that uses it, for either kind.
func TestDeleteMappingRequest(t *testing.T) {
	props := compiledMirror(t, apischema.Properties{
		"mapping_id": {Type: apischema.String},
		"digest":     {Type: apischema.String, Optional: true},
	})
	usb := []proxmox.USBMapping{{ID: "usbdev01", Digest: "d2", Map: []string{"id=1234:5678,node=pve-01"}}}
	pci := []proxmox.PCIMapping{{ID: "gpu01", Digest: "d2", Map: []string{gpuFirst}}}
	for _, kind := range []struct {
		name string
		id   string
		// run is the request helper under test; list and del are the calls it logs.
		run       func(f *mappingFakeClient, p *apischema.Params) (string, string, error)
		list, del string
		fake      func() *mappingFakeClient
	}{
		{"USB", "usbdev01", func(f *mappingFakeClient, p *apischema.Params) (string, string, error) {
			id, outcome, err := deleteUSBMappingRequest(context.Background(), f, p)
			return id, outcome.action, err
		}, "list", "delete", func() *mappingFakeClient {
			return &mappingFakeClient{listings: map[string][]proxmox.USBMapping{"": usb}}
		}},
		{"PCI", "gpu01", func(f *mappingFakeClient, p *apischema.Params) (string, string, error) {
			id, outcome, err := deletePCIMappingRequest(context.Background(), f, p)
			if err == nil && (outcome.snapshot == nil || outcome.snapshot.ID != "gpu01") {
				err = errors.New("the outcome does not hold the snapshot")
			}
			return id, outcome.action, err
		}, "list:", "delete:gpu01", func() *mappingFakeClient { return &mappingFakeClient{pciListing: pci} }},
	} {
		for _, tt := range []struct {
			name     string
			digest   any
			wantCode int
			wantDel  bool
		}{
			{"a stale digest is compared", "d1", fiber.StatusConflict, false},
			{"a current one lets it through", "d2", 0, true},
			{"none is Proxmox's own delete", nil, 0, true},
		} {
			t.Run(kind.name+"/"+tt.name, func(t *testing.T) {
				body := map[string]any{"mapping_id": kind.id}
				if tt.digest != nil {
					body["digest"] = tt.digest
				}
				p, err := props.Validate(body)
				if err != nil {
					t.Fatalf("Validate: %v", err)
				}
				f := kind.fake()
				id, action, err := kind.run(f, p)
				if tt.wantCode != 0 {
					wantStatus(t, err, tt.wantCode)
					if !strings.Contains(err.Error(), kind.name+" mappings changed") {
						t.Errorf("err = %v, want the %s stale message", err, kind.name)
					}
					f.requireCalls(t, kind.list)
					return
				}
				if err != nil || id != kind.id || action != "deleted" {
					t.Errorf("= %q, %q, %v; want %s deleted", id, action, err, kind.id)
				}
				f.requireCalls(t, kind.list, kind.del)
			})
		}
	}
}

// The description reaches the client only when the caller sent one: a non-nil empty
// one is delete=description, so passing one on every update would wipe the
// description on each Add node, Replace and Remove.
func TestUSBMappingUpdateParams(t *testing.T) {
	props := compiledMirror(t, apischema.Properties{
		"map":         {Type: apischema.Array, Items: &apischema.Property{Type: apischema.String}},
		"description": {Type: apischema.String, Optional: true},
		"digest":      {Type: apischema.String},
	})
	for _, tt := range []struct {
		name string
		body map[string]any
		want *string
	}{
		{"left alone", map[string]any{"map": []any{"node=pve-01,id=1234:5678"}, "digest": "d1"}, nil},
		{"removed", map[string]any{"map": []any{"node=pve-01,id=1234:5678"}, "digest": "d1", "description": ""}, strPtr("")},
		{"replaced", map[string]any{"map": []any{"node=pve-01,id=1234:5678"}, "digest": "d1", "description": "x"}, strPtr("x")},
	} {
		t.Run(tt.name, func(t *testing.T) {
			p, err := props.Validate(tt.body)
			if err != nil {
				t.Fatalf("Validate: %v", err)
			}
			got := usbMappingUpdateParams(p)
			if !slices.Equal(got.Map, []string{"node=pve-01,id=1234:5678"}) || got.Digest != "d1" {
				t.Errorf("params = %+v", got)
			}
			if !reflect.DeepEqual(got.Description, tt.want) {
				t.Errorf("description = %v, want %v", got.Description, tt.want)
			}
		})
	}
}

func TestDeleteUSBMapping_Flow(t *testing.T) {
	listing := []proxmox.USBMapping{{ID: "usbdev01", Digest: "d1", Map: []string{"id=1234:5678,node=pve-01"}}}
	other := func(digest string) map[string][]proxmox.USBMapping {
		return map[string][]proxmox.USBMapping{"": {{ID: "usbdev02", Digest: digest, Map: []string{"id=abcd:ef01,node=pve-01"}}}}
	}
	for _, tt := range []struct {
		name        string
		fake        *mappingFakeClient
		digest      string
		wantCode    int // 0: the delete goes ahead
		wantCalls   []string
		wantAction  string
		wantUnknown bool
		wantEntries bool
	}{
		// The snapshot comes FIRST: read after the delete it would find nothing, and every delete would be recorded as a no-op.
		{name: "snapshot, then delete", fake: &mappingFakeClient{listings: map[string][]proxmox.USBMapping{"": listing}, afterDelete: []proxmox.USBMapping{}},
			wantCalls: []string{"list", "delete"}, wantAction: "deleted", wantEntries: true},
		{name: "a matching digest", fake: &mappingFakeClient{listings: map[string][]proxmox.USBMapping{"": listing}}, digest: "d1",
			wantCalls: []string{"list", "delete"}, wantAction: "deleted", wantEntries: true},
		// usb.cfg changed since the caller's listing: nothing is deleted.
		{name: "a stale digest", fake: &mappingFakeClient{listings: map[string][]proxmox.USBMapping{"": listing}}, digest: "d0",
			wantCode: fiber.StatusConflict, wantCalls: []string{"list"}},
		// Asked to compare and could not look: it does not go ahead blind.
		{name: "a digest, and no snapshot", fake: &mappingFakeClient{listErrs: map[string]error{"": proxmox.ErrConnectionFailed}}, digest: "d1",
			wantCode: fiber.StatusBadGateway, wantCalls: []string{"list"}},
		// Without a digest it is Proxmox's delete, recorded with its doubt.
		{name: "no digest, and no snapshot", fake: &mappingFakeClient{listErrs: map[string]error{"": proxmox.ErrConnectionFailed}},
			wantCalls: []string{"list", "delete"}, wantAction: "deleted", wantUnknown: true},
		// No mapping at all: no digest to compare, so the idempotent no-op.
		{name: "already gone, and usb.cfg empty", fake: &mappingFakeClient{listings: map[string][]proxmox.USBMapping{"": {}}}, digest: "d1",
			wantCalls: []string{"list", "delete"}, wantAction: "already_deleted"},
		// Gone, and usb.cfg still has the caller's digest: the caller's view holds it absent too.
		{name: "already gone, usb.cfg unchanged", fake: &mappingFakeClient{listings: other("d1")}, digest: "d1",
			wantCalls: []string{"list", "delete"}, wantAction: "already_deleted"},
		// Gone since the caller's read — deleted, perhaps made again in a moment: the digest is compared all the same.
		{name: "gone since the caller's read", fake: &mappingFakeClient{listings: other("d2")}, digest: "d1",
			wantCode: fiber.StatusConflict, wantCalls: []string{"list"}},
		{name: "Proxmox refuses the delete", fake: &mappingFakeClient{listings: map[string][]proxmox.USBMapping{"": listing}, deleteErr: proxmox.ErrForbidden},
			wantCode: fiber.StatusForbidden, wantCalls: []string{"list", "delete"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			outcome, err := deleteUSBMapping(context.Background(), tt.fake, "usbdev01", tt.digest)
			tt.fake.requireCalls(t, tt.wantCalls...)
			if tt.wantCode != 0 {
				wantStatus(t, err, tt.wantCode)
				return
			}
			if err != nil {
				t.Fatalf("deleteUSBMapping: %v", err)
			}
			if outcome.action != tt.wantAction || outcome.priorStateUnknown != tt.wantUnknown {
				t.Errorf("outcome = (%q, %v), want (%q, %v)", outcome.action, outcome.priorStateUnknown, tt.wantAction, tt.wantUnknown)
			}
			if got := outcome.snapshot != nil && len(outcome.snapshot.Map) == 1; got != tt.wantEntries {
				t.Errorf("snapshot holds the entries = %v, want %v", got, tt.wantEntries)
			}
		})
	}
}
