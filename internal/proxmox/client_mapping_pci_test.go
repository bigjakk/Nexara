package proxmox

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"
)

// The listing as pve-manager's PVE/API2/Cluster/Mapping/PCI.pm index builds it: the
// section's own keys, id and digest added, and "checks" — not USB's "errors" — only
// when check-node was given. mdev and live-migration-capable arrive as Proxmox writes
// a boolean, 1; the second mapping has neither, and no description.
const pciMappingListing = `{"data":[
	{"id":"gpu01","description":"Example GPU","digest":"0123456789abcdef","mdev":1,"live-migration-capable":1,
	 "map":["id=1234:5678,iommugroup=14,node=pve-01,path=0000:01:00.0,subsystem-id=abcd:ef01"],
	 "checks":[]},
	{"id":"nic01","digest":"0123456789abcdef",
	 "map":["id=1234:0002,node=pve-02,path=0000:02:00.0"],
	 "checks":[{"severity":"warning","message":"No mapping for node pve-01."}]}
]}`

func TestListPCIMappings_ChecksAgainstTheNode(t *testing.T) {
	srv, seen := newRecordingServer(t, pciMappingListing)
	c := newTestClient(t, srv.URL)

	got, err := c.ListPCIMappings(context.Background(), "pve-01")
	if err != nil {
		t.Fatalf("ListPCIMappings: %v", err)
	}
	if len(*seen) != 1 {
		t.Fatalf("made %d requests, want 1", len(*seen))
	}
	req := (*seen)[0]
	if req.method != http.MethodGet || req.path != pciMappingsPath {
		t.Errorf("request = %s %s, want GET %s", req.method, req.path, pciMappingsPath)
	}
	if want := (url.Values{"check-node": {"pve-01"}}); !reflect.DeepEqual(req.query, want) {
		t.Errorf("query = %v, want %v", req.query, want)
	}
	want := []PCIMapping{
		{
			ID:          "gpu01",
			Description: "Example GPU",
			Map:         []string{"id=1234:5678,iommugroup=14,node=pve-01,path=0000:01:00.0,subsystem-id=abcd:ef01"},
			Checks:      []MappingCheck{},
			MDev:        true,
			// Decoded from its hyphenated key, as Proxmox writes it.
			LiveMigrationCapable: true,
			Digest:               "0123456789abcdef",
		},
		{
			ID:     "nic01",
			Map:    []string{"id=1234:0002,node=pve-02,path=0000:02:00.0"},
			Checks: []MappingCheck{{Severity: "warning", Message: "No mapping for node pve-01."}},
			Digest: "0123456789abcdef",
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("mappings =\n%+v\nwant\n%+v", got, want)
	}
}

func TestListNodePCIDevicesAllClasses_EmptiesTheBlacklist(t *testing.T) {
	srv, seen := newRecordingServer(t, `{"data":[
		{"id":"0000:01:00.0","class":"0x030000","vendor":"0x1234","device":"0x5678","iommugroup":14,"mdev":1},
		{"id":"0000:00:01.0","class":"0x060400","vendor":"0x1234","device":"0x0001","iommugroup":-1}
	]}`)
	c := newTestClient(t, srv.URL)

	got, err := c.ListNodePCIDevicesAllClasses(context.Background(), "pve-01")
	if err != nil {
		t.Fatalf("ListNodePCIDevicesAllClasses: %v", err)
	}
	req := (*seen)[0]
	if req.path != "/api2/json/nodes/pve-01/hardware/pci" {
		t.Errorf("path = %s", req.path)
	}
	// Sent, and empty: absent, Proxmox applies its default "05;06;0b" and
	// leaves the bridge out.
	if v, sent := req.query["pci-class-blacklist"]; !sent || len(v) != 1 || v[0] != "" {
		t.Errorf("query = %v, want pci-class-blacklist sent empty", req.query)
	}
	if len(got) != 2 || !got[0].MDev || got[1].MDev || got[1].IOMMUGroup != -1 {
		t.Errorf("devices = %+v", got)
	}
}

func intPtr(n int) *int { return &n }

// pciListing is a node's device listing as lspci (pve-common
// src/PVE/SysFSTools.pm) reports it: ids "0x"-prefixed, -1 for no IOMMU
// group, and function 1 listed before function 0 of the same slot.
func pciListing() []NodePCIDevice {
	return []NodePCIDevice{
		{ID: "0000:01:00.1", Vendor: "0x1234", Device: "0x0011", SubsystemVendor: "0xabcd", SubsystemDevice: "0x0001", IOMMUGroup: 14},
		{ID: "0000:01:00.0", Vendor: "0x1234", Device: "0x5678", SubsystemVendor: "0xABCD", SubsystemDevice: "0xEF01", IOMMUGroup: 14},
		{ID: "0000:02:00.0", Vendor: "0x1234", Device: "0x0002", IOMMUGroup: -1},
		{ID: "0000:03:00.0", Vendor: "0x1234", Device: "0x0003", SubsystemVendor: "0xabcd", IOMMUGroup: 0},
		{ID: "0000:04:00.0", Vendor: "0x1234", Device: "0x0004", IOMMUGroup: 7, MDev: true},
	}
}

func TestPCIMapEntryForDevice(t *testing.T) {
	entry := func(path, id, subsystem string, group *int) PCIMapEntry {
		return PCIMapEntry{Node: "pve-01", Path: path, ID: id, SubsystemID: subsystem, IOMMUGroup: group}
	}
	for _, tt := range []struct {
		name, path string
		want       PCIMapEntry
		wantMDev   bool
	}{
		// The listing's "0x" is stripped and its hex lowercased: the entry's pattern
		// admits no prefix, and Proxmox compares with `ne`.
		{"one function", "0000:01:00.0", entry("0000:01:00.0", "1234:5678", "abcd:ef01", intPtr(14)), false},
		// The whole device is checked against function 0 — here listed after function 1, whose ids differ.
		{"the whole device takes function 0's ids", "0000:01:00", entry("0000:01:00", "1234:5678", "abcd:ef01", intPtr(14)), false},
		{"function 1 is its own device", "0000:01:00.1", entry("0000:01:00.1", "1234:0011", "abcd:0001", intPtr(14)), false},
		// -1 is no group, and no group means no iommugroup key: Proxmox refuses one the device does not have.
		{"no IOMMU group and no subsystem", "0000:02:00.0", entry("0000:02:00.0", "1234:0002", "", nil), false},
		// Group 0 is a group like any other; half a subsystem id is none, as pci_device_info reports it.
		{"group 0, and half a subsystem id", "0000:03:00.0", entry("0000:03:00.0", "1234:0003", "", intPtr(0)), false},
		{"a mediated-device capable device needs the mdev flag", "0000:04:00.0", entry("0000:04:00.0", "1234:0004", "", intPtr(7)), true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, mdev, err := PCIMapEntryForDevice("pve-01", tt.path, pciListing())
			if err != nil {
				t.Fatalf("PCIMapEntryForDevice: %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("entry = %s (%+v), want %s", got, got, tt.want)
			}
			if mdev != tt.wantMDev {
				t.Errorf("mdev = %v, want %v", mdev, tt.wantMDev)
			}
		})
	}
}

func TestPCIMapEntryForDevice_Refuses(t *testing.T) {
	long := strings.Repeat("0", 60) + ":01:00.0"
	odd := append(pciListing(),
		NodePCIDevice{ID: "0000:05:00.0", Vendor: "0x12345", Device: "0x0005"},
		NodePCIDevice{ID: "0000:06:00.0", Vendor: "0x1234", Device: "0x0006", SubsystemVendor: "0xabcd", SubsystemDevice: "0xzz01"},
		// Listed, so only the length can refuse it.
		NodePCIDevice{ID: long, Vendor: "0x1234", Device: "0x0007"},
	)
	for _, tt := range []struct {
		name, node, path string
		want             error
	}{
		{"no such device", "pve-01", "0000:09:00.0", ErrInvalidInput},
		{"no function 0 for the whole device", "pve-01", "0000:09:00", ErrInvalidInput},
		{"no domain", "pve-01", "01:00.0", ErrInvalidInput},
		{"uppercase hex", "pve-01", "0000:0A:00.0", ErrInvalidInput},
		{"a list of paths", "pve-01", "0000:01:00.0;0000:02:00.0", ErrInvalidInput},
		{"a path smuggling a key", "pve-01", "0000:01:00.0,node=pve-02", ErrInvalidInput},
		{"a path over 64 characters", "pve-01", long, ErrInvalidInput},
		{"a node smuggling a key", "pve-01,path=0000:02:00.0", "0000:01:00.0", ErrInvalidInput},
		// Proxmox's report, not the caller's input: a server-side error.
		{"a vendor id that is not four digits", "pve-01", "0000:05:00.0", ErrInvalidResponse},
		{"a subsystem id that is not hex", "pve-01", "0000:06:00.0", ErrInvalidResponse},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := PCIMapEntryForDevice(tt.node, tt.path, odd)
			if !errors.Is(err, tt.want) {
				t.Errorf("err = %v, want %v", err, tt.want)
			}
			if tt.want == ErrInvalidResponse && errors.Is(err, ErrInvalidInput) {
				t.Errorf("err = %v blames the caller for Proxmox's report", err)
			}
		})
	}

	// The part of a reported id that is quoted back is bounded.
	huge := []NodePCIDevice{{ID: "0000:07:00.0", Vendor: strings.Repeat("x", 4096), Device: "0x0007"}}
	if _, _, err := PCIMapEntryForDevice("pve-01", "0000:07:00.0", huge); err == nil || len(err.Error()) > 300 {
		t.Errorf("err = %d bytes, want a bounded error", len(fmt.Sprint(err)))
	}
}

