package proxmox

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"unicode"
)

type mappingRequest struct {
	method, path string
	query, form  url.Values
}

// newMappingCaptureServer records every request and answers each with body.
func newMappingCaptureServer(t *testing.T, body string) (*httptest.Server, *[]mappingRequest) {
	t.Helper()
	var seen []mappingRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Errorf("ParseForm: %v", err)
		}
		seen = append(seen, mappingRequest{r.Method, r.URL.Path, r.URL.Query(), r.PostForm})
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv, &seen
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
	srv, seen := newMappingCaptureServer(t, usbMappingListing)
	c := newTestClient(t, srv.URL)

	got, err := c.ListUSBMappings(context.Background(), "pve-01")
	if err != nil {
		t.Fatalf("ListUSBMappings: %v", err)
	}

	if len(*seen) != 1 {
		t.Fatalf("made %d requests, want 1", len(*seen))
	}
	req := (*seen)[0]
	if req.method != http.MethodGet || req.path != "/api2/json/cluster/mapping/usb" {
		t.Errorf("request = %s %s, want GET /api2/json/cluster/mapping/usb", req.method, req.path)
	}
	if want := (url.Values{"check-node": {"pve-01"}}); !reflect.DeepEqual(req.query, want) {
		t.Errorf("query = %v, want %v", req.query, want)
	}

	// Digest is the whole usb.cfg's, repeated on every entry; an update sends
	// it back as its compare-and-swap.
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

// Without a node there is no check, so Proxmox sends no "errors" key; the
// list still carries an empty one, because the SPA reads it unconditionally.
func TestListUSBMappings_WithoutANodeAsksForNoCheck(t *testing.T) {
	srv, seen := newMappingCaptureServer(t, `{"data":[{"id":"usbdev01","map":["id=1234:5678,node=pve-01"]}]}`)
	c := newTestClient(t, srv.URL)

	got, err := c.ListUSBMappings(context.Background(), "")
	if err != nil {
		t.Fatalf("ListUSBMappings: %v", err)
	}
	if q := (*seen)[0].query; len(q) != 0 {
		t.Errorf("query = %v, want none", q)
	}
	if len(got) != 1 || got[0].Errors == nil || len(got[0].Errors) != 0 {
		t.Errorf("errors = %#v, want an empty, non-nil list", got)
	}
}

// Proxmox proxies the listing to the node check-node names and validates it
// as pve-node, so a name that format refuses never leaves this client.
func TestListUSBMappings_RefusesANonNodeName(t *testing.T) {
	for _, node := range []string{"pve.01", "pve-01,x", "-pve", "pve_01"} {
		srv, seen := newMappingCaptureServer(t, `{"data":[]}`)
		c := newTestClient(t, srv.URL)

		if _, err := c.ListUSBMappings(context.Background(), node); !errors.Is(err, ErrInvalidInput) {
			t.Errorf("node %q: err = %v, want ErrInvalidInput", node, err)
		}
		if len(*seen) != 0 {
			t.Errorf("node %q: made %d requests, want none", node, len(*seen))
		}
	}
}

func TestListUSBMappings_EmptyMapIsAList(t *testing.T) {
	srv, _ := newMappingCaptureServer(t, `{"data":[{"id":"usbdev01"}]}`)
	c := newTestClient(t, srv.URL)

	got, err := c.ListUSBMappings(context.Background(), "pve-01")
	if err != nil {
		t.Fatalf("ListUSBMappings: %v", err)
	}
	if got[0].Map == nil {
		t.Error("map is nil, want an empty list")
	}
}

