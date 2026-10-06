package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/gofiber/fiber/v3"

	"github.com/bigjakk/nexara/internal/api/apischema"
	"github.com/bigjakk/nexara/internal/proxmox"
)

// The PCI mapping flows on the Resource Mappings tab. The generic parts — the node
// checks, the merge, the delete's digest compare and the usage scan — are held by
// the USB tests in resource_mappings_test.go; these hold what is PCI's own: the
// update's order and rules, the listing's flags and unreadable entries, and the
// audit rows.

// Devices as the all-classes listing reports them — "0x" ids in any case — and one
// on pve-02 that can provide mediated devices.
var pciTestDevices = map[string][]proxmox.NodePCIDevice{
	"pve-01": {
		{ID: "0000:01:00.0", Vendor: "0x1234", Device: "0x5678", SubsystemVendor: "0xABCD", SubsystemDevice: "0xEF01", IOMMUGroup: 14},
		{ID: "0000:01:00.1", Vendor: "0x1234", Device: "0x5679", IOMMUGroup: 14},
		{ID: "0000:02:00.0", Vendor: "0x1234", Device: "0x5678", IOMMUGroup: 15},
		{ID: "0000:09:00.0", Vendor: "0x1234", Device: "0x0009", IOMMUGroup: 30},
	},
	"pve-02": {
		{ID: "0000:01:00.0", Vendor: "0x1234", Device: "0x5678", IOMMUGroup: 14},
		{ID: "0000:02:00.0", Vendor: "0x1234", Device: "0x5678", IOMMUGroup: 15},
		{ID: "0000:03:00.0", Vendor: "0x1234", Device: "0x0003", IOMMUGroup: 9, MDev: true},
	},
}

// pciMappingUpdateMirror is the PCI update route's declared parameters, as the
// request helper reads them; registry_mappings.go holds the declaration.
func pciMappingUpdateMirror(t *testing.T) apischema.Properties {
	t.Helper()
	node := apischema.StdOption("node-name")
	node.Optional = true
	node.Requires = []string{"add_path"}
	return compiledMirror(t, apischema.Properties{
		"mapping_id":  {Type: apischema.String},
		"map":         {Type: apischema.Array, MinLength: apischema.Ptr(1), Items: &apischema.Property{Type: apischema.String}},
		"add_node":    node,
		"add_path":    {Type: apischema.String, Optional: true, Requires: []string{"add_node"}},
		"replace":     {Type: apischema.String, Optional: true, Requires: []string{"add_node", "add_path"}},
		"description": {Type: apischema.String, Optional: true},
		"digest":      {Type: apischema.String},
	})
}

const (
	gpuFirst  = "id=1234:5678,iommugroup=14,node=pve-01,path=0000:01:00.0,subsystem-id=abcd:ef01"
	gpuSecond = "description=left slot,id=1234:5678,iommugroup=99,node=pve-01,path=0000:02:00.0"
	gpuOther  = "id=1234:5678,iommugroup=14,node=pve-02,path=0000:01:00.0"
)

// gpuListing is one mapping, gpu01, with two entries on pve-01 — the second with a
// stale group and an entry description — and one on pve-02.
func gpuListing(mdev bool) []proxmox.PCIMapping {
	return []proxmox.PCIMapping{
		{ID: "nic01", Digest: "d1", Map: []string{"id=1234:0002,node=pve-02,path=0000:02:00.0"}},
		{ID: "gpu01", Digest: "d1", MDev: proxmox.FlexBool(mdev), Map: []string{gpuFirst, gpuSecond, gpuOther}},
	}
}

func mappingGPUFake(mdev bool) *mappingFakeClient {
	return &mappingFakeClient{pciListing: gpuListing(mdev), devices: pciTestDevices}
}

func pciUpdate(t *testing.T, f *mappingFakeClient, body map[string]any) (pciMappingUpdate, error) {
	t.Helper()
	body["mapping_id"] = "gpu01"
	p, err := pciMappingUpdateMirror(t).Validate(body)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	return updatePCIMappingRequest(context.Background(), f, memberOf("pve-01", "pve-02"), p)
}

