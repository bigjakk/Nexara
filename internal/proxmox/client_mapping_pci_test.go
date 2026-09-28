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

// The listing in the shape pve-manager's PVE/API2/Cluster/Mapping/PCI.pm index
// builds it: the section's own keys, id and digest added, and "checks" — not
// USB's "errors" — only when check-node was given. mdev and
// live-migration-capable arrive as Proxmox writes a boolean, 1; the second
// mapping has neither, and no description.
const pciMappingListing = `{"data":[
	{"id":"gpu01","description":"Example GPU","digest":"0123456789abcdef","mdev":1,"live-migration-capable":1,
	 "map":["id=1234:5678,iommugroup=14,node=pve-01,path=0000:01:00.0,subsystem-id=abcd:ef01"],
	 "checks":[]},
	{"id":"nic01","digest":"0123456789abcdef",
	 "map":["id=1234:0002,node=pve-02,path=0000:02:00.0"],
	 "checks":[{"severity":"warning","message":"No mapping for node pve-01."}]}
]}`

func TestListPCIMappings_ChecksAgainstTheNode(t *testing.T) {
	srv, seen := newMappingCaptureServer(t, pciMappingListing)
	c := newTestClient(t, srv.URL)

	got, err := c.ListPCIMappings(context.Background(), "pve-01")
	if err != nil {
		t.Fatalf("ListPCIMappings: %v", err)
	}
	if len(*seen) != 1 {
		t.Fatalf("made %d requests, want 1", len(*seen))
	}
	req := (*seen)[0]
	if req.method != http.MethodGet || req.path != "/api2/json/cluster/mapping/pci" {
		t.Errorf("request = %s %s, want GET /api2/json/cluster/mapping/pci", req.method, req.path)
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

func TestListPCIMappings_WithoutANodeAsksForNoCheck(t *testing.T) {
	// Without check-node Proxmox sends no checks at all, and a mapping with no
	// entries has no map: both come back as empty lists, not null.
	srv, seen := newMappingCaptureServer(t, `{"data":[{"id":"gpu01","digest":"d"}]}`)
	c := newTestClient(t, srv.URL)

	got, err := c.ListPCIMappings(context.Background(), "")
	if err != nil {
		t.Fatalf("ListPCIMappings: %v", err)
	}
	if q := (*seen)[0].query; len(q) != 0 {
		t.Errorf("query = %v, want none", q)
	}
	if len(got) != 1 || got[0].Map == nil || got[0].Checks == nil {
		t.Errorf("mappings = %+v, want one with an empty map and no checks, both non-nil", got)
	}
}

func TestListPCIMappings_RefusesANonNodeName(t *testing.T) {
	srv, seen := newMappingCaptureServer(t, pciMappingListing)
	c := newTestClient(t, srv.URL)

	if _, err := c.ListPCIMappings(context.Background(), "pve-01,x"); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("err = %v, want ErrInvalidInput", err)
	}
	if len(*seen) != 0 {
		t.Errorf("made %d requests for a refused node name", len(*seen))
	}
}

// pveproxy chooses where to proxy a /nodes/{node} call from the name before it
// validates it, so a name that is no node — an IP, a hostname — is refused
// before anything is sent.
func TestListNodePCIDevicesAllClasses_RefusesANonNodeName(t *testing.T) {
	for _, node := range []string{"192.0.2.10", "host.example.com", "pve-01,x", ""} {
		srv, seen := newMappingCaptureServer(t, `{"data":[]}`)
		c := newTestClient(t, srv.URL)
		if _, err := c.ListNodePCIDevicesAllClasses(context.Background(), node); !errors.Is(err, ErrInvalidInput) {
			t.Errorf("%q: err = %v, want ErrInvalidInput", node, err)
		}
		if len(*seen) != 0 {
			t.Errorf("%q: made %d requests", node, len(*seen))
		}
	}
}

func TestListNodePCIDevicesAllClasses_EmptiesTheBlacklist(t *testing.T) {
	srv, seen := newMappingCaptureServer(t, `{"data":[
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
	for _, tt := range []struct {
		name, path string
		want       PCIMapEntry
		wantMDev   bool
	}{
		{
			// The listing's "0x" is stripped and its hex lowercased: the entry's
			// pattern admits no prefix, and Proxmox compares with `ne`.
			name: "one function",
			path: "0000:01:00.0",
			want: PCIMapEntry{Node: "pve-01", Path: "0000:01:00.0", ID: "1234:5678", SubsystemID: "abcd:ef01", IOMMUGroup: intPtr(14)},
		},
		{
			// The whole device is checked against function 0 — here listed
			// after function 1, whose ids differ.
			name: "the whole device takes function 0's ids",
			path: "0000:01:00",
			want: PCIMapEntry{Node: "pve-01", Path: "0000:01:00", ID: "1234:5678", SubsystemID: "abcd:ef01", IOMMUGroup: intPtr(14)},
		},
		{
			name: "function 1 is its own device",
			path: "0000:01:00.1",
			want: PCIMapEntry{Node: "pve-01", Path: "0000:01:00.1", ID: "1234:0011", SubsystemID: "abcd:0001", IOMMUGroup: intPtr(14)},
		},
		{
			// -1 is no group, and no group means no iommugroup key: Proxmox
			// refuses one the device does not have.
			name: "no IOMMU group and no subsystem",
			path: "0000:02:00.0",
			want: PCIMapEntry{Node: "pve-01", Path: "0000:02:00.0", ID: "1234:0002"},
		},
		{
			// Group 0 is a group like any other; half a subsystem id is
			// none, as pci_device_info reports it.
			name: "group 0, and half a subsystem id",
			path: "0000:03:00.0",
			want: PCIMapEntry{Node: "pve-01", Path: "0000:03:00.0", ID: "1234:0003", IOMMUGroup: intPtr(0)},
		},
		{
			name:     "a mediated-device capable device needs the mdev flag",
			path:     "0000:04:00.0",
			want:     PCIMapEntry{Node: "pve-01", Path: "0000:04:00.0", ID: "1234:0004", IOMMUGroup: intPtr(7)},
			wantMDev: true,
		},
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
		{
			name:  "every key",
			entry: PCIMapEntry{Node: "pve-01", Path: "0000:01:00.0", ID: "1234:5678", SubsystemID: "abcd:ef01", IOMMUGroup: intPtr(14), Description: "left slot"},
			want:  "description=left slot,id=1234:5678,iommugroup=14,node=pve-01,path=0000:01:00.0,subsystem-id=abcd:ef01",
		},
		{
			name:  "group 0 is written",
			entry: PCIMapEntry{Node: "pve-01", Path: "0000:03:00", ID: "1234:0003", IOMMUGroup: intPtr(0)},
			want:  "id=1234:0003,iommugroup=0,node=pve-01,path=0000:03:00",
		},
		{
			name:  "the optional keys left out",
			entry: PCIMapEntry{Node: "pve-01", Path: "0000:02:00.0", ID: "1234:0002"},
			want:  "id=1234:0002,node=pve-01,path=0000:02:00.0",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.entry.String(); got != tt.want {
				t.Errorf("String() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCreatePCIMapping_SendsOneEntry(t *testing.T) {
	for _, tt := range []struct {
		name   string
		params CreatePCIMappingParams
		want   url.Values
	}{
		{
			// Ids are written lowercase whatever the caller passed.
			name: "every value",
			params: CreatePCIMappingParams{
				ID:          "gpu01",
				Description: "Example GPU",
				Entry:       PCIMapEntry{Node: "pve-01", Path: "0000:01:00.0", ID: "1234:ABCD", SubsystemID: "ABCD:EF01", IOMMUGroup: intPtr(14)},
				MDev:        true,
			},
			want: url.Values{
				"id":          {"gpu01"},
				"map":         {"id=1234:abcd,iommugroup=14,node=pve-01,path=0000:01:00.0,subsystem-id=abcd:ef01"},
				"description": {"Example GPU"},
				"mdev":        {"1"},
			},
		},
		{
			// No description and no mdev key at all: Proxmox's defaults.
			name: "only what is required",
			params: CreatePCIMappingParams{
				ID:    "nic01",
				Entry: PCIMapEntry{Node: "pve-02", Path: "0000:02:00", ID: "1234:0002"},
			},
			want: url.Values{
				"id":  {"nic01"},
				"map": {"id=1234:0002,node=pve-02,path=0000:02:00"},
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			srv, seen := newMappingCaptureServer(t, `{"data":null}`)
			c := newTestClient(t, srv.URL)
			if err := c.CreatePCIMapping(context.Background(), tt.params); err != nil {
				t.Fatalf("CreatePCIMapping: %v", err)
			}
			if len(*seen) != 1 {
				t.Fatalf("made %d requests, want 1", len(*seen))
			}
			req := (*seen)[0]
			if req.method != http.MethodPost || req.path != "/api2/json/cluster/mapping/pci" {
				t.Errorf("request = %s %s, want POST /api2/json/cluster/mapping/pci", req.method, req.path)
			}
			if !reflect.DeepEqual(req.form, tt.want) {
				t.Errorf("form = %v, want %v", req.form, tt.want)
			}
		})
	}
}

func TestCreatePCIMapping_RefusesBeforeSending(t *testing.T) {
	good := PCIMapEntry{Node: "pve-01", Path: "0000:01:00.0", ID: "1234:5678"}
	with := func(edit func(*CreatePCIMappingParams)) CreatePCIMappingParams {
		p := CreatePCIMappingParams{ID: "gpu01", Entry: good}
		edit(&p)
		return p
	}
	for _, tt := range []struct {
		name   string
		params CreatePCIMappingParams
	}{
		{"an id starting with a digit", with(func(p *CreatePCIMappingParams) { p.ID = "1gpu" })},
		{"a one-character id", with(func(p *CreatePCIMappingParams) { p.ID = "g" })},
		{"a description with a line feed", with(func(p *CreatePCIMappingParams) { p.Description = "a\nb" })},
		{"a description over 4096 characters", with(func(p *CreatePCIMappingParams) { p.Description = strings.Repeat("é", 4097) })},
		{"a node smuggling a key", with(func(p *CreatePCIMappingParams) { p.Entry.Node = "pve-01,path=0000:02:00.0" })},
		{"no path", with(func(p *CreatePCIMappingParams) { p.Entry.Path = "" })},
		{"a list of paths", with(func(p *CreatePCIMappingParams) { p.Entry.Path = "0000:01:00.0;0000:01:00.1" })},
		{"a path over 64 characters", with(func(p *CreatePCIMappingParams) { p.Entry.Path = strings.Repeat("0", 60) + ":01:00.0" })},
		{"no device id", with(func(p *CreatePCIMappingParams) { p.Entry.ID = "" })},
		{"a device id with 0x", with(func(p *CreatePCIMappingParams) { p.Entry.ID = "0x1234:0x5678" })},
		{"a subsystem id smuggling a key", with(func(p *CreatePCIMappingParams) { p.Entry.SubsystemID = "abcd:ef01,node=pve-02" })},
		{"a negative IOMMU group", with(func(p *CreatePCIMappingParams) { p.Entry.IOMMUGroup = intPtr(-1) })},
		{"an entry description with a comma", with(func(p *CreatePCIMappingParams) { p.Entry.Description = "left,path=0000:02:00.0" })},
		{"an entry description with a carriage return", with(func(p *CreatePCIMappingParams) { p.Entry.Description = "left\r" })},
		{"an entry description over 4096 characters", with(func(p *CreatePCIMappingParams) { p.Entry.Description = strings.Repeat("d", 4097) })},
	} {
		t.Run(tt.name, func(t *testing.T) {
			srv, seen := newMappingCaptureServer(t, `{"data":null}`)
			c := newTestClient(t, srv.URL)
			if err := c.CreatePCIMapping(context.Background(), tt.params); !errors.Is(err, ErrInvalidInput) {
				t.Errorf("err = %v, want ErrInvalidInput", err)
			}
			if len(*seen) != 0 {
				t.Errorf("made %d requests for a refused mapping", len(*seen))
			}
		})
	}
}

// The boundaries Proxmox accepts are accepted here too.
func TestCreatePCIMapping_AcceptsProxmoxsBoundaries(t *testing.T) {
	for _, tt := range []struct {
		name   string
		params CreatePCIMappingParams
	}{
		{"a two-character id", CreatePCIMappingParams{ID: "g1", Entry: PCIMapEntry{Node: "pve-01", Path: "0000:01:00.0", ID: "1234:5678"}}},
		{"a description of 4096 characters", CreatePCIMappingParams{ID: "gpu01", Description: strings.Repeat("é", 4096), Entry: PCIMapEntry{Node: "pve-01", Path: "0000:01:00.0", ID: "1234:5678"}}},
		{"a domain wider than four digits", CreatePCIMappingParams{ID: "gpu01", Entry: PCIMapEntry{Node: "pve-01", Path: "10000:01:00.0", ID: "1234:5678"}}},
		{"a path of 64 characters", CreatePCIMappingParams{ID: "gpu01", Entry: PCIMapEntry{Node: "pve-01", Path: strings.Repeat("0", 56) + ":01:00.0", ID: "1234:5678"}}},
		{"IOMMU group 0", CreatePCIMappingParams{ID: "gpu01", Entry: PCIMapEntry{Node: "pve-01", Path: "0000:01:00.0", ID: "1234:5678", IOMMUGroup: intPtr(0)}}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			srv, seen := newMappingCaptureServer(t, `{"data":null}`)
			c := newTestClient(t, srv.URL)
			if err := c.CreatePCIMapping(context.Background(), tt.params); err != nil {
				t.Errorf("CreatePCIMapping: %v", err)
			}
			if len(*seen) != 1 {
				t.Errorf("made %d requests, want 1", len(*seen))
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

func TestUpdatePCIMapping_SendsTheWholeMap(t *testing.T) {
	yes, no := true, false
	tests := []struct {
		name   string
		params UpdatePCIMappingParams
		want   url.Values
	}{
		{
			name: "the entries rewritten, nothing else touched",
			params: UpdatePCIMappingParams{
				Map:    []string{"path=0000:01:00.0,node=pve-01,id=1234:5678", "node=pve-02,path=0000:02:00.0,id=1234:5678,iommugroup=+3"},
				Digest: "0123456789abcdef",
			},
			want: url.Values{
				"map":    {"id=1234:5678,node=pve-01,path=0000:01:00.0", "id=1234:5678,iommugroup=3,node=pve-02,path=0000:02:00.0"},
				"digest": {"0123456789abcdef"},
			},
		},
		{
			name:   "a new description, and the flag set",
			params: UpdatePCIMappingParams{Map: []string{"id=1234:5678,node=pve-01,path=0000:01:00.0"}, Description: strPtr("Example GPU"), MDev: &yes, Digest: "0123456789abcdef"},
			want: url.Values{
				"map":         {"id=1234:5678,node=pve-01,path=0000:01:00.0"},
				"description": {"Example GPU"},
				"mdev":        {"1"},
				"digest":      {"0123456789abcdef"},
			},
		},
		{
			// Both removals in ONE delete, which Proxmox reads as a list.
			name:   "the description and the flag removed",
			params: UpdatePCIMappingParams{Map: []string{"id=1234:5678,node=pve-01,path=0000:01:00.0"}, Description: strPtr(""), MDev: &no, Digest: "0123456789abcdef"},
			want: url.Values{
				"map":    {"id=1234:5678,node=pve-01,path=0000:01:00.0"},
				"delete": {"description,mdev"},
				"digest": {"0123456789abcdef"},
			},
		},
		{
			name:   "only the flag removed",
			params: UpdatePCIMappingParams{Map: []string{"id=1234:5678,node=pve-01,path=0000:01:00.0"}, MDev: &no, Digest: "0123456789abcdef"},
			want: url.Values{
				"map":    {"id=1234:5678,node=pve-01,path=0000:01:00.0"},
				"delete": {"mdev"},
				"digest": {"0123456789abcdef"},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv, seen := newMappingCaptureServer(t, `{"data":null}`)
			c := newTestClient(t, srv.URL)
			if err := c.UpdatePCIMapping(context.Background(), "gpu01", tt.params); err != nil {
				t.Fatalf("UpdatePCIMapping: %v", err)
			}
			if len(*seen) != 1 {
				t.Fatalf("made %d requests, want 1", len(*seen))
			}
			req := (*seen)[0]
			if req.method != http.MethodPut || req.path != "/api2/json/cluster/mapping/pci/gpu01" {
				t.Errorf("request = %s %s, want PUT /api2/json/cluster/mapping/pci/gpu01", req.method, req.path)
			}
			if !reflect.DeepEqual(req.form, tt.want) {
				t.Errorf("form = %v, want %v", req.form, tt.want)
			}
			// Never a key both set and removed.
			for _, removed := range strings.Split(req.form.Get("delete"), ",") {
				if removed != "" && req.form.Has(removed) {
					t.Errorf("%q is both set and deleted", removed)
				}
			}
		})
	}
}

func TestUpdatePCIMapping_RefusesBeforeSending(t *testing.T) {
	valid := UpdatePCIMappingParams{Map: []string{"id=1234:5678,node=pve-01,path=0000:01:00.0"}, Digest: "0123456789abcdef"}
	for _, tt := range []struct {
		name   string
		id     string
		mutate func(*UpdatePCIMappingParams)
	}{
		{"an id that is a traversal", "..", func(*UpdatePCIMappingParams) {}},
		{"an id with a slash", "pci/../x", func(*UpdatePCIMappingParams) {}},
		{"no digest", "gpu01", func(p *UpdatePCIMappingParams) { p.Digest = "" }},
		{"a digest of 0", "gpu01", func(p *UpdatePCIMappingParams) { p.Digest = "0" }},
		{"a digest over 64 characters", "gpu01", func(p *UpdatePCIMappingParams) { p.Digest = strings.Repeat("a", 65) }},
		{"an empty map", "gpu01", func(p *UpdatePCIMappingParams) { p.Map = nil }},
		{"an entry Proxmox cannot read", "gpu01", func(p *UpdatePCIMappingParams) {
			p.Map = []string{"id=1234:5678,node=pve-01,path=0000:01:00.0", "node=pve-02,path=0000:02:00.0"}
		}},
		{"a description with a line feed", "gpu01", func(p *UpdatePCIMappingParams) { p.Description = strPtr("a\nb") }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			srv, seen := newMappingCaptureServer(t, `{"data":null}`)
			c := newTestClient(t, srv.URL)
			params := valid
			tt.mutate(&params)
			if err := c.UpdatePCIMapping(context.Background(), tt.id, params); !errors.Is(err, ErrInvalidInput) {
				t.Errorf("err = %v, want ErrInvalidInput", err)
			}
			if len(*seen) != 0 {
				t.Errorf("made %d requests, want none", len(*seen))
			}
		})
	}
}

func TestDeletePCIMapping(t *testing.T) {
	srv, seen := newMappingCaptureServer(t, `{"data":null}`)
	c := newTestClient(t, srv.URL)
	if err := c.DeletePCIMapping(context.Background(), "gpu01"); err != nil {
		t.Fatalf("DeletePCIMapping: %v", err)
	}
	if len(*seen) != 1 || (*seen)[0].method != http.MethodDelete || (*seen)[0].path != "/api2/json/cluster/mapping/pci/gpu01" {
		t.Errorf("requests = %+v, want DELETE /api2/json/cluster/mapping/pci/gpu01", *seen)
	}
	for _, id := range []string{"", "..", "gpu/../x", "gpu%2e%2e"} {
		if err := c.DeletePCIMapping(context.Background(), id); !errors.Is(err, ErrInvalidInput) {
			t.Errorf("DeletePCIMapping(%q) = %v, want ErrInvalidInput", id, err)
		}
	}
	if len(*seen) != 1 {
		t.Errorf("made %d requests, want 1", len(*seen))
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
