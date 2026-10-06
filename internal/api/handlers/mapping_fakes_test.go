package handlers

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"

	"github.com/gofiber/fiber/v3"

	"github.com/bigjakk/nexara/internal/proxmox"
)

// mappingFakeClient stands in for the Proxmox client under every mapping flow,
// USB and PCI: each flow's interface (usbMappingReader, pciMappingEditor, …) is
// satisfied by it, and its calls are logged in order in calls so a test can say
// what a flow did and in what order. A field a test leaves unset answers empty.
// What it models of Proxmox, and the flows rely on: the mapping file after a delete
// (afterDelete replaces the plain USB listing once DeleteUSBMapping has succeeded),
// a check-node listing apart from the plain one, and a call that never answers until
// its context ends (the client's ErrConnectionFailed).
type mappingFakeClient struct {
	nodes    []proxmox.NodeListEntry
	nodesErr error

	// listings is the USB listing by check node, "" for the plain one; the PCI
	// pciListing is the plain one, which pciChecked overrides per check node.
	listings   map[string][]proxmox.USBMapping
	listErrs   map[string]error
	pciListing []proxmox.PCIMapping
	pciChecked map[string][]proxmox.PCIMapping
	pciListErr error

	devices    map[string][]proxmox.NodePCIDevice
	devicesErr error

	resources    []proxmox.ClusterResource
	resourcesErr error
	configs      map[int]proxmox.VMConfig
	configErrs   map[int]error

	// hangNodes and hangVMIDs name the check nodes and guests whose call blocks
	// until its context ends, hangResources the guest list and hangDevices the
	// device read.
	hangNodes     map[string]bool
	hangVMIDs     map[int]bool
	hangResources bool
	hangDevices   bool

	// deleteErr, updateErr and createErr answer the writes; afterDelete, when
	// set, replaces the plain USB listing once a delete has been made.
	deleteErr   error
	updateErr   error
	createErr   error
	afterDelete []proxmox.USBMapping

	mu          sync.Mutex
	calls       []string
	asked       []string // the check nodes ListUSBMappings was asked about
	read        []int    // the guests whose config was read
	deleted     bool
	inFlight    int
	maxInFlight int
	reading     chan struct{}
	readingDone bool

	created     []proxmox.CreatePCIMappingParams
	updated     []proxmox.UpdatePCIMappingParams
	usbUpdateID string
	usbUpdate   proxmox.UpdateUSBMappingParams
}

var (
	_ usbMappingReader  = (*proxmox.Client)(nil)
	_ usbMappingDeleter = (*proxmox.Client)(nil)
	_ pciMappingEditor  = (*proxmox.Client)(nil)
	_ pciMappingReader  = (*proxmox.Client)(nil)
	_ pciMappingDeleter = (*proxmox.Client)(nil)
)

func (f *mappingFakeClient) logCall(call string) {
	f.mu.Lock()
	f.calls = append(f.calls, call)
	f.mu.Unlock()
}

func (f *mappingFakeClient) enter() {
	f.mu.Lock()
	f.inFlight++
	f.maxInFlight = max(f.maxInFlight, f.inFlight)
	f.mu.Unlock()
}

func (f *mappingFakeClient) leave() {
	f.mu.Lock()
	f.inFlight--
	f.mu.Unlock()
}

// firstRead is closed when the first guest config read begins, so a test that
// needs a scan in flight waits on it and not on a poll. Ask for it before
// starting the scan.
func (f *mappingFakeClient) firstRead() <-chan struct{} {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.reading == nil {
		f.reading = make(chan struct{})
	}
	return f.reading
}

func mappingHangUntilDone(ctx context.Context) error {
	<-ctx.Done()
	return fmt.Errorf("%w: %v", proxmox.ErrConnectionFailed, ctx.Err())
}

func (f *mappingFakeClient) GetNodes(context.Context) ([]proxmox.NodeListEntry, error) {
	return f.nodes, f.nodesErr
}

func (f *mappingFakeClient) GetClusterResources(ctx context.Context, _ string) ([]proxmox.ClusterResource, error) {
	if f.hangResources {
		return nil, mappingHangUntilDone(ctx)
	}
	return f.resources, f.resourcesErr
}