// A stale digest stops the update at the listing: no device is read and nothing is
// written. The digest is compared before anything else.
func TestUpdatePCIMappingRequest_StaleDigestReadsAndWritesNothing(t *testing.T) {
	f := mappingGPUFake(false)
	_, err := pciUpdate(t, f, map[string]any{
		"map": []any{gpuFirst, gpuSecond, gpuOther}, "add_node": "pve-01", "add_path": "0000:09:00.0", "digest": "d0",
	})
	wantStatus(t, err, fiber.StatusConflict)
	f.requireCalls(t, "list:")
}

// add_node is checked against the cluster before anything is sent: its devices are a
// /nodes/{node} read that pveproxy forwards wherever the name resolves. A failed
// lookup is a failure, not a member.
func TestUpdatePCIMappingRequest_NodeMembershipFirst(t *testing.T) {
	p, err := pciMappingUpdateMirror(t).Validate(map[string]any{
		"mapping_id": "gpu01", "map": []any{gpuFirst}, "add_node": "pve-09", "add_path": "0000:09:00.0", "digest": "d1"})
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	f := mappingGPUFake(false)
	_, err = updatePCIMappingRequest(context.Background(), f, memberOf("pve-01", "pve-02"), p)
	wantStatus(t, err, fiber.StatusNotFound)
	lookupFails := func(context.Context, string) (bool, error) {
		return false, fiber.NewError(fiber.StatusInternalServerError, "Failed to look up the node")
	}
	_, err = updatePCIMappingRequest(context.Background(), f, lookupFails, p)
	wantStatus(t, err, fiber.StatusInternalServerError)
	f.requireCalls(t)
}

func TestUpdatePCIMappingRequest_MissingMapping(t *testing.T) {
	for _, tt := range []struct {
		name    string
		listing []proxmox.PCIMapping
	}{
		{"another mapping listed, the digest current", []proxmox.PCIMapping{{ID: "nic01", Digest: "d1", Map: []string{gpuOther}}}},
		// No mapping at all: no digest to compare, and the mapping is gone.
		{"no mapping at all", nil},
	} {
		t.Run(tt.name, func(t *testing.T) {
			f := &mappingFakeClient{pciListing: tt.listing}
			_, err := pciUpdate(t, f, map[string]any{"map": []any{gpuFirst}, "digest": "d1"})
			wantStatus(t, err, fiber.StatusNotFound)
			if slices.Contains(f.calls, "update:gpu01") {
				t.Error("wrote to a mapping that is gone")
			}
		})
	}
}

// map may only name entries the mapping has, each as the listing returned it and no
// more often: nothing a caller spells itself reaches pci.cfg.
func TestUpdatePCIMappingRequest_MapIsASubMultisetOfTheEntries(t *testing.T) {
	for _, tt := range []struct {
		name string
		keep []any
	}{
		{"an entry of the caller's own", []any{gpuFirst, "id=1234:5678,node=pve-01,path=0000:09:00.0"}},
		{"an entry respelled", []any{"node=pve-01,path=0000:01:00.0,id=1234:5678,iommugroup=14,subsystem-id=abcd:ef01"}},
		{"an entry twice", []any{gpuFirst, gpuFirst}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			f := mappingGPUFake(false)
			_, err := pciUpdate(t, f, map[string]any{"map": tt.keep, "digest": "d1"})
			wantStatus(t, err, fiber.StatusBadRequest)
			if len(f.updated) != 0 {
				t.Errorf("wrote %+v", f.updated)
			}
		})
	}

	// Kept in the caller's order, the rest removed.
	f := mappingGPUFake(false)
	upd, err := pciUpdate(t, f, map[string]any{"map": []any{gpuOther, gpuFirst}, "digest": "d1"})
	if err != nil {
		t.Fatalf("pciUpdate: %v", err)
	}
	if len(f.updated) != 1 || !slices.Equal(f.updated[0].Map, []string{gpuOther, gpuFirst}) || f.updated[0].MDev != nil {
		t.Errorf("wrote %+v", f.updated)
	}
	if !slices.Equal(upd.removed, []string{gpuSecond}) {
		t.Errorf("removed = %q, want the second entry", upd.removed)
	}
}

