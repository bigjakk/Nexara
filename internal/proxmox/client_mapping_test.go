package proxmox

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"unicode"
)

func strPtr(s string) *string { return &s }

// mappingCall and mappingCallID turn a client method and its arguments into the call the wire
// tables below run, so a USB and a PCI row read alike. mappingEdited is a valid request
// with one thing changed.
func mappingCall[P any](method func(*Client, context.Context, P) error, p P) func(*Client) error {
	return func(c *Client) error { return method(c, context.Background(), p) }
}

func mappingCallID[P any](method func(*Client, context.Context, string, P) error, id string, p P) func(*Client) error {
	return func(c *Client) error { return method(c, context.Background(), id, p) }
}

func mappingEdited[P any](valid P, edit func(*P)) P {
	edit(&valid)
	return valid
}

// The USB and PCI writes share one rulebook, so their wire tables are shared: what
// each sends, what each refuses before building a request, and what each accepts at
// Proxmox's own bounds.
const (
	usbMappingsPath   = "/api2/json/cluster/mapping/usb"
	pciMappingsPath   = "/api2/json/cluster/mapping/pci"
	mappingTestDigest = "0123456789abcdef"
)

func TestMappingWrites_SendExactly(t *testing.T) {
	yes, no := true, false
	pciEntry := "id=1234:5678,node=pve-01,path=0000:01:00.0"
	usbUpdate := func(p UpdateUSBMappingParams) func(*Client) error {
		return mappingCallID((*Client).UpdateUSBMapping, "usbdev01", p)
	}
	pciUpdate := func(p UpdatePCIMappingParams) func(*Client) error {
		return mappingCallID((*Client).UpdatePCIMapping, "gpu01", p)
	}
	for _, tt := range []struct {
		name         string
		call         func(*Client) error
		method, path string
		want         url.Values
	}{
		{"USB create by port",
			mappingCall((*Client).CreateUSBMapping, CreateUSBMappingParams{ID: "usbdev01", Description: "Example serial adapter", Node: "pve-01", DeviceID: "1234:5678", Path: "1-2.3"}),
			http.MethodPost, usbMappingsPath,
			url.Values{"id": {"usbdev01"}, "map": {"id=1234:5678,node=pve-01,path=1-2.3"}, "description": {"Example serial adapter"}}},
		// No path key, so Proxmox matches the device wherever it is plugged in; no description key when there is none.
		{"USB create by device id",
			mappingCall((*Client).CreateUSBMapping, CreateUSBMappingParams{ID: "usbdev01", Node: "pve-01", DeviceID: "1234:5678"}),
			http.MethodPost, usbMappingsPath, url.Values{"id": {"usbdev01"}, "map": {"id=1234:5678,node=pve-01"}}},
		// assert_valid compares the id with `ne` against lowercase sysfs hex.
		{"USB create lowercases the device id",
			mappingCall((*Client).CreateUSBMapping, CreateUSBMappingParams{ID: "usbdev01", Node: "pve-01", DeviceID: "ABCD:EF01"}),
			http.MethodPost, usbMappingsPath, url.Values{"id": {"usbdev01"}, "map": {"id=abcd:ef01,node=pve-01"}}},
		{"USB update, two entries, description untouched",
			usbUpdate(UpdateUSBMappingParams{Map: []string{"id=1234:5678,node=pve-01,path=1-2", "node=pve-02,id=abcd:ef01"}, Digest: mappingTestDigest}),
			http.MethodPut, usbMappingsPath + "/usbdev01",
			url.Values{"map": {"id=1234:5678,node=pve-01,path=1-2", "id=abcd:ef01,node=pve-02"}, "digest": {mappingTestDigest}}},
		{"USB update, a new description",
			usbUpdate(UpdateUSBMappingParams{Map: []string{"node=pve-01,id=1234:5678"}, Description: strPtr("Example serial adapter"), Digest: mappingTestDigest}),
			http.MethodPut, usbMappingsPath + "/usbdev01",
			url.Values{"map": {"id=1234:5678,node=pve-01"}, "description": {"Example serial adapter"}, "digest": {mappingTestDigest}}},
		// An empty description removes it rather than storing "".
		{"USB update, an emptied description is deleted",
			usbUpdate(UpdateUSBMappingParams{Map: []string{"node=pve-01,id=1234:5678"}, Description: strPtr(""), Digest: mappingTestDigest}),
			http.MethodPut, usbMappingsPath + "/usbdev01",
			url.Values{"map": {"id=1234:5678,node=pve-01"}, "delete": {"description"}, "digest": {mappingTestDigest}}},
		{"USB update repairs an uppercase device id",
			usbUpdate(UpdateUSBMappingParams{Map: []string{"node=pve-01,id=ABCD:EF01"}, Digest: mappingTestDigest}),
			http.MethodPut, usbMappingsPath + "/usbdev01", url.Values{"map": {"id=abcd:ef01,node=pve-01"}, "digest": {mappingTestDigest}}},
		// A part splits at its first "=" only, so an entry's own description survives.
		{"USB update keeps an entry's description",
			usbUpdate(UpdateUSBMappingParams{Map: []string{"description=left port=top,node=pve-01,id=1234:5678"}, Digest: mappingTestDigest}),
			http.MethodPut, usbMappingsPath + "/usbdev01",
			url.Values{"map": {"description=left port=top,id=1234:5678,node=pve-01"}, "digest": {mappingTestDigest}}},
		// Proxmox's delete takes no digest, so none is sent.
		{"USB delete", mappingCall((*Client).DeleteUSBMapping, "usbdev01"), http.MethodDelete, usbMappingsPath + "/usbdev01", url.Values{}},

		{"PCI create, every value",
			mappingCall((*Client).CreatePCIMapping, CreatePCIMappingParams{ID: "gpu01", Description: "Example GPU", MDev: true,
				Entry: PCIMapEntry{Node: "pve-01", Path: "0000:01:00.0", ID: "1234:ABCD", SubsystemID: "ABCD:EF01", IOMMUGroup: intPtr(14)}}),
			http.MethodPost, pciMappingsPath,
			url.Values{"id": {"gpu01"}, "map": {"id=1234:abcd,iommugroup=14,node=pve-01,path=0000:01:00.0,subsystem-id=abcd:ef01"},
				"description": {"Example GPU"}, "mdev": {"1"}}},
		// No description and no mdev key at all: Proxmox's defaults.
		{"PCI create, only what is required",
			mappingCall((*Client).CreatePCIMapping, CreatePCIMappingParams{ID: "nic01", Entry: PCIMapEntry{Node: "pve-02", Path: "0000:02:00", ID: "1234:0002"}}),
			http.MethodPost, pciMappingsPath, url.Values{"id": {"nic01"}, "map": {"id=1234:0002,node=pve-02,path=0000:02:00"}}},
		{"PCI update, entries rewritten, nothing else touched",
			pciUpdate(UpdatePCIMappingParams{Map: []string{"path=0000:01:00.0,node=pve-01,id=1234:5678", "node=pve-02,path=0000:02:00.0,id=1234:5678,iommugroup=+3"}, Digest: mappingTestDigest}),
			http.MethodPut, pciMappingsPath + "/gpu01",
			url.Values{"map": {pciEntry, "id=1234:5678,iommugroup=3,node=pve-02,path=0000:02:00.0"}, "digest": {mappingTestDigest}}},
		{"PCI update, a new description and the flag set",
			pciUpdate(UpdatePCIMappingParams{Map: []string{pciEntry}, Description: strPtr("Example GPU"), MDev: &yes, Digest: mappingTestDigest}),
			http.MethodPut, pciMappingsPath + "/gpu01",
			url.Values{"map": {pciEntry}, "description": {"Example GPU"}, "mdev": {"1"}, "digest": {mappingTestDigest}}},
		// Both removals in ONE delete, which Proxmox reads as a list.
		{"PCI update, the description and the flag removed",
			pciUpdate(UpdatePCIMappingParams{Map: []string{pciEntry}, Description: strPtr(""), MDev: &no, Digest: mappingTestDigest}),
			http.MethodPut, pciMappingsPath + "/gpu01",
			url.Values{"map": {pciEntry}, "delete": {"description,mdev"}, "digest": {mappingTestDigest}}},
		{"PCI update, only the flag removed",
			pciUpdate(UpdatePCIMappingParams{Map: []string{pciEntry}, MDev: &no, Digest: mappingTestDigest}),
			http.MethodPut, pciMappingsPath + "/gpu01", url.Values{"map": {pciEntry}, "delete": {"mdev"}, "digest": {mappingTestDigest}}},
		{"PCI delete", mappingCall((*Client).DeletePCIMapping, "gpu01"), http.MethodDelete, pciMappingsPath + "/gpu01", url.Values{}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			req := requireSentOnce(t, tt.call)
			if req.method != tt.method || req.path != tt.path {
				t.Errorf("request = %s %s, want %s %s", req.method, req.path, tt.method, tt.path)
			}
			if !reflect.DeepEqual(req.form, tt.want) || len(req.query) != 0 {
				t.Errorf("form = %v, query = %v, want form %v and no query", req.form, req.query, tt.want)
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

// TestMappingWrites_RefuseBeforeSending: every refusal happens before a request is
// built, with ErrInvalidInput. The values are joined into one property string, so a
// stray "," or "=" would add a key of its own, and an id is a path segment.
func TestMappingWrites_RefuseBeforeSending(t *testing.T) {
	usbC := func(edit func(*CreateUSBMappingParams)) func(*Client) error {
		return mappingCall((*Client).CreateUSBMapping, mappingEdited(CreateUSBMappingParams{ID: "usbdev01", Node: "pve-01", DeviceID: "1234:5678", Path: "1-2"}, edit))
	}
	usbU := func(id string, edit func(*UpdateUSBMappingParams)) func(*Client) error {
		return mappingCallID((*Client).UpdateUSBMapping, id, mappingEdited(UpdateUSBMappingParams{Map: []string{"node=pve-01,id=1234:5678"}, Digest: mappingTestDigest}, edit))
	}
	pciC := func(edit func(*CreatePCIMappingParams)) func(*Client) error {
		return mappingCall((*Client).CreatePCIMapping, mappingEdited(CreatePCIMappingParams{ID: "gpu01", Entry: PCIMapEntry{Node: "pve-01", Path: "0000:01:00.0", ID: "1234:5678"}}, edit))
	}
	pciU := func(id string, edit func(*UpdatePCIMappingParams)) func(*Client) error {
		return mappingCallID((*Client).UpdatePCIMapping, id, mappingEdited(UpdatePCIMappingParams{Map: []string{"id=1234:5678,node=pve-01,path=0000:01:00.0"}, Digest: mappingTestDigest}, edit))
	}
	noUSB, noPCI := func(*UpdateUSBMappingParams) {}, func(*UpdatePCIMappingParams) {}

	type refusal struct {
		name string
		call func(*Client) error
	}
	rows := make([]refusal, 0, 96)
	rows = append(rows, []refusal{
		{"USB create: id starting with a digit", usbC(func(p *CreateUSBMappingParams) { p.ID = "1usb" })},
		{"USB create: one-character id", usbC(func(p *CreateUSBMappingParams) { p.ID = "u" })},
		{"USB create: id with a dot", usbC(func(p *CreateUSBMappingParams) { p.ID = "usb.dev" })},
		{"USB create: empty id", usbC(func(p *CreateUSBMappingParams) { p.ID = "" })},
		{"USB create: node smuggling a key", usbC(func(p *CreateUSBMappingParams) { p.Node = "pve-01,path=9-9" })},
		{"USB create: node with an equals sign", usbC(func(p *CreateUSBMappingParams) { p.Node = "pve=01" })},
		{"USB create: node with an underscore", usbC(func(p *CreateUSBMappingParams) { p.Node = "pve_01" })},
		{"USB create: node ending in a dash", usbC(func(p *CreateUSBMappingParams) { p.Node = "pve-" })},
		{"USB create: empty node", usbC(func(p *CreateUSBMappingParams) { p.Node = "" })},
		{"USB create: device id with 0x", usbC(func(p *CreateUSBMappingParams) { p.DeviceID = "0x1234:0x5678" })},
		{"USB create: device id with a dash", usbC(func(p *CreateUSBMappingParams) { p.DeviceID = "1234-5678" })},
		{"USB create: device id smuggling a key", usbC(func(p *CreateUSBMappingParams) { p.DeviceID = "1234:5678,path=9-9" })},
		{"USB create: empty device id", usbC(func(p *CreateUSBMappingParams) { p.DeviceID = "" })},
		{"USB create: port with no port", usbC(func(p *CreateUSBMappingParams) { p.Path = "1-" })},
		{"USB create: port that is a device id", usbC(func(p *CreateUSBMappingParams) { p.Path = "1234:5678" })},
		{"USB create: port smuggling a key", usbC(func(p *CreateUSBMappingParams) { p.Path = "1-2,node=pve-02" })},
		// Well-formed, but longer than any real port: see usbMappingPathMax.
		{"USB create: port over 64 characters", usbC(func(p *CreateUSBMappingParams) { p.Path = "1-" + strings.Repeat("1.", 31) + "1" })},
		{"USB create: description with a line feed", usbC(func(p *CreateUSBMappingParams) { p.Description = "a\nmap node=x" })},
		{"USB create: description with a carriage return", usbC(func(p *CreateUSBMappingParams) { p.Description = "a\rb" })},
		{"USB create: description over 4096 characters", usbC(func(p *CreateUSBMappingParams) { p.Description = strings.Repeat("d", 4097) })},

		{"USB update: id that is a traversal", usbU("..", noUSB)},
		{"USB update: id with a slash", usbU("usb/../x", noUSB)},
		{"USB update: id with a percent escape", usbU("usb%2e%2e", noUSB)},
		{"USB update: empty id", usbU("", noUSB)},
		{"USB update: no digest", usbU("usbdev01", func(p *UpdateUSBMappingParams) { p.Digest = "" })},
		// Perl reads "0" as false, so assert_if_modified would skip the check.
		{"USB update: digest of 0", usbU("usbdev01", func(p *UpdateUSBMappingParams) { p.Digest = "0" })},
		{"USB update: digest over 64 characters", usbU("usbdev01", func(p *UpdateUSBMappingParams) { p.Digest = strings.Repeat("a", 65) })},
		// Proxmox's own UI deletes the mapping instead; an empty map corrupts usb.cfg.
		{"USB update: empty map", usbU("usbdev01", func(p *UpdateUSBMappingParams) { p.Map = nil })},
		{"USB update: empty but non-nil map", usbU("usbdev01", func(p *UpdateUSBMappingParams) { p.Map = []string{} })},
		// qemu-server refuses to start a VM with more than one entry per node.
		{"USB update: two entries for one node", usbU("usbdev01", func(p *UpdateUSBMappingParams) {
			p.Map = []string{"node=pve-01,id=1234:5678", "node=pve-01,id=abcd:ef01,path=1-2"}
		})},
		{"USB update: a malformed entry among good ones", usbU("usbdev01", func(p *UpdateUSBMappingParams) {
			p.Map = []string{"node=pve-01,id=1234:5678", "node=pve-02,id=12345678"}
		})},
		{"USB update: description with a line feed", usbU("usbdev01", func(p *UpdateUSBMappingParams) { p.Description = strPtr("a\nmap node=x") })},
		{"USB update: description over 4096 characters", usbU("usbdev01", func(p *UpdateUSBMappingParams) { p.Description = strPtr(strings.Repeat("d", 4097)) })},

		{"PCI create: id starting with a digit", pciC(func(p *CreatePCIMappingParams) { p.ID = "1gpu" })},
		{"PCI create: one-character id", pciC(func(p *CreatePCIMappingParams) { p.ID = "g" })},
		{"PCI create: description with a line feed", pciC(func(p *CreatePCIMappingParams) { p.Description = "a\nb" })},
		{"PCI create: description over 4096 characters", pciC(func(p *CreatePCIMappingParams) { p.Description = strings.Repeat("é", 4097) })},
		{"PCI create: node smuggling a key", pciC(func(p *CreatePCIMappingParams) { p.Entry.Node = "pve-01,path=0000:02:00.0" })},
		{"PCI create: no path", pciC(func(p *CreatePCIMappingParams) { p.Entry.Path = "" })},
		{"PCI create: a list of paths", pciC(func(p *CreatePCIMappingParams) { p.Entry.Path = "0000:01:00.0;0000:01:00.1" })},
		{"PCI create: path over 64 characters", pciC(func(p *CreatePCIMappingParams) { p.Entry.Path = strings.Repeat("0", 60) + ":01:00.0" })},
		{"PCI create: no device id", pciC(func(p *CreatePCIMappingParams) { p.Entry.ID = "" })},
		{"PCI create: device id with 0x", pciC(func(p *CreatePCIMappingParams) { p.Entry.ID = "0x1234:0x5678" })},
		{"PCI create: subsystem id smuggling a key", pciC(func(p *CreatePCIMappingParams) { p.Entry.SubsystemID = "abcd:ef01,node=pve-02" })},
		{"PCI create: negative IOMMU group", pciC(func(p *CreatePCIMappingParams) { p.Entry.IOMMUGroup = intPtr(-1) })},
		{"PCI create: entry description with a comma", pciC(func(p *CreatePCIMappingParams) { p.Entry.Description = "left,path=0000:02:00.0" })},
		{"PCI create: entry description with a carriage return", pciC(func(p *CreatePCIMappingParams) { p.Entry.Description = "left\r" })},
		{"PCI create: entry description over 4096 characters", pciC(func(p *CreatePCIMappingParams) { p.Entry.Description = strings.Repeat("d", 4097) })},

		{"PCI update: id that is a traversal", pciU("..", noPCI)},
		{"PCI update: id with a slash", pciU("pci/../x", noPCI)},
		{"PCI update: no digest", pciU("gpu01", func(p *UpdatePCIMappingParams) { p.Digest = "" })},
		{"PCI update: digest of 0", pciU("gpu01", func(p *UpdatePCIMappingParams) { p.Digest = "0" })},
		{"PCI update: digest over 64 characters", pciU("gpu01", func(p *UpdatePCIMappingParams) { p.Digest = strings.Repeat("a", 65) })},
		{"PCI update: empty map", pciU("gpu01", func(p *UpdatePCIMappingParams) { p.Map = nil })},
		{"PCI update: an entry Proxmox cannot read", pciU("gpu01", func(p *UpdatePCIMappingParams) {
			p.Map = []string{"id=1234:5678,node=pve-01,path=0000:01:00.0", "node=pve-02,path=0000:02:00.0"}
		})},
		{"PCI update: description with a line feed", pciU("gpu01", func(p *UpdatePCIMappingParams) { p.Description = strPtr("a\nb") })},
	}...)
	// The id is a path segment: anything that could leave the mapping's own path never reaches the server.
	for _, id := range []string{"", ".", "..", "a/../b", "usb%2e%2e", "1usb", "u", "usb dev", `usb\dev`} {
		rows = append(rows, refusal{"USB delete: id " + strconv.Quote(id), mappingCall((*Client).DeleteUSBMapping, id)})
	}
	for _, id := range []string{"", "..", "gpu/../x", "gpu%2e%2e"} {
		rows = append(rows, refusal{"PCI delete: id " + strconv.Quote(id), mappingCall((*Client).DeletePCIMapping, id)})
	}

	for _, tt := range rows {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			requireRefusedUnsent(t, tt.call, ErrInvalidInput)
		})
	}
}

// TestMappingWrites_AcceptProxmoxsBoundaries: the limits are Proxmox's, so they must
// not refuse anything it accepts — a 2-character id, a nested port, a domain wider
// than four digits, and a description of 4096 multi-byte characters (maxLength
// counts characters, not bytes). The one bound of Nexara's own, the port's 64
// characters, is taken at exactly 64.
func TestMappingWrites_AcceptProxmoxsBoundaries(t *testing.T) {
	pciGood := PCIMapEntry{Node: "pve-01", Path: "0000:01:00.0", ID: "1234:5678"}
	pci := func(edit func(*CreatePCIMappingParams)) func(*Client) error {
		return mappingCall((*Client).CreatePCIMapping, mappingEdited(CreatePCIMappingParams{ID: "gpu01", Entry: pciGood}, edit))
	}
	for _, tt := range []struct {
		name string
		call func(*Client) error
	}{
		{"USB create: short id, nested port, long description", mappingCall((*Client).CreateUSBMapping, CreateUSBMappingParams{
			ID: "u-", Node: "n", DeviceID: "abcd:EF01", Path: "10-1.2.3", Description: strings.Repeat("é", 4096)})},
		{"USB create: port of 64 characters", mappingCall((*Client).CreateUSBMapping, CreateUSBMappingParams{
			ID: "usbdev01", Node: "pve-01", DeviceID: "1234:5678", Path: "1-" + strings.Repeat("1.", 30) + "11"})},
		{"USB update: 64-character digest, long descriptions", mappingCallID((*Client).UpdateUSBMapping, "u-", UpdateUSBMappingParams{
			Map: []string{"node=n,id=1234:5678,description=" + strings.Repeat("é", 4096)}, Description: strPtr(strings.Repeat("é", 4096)),
			Digest: strings.Repeat("a", 64)})},
		{"PCI create: two-character id", pci(func(p *CreatePCIMappingParams) { p.ID = "g1" })},
		{"PCI create: description of 4096 characters", pci(func(p *CreatePCIMappingParams) { p.Description = strings.Repeat("é", 4096) })},
		{"PCI create: domain wider than four digits", pci(func(p *CreatePCIMappingParams) { p.Entry.Path = "10000:01:00.0" })},
		{"PCI create: path of 64 characters", pci(func(p *CreatePCIMappingParams) { p.Entry.Path = strings.Repeat("0", 56) + ":01:00.0" })},
		{"PCI create: IOMMU group 0", pci(func(p *CreatePCIMappingParams) { p.Entry.IOMMUGroup = intPtr(0) })},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			requireSentOnce(t, tt.call)
		})
	}
}