func TestCreateUSBMapping_SendsOneEntry(t *testing.T) {
	tests := []struct {
		name   string
		params CreateUSBMappingParams
		want   url.Values
	}{
		{
			name: "by port",
			params: CreateUSBMappingParams{
				ID: "usbdev01", Description: "Example serial adapter",
				Node: "pve-01", DeviceID: "1234:5678", Path: "1-2.3",
			},
			want: url.Values{
				"id":          {"usbdev01"},
				"map":         {"id=1234:5678,node=pve-01,path=1-2.3"},
				"description": {"Example serial adapter"},
			},
		},
		{
			// No path key at all, so Proxmox matches the device wherever it
			// is plugged in; and no description key when there is none.
			name:   "by device id",
			params: CreateUSBMappingParams{ID: "usbdev01", Node: "pve-01", DeviceID: "1234:5678"},
			want: url.Values{
				"id":  {"usbdev01"},
				"map": {"id=1234:5678,node=pve-01"},
			},
		},
		{
			// assert_valid compares the id with `ne` against lowercase sysfs
			// hex, so an uppercase one would make every start fail.
			name:   "the device id is lowercased",
			params: CreateUSBMappingParams{ID: "usbdev01", Node: "pve-01", DeviceID: "ABCD:EF01"},
			want: url.Values{
				"id":  {"usbdev01"},
				"map": {"id=abcd:ef01,node=pve-01"},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv, seen := newMappingCaptureServer(t, `{"data":null}`)
			c := newTestClient(t, srv.URL)

			if err := c.CreateUSBMapping(context.Background(), tt.params); err != nil {
				t.Fatalf("CreateUSBMapping: %v", err)
			}
			if len(*seen) != 1 {
				t.Fatalf("made %d requests, want 1", len(*seen))
			}
			req := (*seen)[0]
			if req.method != http.MethodPost || req.path != "/api2/json/cluster/mapping/usb" {
				t.Errorf("request = %s %s, want POST /api2/json/cluster/mapping/usb", req.method, req.path)
			}
			if !reflect.DeepEqual(req.form, tt.want) {
				t.Errorf("form = %v, want %v", req.form, tt.want)
			}
		})
	}
}

// Every refusal happens before a request is built: the values are joined into
// one property string, so a stray "," or "=" would add a key of its own.
func TestCreateUSBMapping_RefusesWhatProxmoxWould(t *testing.T) {
	valid := CreateUSBMappingParams{ID: "usbdev01", Node: "pve-01", DeviceID: "1234:5678", Path: "1-2"}
	tests := []struct {
		name   string
		mutate func(*CreateUSBMappingParams)
	}{
		{"id starting with a digit", func(p *CreateUSBMappingParams) { p.ID = "1usb" }},
		{"one-character id", func(p *CreateUSBMappingParams) { p.ID = "u" }},
		{"id with a dot", func(p *CreateUSBMappingParams) { p.ID = "usb.dev" }},
		{"empty id", func(p *CreateUSBMappingParams) { p.ID = "" }},
		{"node smuggling a key", func(p *CreateUSBMappingParams) { p.Node = "pve-01,path=9-9" }},
		{"node with an equals sign", func(p *CreateUSBMappingParams) { p.Node = "pve=01" }},
		{"node with an underscore", func(p *CreateUSBMappingParams) { p.Node = "pve_01" }},
		{"node ending in a dash", func(p *CreateUSBMappingParams) { p.Node = "pve-" }},
		{"empty node", func(p *CreateUSBMappingParams) { p.Node = "" }},
		{"device id with 0x", func(p *CreateUSBMappingParams) { p.DeviceID = "0x1234:0x5678" }},
		{"device id with a dash", func(p *CreateUSBMappingParams) { p.DeviceID = "1234-5678" }},
		{"device id smuggling a key", func(p *CreateUSBMappingParams) { p.DeviceID = "1234:5678,path=9-9" }},
		{"empty device id", func(p *CreateUSBMappingParams) { p.DeviceID = "" }},
		{"port with no port", func(p *CreateUSBMappingParams) { p.Path = "1-" }},
		{"port that is a device id", func(p *CreateUSBMappingParams) { p.Path = "1234:5678" }},
		{"port smuggling a key", func(p *CreateUSBMappingParams) { p.Path = "1-2,node=pve-02" }},
		// Well-formed, but longer than any real port: see usbMappingPathMax.
		{"port over 64 characters", func(p *CreateUSBMappingParams) { p.Path = "1-" + strings.Repeat("1.", 31) + "1" }},
		{"description with a line feed", func(p *CreateUSBMappingParams) { p.Description = "a\nmap node=x" }},
		{"description with a carriage return", func(p *CreateUSBMappingParams) { p.Description = "a\rb" }},
		{"description over 4096 characters", func(p *CreateUSBMappingParams) { p.Description = strings.Repeat("d", 4097) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv, seen := newMappingCaptureServer(t, `{"data":null}`)
			c := newTestClient(t, srv.URL)

			params := valid
			tt.mutate(&params)
			err := c.CreateUSBMapping(context.Background(), params)
			if !errors.Is(err, ErrInvalidInput) {
				t.Errorf("err = %v, want ErrInvalidInput", err)
			}
			if len(*seen) != 0 {
				t.Errorf("made %d requests, want none", len(*seen))
			}
		})
	}
}