// The new entry is built from the node's own report of the device, added after the
// kept ones, and sent with the caller's digest, which Proxmox checks again under its lock.
func TestUpdatePCIMappingRequest_AddsTheNodesDevice(t *testing.T) {
	f := mappingGPUFake(false)
	upd, err := pciUpdate(t, f, map[string]any{
		"map": []any{gpuFirst, gpuSecond, gpuOther}, "add_node": "pve-01", "add_path": "0000:09:00.0", "digest": "d1",
	})
	if err != nil {
		t.Fatalf("pciUpdate: %v", err)
	}
	f.requireCalls(t, "list:", "devices:pve-01", "update:gpu01")
	want := proxmox.UpdatePCIMappingParams{
		Map:    []string{gpuFirst, gpuSecond, gpuOther, "id=1234:0009,iommugroup=30,node=pve-01,path=0000:09:00.0"},
		Digest: "d1",
	}
	if !reflect.DeepEqual(f.updated[0], want) {
		t.Errorf("wrote %+v, want %+v", f.updated[0], want)
	}
	if upd.added == nil || upd.added.ID != "1234:0009" || upd.replaced != "" || len(upd.removed) != 0 {
		t.Errorf("update = %+v", upd)
	}
}

// A device the node's entries already name — as itself, as the whole device it is a
// function of, or as a function of the whole device named — is refused. The same
// address on another node is another device.
func TestUpdatePCIMappingRequest_RefusesAnOverlap(t *testing.T) {
	for _, tt := range []struct {
		name, node, path string
		want             int
	}{
		{"the same address", "pve-01", "0000:01:00.0", fiber.StatusBadRequest},
		{"the whole device of an entry's function", "pve-01", "0000:01:00", fiber.StatusBadRequest},
		{"another function of the same device", "pve-01", "0000:01:00.1", 0},
		// pve-01's second entry is 0000:02:00.0; pve-02's device there is another device.
		{"an address another node's entry has", "pve-02", "0000:02:00.0", 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			f := mappingGPUFake(false)
			_, err := pciUpdate(t, f, map[string]any{
				"map": []any{gpuFirst, gpuSecond, gpuOther}, "add_node": tt.node, "add_path": tt.path, "digest": "d1",
			})
			if tt.want == 0 {
				if err != nil || len(f.updated) != 1 {
					t.Fatalf("err = %v, wrote %d; want the device added", err, len(f.updated))
				}
				return
			}
			wantStatus(t, err, tt.want)
			if len(f.updated) != 0 {
				t.Errorf("wrote %+v", f.updated)
			}
		})
	}
}

// Replace puts the new entry where the old one was and keeps the old one's own
// description; the same device again brings a stale group up to date.
func TestUpdatePCIMappingRequest_ReplaceKeepsPositionAndDescription(t *testing.T) {
	f := mappingGPUFake(false)
	upd, err := pciUpdate(t, f, map[string]any{
		"map": []any{gpuFirst, gpuSecond, gpuOther}, "add_node": "pve-01", "add_path": "0000:02:00.0",
		"replace": gpuSecond, "digest": "d1",
	})
	if err != nil {
		t.Fatalf("pciUpdate: %v", err)
	}
	want := []string{gpuFirst, "description=left slot,id=1234:5678,iommugroup=15,node=pve-01,path=0000:02:00.0", gpuOther}
	if !slices.Equal(f.updated[0].Map, want) {
		t.Errorf("map = %q, want %q", f.updated[0].Map, want)
	}
	if upd.replaced != gpuSecond || len(upd.removed) != 0 {
		t.Errorf("update = %+v", upd)
	}
}

