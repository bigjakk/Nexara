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

func TestMapUSBMappingCreateError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
	}{
		{
			// What pve-manager's create dies with, re-died by lock_usb_config,
			// in the JSON envelope checkStatus keeps as the message.
			name: "a taken id is a conflict",
			err: &proxmox.APIError{
				StatusCode: 500,
				Message:    `{"data":null,"message":"create hardware mapping failed: usb ID 'usbdev01' already defined\n"}`,
			},
			want: fiber.StatusConflict,
		},
		{
			name: "a taken id is a conflict without the envelope too",
			err: &proxmox.APIError{
				StatusCode: 500,
				Message:    "create hardware mapping failed: usb ID 'usbdev01' already defined",
			},
			want: fiber.StatusConflict,
		},
		{
			// The client refused it; it never reached Proxmox.
			name: "a malformed device id stays a 400",
			err:  fmt.Errorf("%w: USB device id %q must be vendor:product", proxmox.ErrInvalidInput, "12345678"),
			want: fiber.StatusBadRequest,
		},
		{
			// A PVEAdmin token: no Mapping.Modify.
			name: "a token without Mapping.Modify stays a 403",
			err:  proxmox.ErrForbidden,
			want: fiber.StatusForbidden,
		},
		{
			name: "an unreachable cluster stays a 502",
			err:  proxmox.ErrConnectionFailed,
			want: fiber.StatusBadGateway,
		},
		{
			// The lock itself failing is the same wrapper with another cause.
			name: "an unrelated create failure stays a 502",
			err: &proxmox.APIError{
				StatusCode: 500,
				Message:    "create hardware mapping failed: can't lock file '/var/lock/pve-manager/pve-mapping-usb.lck' - got timeout",
			},
			want: fiber.StatusBadGateway,
		},
		{
			// The phrase in the client's wrap is the caller's own input, so
			// it must not decide the status; only Proxmox's words are read.
			name: "the phrase in the client's wrap is not Proxmox's",
			err: fmt.Errorf("create USB mapping %s: %w", "already defined",
				&proxmox.APIError{StatusCode: 500, Message: "unable to read usb.cfg"}),
			want: fiber.StatusBadGateway,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var fe *fiber.Error
			err := mapUSBMappingCreateError(tt.err)
			if !errors.As(err, &fe) {
				t.Fatalf("mapUSBMappingCreateError(%v) = %v, want a *fiber.Error", tt.err, err)
			}
			if fe.Code != tt.want {
				t.Errorf("status = %d, want %d (message %q)", fe.Code, tt.want, fe.Message)
			}
		})
	}
	if mapUSBMappingCreateError(nil) != nil {
		t.Error("mapUSBMappingCreateError(nil) should stay nil")
	}
}