func (f *mappingFakeClient) GetVMConfig(ctx context.Context, _ string, vmid int) (proxmox.VMConfig, error) {
	f.enter()
	defer f.leave()
	f.mu.Lock()
	f.read = append(f.read, vmid)
	if f.reading == nil {
		f.reading = make(chan struct{})
	}
	if !f.readingDone {
		f.readingDone = true
		close(f.reading)
	}
	f.mu.Unlock()
	if f.hangVMIDs[vmid] {
		return nil, mappingHangUntilDone(ctx)
	}
	if err := f.configErrs[vmid]; err != nil {
		return nil, err
	}
	return f.configs[vmid], nil
}

func (f *mappingFakeClient) ListUSBMappings(ctx context.Context, checkNode string) ([]proxmox.USBMapping, error) {
	if checkNode == "" {
		f.mu.Lock()
		f.calls = append(f.calls, "list")
		deleted := f.deleted
		f.mu.Unlock()
		if deleted && f.afterDelete != nil {
			return f.afterDelete, nil
		}
	} else {
		f.enter()
		defer f.leave()
		f.mu.Lock()
		f.asked = append(f.asked, checkNode)
		f.mu.Unlock()
		if f.hangNodes[checkNode] {
			return nil, mappingHangUntilDone(ctx)
		}
	}
	if err := f.listErrs[checkNode]; err != nil {
		return nil, err
	}
	return f.listings[checkNode], nil
}

func (f *mappingFakeClient) UpdateUSBMapping(_ context.Context, id string, params proxmox.UpdateUSBMappingParams) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.usbUpdateID, f.usbUpdate = id, params
	return f.updateErr
}

func (f *mappingFakeClient) DeleteUSBMapping(context.Context, string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "delete")
	if f.deleteErr != nil {
		return f.deleteErr
	}
	f.deleted = true
	return nil
}

func (f *mappingFakeClient) ListPCIMappings(_ context.Context, checkNode string) ([]proxmox.PCIMapping, error) {
	f.logCall("list:" + checkNode)
	if f.pciListErr != nil {
		return nil, f.pciListErr
	}
	if checked, ok := f.pciChecked[checkNode]; ok {
		return checked, nil
	}
	return f.pciListing, nil
}

func (f *mappingFakeClient) ListNodePCIDevicesAllClasses(ctx context.Context, node string) ([]proxmox.NodePCIDevice, error) {
	f.logCall("devices:" + node)
	if f.hangDevices {
		return nil, mappingHangUntilDone(ctx)
	}
	return f.devices[node], f.devicesErr
}

func (f *mappingFakeClient) CreatePCIMapping(_ context.Context, params proxmox.CreatePCIMappingParams) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "create:"+params.ID)
	f.created = append(f.created, params)
	return f.createErr
}

func (f *mappingFakeClient) UpdatePCIMapping(_ context.Context, id string, params proxmox.UpdatePCIMappingParams) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "update:"+id)
	f.updated = append(f.updated, params)
	return f.updateErr
}

func (f *mappingFakeClient) DeletePCIMapping(_ context.Context, id string) error {
	f.logCall("delete:" + id)
	return f.deleteErr
}

// requireCalls fails unless the calls the flow made are exactly want, in order.
func (f *mappingFakeClient) requireCalls(t *testing.T, want ...string) {
	t.Helper()
	f.mu.Lock()
	got := slices.Clone(f.calls)
	f.mu.Unlock()
	if !slices.Equal(got, want) {
		t.Errorf("calls = %v, want %v", got, want)
	}
}

func onlineNodes(names ...string) []proxmox.NodeListEntry {
	out := make([]proxmox.NodeListEntry, 0, len(names))
	for _, n := range names {
		out = append(out, proxmox.NodeListEntry{Node: n, Status: "online"})
	}
	return out
}

func usageGuest(vmid int, node, name, typ string) proxmox.ClusterResource {
	return proxmox.ClusterResource{Type: typ, VMID: vmid, Node: node, Name: name}
}

// memberOf is a membership check that knows exactly these nodes.
func memberOf(nodes ...string) func(context.Context, string) (bool, error) {
	return func(_ context.Context, node string) (bool, error) {
		return slices.Contains(nodes, node), nil
	}
}

// wantStatus fails unless err is a *fiber.Error with the status want.
func wantStatus(t *testing.T, err error, want int) {
	t.Helper()
	var fe *fiber.Error
	if !errors.As(err, &fe) || fe.Code != want {
		t.Fatalf("err = %v, want a %d", err, want)
	}
}