func TestUpdatePCIMappingRequest_ReplaceRefusals(t *testing.T) {
	for _, tt := range []struct {
		name string
		body map[string]any
	}{
		{"an entry not in map", map[string]any{"map": []any{gpuFirst, gpuOther}, "add_node": "pve-01",
			"add_path": "0000:02:00.0", "replace": gpuSecond, "digest": "d1"}},
		// A device pve-01 has free: only the node refuses it.
		{"an entry on another node", map[string]any{"map": []any{gpuFirst, gpuSecond, gpuOther}, "add_node": "pve-01",
			"add_path": "0000:09:00.0", "replace": gpuOther, "digest": "d1"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			f := mappingGPUFake(false)
			_, err := pciUpdate(t, f, tt.body)
			wantStatus(t, err, fiber.StatusBadRequest)
			if len(f.updated) != 0 {
				t.Errorf("wrote %+v", f.updated)
			}
		})
	}

	// An entry Nexara cannot read can only be removed.
	odd := "node=pve-01,path=0000:02:00.0,foo=bar"
	f := &mappingFakeClient{pciListing: []proxmox.PCIMapping{{ID: "gpu01", Digest: "d1", Map: []string{gpuFirst, odd}}}, devices: pciTestDevices}
	_, err := pciUpdate(t, f, map[string]any{"map": []any{gpuFirst, odd}, "add_node": "pve-01",
		"add_path": "0000:02:00.0", "replace": odd, "digest": "d1"})
	wantStatus(t, err, fiber.StatusBadRequest)
	if !strings.Contains(err.Error(), "cannot read the entry to replace") {
		t.Errorf("err = %v, want it to say the entry cannot be read", err)
	}
}

// An entry Nexara cannot read cannot be kept, whatever else the update does: it is
// refused with its reason before any device is read, and leaving it out of map is
// the save that goes through.
func TestUpdatePCIMappingRequest_RefusesAKeptEntryItCannotRead(t *testing.T) {
	odd := "node=pve-01,path=0000:02:00.0,foo=bar"
	fake := func() *mappingFakeClient {
		return &mappingFakeClient{pciListing: []proxmox.PCIMapping{{ID: "gpu01", Digest: "d1", Map: []string{gpuFirst, odd}}}, devices: pciTestDevices}
	}
	for _, tt := range []struct {
		name string
		body map[string]any
	}{
		{"adding a device", map[string]any{"map": []any{gpuFirst, odd}, "add_node": "pve-01", "add_path": "0000:09:00.0", "digest": "d1"}},
		{"changing the description", map[string]any{"map": []any{gpuFirst, odd}, "description": "Example GPU", "digest": "d1"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			f := fake()
			_, err := pciUpdate(t, f, tt.body)
			wantStatus(t, err, fiber.StatusBadRequest)
			if !strings.Contains(err.Error(), `unknown key "foo"`) || !strings.Contains(err.Error(), "leave it out of map") {
				t.Errorf("err = %v, want the reason and the way out", err)
			}
			f.requireCalls(t, "list:") // nothing read or written after the listing
		})
	}

	f := fake()
	if _, err := pciUpdate(t, f, map[string]any{"map": []any{gpuFirst}, "digest": "d1"}); err != nil {
		t.Fatalf("pciUpdate without the entry: %v", err)
	}
	if len(f.updated) != 1 || !slices.Equal(f.updated[0].Map, []string{gpuFirst}) {
		t.Errorf("updated = %+v, want the map without the entry", f.updated)
	}
}

func pciFlag(b bool) *bool { return &b }

func deref(b *bool) string {
	if b == nil {
		return "unchanged"
	}
	return fmt.Sprint(*b)
}