// The limits are Proxmox's, so they must not refuse anything it accepts: a
// 2-character id with a dash, a nested port, and a 4096-character description
// of multi-byte characters (maxLength counts characters, not bytes). The one
// bound of Nexara's own, the port's 64 characters, is taken at exactly 64.
func TestCreateUSBMapping_AcceptsProxmoxsBoundaries(t *testing.T) {
	for _, params := range []CreateUSBMappingParams{
		{
			ID: "u-", Node: "n", DeviceID: "abcd:EF01", Path: "10-1.2.3",
			Description: strings.Repeat("é", 4096),
		},
		{ID: "usbdev01", Node: "pve-01", DeviceID: "1234:5678", Path: "1-" + strings.Repeat("1.", 30) + "11"},
	} {
		srv, seen := newMappingCaptureServer(t, `{"data":null}`)
		c := newTestClient(t, srv.URL)

		if len(params.Path) > 64 {
			t.Fatalf("fixture port is %d characters; the test needs at most 64", len(params.Path))
		}
		if err := c.CreateUSBMapping(context.Background(), params); err != nil {
			t.Fatalf("CreateUSBMapping(%+v): %v", params, err)
		}
		if len(*seen) != 1 {
			t.Fatalf("made %d requests, want 1", len(*seen))
		}
	}
}

func strPtr(s string) *string { return &s }