// The listing in the shape pve-manager's PVE/API2/Cluster/Mapping/USB.pm index
// builds it: the section's own keys, id and digest added, and "errors" only
// when check-node was given. The second mapping has no description key at all,
// which is how Proxmox sends a mapping that never had one.
const usbMappingListing = `{"data":[
	{"id":"usbdev01","description":"Example serial adapter","digest":"0123456789abcdef",
	 "map":["id=1234:5678,node=pve-01,path=1-2"],
	 "errors":[]},
	{"id":"usbdev02","digest":"0123456789abcdef",
	 "map":["id=abcd:ef01,node=pve-02"],
	 "errors":[{"severity":"warning","message":"No mapping for node pve-01."}]}
]}`

func TestListUSBMappings_ChecksAgainstTheNode(t *testing.T) {
	srv, seen := newRecordingServer(t, usbMappingListing)
	c := newTestClient(t, srv.URL)

	got, err := c.ListUSBMappings(context.Background(), "pve-01")
	if err != nil {
		t.Fatalf("ListUSBMappings: %v", err)
	}
	if len(*seen) != 1 {
		t.Fatalf("made %d requests, want 1", len(*seen))
	}
	req := (*seen)[0]
	if req.method != http.MethodGet || req.path != usbMappingsPath {
		t.Errorf("request = %s %s, want GET %s", req.method, req.path, usbMappingsPath)
	}
	if want := (url.Values{"check-node": {"pve-01"}}); !reflect.DeepEqual(req.query, want) {
		t.Errorf("query = %v, want %v", req.query, want)
	}

	// Digest is the whole usb.cfg's, repeated on every entry; an update sends it
	// back as its compare-and-swap.
	want := []USBMapping{
		{
			ID:          "usbdev01",
			Description: "Example serial adapter",
			Map:         []string{"id=1234:5678,node=pve-01,path=1-2"},
			Errors:      []MappingCheck{},
			Digest:      "0123456789abcdef",
		},
		{
			ID:     "usbdev02",
			Map:    []string{"id=abcd:ef01,node=pve-02"},
			Errors: []MappingCheck{{Severity: "warning", Message: "No mapping for node pve-01."}},
			Digest: "0123456789abcdef",
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("mappings =\n%#v\nwant\n%#v", got, want)
	}
}

// Without a node there is no check, so Proxmox sends no "errors"/"checks" key, and
// a mapping with no entries has no map: the lists still carry empty ones, never
// null, because the SPA reads them unconditionally.
func TestListMappings_WithoutANodeAsksForNoCheck(t *testing.T) {
	t.Run("USB", func(t *testing.T) {
		srv, seen := newRecordingServer(t, `{"data":[{"id":"usbdev01"}]}`)
		got, err := newTestClient(t, srv.URL).ListUSBMappings(context.Background(), "")
		if err != nil {
			t.Fatalf("ListUSBMappings: %v", err)
		}
		if q := (*seen)[0].query; len(q) != 0 {
			t.Errorf("query = %v, want none", q)
		}
		if len(got) != 1 || got[0].Errors == nil || len(got[0].Errors) != 0 || got[0].Map == nil {
			t.Errorf("mappings = %+v, want one with an empty map and no errors, both non-nil", got)
		}
	})
	t.Run("PCI", func(t *testing.T) {
		srv, seen := newRecordingServer(t, `{"data":[{"id":"gpu01","digest":"d"}]}`)
		got, err := newTestClient(t, srv.URL).ListPCIMappings(context.Background(), "")
		if err != nil {
			t.Fatalf("ListPCIMappings: %v", err)
		}
		if q := (*seen)[0].query; len(q) != 0 {
			t.Errorf("query = %v, want none", q)
		}
		if len(got) != 1 || got[0].Map == nil || got[0].Checks == nil {
			t.Errorf("mappings = %+v, want one with an empty map and no checks, both non-nil", got)
		}
	})
}

// Proxmox proxies a listing to the node check-node names and validates it as
// pve-node, and pveproxy chooses where to proxy a /nodes/{node} call from the name
// before it validates it, so a name that is no node — an IP, a hostname, a list —
// is refused before anything is sent.
func TestMappingNodeReadsRefuseANonNodeName(t *testing.T) {
	ctx := context.Background()
	for _, tt := range []struct {
		name  string
		call  func(*Client, string) error
		nodes []string
	}{
		{"ListUSBMappings", func(c *Client, node string) error { _, err := c.ListUSBMappings(ctx, node); return err },
			[]string{"pve.01", "pve-01,x", "-pve", "pve_01"}},
		{"ListPCIMappings", func(c *Client, node string) error { _, err := c.ListPCIMappings(ctx, node); return err },
			[]string{"pve.01", "pve-01,x", "-pve", "pve_01"}},
		{"ListNodePCIDevicesAllClasses", func(c *Client, node string) error { _, err := c.ListNodePCIDevicesAllClasses(ctx, node); return err },
			[]string{"192.0.2.10", "host.example.com", "pve-01,x", ""}},
	} {
		for _, node := range tt.nodes {
			t.Run(tt.name+"/"+url.PathEscape(node), func(t *testing.T) {
				t.Parallel()
				requireRefusedUnsent(t, func(c *Client) error { return tt.call(c, node) }, ErrInvalidInput)
			})
		}
	}
}

// TestParseUSBMapEntry_TranscribesParsePropertyString holds the parse to
// pve-common's parse_property_string for $map_fmt, one row per branch.
func TestParseUSBMapEntry_TranscribesParsePropertyString(t *testing.T) {
	accepts := []struct {
		name, raw string
		want      USBMapEntry
	}{
		{"keys in any order", "path=1-2.3,id=1234:5678,node=pve-01",
			USBMapEntry{Node: "pve-01", ID: "1234:5678", Path: "1-2.3"}},
		// next if $part =~ /^\s*$/ — Unicode white space included, since the string is decoded by then.
		{"blank parts are skipped", "node=pve-01, ,\t, ,id=1234:5678,",
			USBMapEntry{Node: "pve-01", ID: "1234:5678"}},
		{"a part splits at its first equals sign", "node=pve-01,id=1234:5678,description=a=b",
			USBMapEntry{Node: "pve-01", ID: "1234:5678", Description: "a=b"}},
		{"the id is lowercased", "node=pve-01,id=ABCD:EF01",
			USBMapEntry{Node: "pve-01", ID: "abcd:ef01"}},
		{"a 4096-character description", "node=n,id=1234:5678,description=" + strings.Repeat("é", 4096),
			USBMapEntry{Node: "n", ID: "1234:5678", Description: strings.Repeat("é", 4096)}},
		{"a 64-character port", "node=n,id=1234:5678,path=1-" + strings.Repeat("1.", 30) + "11",
			USBMapEntry{Node: "n", ID: "1234:5678", Path: "1-" + strings.Repeat("1.", 30) + "11"}},
	}
	for _, tt := range accepts {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseUSBMapEntry(tt.raw)
			if err != nil {
				t.Fatalf("ParseUSBMapEntry(%q): %v", tt.raw, err)
			}
			if got != tt.want {
				t.Errorf("ParseUSBMapEntry(%q) = %+v, want %+v", tt.raw, got, tt.want)
			}
		})
	}

	refuses := []struct{ name, raw string }{
		{"empty", ""},
		{"a value without a key", "node=pve-01,id=1234:5678,1-2"},
		{"an empty key", "node=pve-01,id=1234:5678,=x"},
		{"an empty value", "node=pve-01,id=1234:5678,path="},
		// The one key whose own check would not catch an empty value.
		{"an empty description", "node=pve-01,id=1234:5678,description="},
		{"an unknown key", "node=pve-01,id=1234:5678,host=1-2"},
		// Nothing is trimmed, so " node" is not "node".
		{"a key with a space", "node=pve-01, id=1234:5678"},
		{"a value with a space", "node=pve-01,id=1234:5678 "},
		{"a duplicate key", "node=pve-01,id=1234:5678,id=abcd:ef01"},
		{"no node", "id=1234:5678"},
		{"no id", "node=pve-01"},
		{"a node that is not a node name", "node=pve_01,id=1234:5678"},
		{"a device id with 0x", "node=pve-01,id=0x1234:0x5678"},
		{"a port with no port", "node=pve-01,id=1234:5678,path=1-"},
		{"a port over 64 characters", "node=pve-01,id=1234:5678,path=1-" + strings.Repeat("1.", 31) + "1"},
		{"a description over 4096 characters", "node=n,id=1234:5678,description=" + strings.Repeat("d", 4097)},
		// usb.cfg holds one property per line.
		{"a line feed", "node=pve-01,id=1234:5678,description=a\nmap node=x"},
		{"a trailing line feed", "node=pve-01,id=1234:5678\n"},
		// Proxmox skips a part of only white space, line feed included, and would store it: in usb.cfg it ends the line, and the section.
		{"a line feed in a blank part", "node=pve-01,id=1234:5678,\n"},
		{"a carriage return", "node=pve-01,id=1234:5678,description=a\rb"},
	}
	for _, tt := range refuses {
		t.Run(tt.name, func(t *testing.T) {
			if got, err := ParseUSBMapEntry(tt.raw); !errors.Is(err, ErrInvalidInput) {
				t.Errorf("ParseUSBMapEntry(%q) = %+v, %v; want ErrInvalidInput", tt.raw, got, err)
			}
		})
	}
}