// The mdev flag must match every entry's device. While the mapping keeps other
// entries a device that does not match is refused, and the refusal says which side is
// which; as the only entry, the device sets the flag.
func TestUpdatePCIMappingRequest_TheMdevRule(t *testing.T) {
	for _, tt := range []struct {
		name string
		mdev bool
		node string
		path string
		want string
	}{
		{"an mdev device beside entries that are not", false, "pve-02", "0000:03:00.0",
			"can provide mediated devices, but mapping gpu01 is not set to use them"},
		{"a plain device beside mdev entries", true, "pve-01", "0000:09:00.0",
			"cannot provide mediated devices, but mapping gpu01 is set to use them"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			f := mappingGPUFake(tt.mdev)
			_, err := pciUpdate(t, f, map[string]any{"map": []any{gpuOther}, "add_node": tt.node, "add_path": tt.path, "digest": "d1"})
			wantStatus(t, err, fiber.StatusBadRequest)
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("err = %v, want it to say which side is which", err)
			}
			if len(f.updated) != 0 {
				t.Errorf("wrote %+v", f.updated)
			}
		})
	}
	for _, tt := range []struct {
		name       string
		flag       bool
		node, path string
		replace    string
		keep       []any
		want       *bool
	}{
		{"the only entry replaced by an mdev device sets the flag", false, "pve-02", "0000:03:00.0", gpuOther, []any{gpuOther}, pciFlag(true)},
		{"the only entry replaced by a plain device clears it", true, "pve-01", "0000:09:00.0", gpuFirst, []any{gpuFirst}, pciFlag(false)},
		{"a device that matches leaves it alone", false, "pve-01", "0000:09:00.0", "", []any{gpuFirst}, nil},
	} {
		t.Run(tt.name, func(t *testing.T) {
			f := &mappingFakeClient{devices: pciTestDevices, pciListing: []proxmox.PCIMapping{{ID: "gpu01", Digest: "d1", MDev: proxmox.FlexBool(tt.flag),
				Map: []string{gpuFirst, gpuOther}}}}
			body := map[string]any{"map": tt.keep, "add_node": tt.node, "add_path": tt.path, "digest": "d1"}
			if tt.replace != "" {
				body["replace"] = tt.replace
			}
			if _, err := pciUpdate(t, f, body); err != nil {
				t.Fatalf("pciUpdate: %v", err)
			}
			if got := f.updated[0].MDev; !reflect.DeepEqual(got, tt.want) {
				t.Errorf("mdev = %v, want %v", deref(got), deref(tt.want))
			}
		})
	}
}

func TestUpdatePCIMappingRequest_DeviceReadFailures(t *testing.T) {
	add := map[string]any{"map": []any{gpuFirst}, "add_node": "pve-01", "add_path": "0000:09:00.0", "digest": "d1"}
	t.Run("a device the node does not report", func(t *testing.T) {
		_, err := pciUpdate(t, mappingGPUFake(false), map[string]any{"map": []any{gpuFirst}, "add_node": "pve-01", "add_path": "0000:0b:00.0", "digest": "d1"})
		wantStatus(t, err, fiber.StatusBadRequest)
	})
	t.Run("the read failing", func(t *testing.T) {
		f := mappingGPUFake(false)
		f.devicesErr = proxmox.ErrConnectionFailed
		_, err := pciUpdate(t, f, add)
		wantStatus(t, err, fiber.StatusBadGateway)
	})
	t.Run("the read held to its own deadline", func(t *testing.T) {
		old := pciMappingDeviceTimeout
		pciMappingDeviceTimeout = 100 * time.Millisecond
		t.Cleanup(func() { pciMappingDeviceTimeout = old })
		f := mappingGPUFake(false)
		f.hangDevices = true
		start := time.Now()
		_, err := pciUpdate(t, f, add)
		wantStatus(t, err, fiber.StatusBadGateway)
		if elapsed := time.Since(start); elapsed > 3*time.Second {
			t.Errorf("the read took %s; its deadline is %s", elapsed, pciMappingDeviceTimeout)
		}
	})
}

func TestUpdatePCIMappingRequest_Description(t *testing.T) {
	for _, tt := range []struct {
		name string
		desc any
		want *string
	}{
		{"left alone", nil, nil},
		{"removed", "", strPtr("")},
		{"replaced", "Example GPU", strPtr("Example GPU")},
	} {
		t.Run(tt.name, func(t *testing.T) {
			f := mappingGPUFake(false)
			body := map[string]any{"map": []any{gpuFirst, gpuSecond, gpuOther}, "digest": "d1"}
			if tt.desc != nil {
				body["description"] = tt.desc
			}
			if _, err := pciUpdate(t, f, body); err != nil {
				t.Fatalf("pciUpdate: %v", err)
			}
			if got := f.updated[0].Description; !reflect.DeepEqual(got, tt.want) {
				t.Errorf("description = %v, want %v", got, tt.want)
			}
		})
	}
}

