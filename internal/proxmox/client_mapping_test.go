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

	want := []USBMapping{
		{
			ID:          "usbdev01",
			Description: "Example serial adapter",
			Map:         []string{"id=1234:5678,node=pve-01,path=1-2"},
			Errors:      []MappingCheck{},
		},
		{
			ID:     "usbdev02",
			Map:    []string{"id=abcd:ef01,node=pve-02"},
			Errors: []MappingCheck{{Severity: "warning", Message: "No mapping for node pve-01."}},
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
				"map":         {"node=pve-01,id=1234:5678,path=1-2.3"},
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
				"map": {"node=pve-01,id=1234:5678"},
			},
		},
		{
			// assert_valid compares the id with `ne` against lowercase sysfs
			// hex, so an uppercase one would make every start fail.
			name:   "the device id is lowercased",
			params: CreateUSBMappingParams{ID: "usbdev01", Node: "pve-01", DeviceID: "ABCD:EF01"},
			want: url.Values{
				"id":  {"usbdev01"},
				"map": {"node=pve-01,id=abcd:ef01"},
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