func TestPCIMapEntry_String(t *testing.T) {
	for _, tt := range []struct {
		name  string
		entry PCIMapEntry
		want  string
	}{
		{"every key", PCIMapEntry{Node: "pve-01", Path: "0000:01:00.0", ID: "1234:5678", SubsystemID: "abcd:ef01", IOMMUGroup: intPtr(14), Description: "left slot"},
			"description=left slot,id=1234:5678,iommugroup=14,node=pve-01,path=0000:01:00.0,subsystem-id=abcd:ef01"},
		{"group 0 is written", PCIMapEntry{Node: "pve-01", Path: "0000:03:00", ID: "1234:0003", IOMMUGroup: intPtr(0)},
			"id=1234:0003,iommugroup=0,node=pve-01,path=0000:03:00"},
		{"the optional keys left out", PCIMapEntry{Node: "pve-01", Path: "0000:02:00.0", ID: "1234:0002"},
			"id=1234:0002,node=pve-01,path=0000:02:00.0"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.entry.String(); got != tt.want {
				t.Errorf("String() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestParsePCIMapEntry_TranscribesParsePropertyString holds the parse to
// pve-common's parse_property_string over PCI's $map_fmt, one row per branch,
// as Proxmox reads a STORED entry: a ";"-list of paths and a signed group are
// Proxmox's and are read, where a new entry of Nexara's has neither.
func TestParsePCIMapEntry_TranscribesParsePropertyString(t *testing.T) {
	group := func(n int) *int { return &n }
	accepts := []struct {
		name string
		raw  string
		want PCIMapEntry
	}{
		{"the keys in any order", "path=0000:01:00.0,node=pve-01,id=1234:5678",
			PCIMapEntry{Node: "pve-01", Path: "0000:01:00.0", ID: "1234:5678"}},
		{"every key", "description=Example GPU,id=1234:5678,iommugroup=14,node=pve-01,path=0000:01:00.0,subsystem-id=abcd:ef01",
			PCIMapEntry{Node: "pve-01", Path: "0000:01:00.0", ID: "1234:5678", SubsystemID: "abcd:ef01", IOMMUGroup: group(14), Description: "Example GPU"}},
		{"the whole device", "id=1234:5678,node=pve-01,path=0000:01:00",
			PCIMapEntry{Node: "pve-01", Path: "0000:01:00", ID: "1234:5678"}},
		// $map_fmt's path pattern admits a list, which Proxmox stores.
		{"a list of paths", "id=1234:5678,node=pve-01,path=0000:01:00.0;0000:02:00.0",
			PCIMapEntry{Node: "pve-01", Path: "0000:01:00.0;0000:02:00.0", ID: "1234:5678"}},
		// is_integer is ^[+-]?\d+$: a sign, a negative, leading zeros.
		{"a signed group", "id=1234:5678,iommugroup=+3,node=pve-01,path=0000:01:00.0",
			PCIMapEntry{Node: "pve-01", Path: "0000:01:00.0", ID: "1234:5678", IOMMUGroup: group(3)}},
		{"a negative group", "id=1234:5678,iommugroup=-1,node=pve-01,path=0000:01:00.0",
			PCIMapEntry{Node: "pve-01", Path: "0000:01:00.0", ID: "1234:5678", IOMMUGroup: group(-1)}},
		{"a group with leading zeros", "id=1234:5678,iommugroup=007,node=pve-01,path=0000:01:00.0",
			PCIMapEntry{Node: "pve-01", Path: "0000:01:00.0", ID: "1234:5678", IOMMUGroup: group(7)}},
		// A wide domain: the pattern's {4,} has no ceiling, and a stored
		// entry is read as Proxmox reads it, past Nexara's own 64.
		{"a domain wider than four digits", "id=1234:5678,node=pve-01,path=" + strings.Repeat("0", 70) + ":01:00.0",
			PCIMapEntry{Node: "pve-01", Path: strings.Repeat("0", 70) + ":01:00.0", ID: "1234:5678"}},
		// Uppercase ids are Proxmox's to store and are read, then written
		// back lowercase: assert_valid compares them with sysfs's lowercase.
		{"uppercase ids come back lowercase", "id=ABCD:EF01,node=pve-01,path=0000:01:00.0,subsystem-id=1A2B:3C4D",
			PCIMapEntry{Node: "pve-01", Path: "0000:01:00.0", ID: "abcd:ef01", SubsystemID: "1a2b:3c4d"}},
		// A part splits at its first "=", so a description may hold more.
		{"a description with an equals sign", "description=a=b,id=1234:5678,node=pve-01,path=0000:01:00.0",
			PCIMapEntry{Node: "pve-01", Path: "0000:01:00.0", ID: "1234:5678", Description: "a=b"}},
		// A part that is only white space is skipped, as a blank one is.
		{"a blank part", "id=1234:5678, ,node=pve-01,,path=0000:01:00.0",
			PCIMapEntry{Node: "pve-01", Path: "0000:01:00.0", ID: "1234:5678"}},
		{"a description of 4096 characters", "description=" + strings.Repeat("é", 4096) + ",id=1234:5678,node=pve-01,path=0000:01:00.0",
			PCIMapEntry{Node: "pve-01", Path: "0000:01:00.0", ID: "1234:5678", Description: strings.Repeat("é", 4096)}},
	}
	for _, tt := range accepts {
		t.Run("accepts "+tt.name, func(t *testing.T) {
			got, err := ParsePCIMapEntry(tt.raw)
			if err != nil {
				t.Fatalf("ParsePCIMapEntry(%q): %v", tt.raw, err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("= %+v, want %+v", got, tt.want)
			}
		})
	}

	refuses := []struct{ name, raw string }{
		{"no node", "id=1234:5678,path=0000:01:00.0"},
		{"no path", "id=1234:5678,node=pve-01"},
		{"no id", "node=pve-01,path=0000:01:00.0"},
		{"an unknown key", "id=1234:5678,node=pve-01,path=0000:01:00.0,mdev=1"},
		{"a key twice", "id=1234:5678,node=pve-01,path=0000:01:00.0,node=pve-02"},
		{"a value without a key", "id=1234:5678,node=pve-01,path=0000:01:00.0,extra"},
		{"a key without a value", "id=1234:5678,node=pve-01,path=0000:01:00.0,description="},
		{"a value without a key name", "id=1234:5678,node=pve-01,path=0000:01:00.0,=x"},
		// Nothing is trimmed: " node" is an unknown key.
		{"a key with a space", "id=1234:5678, node=pve-01,path=0000:01:00.0"},
		{"a node that is not a node name", "id=1234:5678,node=pve 01,path=0000:01:00.0"},
		{"an id with 0x", "id=0x1234:0x5678,node=pve-01,path=0000:01:00.0"},
		{"a subsystem id that is not one", "id=1234:5678,node=pve-01,path=0000:01:00.0,subsystem-id=12345678"},
		{"a path without the domain", "id=1234:5678,node=pve-01,path=01:00.0"},
		{"a path in uppercase", "id=1234:5678,node=pve-01,path=0000:0A:00.0"},
		{"a list ending in ;", "id=1234:5678,node=pve-01,path=0000:01:00.0;"},
		{"a group that is not a number", "id=1234:5678,iommugroup=x,node=pve-01,path=0000:01:00.0"},
		{"a group with a fraction", "id=1234:5678,iommugroup=1.5,node=pve-01,path=0000:01:00.0"},
		// Nexara's own: ASCII digits only, and a group that fits an int.
		{"a group in another script's digits", "id=1234:5678,iommugroup=٣,node=pve-01,path=0000:01:00.0"},
		{"a group too large", "id=1234:5678,iommugroup=99999999999999999999,node=pve-01,path=0000:01:00.0"},
		{"a line feed", "id=1234:5678,node=pve-01,path=0000:01:00.0\n"},
		{"a carriage return in a description", "description=a\rb,id=1234:5678,node=pve-01,path=0000:01:00.0"},
		{"a description over 4096 characters", "description=" + strings.Repeat("é", 4097) + ",id=1234:5678,node=pve-01,path=0000:01:00.0"},
	}
	for _, tt := range refuses {
		t.Run("refuses "+tt.name, func(t *testing.T) {
			if _, err := ParsePCIMapEntry(tt.raw); !errors.Is(err, ErrInvalidInput) {
				t.Errorf("ParsePCIMapEntry(%q) = %v, want ErrInvalidInput", tt.raw, err)
			}
		})
	}
}

// A stored entry is written back as Proxmox would read it the same: the
// round trip through String and the parse changes nothing but the spelling —
// ids lowercased, the group's sign and zeros dropped, the keys in Nexara's
// order.
func TestParsePCIMapEntry_RoundTrip(t *testing.T) {
	for raw, want := range map[string]string{
		"path=0000:01:00.0;0000:02:00.0,node=pve-01,iommugroup=+07,id=ABCD:EF01,description=left slot": "description=left slot,id=abcd:ef01,iommugroup=7,node=pve-01,path=0000:01:00.0;0000:02:00.0",
		"node=pve-01,path=0000:01:00,id=1234:5678,iommugroup=-1,subsystem-id=1A2B:3C4D":                "id=1234:5678,iommugroup=-1,node=pve-01,path=0000:01:00,subsystem-id=1a2b:3c4d",
	} {
		e, err := ParsePCIMapEntry(raw)
		if err != nil {
			t.Fatalf("ParsePCIMapEntry(%q): %v", raw, err)
		}
		if got := e.String(); got != want {
			t.Errorf("%q written back as %q, want %q", raw, got, want)
		}
		again, err := ParsePCIMapEntry(e.String())
		if err != nil || !reflect.DeepEqual(again, e) {
			t.Errorf("%q does not read back as it was written: %+v, %v", e.String(), again, err)
		}
	}
}

func TestCanonicalPCIMap(t *testing.T) {
	got, err := CanonicalPCIMap([]string{
		"path=0000:02:00.0,node=pve-01,id=1234:0002",
		// Several entries for one node are alternatives, kept in order.
		"node=pve-01,path=0000:01:00.0,id=ABCD:EF01",
	})
	if err != nil {
		t.Fatalf("CanonicalPCIMap: %v", err)
	}
	want := []string{"id=1234:0002,node=pve-01,path=0000:02:00.0", "id=abcd:ef01,node=pve-01,path=0000:01:00.0"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("= %q, want %q", got, want)
	}
	for _, bad := range [][]string{nil, {}, {"id=1234:0002,node=pve-01,path=0000:02:00.0", "node=pve-01"}} {
		if _, err := CanonicalPCIMap(bad); !errors.Is(err, ErrInvalidInput) {
			t.Errorf("CanonicalPCIMap(%q) = %v, want ErrInvalidInput", bad, err)
		}
	}
}

func TestVMConfigPCIMappingKeys(t *testing.T) {
	config := VMConfig{
		"hostpci10": "mapping=gpu01,pcie=1",
		"hostpci2":  "pcie=1,mapping=gpu01",
		"hostpci0":  "0000:01:00.0,pcie=1",
		"hostpci1":  "mapping=gpu011",
		"hostpci3":  "mapping=GPU01",
		// A usbN naming the id is another kind's.
		"usb0":        "mapping=gpu01",
		"hostpcifoo":  "mapping=gpu01",
		"hostpci4":    42,
		"description": "mapping=gpu01",
	}
	if got, want := config.PCIMappingKeys("gpu01"), []string{"hostpci2", "hostpci10"}; !reflect.DeepEqual(got, want) {
		t.Errorf("PCIMappingKeys = %v, want %v", got, want)
	}
}

// PCIMapEntryProblem says what ParsePCIMapEntry refuses an entry for without
// repeating the entry, a part of it cut to 40 characters, and ParsePCIMapEntry
// says the same after the entry. An entry that reads has no problem.
func TestPCIMapEntryProblem(t *testing.T) {
	long := strings.Repeat("x", 500)
	for _, tt := range []struct {
		raw, want string
	}{
		{"id=1234:5678,node=pve-01,path=0000:01:00.0", ""},
		{"id=1234:5678,node=pve-01,path=0000:01:00.0,foo=bar",
			`The entry has an unknown key "foo"; the keys are node, path, id, subsystem-id, iommugroup and description.`},
		{"id=1234:5678,path=0000:01:00.0", "The entry needs node=<a Proxmox node name>."},
		{"id=1234:5678,node=pve-01,path=0000:01:00.0," + long, `The entry has a value without a key ("` + long[:40] + `").`},
	} {
		if got := PCIMapEntryProblem(tt.raw); got != tt.want {
			t.Errorf("PCIMapEntryProblem(%.60q) = %q, want %q", tt.raw, got, tt.want)
		}
		_, err := ParsePCIMapEntry(tt.raw)
		if tt.want == "" {
			if err != nil {
				t.Errorf("ParsePCIMapEntry(%.60q): %v", tt.raw, err)
			}
			continue
		}
		phrase := strings.TrimSuffix(strings.TrimPrefix(tt.want, "The entry "), ".")
		if err == nil || !strings.HasSuffix(err.Error(), fmt.Sprintf("PCI mapping entry %q %s", tt.raw, phrase)) {
			t.Errorf("ParsePCIMapEntry(%.60q) = %v, want the entry and then %q", tt.raw, err, phrase)
		}
	}
}