// The update's audit row: every entry as written, what was added with the ids the
// node reported, what it replaced or removed, a flag change and the description.
func TestPCIMappingUpdateDetails(t *testing.T) {
	group := 30
	yes := true
	upd := pciMappingUpdate{
		id:       "gpu01",
		params:   proxmox.UpdatePCIMappingParams{MDev: &yes, Description: strPtr("")},
		added:    &proxmox.PCIMapEntry{Node: "pve-01", Path: "0000:09:00.0", ID: "1234:0009", IOMMUGroup: &group},
		replaced: gpuSecond,
		removed:  []string{gpuOther},
	}
	got := string(pciMappingUpdateDetails(upd, []string{gpuFirst, "id=1234:0009,iommugroup=30,node=pve-01,path=0000:09:00.0"}))
	want := `{"added":{"device_id":"1234:0009","iommugroup":30,"node":"pve-01","path":"0000:09:00.0"},` +
		`"description_removed":true,"map":["` + gpuFirst + `","id=1234:0009,iommugroup=30,node=pve-01,path=0000:09:00.0"],` +
		`"map_count":2,"mdev":true,"removed":["` + gpuOther + `"],"removed_count":1,"replaced":"` + gpuSecond + `"}`
	if got != want {
		t.Errorf("details =\n%s\nwant\n%s", got, want)
	}
}

func TestPCIMappingDeleteDetails(t *testing.T) {
	snapshot := &proxmox.PCIMapping{ID: "gpu01", Description: "Example GPU", MDev: true, LiveMigrationCapable: true, Map: []string{gpuFirst}}
	if got, want := string(pciMappingDeleteDetails(snapshot, false)),
		`{"description":"Example GPU","live_migration_capable":true,"map":["`+gpuFirst+`"],"map_count":1,"mdev":true}`; got != want {
		t.Errorf("details = %s, want %s", got, want)
	}
	if got := string(pciMappingDeleteDetails(nil, true)); got != `{"prior_state_unknown":true}` {
		t.Errorf("details = %s", got)
	}
}

// An entry over the cap has only its description cut, and stays a PCI entry.
func TestPCIMappingAuditEntriesAreCapped(t *testing.T) {
	long := "description=" + strings.Repeat("é", 600) + ",id=1234:5678,node=pve-01,path=0000:01:00.0"
	got := pciMappingAuditEntries([]string{long, gpuFirst})
	e, err := proxmox.ParsePCIMapEntry(got[0])
	if err != nil || e.Node != "pve-01" || e.Path != "0000:01:00.0" {
		t.Fatalf("recorded %q (%v), want the entry kept whole but for its description", got[0], err)
	}
	if n := utf8.RuneCountInString(e.Description); n == 0 || n > usbMappingAuditTextMax+1 {
		t.Errorf("the description is %d characters, want it cut to %d and the ellipsis", n, usbMappingAuditTextMax)
	}
	if got[1] != gpuFirst {
		t.Errorf("an entry that fits = %q, want it as it was", got[1])
	}
}