func TestUpdateUSBMapping_SendsTheWholeMap(t *testing.T) {
	tests := []struct {
		name   string
		params UpdateUSBMappingParams
		want   url.Values
	}{
		{
			// One "map" key per entry, in the order given, each re-written
			// in description, id, node, path order; the description key
			// absent because the caller left it alone.
			name: "two entries, description untouched",
			params: UpdateUSBMappingParams{
				Map:    []string{"id=1234:5678,node=pve-01,path=1-2", "node=pve-02,id=abcd:ef01"},
				Digest: "0123456789abcdef",
			},
			want: url.Values{
				"map":    {"id=1234:5678,node=pve-01,path=1-2", "id=abcd:ef01,node=pve-02"},
				"digest": {"0123456789abcdef"},
			},
		},
		{
			name: "a new description",
			params: UpdateUSBMappingParams{
				Map:         []string{"node=pve-01,id=1234:5678"},
				Description: strPtr("Example serial adapter"),
				Digest:      "0123456789abcdef",
			},
			want: url.Values{
				"map":         {"id=1234:5678,node=pve-01"},
				"description": {"Example serial adapter"},
				"digest":      {"0123456789abcdef"},
			},
		},
		{
			// An empty description removes it rather than storing "".
			name: "an emptied description is deleted",
			params: UpdateUSBMappingParams{
				Map:         []string{"node=pve-01,id=1234:5678"},
				Description: strPtr(""),
				Digest:      "0123456789abcdef",
			},
			want: url.Values{
				"map":    {"id=1234:5678,node=pve-01"},
				"delete": {"description"},
				"digest": {"0123456789abcdef"},
			},
		},
		{
			// assert_valid compares the id with `ne` against lowercase sysfs
			// hex, so an entry made elsewhere as uppercase is repaired.
			name: "an uppercase device id is written lowercase",
			params: UpdateUSBMappingParams{
				Map:    []string{"node=pve-01,id=ABCD:EF01"},
				Digest: "0123456789abcdef",
			},
			want: url.Values{
				"map":    {"id=abcd:ef01,node=pve-01"},
				"digest": {"0123456789abcdef"},
			},
		},
		{
			// An entry's own description survives, "=" and all: a part
			// splits at its first "=" only.
			name: "an entry's description is kept",
			params: UpdateUSBMappingParams{
				Map:    []string{"description=left port=top,node=pve-01,id=1234:5678"},
				Digest: "0123456789abcdef",
			},
			want: url.Values{
				"map":    {"description=left port=top,id=1234:5678,node=pve-01"},
				"digest": {"0123456789abcdef"},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv, seen := newMappingCaptureServer(t, `{"data":null}`)
			c := newTestClient(t, srv.URL)

			if err := c.UpdateUSBMapping(context.Background(), "usbdev01", tt.params); err != nil {
				t.Fatalf("UpdateUSBMapping: %v", err)
			}
			if len(*seen) != 1 {
				t.Fatalf("made %d requests, want 1", len(*seen))
			}
			req := (*seen)[0]
			if req.method != http.MethodPut || req.path != "/api2/json/cluster/mapping/usb/usbdev01" {
				t.Errorf("request = %s %s, want PUT /api2/json/cluster/mapping/usb/usbdev01", req.method, req.path)
			}
			if !reflect.DeepEqual(req.form, tt.want) {
				t.Errorf("form = %v, want %v", req.form, tt.want)
			}
		})
	}
}

// Everything is refused before a request is built: the map replaces the
// mapping's whole list, and usb.cfg is cluster-wide configuration.
func TestUpdateUSBMapping_RefusesBeforeSending(t *testing.T) {
	valid := UpdateUSBMappingParams{Map: []string{"node=pve-01,id=1234:5678"}, Digest: "0123456789abcdef"}
	tests := []struct {
		name   string
		id     string
		mutate func(*UpdateUSBMappingParams)
	}{
		{"an id that is a traversal", "..", func(*UpdateUSBMappingParams) {}},
		{"an id with a slash", "usb/../x", func(*UpdateUSBMappingParams) {}},
		{"an id with a percent escape", "usb%2e%2e", func(*UpdateUSBMappingParams) {}},
		{"an empty id", "", func(*UpdateUSBMappingParams) {}},
		{"no digest", "usbdev01", func(p *UpdateUSBMappingParams) { p.Digest = "" }},
		// Perl reads "0" as false, so assert_if_modified would skip the check.
		{"a digest of 0", "usbdev01", func(p *UpdateUSBMappingParams) { p.Digest = "0" }},
		{"a digest over 64 characters", "usbdev01", func(p *UpdateUSBMappingParams) { p.Digest = strings.Repeat("a", 65) }},
		// Proxmox's own UI deletes the mapping instead; an empty map
		// corrupts usb.cfg.
		{"an empty map", "usbdev01", func(p *UpdateUSBMappingParams) { p.Map = nil }},
		{"an empty but non-nil map", "usbdev01", func(p *UpdateUSBMappingParams) { p.Map = []string{} }},
		// qemu-server refuses to start a VM with more than one entry per node.
		{"two entries for one node", "usbdev01", func(p *UpdateUSBMappingParams) {
			p.Map = []string{"node=pve-01,id=1234:5678", "node=pve-01,id=abcd:ef01,path=1-2"}
		}},
		{"a malformed entry among good ones", "usbdev01", func(p *UpdateUSBMappingParams) {
			p.Map = []string{"node=pve-01,id=1234:5678", "node=pve-02,id=12345678"}
		}},
		{"a description with a line feed", "usbdev01", func(p *UpdateUSBMappingParams) { p.Description = strPtr("a\nmap node=x") }},
		{"a description over 4096 characters", "usbdev01", func(p *UpdateUSBMappingParams) {
			p.Description = strPtr(strings.Repeat("d", 4097))
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv, seen := newMappingCaptureServer(t, `{"data":null}`)
			c := newTestClient(t, srv.URL)

			params := valid
			tt.mutate(&params)
			if err := c.UpdateUSBMapping(context.Background(), tt.id, params); !errors.Is(err, ErrInvalidInput) {
				t.Errorf("err = %v, want ErrInvalidInput", err)
			}
			if len(*seen) != 0 {
				t.Errorf("made %d requests, want none", len(*seen))
			}
		})
	}
}