func TestVMConfigUSBMappingKeys(t *testing.T) {
	config := VMConfig{
		"usb10":    "mapping=usbdev01",
		"usb2":     "usb3=1,mapping=usbdev01",
		"usb0":     "mapping=usbdev01,usb3=1",
		"usb1":     "mapping=usbdev011",        // another mapping
		"usb3":     "host=1-2",                 // a raw device
		"usb4":     "mapping=USBDEV01",         // qemu-server looks the id up by exact key
		"usb5":     "spice",                    // a SPICE redirection
		"hostpci0": "mapping=usbdev01",         // not a USB key
		"usbx":     "mapping=usbdev01",         // not a usbN key
		"cores":    float64(2),                 // not a string
		"name":     "linux01,mapping=usbdev01", // not a usbN key
	}
	// Numeric order, not string order: usb10 last.
	if got, want := config.USBMappingKeys("usbdev01"), []string{"usb0", "usb2", "usb10"}; !reflect.DeepEqual(got, want) {
		t.Errorf("USBMappingKeys = %v, want %v", got, want)
	}
	if got := config.USBMappingKeys("usbdev02"); len(got) != 0 {
		t.Errorf("USBMappingKeys(unused) = %v, want none", got)
	}
}

// sectionConfigRead is what usb.cfg gives back for a map item written as
// "\tmap <item>": pve-common SectionConfig parse_config reads each property as
// m/^\s+(\S+)(\s+(.*\S))?\s*$/, so the value loses the white space that ends the line.
func sectionConfigRead(item string) string {
	return strings.TrimRightFunc(item, unicode.IsSpace)
}