// The listing merges each node's check — under "checks" — and gives every mapping its
// flags and the entries Nexara cannot read, from the mapping it came from.
func TestListClusterPCIMappings(t *testing.T) {
	odd := "node=pve-02,path=0000:07:00.0,foo=bar"
	fail := proxmox.MappingCheck{Severity: "error", Message: "Invalid configuration: 'id' does not match for 'gpu01'"}
	f := &mappingFakeClient{
		nodes: onlineNodes("pve-01", "pve-02"),
		pciListing: []proxmox.PCIMapping{
			// The two flags set apart, so neither can pass for the other.
			{ID: "vgpu01", Digest: "d1", MDev: true, Map: []string{"id=1234:0003,iommugroup=9,node=pve-02,path=0000:03:00.0"}},
			{ID: "gpu01", Digest: "d1", LiveMigrationCapable: true, Map: []string{gpuFirst, odd}},
		},
		pciChecked: map[string][]proxmox.PCIMapping{
			"pve-01": {{ID: "vgpu01", Digest: "d1"}, {ID: "gpu01", Digest: "d1", Checks: []proxmox.MappingCheck{fail}}},
			"pve-02": {{ID: "vgpu01", Digest: "d1"}, {ID: "gpu01", Digest: "d1"}},
		},
	}
	got, err := listClusterPCIMappings(context.Background(), f)
	if err != nil {
		t.Fatalf("listClusterPCIMappings: %v", err)
	}
	if len(got) != 2 || got[0].ID != "vgpu01" || !got[0].MDev || got[0].LiveMigrationCapable || len(got[0].UnreadableEntries) != 0 {
		t.Fatalf("vgpu01 = %+v", got[0])
	}
	g := got[1]
	if g.ID != "gpu01" || g.MDev || !g.LiveMigrationCapable {
		t.Errorf("gpu01 = %+v", g)
	}
	if c := g.NodeChecks["pve-01"]; len(c) != 1 || c[0] != fail {
		t.Errorf("pve-01 checks = %+v, want the failure", c)
	}
	reason, ok := g.UnreadableEntries[odd]
	if !ok || !strings.Contains(reason, `unknown key "foo"`) || len(g.UnreadableEntries) != 1 {
		t.Errorf("unreadable = %+v, want the odd entry's reason", g.UnreadableEntries)
	}
	// The entry is the reason's key and in map already: the reason does not carry it a third time.
	if strings.Contains(reason, odd) {
		t.Errorf("reason %q repeats the entry", reason)
	}
	raw, _ := json.Marshal(g)
	for _, key := range []string{`"mdev":false`, `"live_migration_capable":true`, `"unreadable_entries":{`, `"node_checks":{`, `"digest":"d1"`} {
		if !strings.Contains(string(raw), key) {
			t.Errorf("JSON %s lacks %s", raw, key)
		}
	}
}

func TestPCIPathsOverlap(t *testing.T) {
	for _, tt := range []struct {
		a, b string
		want bool
	}{
		{"0000:01:00.0", "0000:01:00.0", true},
		{"0000:01:00", "0000:01:00.1", true},
		{"0000:01:00.1", "0000:01:00", true},
		{"0000:01:00", "0000:01:00", true},
		{"0000:01:00.0", "0000:01:00.1", false},
		{"0000:01:00.0", "0000:02:00.0", false},
		{"0000:02:00.0;0000:01:00.0", "0000:01:00", true},
		{"0000:02:00.0;0000:03:00.0", "0000:01:00", false},
	} {
		if got := pciPathsOverlap(tt.a, tt.b); got != tt.want {
			t.Errorf("pciPathsOverlap(%q, %q) = %v, want %v", tt.a, tt.b, got, tt.want)
		}
	}
}

// The usage scan reads hostpciN, and finds the users: a scan handed the kind where
// the id belongs would find none.
func TestScanPCIMappingUsage(t *testing.T) {
	f := &mappingFakeClient{
		nodes: onlineNodes("pve-01"),
		resources: []proxmox.ClusterResource{
			usageGuest(102, "pve-01", "linux02", "qemu"),
			usageGuest(101, "pve-01", "linux01", "qemu"),
		},
		configs: map[int]proxmox.VMConfig{
			101: {"hostpci3": "mapping=gpu01,pcie=1", "usb0": "mapping=gpu01"},
			102: {"usb0": "mapping=gpu01", "hostpci0": "mapping=gpu011"},
		},
	}
	usage, err := scanPCIMappingUsage(context.Background(), f, "gpu01")
	if err != nil {
		t.Fatalf("scanPCIMappingUsage: %v", err)
	}
	want := []mappingGuest{{VMID: 101, Name: "linux01", Node: "pve-01", Keys: []string{"hostpci3"}}}
	if !reflect.DeepEqual(usage.Users, want) || usage.Checked != 2 || usage.MappingID != "gpu01" {
		t.Errorf("usage = %+v, want users %+v", usage, want)
	}
}