// Proxmox's bounds, taken at the bound: a 64-character digest (the standard
// option's maxLength) and a 4096-character description of multi-byte
// characters.
func TestUpdateUSBMapping_AcceptsProxmoxsBoundaries(t *testing.T) {
	srv, seen := newMappingCaptureServer(t, `{"data":null}`)
	c := newTestClient(t, srv.URL)

	params := UpdateUSBMappingParams{
		Map:         []string{"node=n,id=1234:5678,description=" + strings.Repeat("é", 4096)},
		Description: strPtr(strings.Repeat("é", 4096)),
		Digest:      strings.Repeat("a", 64),
	}
	if err := c.UpdateUSBMapping(context.Background(), "u-", params); err != nil {
		t.Fatalf("UpdateUSBMapping: %v", err)
	}
	if len(*seen) != 1 {
		t.Fatalf("made %d requests, want 1", len(*seen))
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
		// next if $part =~ /^\s*$/ — Unicode white space included, since the
		// string is decoded by then.
		{"blank parts are skipped", "node=pve-01, ,\t, ,id=1234:5678,",
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
		// Proxmox skips a part of only white space, line feed included, and
		// would store it: in usb.cfg it ends the line, and the section.
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

func TestDeleteUSBMapping(t *testing.T) {
	srv, seen := newMappingCaptureServer(t, `{"data":null}`)
	c := newTestClient(t, srv.URL)

	if err := c.DeleteUSBMapping(context.Background(), "usbdev01"); err != nil {
		t.Fatalf("DeleteUSBMapping: %v", err)
	}
	if len(*seen) != 1 {
		t.Fatalf("made %d requests, want 1", len(*seen))
	}
	req := (*seen)[0]
	if req.method != http.MethodDelete || req.path != "/api2/json/cluster/mapping/usb/usbdev01" {
		t.Errorf("request = %s %s, want DELETE /api2/json/cluster/mapping/usb/usbdev01", req.method, req.path)
	}
	// Proxmox's delete takes no digest, so none is sent.
	if len(req.query) != 0 || len(req.form) != 0 {
		t.Errorf("query = %v, form = %v, want neither", req.query, req.form)
	}
}

// The id is a path segment, so anything that could leave the mapping's own
// path never reaches the server.
func TestDeleteUSBMapping_RefusesANonMappingID(t *testing.T) {
	for _, id := range []string{"", ".", "..", "a/../b", "usb%2e%2e", "1usb", "u", "usb dev", "usb\\dev"} {
		srv, seen := newMappingCaptureServer(t, `{"data":null}`)
		c := newTestClient(t, srv.URL)

		if err := c.DeleteUSBMapping(context.Background(), id); !errors.Is(err, ErrInvalidInput) {
			t.Errorf("id %q: err = %v, want ErrInvalidInput", id, err)
		}
		if len(*seen) != 0 {
			t.Errorf("id %q: made %d requests, want none", id, len(*seen))
		}
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
// m/^\s+(\S+)(\s+(.*\S))?\s*$/, so the value loses the white space that
// ends the line.
func sectionConfigRead(item string) string {
	return strings.TrimRightFunc(item, unicode.IsSpace)
}

// Every entry this client writes must come back from usb.cfg as it went in.
// One ending in white space would not: a description of only spaces would
// come back as "description=", which Proxmox refuses, dropping the entry.
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