// Every entry this client writes must come back from usb.cfg as it went in. One
// ending in white space would not: a description of only spaces would come back as
// "description=", which Proxmox refuses, dropping the entry.
func TestUSBMapEntry_SurvivesTheUSBCfgRoundTrip(t *testing.T) {
	for _, raw := range []string{
		"node=pve-01,id=1234:5678,description=  ",
		"node=pve-01,id=1234:5678,description=left port \t",
		"description=  ,node=pve-01,id=1234:5678,path=1-2",
		"node=pve-01,id=ABCD:EF01",
		"path=1-2.3,node=pve-01,id=1234:5678,description=a=b",
	} {
		e, err := ParseUSBMapEntry(raw)
		if err != nil {
			t.Fatalf("ParseUSBMapEntry(%q): %v", raw, err)
		}
		written := e.String()
		read := sectionConfigRead(written)
		if read != written {
			t.Errorf("%q is written as %q, which usb.cfg reads back as %q", raw, written, read)
		}
		again, err := ParseUSBMapEntry(read)
		if err != nil {
			t.Errorf("%q is written as %q, which Proxmox then refuses: %v", raw, written, err)
			continue
		}
		if again != e {
			t.Errorf("%q round-trips to %+v, want %+v", raw, again, e)
		}
	}
}