func TestMapUSBMappingUpdateError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
		msg  string
	}{
		{
			// assert_if_modified, re-died by lock_usb_config, in the JSON
			// envelope checkStatus keeps as the message.
			name: "a stale digest is a conflict",
			err: &proxmox.APIError{StatusCode: 500,
				Message: `{"data":null,"message":"update hardware mapping failed: detected modified configuration - file changed by other user? Try again.\n"}`},
			want: fiber.StatusConflict,
			msg:  usbMappingStaleMessage,
		},
		{
			name: "a stale digest is a conflict without the envelope too",
			err: &proxmox.APIError{StatusCode: 500,
				Message: "update hardware mapping failed: detected modified configuration - file changed by other user? Try again."},
			want: fiber.StatusConflict,
		},
		{
			name: "a missing mapping is not found",
			err: &proxmox.APIError{StatusCode: 500,
				Message: `{"data":null,"message":"update hardware mapping failed: usb ID 'usbdev01' does not exist\n"}`},
			want: fiber.StatusNotFound,
		},
		{
			// The digest is compared first upstream, so a message carrying
			// both (synthetic) must answer as the digest does.
			name: "the digest wins over the missing mapping",
			err: &proxmox.APIError{StatusCode: 500,
				Message: "detected modified configuration; usb ID 'usbdev01' does not exist"},
			want: fiber.StatusConflict,
		},
		{
			name: "a malformed entry refused by the client stays a 400",
			err:  fmt.Errorf("%w: USB mapping entry %q needs id=<vendor:product>", proxmox.ErrInvalidInput, "node=pve-01"),
			want: fiber.StatusBadRequest,
		},
		{
			name: "a token without Mapping.Modify stays a 403",
			err:  proxmox.ErrForbidden,
			want: fiber.StatusForbidden,
		},
		{
			name: "an unreachable cluster stays a 502",
			err:  proxmox.ErrConnectionFailed,
			want: fiber.StatusBadGateway,
		},
		{
			name: "a lock timeout stays a 502",
			err: &proxmox.APIError{StatusCode: 500,
				Message: "update hardware mapping failed: can't lock file '/var/lock/pve-manager/pve-mapping-usb.lck' - got timeout"},
			want: fiber.StatusBadGateway,
		},
		{
			// Only Proxmox's words decide the status, never the client's
			// wrap, which carries the caller's id.
			name: "a phrase in the client's wrap is not Proxmox's",
			err: fmt.Errorf("update USB mapping %s: %w", "does not exist detected modified configuration",
				&proxmox.APIError{StatusCode: 500, Message: "unable to read usb.cfg"}),
			want: fiber.StatusBadGateway,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var fe *fiber.Error
			err := mapUSBMappingUpdateError(tt.err)
			if !errors.As(err, &fe) {
				t.Fatalf("mapUSBMappingUpdateError(%v) = %v, want a *fiber.Error", tt.err, err)
			}
			if fe.Code != tt.want {
				t.Errorf("status = %d, want %d (message %q)", fe.Code, tt.want, fe.Message)
			}
			if tt.msg != "" && fe.Message != tt.msg {
				t.Errorf("message = %q, want %q", fe.Message, tt.msg)
			}
		})
	}
	if mapUSBMappingUpdateError(nil) != nil {
		t.Error("mapUSBMappingUpdateError(nil) should stay nil")
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

// The row keeps what it takes to create the mapping again, and marks a row
// whose snapshot could not be taken rather than leaving it looking empty.
func TestUSBMappingDeleteDetails(t *testing.T) {
	for _, tt := range []struct {
		name     string
		snapshot *proxmox.USBMapping
		unknown  bool
		want     string
	}{
		{"entries and description",
			&proxmox.USBMapping{ID: "usbdev01", Description: "Example serial adapter",
				Map: []string{"node=pve-01,id=1234:5678,path=1-2", "node=pve-02,id=1234:5678"}},
			false,
			`{"description":"Example serial adapter","map":["node=pve-01,id=1234:5678,path=1-2","node=pve-02,id=1234:5678"],"map_count":2}`},
		// Recorded as Proxmox held it — an uppercase id included, which is
		// what kept VMs from starting — not in Nexara's own form.
		{"an entry is recorded as it was",
			&proxmox.USBMapping{ID: "usbdev01", Map: []string{"id=ABCD:EF01,node=pve-01"}},
			false,
			`{"map":["id=ABCD:EF01,node=pve-01"],"map_count":1}`},
		{"no description key when there is none",
			&proxmox.USBMapping{ID: "usbdev01", Map: []string{"node=pve-01,id=1234:5678"}},
			false,
			`{"map":["node=pve-01,id=1234:5678"],"map_count":1}`},
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
	desc, empty := "Example serial adapter", ""
	for _, tt := range []struct {
		name        string
		description *string
		want        string
	}{
		{"the description left alone", nil,
			`{"map":["node=pve-01,id=1234:5678","node=pve-02,id=1234:5678,path=1-2"],"map_count":2}`},
		{"a new description", &desc,
			`{"description":"Example serial adapter","map":["node=pve-01,id=1234:5678","node=pve-02,id=1234:5678,path=1-2"],"map_count":2}`},
		{"the description removed", &empty,
			`{"description_removed":true,"map":["node=pve-01,id=1234:5678","node=pve-02,id=1234:5678,path=1-2"],"map_count":2}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := string(usbMappingUpdateDetails(written, tt.description)); got != tt.want {
				t.Errorf("details = %s, want %s", got, tt.want)
			}
		})
	}
}

// audit_log is never trimmed and these values are the caller's, so a row
// holds at most usbMappingAuditEntries entries of at most
// usbMappingAuditTextMax characters each, and says how many there were.
func TestUSBMappingAuditDetailsAreCapped(t *testing.T) {
	long := strings.Repeat("é", 4096)
	entries := make([]string, 0, 70)
	for i := range 70 {
		entries = append(entries, fmt.Sprintf("description=%s,id=1234:5678,node=pve-%02d", long, i))
	}
	check := func(t *testing.T, raw json.RawMessage, wantDescription bool) {
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
			t.Errorf("recorded %d entries with map_count %d, want %d and 70",
				len(detail.Map), detail.MapCount, usbMappingAuditEntries)
		}
		for i, e := range detail.Map {
			// The cut is the description's: the entry keeps what makes it
			// one — node, device id — however long its description was.
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
		if n := utf8.RuneCountInString(detail.Description); wantDescription && (n == 0 || n > usbMappingAuditTextMax+1) {
			t.Errorf("description of %d characters recorded, want 1..%d and the ellipsis", n, usbMappingAuditTextMax)
		}
	}
	// An entry Proxmox holds that does not parse — written by hand, say — is
	// cut whole rather than dropped.
	t.Run("an entry that does not parse", func(t *testing.T) {
		var detail struct {
			Map []string `json:"map"`
		}
		raw := "odd=" + strings.Repeat("x", 600)
		if err := json.Unmarshal(usbMappingDeleteDetails(&proxmox.USBMapping{Map: []string{raw}}, false), &detail); err != nil {
			t.Fatalf("details are not JSON: %v", err)
		}
		if len(detail.Map) != 1 || utf8.RuneCountInString(detail.Map[0]) != usbMappingAuditTextMax+1 ||
			!strings.HasPrefix(detail.Map[0], "odd=xxx") {
			t.Errorf("map = %q, want the entry cut to %d and the ellipsis", detail.Map, usbMappingAuditTextMax)
		}
	})
	// Nothing but the port holds a node name to a length, so the rewritten
	// entry is bounded as a whole as well.
	t.Run("an entry long in its node name", func(t *testing.T) {
		var detail struct {
			Map []string `json:"map"`
		}
		raw := "description=" + strings.Repeat("é", 600) + ",id=1234:5678,node=" + strings.Repeat("n", 2000)
		if err := json.Unmarshal(usbMappingDeleteDetails(&proxmox.USBMapping{Map: []string{raw}}, false), &detail); err != nil {
			t.Fatalf("details are not JSON: %v", err)
		}
		if len(detail.Map) != 1 || utf8.RuneCountInString(detail.Map[0]) > 2*usbMappingAuditTextMax+1 {
			t.Errorf("recorded %d characters, want at most %d and the ellipsis",
				utf8.RuneCountInString(detail.Map[0]), 2*usbMappingAuditTextMax)
		}
	})
	t.Run("update", func(t *testing.T) {
		check(t, usbMappingUpdateDetails(entries, &long), true)
	})
	t.Run("delete", func(t *testing.T) {
		check(t, usbMappingDeleteDetails(&proxmox.USBMapping{ID: "usbdev01", Description: long, Map: entries}, false), true)
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
		// The first one: Proxmox's own parse refuses the entry, and its check
		// on that node then reports no entry there, which is what is shown.
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

// fakeMappingReader answers the listing, node and guest reads from fixtures,
// and records what it was asked.
type fakeMappingReader struct {
	nodes    []proxmox.NodeListEntry
	nodesErr error

	// listings is keyed by check node, "" for the plain listing.
	listings map[string][]proxmox.USBMapping
	listErrs map[string]error

	resources    []proxmox.ClusterResource
	resourcesErr error
	configs      map[int]proxmox.VMConfig
	configErrs   map[int]error

	// hang lists the check nodes and vmids whose call blocks until its
	// context ends, and hangResources the guest list; delay slows every
	// other call.
	hangNodes     map[string]bool
	hangVMIDs     map[int]bool
	hangResources bool
	delay         time.Duration

	// deleteErr answers DeleteUSBMapping; afterDelete, when set, replaces
	// the plain listing once a delete has been made.
	deleteErr   error
	afterDelete []proxmox.USBMapping

	mu          sync.Mutex
	calls       []string
	deleted     bool
	asked       []string
	read        []int
	inFlight    int
	maxInFlight int
}

func (f *fakeMappingReader) enter() {
	f.mu.Lock()
	f.inFlight++
	f.maxInFlight = max(f.maxInFlight, f.inFlight)
	f.mu.Unlock()
}

func (f *fakeMappingReader) leave() {
	f.mu.Lock()
	f.inFlight--
	f.mu.Unlock()
}

func (f *fakeMappingReader) GetNodes(context.Context) ([]proxmox.NodeListEntry, error) {
	return f.nodes, f.nodesErr
}

func (f *fakeMappingReader) ListUSBMappings(ctx context.Context, checkNode string) ([]proxmox.USBMapping, error) {
	if checkNode == "" {
		f.mu.Lock()
		f.calls = append(f.calls, "list")
		deleted := f.deleted
		f.mu.Unlock()
		if deleted && f.afterDelete != nil {
			return f.afterDelete, nil
		}
	}
	if checkNode != "" {
		f.enter()
		defer f.leave()
		f.mu.Lock()
		f.asked = append(f.asked, checkNode)
		f.mu.Unlock()
		if f.hangNodes[checkNode] {
			<-ctx.Done()
			return nil, fmt.Errorf("%w: %v", proxmox.ErrConnectionFailed, ctx.Err())
		}
		time.Sleep(f.delay)
	}
	if err := f.listErrs[checkNode]; err != nil {
		return nil, err
	}
	return f.listings[checkNode], nil
}

func (f *fakeMappingReader) GetClusterResources(ctx context.Context, _ string) ([]proxmox.ClusterResource, error) {
	if f.hangResources {
		<-ctx.Done()
		return nil, fmt.Errorf("%w: %v", proxmox.ErrConnectionFailed, ctx.Err())
	}
	return f.resources, f.resourcesErr
}

func (f *fakeMappingReader) DeleteUSBMapping(context.Context, string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "delete")
	if f.deleteErr != nil {
		return f.deleteErr
	}
	f.deleted = true
	return nil
}

func (f *fakeMappingReader) GetVMConfig(ctx context.Context, _ string, vmid int) (proxmox.VMConfig, error) {
	f.enter()
	defer f.leave()
	f.mu.Lock()
	f.read = append(f.read, vmid)
	f.mu.Unlock()
	if f.hangVMIDs[vmid] {
		<-ctx.Done()
		return nil, fmt.Errorf("%w: %v", proxmox.ErrConnectionFailed, ctx.Err())
	}
	time.Sleep(f.delay)
	if err := f.configErrs[vmid]; err != nil {
		return nil, err
	}
	return f.configs[vmid], nil
}

var (
	_ usbMappingReader  = (*proxmox.Client)(nil)
	_ usbMappingDeleter = (*proxmox.Client)(nil)
)

func onlineNodes(names ...string) []proxmox.NodeListEntry {
	out := make([]proxmox.NodeListEntry, 0, len(names))
	for _, n := range names {
		out = append(out, proxmox.NodeListEntry{Node: n, Status: "online"})
	}
	return out
}

func TestCheckUSBMappingNodes(t *testing.T) {
	old := usbMappingCheckTimeout
	usbMappingCheckTimeout = 200 * time.Millisecond
	t.Cleanup(func() { usbMappingCheckTimeout = old })

	listing := []proxmox.USBMapping{{ID: "usbdev01", Digest: "d1", Errors: []proxmox.MappingCheck{}}}
	f := &fakeMappingReader{
		nodes: append(onlineNodes("pve-01", "pve-04", "pve-05"),
			proxmox.NodeListEntry{Node: "pve-02", Status: "offline"},
			proxmox.NodeListEntry{Node: "pve-03", Status: "unknown"}),
		listings: map[string][]proxmox.USBMapping{"pve-01": listing},
		listErrs: map[string]error{
			"pve-04": &proxmox.APIError{StatusCode: 500, Message: `{"data":null,"message":"hostname lookup 'pve-04' failed\n"}`},
		},
		hangNodes: map[string]bool{"pve-05": true},
	}

	got := checkUSBMappingNodes(context.Background(), f, []string{"pve-01", "pve-02", "pve-03", "pve-04", "pve-05", "pve-09"})

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
	// Only the online members are asked.
	slices.Sort(f.asked)
	if want := []string{"pve-01", "pve-04", "pve-05"}; !slices.Equal(f.asked, want) {
		t.Errorf("asked %v, want %v", f.asked, want)
	}
}

// Without the cluster's node list no node is asked: an entry may name any
// host, and Proxmox would resolve a non-member through DNS and connect to it.
// Every node still gets its reason — none reads as checked.
func TestCheckUSBMappingNodes_WithoutTheNodeListAsksNone(t *testing.T) {
	f := &fakeMappingReader{
		nodesErr: proxmox.ErrConnectionFailed,
		listings: map[string][]proxmox.USBMapping{"pve-01": {}, "pve-02": {}},
	}
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

// The listing's checks share one budget: a node not answered, or not even
// asked, by then is reported out of time — never left out, never clean.
func TestCheckUSBMappingNodes_BudgetRunsOut(t *testing.T) {
	oldTimeout, oldBudget := usbMappingCheckTimeout, usbMappingCheckBudget
	usbMappingCheckTimeout, usbMappingCheckBudget = 5*time.Second, 150*time.Millisecond
	t.Cleanup(func() { usbMappingCheckTimeout, usbMappingCheckBudget = oldTimeout, oldBudget })

	nodes := make([]string, 0, 3*usbMappingCheckConcurrency)
	f := &fakeMappingReader{hangNodes: map[string]bool{}}
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
	// Only the first wave was ever asked: the rest waited for a worker, and
	// the budget was gone by the time one was free.
	if len(f.asked) != usbMappingCheckConcurrency {
		t.Errorf("asked %d nodes, want %d", len(f.asked), usbMappingCheckConcurrency)
	}
}

func TestCheckUSBMappingNodes_BoundsConcurrency(t *testing.T) {
	nodes := make([]string, 0, 12)
	for i := range 12 {
		nodes = append(nodes, fmt.Sprintf("pve-%02d", i+1))
	}
	f := &fakeMappingReader{nodes: onlineNodes(nodes...), delay: 20 * time.Millisecond}
	got := checkUSBMappingNodes(context.Background(), f, nodes)
	if len(got) != len(nodes) {
		t.Fatalf("got %d results, want %d", len(got), len(nodes))
	}
	if f.maxInFlight > usbMappingCheckConcurrency {
		t.Errorf("%d checks ran at once, want at most %d", f.maxInFlight, usbMappingCheckConcurrency)
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
		// Clean: Proxmox reports nothing. Its nil Errors must still read as
		// a clean check, not as a missing one.
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

	empty := got[1]
	if empty.Map == nil || empty.NodeChecks == nil || empty.Unchecked == nil {
		t.Errorf("mapping with no entries = %#v, want empty, non-nil collections", empty)
	}
}

func usageGuest(vmid int, node, name, typ string) proxmox.ClusterResource {
	return proxmox.ClusterResource{Type: typ, VMID: vmid, Node: node, Name: name}
}

func TestScanUSBMappingUsage(t *testing.T) {
	oldTimeout := usbMappingUsageTimeout
	usbMappingUsageTimeout = 200 * time.Millisecond
	t.Cleanup(func() { usbMappingUsageTimeout = oldTimeout })

	f := &fakeMappingReader{
		nodes: append(onlineNodes("pve-01", "pve-02"), proxmox.NodeListEntry{Node: "pve-03", Status: "offline"}),
		// Out of order, so the sort is tested; and the guest on the offline
		// node after two whose reads fail, so its unchecked entry is written
		// while theirs are — which -race reports if the two are not
		// synchronised.
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
			// Another mapping, a raw device, a mapping only in a PCI key, and
			// the id in another case: none of them uses it.
			102: {"usb0": "mapping=usbdev011", "usb1": "host=1-2", "hostpci0": "mapping=usbdev01",
				"usb2": "mapping=USBDEV01"},
			103: {},
		},
		configErrs: map[int]error{
			106: &proxmox.APIError{StatusCode: 500, Message: `{"data":null,"message":"Configuration file 'nodes/pve-02/qemu-server/106.conf' does not exist\n"}`},
		},
		hangVMIDs: map[int]bool{107: true},
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
		{VMID: 106, Name: "win06", Node: "pve-02",
			Reason: "Configuration file 'nodes/pve-02/qemu-server/106.conf' does not exist"},
		{VMID: 107, Name: "linux07", Node: "pve-02", Reason: "No answer within 200ms."},
	}
	if !reflect.DeepEqual(usage.Unchecked, wantUnchecked) {
		t.Errorf("unchecked = %+v, want %+v", usage.Unchecked, wantUnchecked)
	}
	if usage.Checked != 4 {
		t.Errorf("checked = %d, want 4 (101, 102, 103, 105)", usage.Checked)
	}
	// Neither the container nor the guest on the offline node is read.
	slices.Sort(f.read)
	if want := []int{101, 102, 103, 105, 106, 107}; !slices.Equal(f.read, want) {
		t.Errorf("read %v, want %v", f.read, want)
	}
}

// The guest list is the scan: without it there is nothing to say, so the
// whole request fails rather than answering "no users".
func TestScanUSBMappingUsage_FailsWithoutTheGuestList(t *testing.T) {
	f := &fakeMappingReader{resourcesErr: proxmox.ErrConnectionFailed}
	if _, err := scanUSBMappingUsage(context.Background(), f, "usbdev01"); !errors.Is(err, proxmox.ErrConnectionFailed) {
		t.Errorf("err = %v, want the listing's error", err)
	}
}

func TestScanUSBMappingUsage_WithoutNodeStatusReadsEveryGuest(t *testing.T) {
	f := &fakeMappingReader{
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

// A guest not read within the scan's budget is reported unchecked with the
// budget as its reason — never as a guest that does not use the mapping, and
// never blamed on its own timeout.
func TestScanUSBMappingUsage_BudgetRunsOut(t *testing.T) {
	oldTimeout, oldBudget := usbMappingUsageTimeout, usbMappingUsageBudget
	usbMappingUsageTimeout, usbMappingUsageBudget = 5*time.Second, 150*time.Millisecond
	t.Cleanup(func() { usbMappingUsageTimeout, usbMappingUsageBudget = oldTimeout, oldBudget })

	f := &fakeMappingReader{nodes: onlineNodes("pve-01"), hangVMIDs: map[int]bool{}}
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

func TestScanUSBMappingUsage_BoundsConcurrency(t *testing.T) {
	f := &fakeMappingReader{nodes: onlineNodes("pve-01"), delay: 10 * time.Millisecond}
	for vmid := 101; vmid <= 130; vmid++ {
		f.resources = append(f.resources, usageGuest(vmid, "pve-01", "linux01", "qemu"))
	}
	usage, err := scanUSBMappingUsage(context.Background(), f, "usbdev01")
	if err != nil {
		t.Fatalf("scanUSBMappingUsage: %v", err)
	}
	if usage.Checked != 30 {
		t.Errorf("checked = %d, want 30", usage.Checked)
	}
	if f.maxInFlight > usbMappingUsageConcurrency {
		t.Errorf("%d reads ran at once, want at most %d", f.maxInFlight, usbMappingUsageConcurrency)
	}
}

// The guest list is read within the scan's budget too, so a Proxmox that
// never answers it cannot hold the delete dialog for the client's timeout.
func TestScanUSBMappingUsage_GuestListIsInsideTheBudget(t *testing.T) {
	oldBudget := usbMappingUsageBudget
	usbMappingUsageBudget = 100 * time.Millisecond
	t.Cleanup(func() { usbMappingUsageBudget = oldBudget })

	f := &fakeMappingReader{hangResources: true}
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
	t.Run("every item once, at most workers at a time", func(t *testing.T) {
		var mu sync.Mutex
		var inFlight, maxInFlight int
		seen := map[int]int{}
		items := make([]int, 0, 30)
		for i := range 30 {
			items = append(items, i)
		}
		eachWithin(context.Background(), 4, items, func(i int) {
			mu.Lock()
			inFlight++
			maxInFlight = max(maxInFlight, inFlight)
			seen[i]++
			mu.Unlock()
			time.Sleep(5 * time.Millisecond)
			mu.Lock()
			inFlight--
			mu.Unlock()
		}, func(int) {
			t.Error("an item was skipped with the budget unspent")
		})
		if maxInFlight > 4 {
			t.Errorf("%d ran at once, want at most 4", maxInFlight)
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

// usbMappingUsageMirror is the usage route's declared parameters, as
// withRequestParams needs them; registry_mappings.go holds the declaration.
func usbMappingUsageMirror(t *testing.T) apischema.Properties {
	t.Helper()
	return compiledMirror(t, apischema.Properties{
		"cluster_id": apischema.StdOption("cluster-id"),
		"mapping_id": {Type: apischema.String, MaxLength: apischema.Ptr(128)},
	})
}

// A caller over a cap on scans in flight gets 429 at once — before any
// Proxmox client is made (this handler has none to make: it would panic), so
// nothing is read on its behalf.
func TestGetUSBMappingUsage_CapsScansInFlight(t *testing.T) {
	cluster := uuid.New()
	for range usbMappingUsageScansPerCluster {
		_, release, err := usbMappingUsageScans.acquire(context.Background(), cluster, uuid.New())
		if err != nil {
			t.Fatalf("acquire: %v", err)
		}
		t.Cleanup(release)
	}

	app := fiber.New(fiber.Config{ErrorHandler: testErrorHandler})
	app.Get("/api/v1/clusters/:cluster_id/usb-mappings/:mapping_id/usage",
		withRequestParams(t, usbMappingUsageMirror(t), []string{"cluster_id", "mapping_id"},
			(&VMHandler{}).GetUSBMappingUsage))
	resp, err := app.Test(httptest.NewRequest(http.MethodGet,
		"/api/v1/clusters/"+cluster.String()+"/usb-mappings/usbdev01/usage", nil))
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != fiber.StatusTooManyRequests {
		t.Errorf("status = %d, want 429", resp.StatusCode)
	}
}

// Per cluster, so no caller holds what another cluster's check needs; per
// user, so no one account holds both of a cluster's — and a user's newer scan
// replaces their older one instead of being refused behind it.
func TestUsageScanSlots(t *testing.T) {
	bg := context.Background()
	refused := func(t *testing.T, err error) {
		t.Helper()
		var fe *fiber.Error
		if !errors.As(err, &fe) || fe.Code != fiber.StatusTooManyRequests {
			t.Errorf("acquire = %v, want 429", err)
		}
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
		// Bob and carol hold both slots: a double release must not have
		// counted a third free.
		_, _, err = slots.acquire(bg, cluster, uuid.New())
		refused(t, err)
	})

	t.Run("a user's newer scan replaces their older one", func(t *testing.T) {
		slots := newUsageScanSlots()
		cluster, alice := uuid.New(), uuid.New()
		olderCtx, releaseOlder, err := slots.acquire(bg, cluster, alice)
		if err != nil {
			t.Fatalf("older: %v", err)
		}
		// The older scan stops as soon as its context is cancelled — what
		// scanUSBMappingUsage does — and lets go of its slot.
		go func() {
			<-olderCtx.Done()
			releaseOlder()
		}()
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
		// A second release of the older scan — by a caller's defer, say —
		// must not free the newer one's slot.
		releaseOlder()
		if slots.byUser[alice] == nil || slots.byCluster[cluster] != 1 {
			t.Errorf("users %v, clusters %v; want the newer scan still counted", slots.byUser, slots.byCluster)
		}
	})

	// The re-check after the wait: two newer requests of one user racing for
	// the slot the older scan gives up. Whether they arrive together (one
	// gets the slot, the other a 429) or one after the other (the later one
	// replaces the earlier), the user ends with exactly ONE scan running and
	// one slot held — never two registered side by side.
	t.Run("two newer requests at once: one scan runs", func(t *testing.T) {
		type held struct {
			ctx     context.Context
			release func()
		}
		// Every scan here stops the moment it is cancelled, as a real one does.
		acquireStopping := func(slots *usageScanSlots, cluster, user uuid.UUID) (held, error) {
			ctx, release, err := slots.acquire(bg, cluster, user)
			if err != nil {
				return held{}, err
			}
			go func() {
				<-ctx.Done()
				release()
			}()
			return held{ctx, release}, nil
		}
		for range 50 {
			slots := newUsageScanSlots()
			cluster, alice := uuid.New(), uuid.New()
			if _, err := acquireStopping(slots, cluster, alice); err != nil {
				t.Fatalf("older: %v", err)
			}
			results := make(chan held, 2)
			for range 2 {
				go func() {
					h, _ := acquireStopping(slots, cluster, alice)
					results <- h
				}()
			}
			// Both answered before any is judged: the later of two that both
			// got the slot cancels the earlier one's scan as it takes over.
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

	// A request the cluster has no room for is refused BEFORE it stops the
	// user's older scan elsewhere: otherwise it would cost them both checks.
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

	// On a busy cluster — the user's older scan and someone else's — the
	// user's newer check replaces the older one: its slot is room.
	t.Run("the user's own older scan on the cluster counts as room", func(t *testing.T) {
		slots := newUsageScanSlots()
		cluster, alice := uuid.New(), uuid.New()
		olderCtx, releaseOlder, err := slots.acquire(bg, cluster, alice)
		if err != nil {
			t.Fatalf("older: %v", err)
		}
		go func() {
			<-olderCtx.Done()
			releaseOlder()
		}()
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

	// The room is looked at again after the wait: another user may have
	// taken the last slot meanwhile.
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
			// Carol takes Y's last slot while alice's newer check is still
			// waiting for her older scan to stop.
			<-olderCtx.Done()
			_, _, err := slots.acquire(bg, clusterY, uuid.New())
			carol <- err
			releaseOlder()
		}()
		// Refused for want of room — not with one of the other two 429s
		// acquire can answer (an older scan still stopping, or another of the
		// user's checks taking the slot first).
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

// The handler's whole path: the scan runs under the slot's context, so a
// user's newer check stops their older one — here an older scan stuck on 40
// guests that never answer — and goes ahead at once.
func TestCheckUSBMappingUsage_NewerReplacesOlder(t *testing.T) {
	old := usbMappingUsageSupersedeWait
	usbMappingUsageSupersedeWait = 500 * time.Millisecond
	t.Cleanup(func() { usbMappingUsageSupersedeWait = old })

	slots := newUsageScanSlots()
	cluster, alice := uuid.New(), uuid.New()
	stuck := &fakeMappingReader{nodes: onlineNodes("pve-01"), hangVMIDs: map[int]bool{}}
	for vmid := 101; vmid <= 140; vmid++ {
		stuck.resources = append(stuck.resources, usageGuest(vmid, "pve-01", "linux01", "qemu"))
		stuck.hangVMIDs[vmid] = true
	}
	quick := &fakeMappingReader{
		nodes:     onlineNodes("pve-01"),
		resources: []proxmox.ClusterResource{usageGuest(101, "pve-01", "linux01", "qemu")},
		configs:   map[int]proxmox.VMConfig{101: {"usb0": "mapping=usbdev01"}},
	}

	olderErr := make(chan error, 1)
	go func() {
		_, err := checkUSBMappingUsage(context.Background(), slots, cluster, alice, "usbdev01",
			func() (usbMappingReader, error) { return stuck, nil })
		olderErr <- err
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		stuck.mu.Lock()
		reading := stuck.inFlight > 0
		stuck.mu.Unlock()
		if reading {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the older scan never started reading")
		}
		time.Sleep(time.Millisecond)
	}

	usage, err := checkUSBMappingUsage(context.Background(), slots, cluster, alice, "usbdev01",
		func() (usbMappingReader, error) { return quick, nil })
	if err != nil {
		t.Fatalf("the newer check: %v", err)
	}
	if len(usage.Users) != 1 || usage.Users[0].VMID != 101 {
		t.Errorf("the newer check = %+v, want its own answer", usage)
	}
	select {
	case err := <-olderErr:
		if !errors.Is(err, errUSBMappingUsageSuperseded) {
			t.Errorf("the older check = %v, want errUSBMappingUsageSuperseded", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the older check never answered")
	}
}

// A check that gets a slot and then cannot make its Proxmox client — a
// cluster gone from the database, say — still gives the slot back: a leaked
// one would hold the user at "still stopping" and, twice over, the cluster at
// 429 until a restart.
func TestCheckUSBMappingUsage_ReleasesWhenTheClientFails(t *testing.T) {
	slots := newUsageScanSlots()
	cluster, alice := uuid.New(), uuid.New()
	noClient := fiber.NewError(fiber.StatusNotFound, "Cluster not found")
	_, err := checkUSBMappingUsage(context.Background(), slots, cluster, alice, "usbdev01",
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

// A replaced scan answers that it was replaced — not its partial result,
// whose unread guests would carry untrue reasons, and not the error its
// cancelled calls produced.
func TestUSBMappingUsageAnswer(t *testing.T) {
	done := usbMappingUsage{MappingID: "usbdev01", Checked: 3, Users: []usbMappingGuest{}, Unchecked: []usbMappingGuest{}}

	live := context.Background()
	if got, err := usbMappingUsageAnswer(live, done, nil); err != nil || got.Checked != 3 {
		t.Errorf("a finished scan = %+v, %v; want its result", got, err)
	}
	var fe *fiber.Error
	if _, err := usbMappingUsageAnswer(live, done, proxmox.ErrConnectionFailed); !errors.As(err, &fe) || fe.Code != fiber.StatusBadGateway {
		t.Errorf("a failed scan = %v, want the Proxmox error as a 502", err)
	}

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	for _, scanErr := range []error{nil, proxmox.ErrConnectionFailed} {
		if _, err := usbMappingUsageAnswer(cancelled, done, scanErr); !errors.Is(err, errUSBMappingUsageSuperseded) {
			t.Errorf("a replaced scan (scan error %v) = %v, want errUSBMappingUsageSuperseded", scanErr, err)
		}
	}
}

// fakeMappingUpdater records the update it was asked to make.
type fakeMappingUpdater struct {
	id     string
	params proxmox.UpdateUSBMappingParams
	err    error
}

func (f *fakeMappingUpdater) UpdateUSBMapping(_ context.Context, id string, params proxmox.UpdateUSBMappingParams) error {
	f.id, f.params = id, params
	return f.err
}

// usbMappingUpdateMirror is the update route's declared parameters, as the
// request helpers read them; registry_mappings.go holds the declaration.
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
	f := &fakeMappingUpdater{}
	id, params, err := updateUSBMappingRequest(context.Background(), f, p)
	if err != nil {
		t.Fatalf("updateUSBMappingRequest: %v", err)
	}
	if id != "usbdev01" || f.id != "usbdev01" || f.params.Digest != "d1" ||
		!slices.Equal(f.params.Map, []string{"node=pve-01,id=1234:5678"}) || f.params.Description != nil {
		t.Errorf("sent %q %+v (returned %q %+v), want the route's values and no description", f.id, f.params, id, params)
	}

	// Proxmox's stale-digest die comes back as the route's 409.
	f.err = &proxmox.APIError{StatusCode: 500, Message: "update hardware mapping failed: detected modified configuration"}
	var fe *fiber.Error
	if _, _, err := updateUSBMappingRequest(context.Background(), f, p); !errors.As(err, &fe) || fe.Code != fiber.StatusConflict {
		t.Errorf("err = %v, want a 409", err)
	}
}

// The route's digest reaches the check that uses it.
func TestDeleteUSBMappingRequest(t *testing.T) {
	props := compiledMirror(t, apischema.Properties{
		"mapping_id": {Type: apischema.String},
		"digest":     {Type: apischema.String, Optional: true},
	})
	listing := []proxmox.USBMapping{{ID: "usbdev01", Digest: "d2", Map: []string{"id=1234:5678,node=pve-01"}}}
	for _, tt := range []struct {
		name      string
		body      map[string]any
		wantCode  int
		wantCalls []string
	}{
		{"a stale digest is compared", map[string]any{"mapping_id": "usbdev01", "digest": "d1"},
			fiber.StatusConflict, []string{"list"}},
		{"a current one lets it through", map[string]any{"mapping_id": "usbdev01", "digest": "d2"},
			0, []string{"list", "delete"}},
		{"none is Proxmox's own delete", map[string]any{"mapping_id": "usbdev01"},
			0, []string{"list", "delete"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			p, err := props.Validate(tt.body)
			if err != nil {
				t.Fatalf("Validate: %v", err)
			}
			f := &fakeMappingReader{listings: map[string][]proxmox.USBMapping{"": listing}}
			id, outcome, err := deleteUSBMappingRequest(context.Background(), f, p)
			if !slices.Equal(f.calls, tt.wantCalls) {
				t.Errorf("calls = %v, want %v", f.calls, tt.wantCalls)
			}
			if tt.wantCode != 0 {
				var fe *fiber.Error
				if !errors.As(err, &fe) || fe.Code != tt.wantCode {
					t.Errorf("err = %v, want a %d", err, tt.wantCode)
				}
				return
			}
			if err != nil || id != "usbdev01" || outcome.action != "deleted" {
				t.Errorf("= %q, %+v, %v; want usbdev01 deleted", id, outcome, err)
			}
		})
	}
}

// The description reaches the client only when the caller sent one: a
// non-nil empty one is delete=description, so passing one on every update
// would wipe the description on each Add node, Replace and Remove.
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
			switch {
			case tt.want == nil && got.Description != nil:
				t.Errorf("description = %q, want it left alone (nil)", *got.Description)
			case tt.want != nil && (got.Description == nil || *got.Description != *tt.want):
				t.Errorf("description = %v, want %q", got.Description, *tt.want)
			}
		})
	}
}

func TestDeleteUSBMapping_Flow(t *testing.T) {
	mapping := proxmox.USBMapping{ID: "usbdev01", Digest: "d1", Map: []string{"id=1234:5678,node=pve-01"}}
	listing := []proxmox.USBMapping{mapping}
	for _, tt := range []struct {
		name        string
		fake        *fakeMappingReader
		digest      string
		wantCode    int // 0: the delete goes ahead
		wantCalls   []string
		wantAction  string
		wantUnknown bool
		wantEntries bool
	}{
		{
			// The snapshot comes FIRST: read after the delete it would find
			// nothing, and every delete would be recorded as a no-op.
			name:        "snapshot, then delete",
			fake:        &fakeMappingReader{listings: map[string][]proxmox.USBMapping{"": listing}, afterDelete: []proxmox.USBMapping{}},
			wantCalls:   []string{"list", "delete"},
			wantAction:  "deleted",
			wantEntries: true,
		},
		{
			name:        "a matching digest",
			fake:        &fakeMappingReader{listings: map[string][]proxmox.USBMapping{"": listing}},
			digest:      "d1",
			wantCalls:   []string{"list", "delete"},
			wantAction:  "deleted",
			wantEntries: true,
		},
		{
			// usb.cfg changed since the caller's listing: nothing is deleted.
			name:      "a stale digest",
			fake:      &fakeMappingReader{listings: map[string][]proxmox.USBMapping{"": listing}},
			digest:    "d0",
			wantCode:  fiber.StatusConflict,
			wantCalls: []string{"list"},
		},
		{
			// Asked to compare and could not look: it does not go ahead blind.
			name:      "a digest, and no snapshot",
			fake:      &fakeMappingReader{listErrs: map[string]error{"": proxmox.ErrConnectionFailed}},
			digest:    "d1",
			wantCode:  fiber.StatusBadGateway,
			wantCalls: []string{"list"},
		},
		{
			// Without a digest it is Proxmox's delete, recorded with its doubt.
			name:        "no digest, and no snapshot",
			fake:        &fakeMappingReader{listErrs: map[string]error{"": proxmox.ErrConnectionFailed}},
			wantCalls:   []string{"list", "delete"},
			wantAction:  "deleted",
			wantUnknown: true,
		},
		{
			// No mapping at all: no digest to compare, so the idempotent no-op.
			name:       "already gone, and usb.cfg empty",
			fake:       &fakeMappingReader{listings: map[string][]proxmox.USBMapping{"": {}}},
			digest:     "d1",
			wantCalls:  []string{"list", "delete"},
			wantAction: "already_deleted",
		},
		{
			// Gone, and usb.cfg still has the caller's digest: nothing changed
			// but... nothing — the caller's view holds it absent too.
			name: "already gone, usb.cfg unchanged",
			fake: &fakeMappingReader{listings: map[string][]proxmox.USBMapping{"": {
				{ID: "usbdev02", Digest: "d1", Map: []string{"id=abcd:ef01,node=pve-01"}}}}},
			digest:     "d1",
			wantCalls:  []string{"list", "delete"},
			wantAction: "already_deleted",
		},
		{
			// Gone since the caller's read — deleted, perhaps made again in a
			// moment: the digest is compared all the same.
			name: "gone since the caller's read",
			fake: &fakeMappingReader{listings: map[string][]proxmox.USBMapping{"": {
				{ID: "usbdev02", Digest: "d2", Map: []string{"id=abcd:ef01,node=pve-01"}}}}},
			digest:    "d1",
			wantCode:  fiber.StatusConflict,
			wantCalls: []string{"list"},
		},
		{
			name:      "Proxmox refuses the delete",
			fake:      &fakeMappingReader{listings: map[string][]proxmox.USBMapping{"": listing}, deleteErr: proxmox.ErrForbidden},
			wantCode:  fiber.StatusForbidden,
			wantCalls: []string{"list", "delete"},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			outcome, err := deleteUSBMapping(context.Background(), tt.fake, "usbdev01", tt.digest)
			if !slices.Equal(tt.fake.calls, tt.wantCalls) {
				t.Errorf("calls = %v, want %v", tt.fake.calls, tt.wantCalls)
			}
			if tt.wantCode != 0 {
				var fe *fiber.Error
				if !errors.As(err, &fe) || fe.Code != tt.wantCode {
					t.Fatalf("err = %v, want a %d", err, tt.wantCode)
				}
				return
			}
			if err != nil {
				t.Fatalf("deleteUSBMapping: %v", err)
			}
			if outcome.action != tt.wantAction || outcome.priorStateUnknown != tt.wantUnknown {
				t.Errorf("outcome = (%q, %v), want (%q, %v)", outcome.action, outcome.priorStateUnknown,
					tt.wantAction, tt.wantUnknown)
			}
			if got := outcome.snapshot != nil && len(outcome.snapshot.Map) == 1; got != tt.wantEntries {
				t.Errorf("snapshot holds the entries = %v, want %v", got, tt.wantEntries)
			}
		})
	}
}
