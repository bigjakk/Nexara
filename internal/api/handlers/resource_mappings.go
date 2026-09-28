package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/bigjakk/nexara/internal/api/apischema"
	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/proxmox"
)

// The routes are declared in internal/api/registry_mappings.go, which states
// their permissions and parameters and records why they exist: a token can
// pass a host device through only by mapping. What stays here is the audit
// rows, the mapping of Proxmox's die strings onto 404 and 409, and the two
// reads Proxmox has no single call for — every mapping checked on every node
// it names, and which guests use a mapping.

// errMappingNodeNotMember answers a mapping route for a node the cluster does
// not have.
var errMappingNodeNotMember = fiber.NewError(fiber.StatusNotFound, "Node not found in this cluster")

// nodeLookup is the query nodeMembership asks.
type nodeLookup interface {
	GetNodeByClusterAndName(ctx context.Context, arg db.GetNodeByClusterAndNameParams) (db.Node, error)
}

// nodeMembership says whether a node is one of clusterID's, for the mapping
// routes whose Proxmox call pveproxy forwards to the node by name. Only "no
// such row" is "not a member"; a lookup that fails is a failure, never a
// yes.
func nodeMembership(q nodeLookup, clusterID uuid.UUID) func(context.Context, string) (bool, error) {
	return func(ctx context.Context, node string) (bool, error) {
		_, err := q.GetNodeByClusterAndName(ctx, db.GetNodeByClusterAndNameParams{ClusterID: clusterID, Name: node})
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		if err != nil {
			return false, fiber.NewError(fiber.StatusInternalServerError, "Failed to look up the node")
		}
		return true, nil
	}
}

// nodeInCluster is nodeMembership over the handler's queries.
func (h *VMHandler) nodeInCluster(clusterID uuid.UUID) func(context.Context, string) (bool, error) {
	return nodeMembership(h.queries, clusterID)
}

// listNodeMappings lists a kind's mappings checked against node, once node is
// known to be one of the cluster's. Nothing is sent before then: Proxmox runs
// the check on the node itself (the listing's proxyto_callback), and pveproxy
// resolves the host to forward to from the name before it validates it — a
// name that is no member would have the node connect wherever it resolves.
func listNodeMappings[T any](ctx context.Context, isMember func(context.Context, string) (bool, error), node string,
	list func(context.Context, string) ([]T, error)) ([]T, error) {
	member, err := isMember(ctx, node)
	if err != nil {
		return nil, err
	}
	if !member {
		return nil, errMappingNodeNotMember
	}
	mappings, err := list(ctx, node)
	if err != nil {
		return nil, mapProxmoxError(err)
	}
	return mappings, nil
}

// ListNodeUSBMappings handles GET /api/v1/clusters/:cluster_id/nodes/:node_name/usb-mappings.
func (h *VMHandler) ListNodeUSBMappings(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	mappings, err := listNodeMappings(c.Context(), h.nodeInCluster(clusterID), p.String("node_name"), pxClient.ListUSBMappings)
	if err != nil {
		return err
	}
	return RespondItems(c, mappings)
}

// CreateUSBMapping handles POST /api/v1/clusters/:cluster_id/usb-mappings.
func (h *VMHandler) CreateUSBMapping(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}
	req := proxmox.CreateUSBMappingParams{
		ID:          p.String("mapping_id"),
		Description: p.String("description"),
		Node:        p.String("node"),
		DeviceID:    p.String("device_id"),
		Path:        p.String("path"),
	}

	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	if err := pxClient.CreateUSBMapping(c.Context(), req); err != nil {
		return mapMappingCreateError("USB", err)
	}

	// The device id as Proxmox stores it — the client lowercases it — and the
	// port only when there is one.
	audit := map[string]string{
		"mapping_id": req.ID,
		"node":       req.Node,
		"device_id":  strings.ToLower(req.DeviceID),
	}
	if req.Path != "" {
		audit["path"] = req.Path
	}
	details, _ := json.Marshal(audit)
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), "usb_mapping", req.ID, "created", details)
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"status": "ok"})
}

// mappingTakenPhrases is how Proxmox refuses a mapping id that is taken, for
// either kind: pve-manager PVE/API2/Cluster/Mapping/USB.pm's create dies
// "usb ID '$id' already defined" inside lock_usb_config, which re-dies it as
// "create hardware mapping failed: usb ID '…' already defined" — a plain 500 —
// and PCI.pm's dies "pci ID '$id' already defined" the same way. It says
// "already defined", not "already exists", so mapDuplicateNameError cannot see
// it. A mapping id is a pve-configid and cannot hold a space, so the id
// Proxmox echoes back can never be what matches.
var mappingTakenPhrases = []string{"already defined"}

// mapMappingCreateError answers a taken mapping id with 409, where
// mapProxmoxError alone would report the cluster as failing. kind is "USB" or
// "PCI", for the message.
func mapMappingCreateError(kind string, err error) error {
	return mapProxmoxDieError(fiber.StatusConflict,
		"A "+kind+" mapping with that ID already exists", mappingTakenPhrases, err)
}

// --- PCI mappings ---------------------------------------------------------------

// ListNodePCIMappings handles GET /api/v1/clusters/:cluster_id/nodes/:node_name/pci-mappings.
func (h *VMHandler) ListNodePCIMappings(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	mappings, err := listNodeMappings(c.Context(), h.nodeInCluster(clusterID), p.String("node_name"), pxClient.ListPCIMappings)
	if err != nil {
		return err
	}
	return RespondItems(c, mappings)
}

// pciMappingCreator is what a PCI mapping create needs of the Proxmox client.
type pciMappingCreator interface {
	ListNodePCIDevicesAllClasses(ctx context.Context, node string) ([]proxmox.NodePCIDevice, error)
	CreatePCIMapping(ctx context.Context, params proxmox.CreatePCIMappingParams) error
}

// createPCIMapping creates mapping id with one entry for node's device at
// path, the entry built from the node's own report of the device
// (proxmox.PCIMapEntryForDevice says why nothing else will do), and returns
// what it created.
//
// isMember says whether node is one of the cluster's, and is asked before
// anything is sent: reading the node's devices is a /nodes/{node} call, which
// pveproxy forwards to whatever host the name resolves to — a name that is no
// member, and not only a malformed one, would have the node connect out where
// the caller chose, and answer with what it found there.
func createPCIMapping(ctx context.Context, px pciMappingCreator, isMember func(context.Context, string) (bool, error),
	id, node, path, description string) (proxmox.CreatePCIMappingParams, error) {
	member, err := isMember(ctx, node)
	if err != nil {
		return proxmox.CreatePCIMappingParams{}, err
	}
	if !member {
		return proxmox.CreatePCIMappingParams{}, errMappingNodeNotMember
	}
	devices, err := px.ListNodePCIDevicesAllClasses(ctx, node)
	if err != nil {
		return proxmox.CreatePCIMappingParams{}, mapProxmoxError(err)
	}
	entry, mdev, err := proxmox.PCIMapEntryForDevice(node, path, devices)
	if err != nil {
		return proxmox.CreatePCIMappingParams{}, mapProxmoxError(err)
	}
	params := proxmox.CreatePCIMappingParams{ID: id, Description: description, Entry: entry, MDev: mdev}
	if err := px.CreatePCIMapping(ctx, params); err != nil {
		return proxmox.CreatePCIMappingParams{}, mapMappingCreateError("PCI", err)
	}
	return params, nil
}

// pciMappingCreateDetails is the audit row of a created PCI mapping: the
// entry as it was written, which is enough to recreate it.
func pciMappingCreateDetails(params proxmox.CreatePCIMappingParams) json.RawMessage {
	detail := map[string]any{
		"mapping_id": params.ID,
		"node":       params.Entry.Node,
		"path":       params.Entry.Path,
		"device_id":  params.Entry.ID,
	}
	if params.Entry.SubsystemID != "" {
		detail["subsystem_id"] = params.Entry.SubsystemID
	}
	if params.Entry.IOMMUGroup != nil {
		detail["iommugroup"] = *params.Entry.IOMMUGroup
	}
	if params.MDev {
		detail["mdev"] = true
	}
	details, _ := json.Marshal(detail)
	return details
}

// CreatePCIMapping handles POST /api/v1/clusters/:cluster_id/pci-mappings.
func (h *VMHandler) CreatePCIMapping(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	created, err := createPCIMapping(c.Context(), pxClient, h.nodeInCluster(clusterID),
		p.String("mapping_id"), p.String("node"), p.String("path"), p.String("description"))
	if err != nil {
		return err
	}
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), "pci_mapping", created.ID, "created",
		pciMappingCreateDetails(created))
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"status": "ok"})
}

// --- The kinds, generically ---------------------------------------------------

// mappingView is what the flows below need to know of one kind of mapping:
// where its type keeps the id, the file digest, the description, the node
// entries and a node's check. USB and PCI mappings carry the same things, each
// under a type of its own — the check under a key of its own, "errors" and
// "checks" — so the listing's check fan-out and merge and the delete are
// written once, over a view. The usage scan is written once too, over the
// kind's config keys instead (scanMappingUsage).
type mappingView[M any] struct {
	// kind names the kind in messages: "USB" or "PCI".
	kind        string
	id          func(M) string
	digest      func(M) string
	description func(M) string
	entries     func(M) []string
	checks      func(M) []proxmox.MappingCheck
}

// usbMappingView reads a USB mapping, whose check is under "errors".
var usbMappingView = mappingView[proxmox.USBMapping]{
	kind:        "USB",
	id:          func(m proxmox.USBMapping) string { return m.ID },
	digest:      func(m proxmox.USBMapping) string { return m.Digest },
	description: func(m proxmox.USBMapping) string { return m.Description },
	entries:     func(m proxmox.USBMapping) []string { return m.Map },
	checks:      func(m proxmox.USBMapping) []proxmox.MappingCheck { return m.Errors },
}

// --- The cluster-wide listing -------------------------------------------------

// usbMappingCheckTimeout bounds each call a cluster-wide listing makes per
// node, of either kind, and a usage scan's read of the nodes' status. Proxmox
// runs a check ON the node it names (the listing's proxyto_callback), so a
// node that is up in the cluster's view but not answering would otherwise hold
// the whole page for the client's five-minute timeout: fiber.Ctx.Context()
// carries no deadline of its own. A variable only so the tests can shorten it,
// named for the USB tests that do; the flows read it when they run, so both
// kinds see what a test set.
var usbMappingCheckTimeout = 10 * time.Second

// usbMappingCheckBudget bounds all of one listing's node checks together, so
// a cluster of slow nodes cannot hold the page for a timeout per node. A node
// not asked, or not answered, by then is reported unchecked. Both kinds', and
// a variable for the tests, as usbMappingCheckTimeout is.
var usbMappingCheckBudget = 20 * time.Second

// mappingCheckConcurrency bounds how many nodes are checked at once.
const mappingCheckConcurrency = 4

// usbMappingCheckConcurrency is mappingCheckConcurrency by the name the USB
// tests use.
const usbMappingCheckConcurrency = mappingCheckConcurrency

// clusterMapping is one mapping as a cluster-wide listing answers it, of
// either kind: the mapping as Proxmox stores it, and for every node one of its
// entries names, either what Proxmox reported checking it there or why it was
// not checked. Every such node is in exactly one of the two maps — a node that
// could not be checked is never reported as a clean check.
type clusterMapping struct {
	ID          string   `json:"id"`
	Description string   `json:"description"`
	Map         []string `json:"map"`
	// Digest is the whole config file's — usb.cfg's or pci.cfg's — from the
	// same read as Map. An update sends it back, so it conflicts if any
	// mapping of the kind changed since.
	Digest string `json:"digest"`
	// NodeChecks is what Proxmox reported for this mapping running the check
	// on that node: an empty list is a clean check.
	NodeChecks map[string][]proxmox.MappingCheck `json:"node_checks"`
	// Unchecked says why a node was not checked.
	Unchecked map[string]string `json:"unchecked"`
}

// mappingNodeCheck is one node's check-node listing of a kind, or why there
// is none.
type mappingNodeCheck[M any] struct {
	mappings []M
	// reason is set, and mappings unused, when the node was not checked.
	reason string
}

// usbMappingNodeCheck is one node's USB check-node listing.
type usbMappingNodeCheck = mappingNodeCheck[proxmox.USBMapping]

// usbMappingLister reads the cluster's USB mappings, optionally checked on a
// node. The interfaces here are the parts of the Proxmox client each flow
// uses, so every flow can be tested without a cluster.
type usbMappingLister interface {
	ListUSBMappings(ctx context.Context, checkNode string) ([]proxmox.USBMapping, error)
}

// nodeLister reads the cluster's nodes and their status.
type nodeLister interface {
	GetNodes(ctx context.Context) ([]proxmox.NodeListEntry, error)
}

// guestConfigReader is what a usage scan reads, for either kind: the cluster's
// guests, its nodes' status, and each VM's configuration.
type guestConfigReader interface {
	nodeLister
	GetClusterResources(ctx context.Context, resourceType string) ([]proxmox.ClusterResource, error)
	GetVMConfig(ctx context.Context, node string, vmid int) (proxmox.VMConfig, error)
}

// usbMappingReader is what the USB listing and usage scan read through.
type usbMappingReader interface {
	usbMappingLister
	guestConfigReader
}

// usbMappingDeleter is what the USB delete flow reads and writes.
type usbMappingDeleter interface {
	usbMappingLister
	DeleteUSBMapping(ctx context.Context, id string) error
}

// eachWithin runs work on every item, on at most workers goroutines. An item
// not yet started when budget ends goes to skipped instead, and its work never
// runs. It returns once every item has gone to one or the other, so the
// callers' results are complete — and it holds at most workers goroutines
// however many items there are.
func eachWithin[T any](budget context.Context, workers int, items []T, work, skipped func(T)) {
	// At least one, or the first send below would wait for a worker forever.
	workers = max(workers, 1)
	jobs := make(chan T)
	var wg sync.WaitGroup
	for range min(workers, len(items)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for item := range jobs {
				if budget.Err() != nil {
					skipped(item)
					continue
				}
				work(item)
			}
		}()
	}
	for _, item := range items {
		jobs <- item
	}
	close(jobs)
	wg.Wait()
}

// mapEntryNode is the node a stored entry names, of either kind, or "".
// Deliberately lenient — the first node= value, whatever else the entry holds
// — because it only decides which node to ask: the node's own check is the
// authority on the entry. An entry Proxmox cannot parse is left to that check
// (USB's skips it with a warning in get_node_mapping, and then reports the
// node as having no entry), and what it reports is what the page shows.
func mapEntryNode(raw string) string {
	for _, part := range strings.Split(raw, ",") {
		if node, ok := strings.CutPrefix(part, "node="); ok {
			return node
		}
	}
	return ""
}

// usbMapEntryNode is mapEntryNode by the name the USB tests use.
func usbMapEntryNode(raw string) string {
	return mapEntryNode(raw)
}

// mappingEntryNodes is every node an entry of the listing names, each once.
func mappingEntryNodes[M any](view mappingView[M], mappings []M) []string {
	var nodes []string
	for _, m := range mappings {
		for _, raw := range view.entries(m) {
			if node := mapEntryNode(raw); node != "" && !slices.Contains(nodes, node) {
				nodes = append(nodes, node)
			}
		}
	}
	return nodes
}

// usbMappingEntryNodes is mappingEntryNodes for USB mappings.
func usbMappingEntryNodes(mappings []proxmox.USBMapping) []string {
	return mappingEntryNodes(usbMappingView, mappings)
}

// mappingNodeState is why a node is not worth asking, or "" when it is, from
// the cluster's node list: its members and their status.
func mappingNodeState(node string, members map[string]string) string {
	status, member := members[node]
	switch {
	case !member:
		return "This cluster has no node of that name."
	case status == "online":
		return ""
	case status == "offline":
		return "The node is offline."
	default:
		return fmt.Sprintf("The node is not online (Proxmox reports it as %q).", status)
	}
}

// checkMappingNodes runs a kind's check-node listing, list, on each node that
// the mappings name and that is an online member of the cluster, a few at a
// time, each under its own deadline and all within usbMappingCheckBudget, and
// returns what each answered — or why it was not asked or did not answer.
// Every node in nodes gets an entry.
//
// Without the cluster's node list nothing is asked. An entry can name any
// node-name-shaped host — Proxmox does not check it against the cluster — and
// Proxmox resolves a name that is not a member through DNS and connects to it,
// so asking blind would let whoever stored the names point the cluster at
// hosts of their choosing on every page load. list is a method expression, run
// on px, so the node list that decides who is asked and the checks it lets
// through always go to the same cluster.
func checkMappingNodes[M any, P nodeLister](ctx context.Context, px P, list func(P, context.Context, string) ([]M, error),
	view mappingView[M], nodes []string) map[string]mappingNodeCheck[M] {
	out := make(map[string]mappingNodeCheck[M], len(nodes))
	if len(nodes) == 0 {
		return out
	}
	budget, cancel := context.WithTimeout(ctx, usbMappingCheckBudget)
	defer cancel()

	statusCtx, cancelStatus := context.WithTimeout(budget, usbMappingCheckTimeout)
	entries, statusErr := px.GetNodes(statusCtx)
	cancelStatus()
	if statusErr != nil {
		slog.Warn(view.kind+" mapping listing: could not read the cluster's nodes; checking none", "error", statusErr)
		for _, node := range nodes {
			out[node] = mappingNodeCheck[M]{reason: "Could not read the cluster's nodes, so no node was checked."}
		}
		return out
	}
	members := make(map[string]string, len(entries))
	for _, n := range entries {
		members[n.Node] = n.Status
	}

	// Decided before any worker starts, so these writes to out race with
	// nothing; from the fan-out on, out is written only under mu.
	ask := make([]string, 0, len(nodes))
	for _, node := range nodes {
		if reason := mappingNodeState(node, members); reason != "" {
			out[node] = mappingNodeCheck[M]{reason: reason}
			continue
		}
		ask = append(ask, node)
	}

	var mu sync.Mutex
	record := func(node string, check mappingNodeCheck[M]) {
		mu.Lock()
		out[node] = check
		mu.Unlock()
	}
	outOfTime := mappingOutOfTime("The checks", usbMappingCheckBudget)
	eachWithin(budget, mappingCheckConcurrency, ask,
		func(node string) {
			callCtx, cancel := context.WithTimeout(budget, usbMappingCheckTimeout)
			defer cancel()
			mappings, err := list(px, callCtx, node)
			switch {
			case err == nil:
				record(node, mappingNodeCheck[M]{mappings: mappings})
			case budget.Err() != nil:
				record(node, mappingNodeCheck[M]{reason: outOfTime})
			default:
				record(node, mappingNodeCheck[M]{reason: unansweredReason(callCtx, err, usbMappingCheckTimeout)})
			}
		},
		func(node string) {
			record(node, mappingNodeCheck[M]{reason: outOfTime})
		})
	return out
}

// checkUSBMappingNodes is checkMappingNodes for USB mappings.
func checkUSBMappingNodes(ctx context.Context, px usbMappingReader, nodes []string) map[string]usbMappingNodeCheck {
	return checkMappingNodes(ctx, px, usbMappingReader.ListUSBMappings, usbMappingView, nodes)
}

// mappingOutOfTime is an unchecked item's reason when the whole run's budget
// ran out before it was asked, or while it was.
func mappingOutOfTime(what string, budget time.Duration) string {
	return fmt.Sprintf("%s ran out of their %s before this one answered.", what, budget)
}

// unansweredReason says why a call to one node or guest came back without an
// answer, for a caller that reports it per item rather than failing the whole
// request. A deadline is named as one: the client reports it as a connection
// failure, which alone would read as the node being unreachable.
func unansweredReason(ctx context.Context, err error, timeout time.Duration) string {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return fmt.Sprintf("No answer within %s.", timeout)
	}
	var fe *fiber.Error
	if errors.As(mapProxmoxError(err), &fe) && fe.Message != "" {
		return fe.Message
	}
	return "The request failed."
}

// mergeMappingChecks folds the per-node checks into the plain listing. It
// answers one mapping for each of plain, in plain's order, so a caller can
// pair each answer with the mapping it came from by position.
//
// A node's check is used for a mapping only when it came from the same config
// file as the listing — the digests match. Otherwise the check describes a
// file the page is not showing, and the node is reported unchecked rather than
// letting, say, a check of an entry since replaced vouch for the new one.
func mergeMappingChecks[M any](view mappingView[M], plain []M, checks map[string]mappingNodeCheck[M]) []clusterMapping {
	out := make([]clusterMapping, 0, len(plain))
	for _, m := range plain {
		cm := clusterMapping{
			ID:          view.id(m),
			Description: view.description(m),
			Map:         view.entries(m),
			Digest:      view.digest(m),
			NodeChecks:  map[string][]proxmox.MappingCheck{},
			Unchecked:   map[string]string{},
		}
		if cm.Map == nil {
			cm.Map = []string{}
		}
		for _, raw := range cm.Map {
			node := mapEntryNode(raw)
			if node == "" {
				continue
			}
			check, asked := checks[node]
			if !asked {
				// Not reachable through the cluster-wide listings, which ask
				// every node an entry names; said rather than assumed clean.
				cm.Unchecked[node] = "Nexara did not check this node."
				continue
			}
			if check.reason != "" {
				cm.Unchecked[node] = check.reason
				continue
			}
			i := slices.IndexFunc(check.mappings, func(c M) bool { return view.id(c) == cm.ID })
			switch {
			case i < 0:
				cm.Unchecked[node] = "The node's check did not list this mapping."
			case view.digest(check.mappings[i]) != cm.Digest:
				cm.Unchecked[node] = "The " + view.kind + " mappings changed while they were being checked. Reload to check again."
			default:
				errs := view.checks(check.mappings[i])
				if errs == nil {
					errs = []proxmox.MappingCheck{}
				}
				cm.NodeChecks[node] = errs
			}
		}
		out = append(out, cm)
	}
	return out
}

// mergeUSBMappingChecks is mergeMappingChecks for USB mappings.
func mergeUSBMappingChecks(plain []proxmox.USBMapping, checks map[string]usbMappingNodeCheck) []clusterMapping {
	return mergeMappingChecks(usbMappingView, plain, checks)
}

// ListClusterUSBMappings handles GET /api/v1/clusters/:cluster_id/usb-mappings.
func (h *VMHandler) ListClusterUSBMappings(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	// The plain listing is the page: the mappings and the digest an edit
	// pins come from this one read, and the checks are only merged onto it.
	plain, err := pxClient.ListUSBMappings(c.Context(), "")
	if err != nil {
		return mapProxmoxError(err)
	}
	checks := checkUSBMappingNodes(c.Context(), pxClient, usbMappingEntryNodes(plain))
	return RespondItems(c, mergeUSBMappingChecks(plain, checks))
}

// --- Update and delete --------------------------------------------------------

// usbMappingStaleMessage is the 409 an update answers when the digest it
// carried no longer matches usb.cfg. Nothing was changed: Proxmox compares
// the digest before it looks at the mapping.
const usbMappingStaleMessage = "The cluster's USB mappings changed since they were loaded, so nothing was " +
	"changed — reload and try again. A change to any USB mapping counts, not only to this one."

// usbMappingMissingPhrases is how Proxmox's update refuses an id with no
// mapping: pve-manager PVE/API2/Cluster/Mapping/USB.pm, update, dies
// "usb ID '$id' does not exist" inside lock_usb_config, re-died as
// "update hardware mapping failed: usb ID '…' does not exist" — a plain 500.
// The id is a pve-configid and cannot hold a space, so the id Proxmox echoes
// can never be what matches.
var usbMappingMissingPhrases = []string{"does not exist"}

// mapUSBMappingUpdateError answers the two ways an update can find its view
// stale, on top of mapProxmoxError: a digest that no longer matches is 409
// (assert_if_modified's "detected modified configuration", staleDigestPhrases
// in acme.go), and a mapping that is gone is 404. The digest is compared
// first, as the update does, so a stale request never reaches the 404.
func mapUSBMappingUpdateError(err error) error {
	stale := mapProxmoxDieError(fiber.StatusConflict, usbMappingStaleMessage, staleDigestPhrases, err)
	var fe *fiber.Error
	if errors.As(stale, &fe) && fe.Code == fiber.StatusConflict {
		return stale
	}
	return mapMissingObjectError("No USB mapping with that ID — it may have been deleted", usbMappingMissingPhrases, err)
}

// UpdateUSBMapping handles PUT /api/v1/clusters/:cluster_id/usb-mappings/:mapping_id.
func (h *VMHandler) UpdateUSBMapping(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	id, params, err := updateUSBMappingRequest(c.Context(), pxClient, p)
	if err != nil {
		return err
	}

	// The entries as Proxmox now stores them. The client has just written
	// exactly this, so the error cannot happen here.
	written, _ := proxmox.CanonicalUSBMap(params.Map)
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), "usb_mapping", id, "updated",
		usbMappingUpdateDetails(written, params.Description))
	return c.JSON(fiber.Map{"status": "ok"})
}

// usbMappingUpdater is what the update flow writes through.
type usbMappingUpdater interface {
	UpdateUSBMapping(ctx context.Context, id string, params proxmox.UpdateUSBMappingParams) error
}

// updateUSBMappingRequest makes the update the request asks for — everything
// between reading the request and auditing it — so the whole path from the
// route's parameters to the client call is tested, not only its parts.
func updateUSBMappingRequest(ctx context.Context, px usbMappingUpdater, p *apischema.Params) (string, proxmox.UpdateUSBMappingParams, error) {
	id := p.String("mapping_id")
	params := usbMappingUpdateParams(p)
	if err := px.UpdateUSBMapping(ctx, id, params); err != nil {
		return "", proxmox.UpdateUSBMappingParams{}, mapUSBMappingUpdateError(err)
	}
	return id, params, nil
}

// usbMappingUpdateParams reads an update's body. The description goes on only
// when the caller sent one: the client turns a non-nil empty description into
// delete=description, so passing one for every update would wipe the
// description on each Add node, Replace and Remove.
func usbMappingUpdateParams(p *apischema.Params) proxmox.UpdateUSBMappingParams {
	params := proxmox.UpdateUSBMappingParams{
		Map:    p.Strings("map"),
		Digest: p.String("digest"),
	}
	if description, set := p.OptString("description"); set {
		params.Description = &description
	}
	return params
}

// usbMappingAuditEntries caps how many of a mapping's entries an audit row
// records, and usbMappingAuditTextMax how long each entry, and a description,
// may be there (auditTruncate): audit_log is never trimmed, and both come from
// the caller. A real mapping comes nowhere near either — one entry per node,
// each a few dozen characters — so its row still holds what it takes to create
// it again; map_count says when a row holds fewer entries than there were.
const (
	usbMappingAuditEntries = 64
	usbMappingAuditTextMax = 512
)

// usbMappingAuditMap is a mapping's entries as an audit row records them.
//
// An entry that fits is recorded exactly as it was — a delete's row shows
// what Proxmox held, an uppercase id and all. One that does not fit has only
// its description cut: the description may come first (as
// proxmox.USBMapEntry.String writes it), so cutting the whole string could
// keep a long description and lose the node, device id and port — the part
// the row is for. Such an entry is parsed, its description cut, and written
// again in Nexara's form. One too long that does not parse — stored by hand,
// say — is cut whole.
func usbMappingAuditMap(detail map[string]any, entries []string) {
	recorded := make([]string, 0, min(len(entries), usbMappingAuditEntries))
	for _, raw := range entries[:min(len(entries), usbMappingAuditEntries)] {
		if utf8.RuneCountInString(raw) <= usbMappingAuditTextMax {
			recorded = append(recorded, raw)
			continue
		}
		e, err := proxmox.ParseUSBMapEntry(raw)
		if err != nil {
			recorded = append(recorded, auditTruncate(raw, usbMappingAuditTextMax))
			continue
		}
		e.Description = auditTruncate(e.Description, usbMappingAuditTextMax)
		// And the whole of it bounded too: nothing but the port holds the
		// node name to a length (pve-node has none), so an entry can be long
		// with a short description.
		recorded = append(recorded, auditTruncate(e.String(), 2*usbMappingAuditTextMax))
	}
	detail["map"] = recorded
	detail["map_count"] = len(entries)
}

// usbMappingUpdateDetails is the audit detail of a completed update: every
// entry as written — the update replaces the list, so the row holds all of
// it, not a change — and the description only when the update touched it.
// A removed description is recorded as removed rather than as "", which
// would read the same as an update that never mentioned it.
func usbMappingUpdateDetails(written []string, description *string) json.RawMessage {
	detail := map[string]any{}
	usbMappingAuditMap(detail, written)
	if description != nil {
		if *description == "" {
			detail["description_removed"] = true
		} else {
			detail["description"] = auditTruncate(*description, usbMappingAuditTextMax)
		}
	}
	details, _ := json.Marshal(detail)
	return details
}

// findMapping reads the mapping id from a kind's plain listing, list run on
// px: the mapping when it is there, nil when the listing does not hold it, and
// the error when the listing could not be read. fileDigest is the kind's
// config file digest, which every listed mapping carries — "" only when the
// listing holds no mapping at all.
func findMapping[M, P any](ctx context.Context, px P, list func(P, context.Context, string) ([]M, error),
	view mappingView[M], id string) (mapping *M, fileDigest string, err error) {
	mappings, err := list(px, ctx, "")
	if err != nil {
		return nil, "", err
	}
	for i := range mappings {
		if fileDigest == "" {
			fileDigest = view.digest(mappings[i])
		}
		if view.id(mappings[i]) == id {
			mapping = &mappings[i]
		}
	}
	return mapping, fileDigest, nil
}

// classifyMappingDelete decides what a completed DELETE of a mapping may
// claim, from the pre-delete snapshot attempt. The HA-rule precedent,
// classifyHARuleDelete in ha.go, for the same reason: Proxmox's delete
// succeeds whether or not the mapping exists, so its 200 says the mapping is
// gone now, never that this request removed it.
//
// The snapshot and the delete are two round trips and Proxmox's delete takes
// no digest, so this narrows the doubt rather than closing it: a mapping
// created or deleted by someone else between the two reads is misreported.
// "already_deleted" is evidence of a no-op, not proof of one.
func classifyMappingDelete[M any](snapshot *M, snapErr error) (action string, priorStateUnknown bool) {
	switch {
	case snapshot != nil:
		return "deleted", false
	case snapErr != nil:
		// Could not look, so the delete is recorded as one — a filter keyed
		// on "deleted" must not miss a real deletion — with the doubt marked.
		return "deleted", true
	default:
		return "already_deleted", false
	}
}

// classifyUSBMappingDelete is classifyMappingDelete for a USB mapping, by the
// name the USB tests use.
func classifyUSBMappingDelete(snapshot *proxmox.USBMapping, snapErr error) (action string, priorStateUnknown bool) {
	return classifyMappingDelete(snapshot, snapErr)
}

// usbMappingDeleteDetails is the audit detail of a completed delete: the
// mapping as it was, which is enough to create it again.
func usbMappingDeleteDetails(snapshot *proxmox.USBMapping, priorStateUnknown bool) json.RawMessage {
	detail := map[string]any{}
	if priorStateUnknown {
		// The error stays out of the row — it goes to the log instead — since
		// view:audit reaches every Viewer and a connection failure names the
		// Proxmox host.
		detail["prior_state_unknown"] = true
	}
	if snapshot != nil {
		usbMappingAuditMap(detail, snapshot.Map)
		if snapshot.Description != "" {
			detail["description"] = auditTruncate(snapshot.Description, usbMappingAuditTextMax)
		}
	}
	details, _ := json.Marshal(detail)
	return details
}

// mappingDeleteStaleMessage is the 409 a delete of a kind's mapping answers
// when the digest it carried no longer matches the kind's config file.
// Nothing was deleted.
func mappingDeleteStaleMessage(kind string) string {
	return "The cluster's " + kind + " mappings changed since they were loaded, so nothing was deleted — " +
		"reload and try again. A change to any " + kind + " mapping counts, not only to this one."
}

// mappingDeleteOutcome is what a completed delete may claim, for its audit
// row.
type mappingDeleteOutcome[M any] struct {
	snapshot          *M
	snapErr           error
	action            string
	priorStateUnknown bool
}

// usbMappingDeleteOutcome is what a completed delete of a USB mapping may
// claim.
type usbMappingDeleteOutcome = mappingDeleteOutcome[proxmox.USBMapping]

// deleteMapping is the delete flow, for either kind: a snapshot of the
// mapping first, through the kind's plain listing, list — the audit row's
// entries, and what tells a deletion from a no-op — then Proxmox's delete,
// del. Both are method expressions of the kind's own client interface, P, run
// on px: an interface that holds only its kind's calls cannot pair one kind's
// listing and digest with the other kind's delete.
//
// With a digest, the delete is refused with 409 unless the kind's config file
// still has the digest the caller's listing had — compared whether or not the
// mapping is still listed, since one deleted and made again since is a change
// too. Proxmox's delete takes no digest, so this is Nexara's check, made
// against its own read: it narrows the window to the round trip between that
// read and the delete rather than closing it. It is what keeps a delete aimed
// at the mapping the operator was shown — its entries, say — from removing one
// that changed meanwhile. A snapshot that could not be read cannot be
// compared, so a delete asked to compare does not go ahead. Only a listing
// with no mapping at all carries no digest to compare: then the delete is the
// no-op it would have been without one.
func deleteMapping[M, P any](ctx context.Context, px P, list func(P, context.Context, string) ([]M, error),
	del func(P, context.Context, string) error, view mappingView[M], id, digest string) (mappingDeleteOutcome[M], error) {
	snapshot, fileDigest, snapErr := findMapping(ctx, px, list, view, id)
	if digest != "" {
		switch {
		case snapErr != nil:
			return mappingDeleteOutcome[M]{}, mapProxmoxError(snapErr)
		case fileDigest != "" && fileDigest != digest:
			return mappingDeleteOutcome[M]{}, fiber.NewError(fiber.StatusConflict, mappingDeleteStaleMessage(view.kind))
		}
	}
	if err := del(px, ctx, id); err != nil {
		return mappingDeleteOutcome[M]{}, mapProxmoxError(err)
	}
	action, priorStateUnknown := classifyMappingDelete(snapshot, snapErr)
	return mappingDeleteOutcome[M]{
		snapshot:          snapshot,
		snapErr:           snapErr,
		action:            action,
		priorStateUnknown: priorStateUnknown,
	}, nil
}

// deleteUSBMapping is deleteMapping for a USB mapping.
func deleteUSBMapping(ctx context.Context, px usbMappingDeleter, id, digest string) (usbMappingDeleteOutcome, error) {
	return deleteMapping(ctx, px, usbMappingDeleter.ListUSBMappings, usbMappingDeleter.DeleteUSBMapping,
		usbMappingView, id, digest)
}

// deleteUSBMappingRequest makes the delete the request asks for: the id from
// the path and the digest when the caller sent one — everything between
// reading the request and auditing it, so the route's digest is tested all
// the way to the check that uses it.
func deleteUSBMappingRequest(ctx context.Context, px usbMappingDeleter,
	p *apischema.Params) (id string, outcome usbMappingDeleteOutcome, err error) {
	id = p.String("mapping_id")
	digest, _ := p.OptString("digest")
	outcome, err = deleteUSBMapping(ctx, px, id, digest)
	return id, outcome, err
}

// DeleteUSBMapping handles DELETE /api/v1/clusters/:cluster_id/usb-mappings/:mapping_id.
//
// Idempotent, like the Proxmox endpoint underneath: deleting a mapping that is
// already gone succeeds, and the audit row records it as the no-op it was.
func (h *VMHandler) DeleteUSBMapping(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	id, outcome, err := deleteUSBMappingRequest(c.Context(), pxClient, p)
	if err != nil {
		return err
	}
	if outcome.priorStateUnknown {
		slog.Warn("USB mapping delete: could not read the mappings to snapshot this one; auditing without its entries",
			"cluster_id", clusterID, "mapping_id", id, "error", outcome.snapErr)
	}
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), "usb_mapping", id, outcome.action,
		usbMappingDeleteDetails(outcome.snapshot, outcome.priorStateUnknown))
	return c.JSON(fiber.Map{"status": "ok"})
}

// --- Usage --------------------------------------------------------------------

// usbMappingUsageTimeout bounds each guest's config read, and
// usbMappingUsageBudget the whole scan, the guest list included: one read per
// VM adds up on a large cluster, and the delete dialog is waiting on the
// answer. A guest not read by then is reported unchecked, never as not using
// the mapping. They bound the scans of both kinds; variables only so the tests
// can shorten them, named for the USB tests that do.
var (
	usbMappingUsageTimeout = 10 * time.Second
	usbMappingUsageBudget  = 30 * time.Second
)

// mappingUsageConcurrency bounds how many configs one scan reads at once.
const mappingUsageConcurrency = 8

// usbMappingUsageConcurrency is mappingUsageConcurrency by the name the USB
// tests use.
const usbMappingUsageConcurrency = mappingUsageConcurrency

// mappingUsageScansPerCluster bounds the scans in flight against one cluster,
// of both kinds together, and usageScanSlots holds each user to one at a time.
// Each scan is a Proxmox request per VM, and the usage routes' limiter
// (mappingUsageLimiter in internal/api/middleware.go, per user, one for both
// kinds) bounds how often one starts, not how many overlap. Per cluster, so no caller can hold
// the slots another cluster's check needs; per user, so no one account can
// hold both of a cluster's. A caller over the cluster's cap gets 429 at once
// rather than a wait: the SPA retries a 429 once, and the delete dialog shows
// a failed check as a warning and still offers the delete.
const mappingUsageScansPerCluster = 2

// usbMappingUsageScansPerCluster is mappingUsageScansPerCluster by the name
// the USB tests use.
const usbMappingUsageScansPerCluster = mappingUsageScansPerCluster

// usbMappingUsageSupersedeWait bounds how long a user's new scan waits for
// the older one it replaces to stop, whichever kind either scans. A variable
// only so the tests can shorten it, named for the USB tests that do.
var usbMappingUsageSupersedeWait = 2 * time.Second

// errMappingUsageClusterFull answers a scan the cluster has no room for.
var errMappingUsageClusterFull = fiber.NewError(fiber.StatusTooManyRequests,
	"Other checks of which VMs use a mapping are running on this cluster; try again in a moment.")

// errUSBMappingUsageClusterFull is errMappingUsageClusterFull by the name the
// USB tests use.
var errUSBMappingUsageClusterFull = errMappingUsageClusterFull

// errMappingUsageSuperseded answers a scan a newer one of the same user's
// replaced before it finished.
var errMappingUsageSuperseded = fiber.NewError(fiber.StatusConflict,
	"A newer check of which VMs use a mapping, by the same user, replaced this one.")

// errUSBMappingUsageSuperseded is errMappingUsageSuperseded by the name the
// USB tests use.
var errUSBMappingUsageSuperseded = errMappingUsageSuperseded

// userScan is one user's scan in flight: which cluster it holds a slot of,
// how to stop it, and when it has.
type userScan struct {
	cluster uuid.UUID
	cancel  context.CancelFunc
	done    chan struct{}
}

// usageScanSlots counts the usage scans in flight. Entries leave the maps
// when their scans end, so the maps hold only what is running.
type usageScanSlots struct {
	mu        sync.Mutex
	byCluster map[uuid.UUID]int
	byUser    map[uuid.UUID]*userScan
}

func newUsageScanSlots() *usageScanSlots {
	return &usageScanSlots{byCluster: map[uuid.UUID]int{}, byUser: map[uuid.UUID]*userScan{}}
}

// acquire takes a slot for user's scan of cluster, and returns the context
// the scan runs under and its release — or the 429.
//
// A user's newer scan REPLACES their older one rather than being refused
// behind it: the older one's context is cancelled, and this one waits, at most
// usbMappingUsageSupersedeWait, for it to let go. The older one is almost
// always a delete confirmation already closed — its request cannot be seen
// to go away, since the server never learns of a disconnect — and refusing
// the newer one would cost the dialog the operator is looking at its check.
// The older request answers errMappingUsageSuperseded.
//
// release frees the slot; calling it again does nothing, so a second call can
// never undercount the cluster and let a third scan in.
func (s *usageScanSlots) acquire(parent context.Context, cluster, user uuid.UUID) (context.Context, func(), error) {
	s.mu.Lock()
	older := s.byUser[user]
	// The cluster's room is looked at before the older scan is stopped: a
	// request that would be refused anyway must not cost the user that one
	// too. The older scan's own slot counts as room when it is on this
	// cluster, since stopping it frees that slot.
	if s.byCluster[cluster] >= mappingUsageScansPerCluster && (older == nil || older.cluster != cluster) {
		s.mu.Unlock()
		return nil, nil, errMappingUsageClusterFull
	}
	if older != nil {
		older.cancel()
		s.mu.Unlock()
		select {
		case <-older.done:
		case <-time.After(usbMappingUsageSupersedeWait):
			return nil, nil, fiber.NewError(fiber.StatusTooManyRequests,
				"Your previous check of which VMs use a mapping is still stopping; try again in a moment.")
		}
		s.mu.Lock()
		// Another request of the user's may have taken the slot meanwhile.
		if s.byUser[user] != nil {
			s.mu.Unlock()
			return nil, nil, fiber.NewError(fiber.StatusTooManyRequests,
				"Another check of which VMs use a mapping, by you, has just started; try again in a moment.")
		}
	}
	// Looked at again: others may have taken the room while this one waited.
	if s.byCluster[cluster] >= mappingUsageScansPerCluster {
		s.mu.Unlock()
		return nil, nil, errMappingUsageClusterFull
	}
	ctx, cancel := context.WithCancel(parent)
	scan := &userScan{cluster: cluster, cancel: cancel, done: make(chan struct{})}
	s.byUser[user] = scan
	s.byCluster[cluster]++
	s.mu.Unlock()

	var once sync.Once
	return ctx, func() {
		once.Do(func() {
			cancel()
			s.mu.Lock()
			// Always this scan's: a user's next scan is registered only
			// after this release has closed done (acquire waits for it).
			delete(s.byUser, user)
			s.byCluster[cluster]--
			if s.byCluster[cluster] <= 0 {
				delete(s.byCluster, cluster)
			}
			s.mu.Unlock()
			close(scan.done)
		})
	}, nil
}

// mappingUsageScans is every usage scan in flight in this process, of both
// kinds: one pool, so the caps hold for a user's and a cluster's checks
// whichever kind they are of.
var mappingUsageScans = newUsageScanSlots()

// usbMappingUsageScans is the same pool, by the name the USB tests use: a copy
// of the pointer, so a test acquires through it and never reassigns it — a
// reassigned one would no longer be the pool the handlers use.
var usbMappingUsageScans = mappingUsageScans

// mappingBudgetReason is an unchecked guest's reason when the scan's budget
// ran out before its config was read, or while it was being read.
func mappingBudgetReason() string {
	return fmt.Sprintf("Not read: the scan ran out of its %s.", usbMappingUsageBudget)
}

// mappingGuest is a guest in a usage answer.
type mappingGuest struct {
	VMID int    `json:"vmid"`
	Name string `json:"name"`
	Node string `json:"node"`
	// Keys are the config keys that pass the mapping through — usbN or
	// hostpciN; set for a user.
	Keys []string `json:"keys,omitempty"`
	// Reason says why the config was not read; set for an unchecked guest.
	Reason string `json:"reason,omitempty"`
}

// usbMappingGuest is a guest in a USB mapping's usage answer.
type usbMappingGuest = mappingGuest

// mappingUsage is which VMs use a mapping. Unchecked is not a subset of
// anything: a guest listed there may or may not use the mapping.
type mappingUsage struct {
	MappingID string         `json:"mapping_id"`
	Checked   int            `json:"checked"`
	Users     []mappingGuest `json:"users"`
	Unchecked []mappingGuest `json:"unchecked"`
}

// usbMappingUsage is which VMs use a USB mapping.
type usbMappingUsage = mappingUsage

// scanMappingUsage reads the config of every VM in the cluster and reports
// the ones whose keys — keys(config, id), the usbN or hostpciN that name the
// mapping — are not empty, and every one it could not read. kind names the
// mapping's kind in the log, where an id alone could be either kind's.
//
// Nexara keeps no guest configs, so this is the only way to know. It reads
// live: the cluster's guest list and node status from Proxmox, then each VM's
// config on the node that holds it. A VM on a node that is not online, or
// whose read fails or runs out of time, is reported unchecked with the reason.
// Without the node list every VM is read: the guest list comes from Proxmox,
// so it names only real members, and a read of a VM on a dead node fails
// alone. Containers are not read: a mapping is passed through by a VM's usbN
// or hostpciN, which a container does not have.
func scanMappingUsage(ctx context.Context, px guestConfigReader, kind, id string,
	keys func(proxmox.VMConfig, string) []string) (mappingUsage, error) {
	usage := mappingUsage{MappingID: id, Users: []mappingGuest{}, Unchecked: []mappingGuest{}}
	budget, cancel := context.WithTimeout(ctx, usbMappingUsageBudget)
	defer cancel()

	guests, err := px.GetClusterResources(budget, "vm")
	if err != nil {
		return usage, err
	}
	statusCtx, cancelStatus := context.WithTimeout(budget, usbMappingCheckTimeout)
	entries, statusErr := px.GetNodes(statusCtx)
	cancelStatus()
	if statusErr != nil {
		slog.Warn(kind+" mapping usage: could not read the nodes' status; reading every guest's config",
			"mapping_id", id, "error", statusErr)
	}
	members := make(map[string]string, len(entries))
	for _, n := range entries {
		members[n.Node] = n.Status
	}

	// Decided before any worker starts, so these appends race with nothing;
	// from the fan-out on, usage is written only under mu.
	read := make([]mappingGuest, 0, len(guests))
	for _, g := range guests {
		if g.Type != "qemu" {
			continue
		}
		guest := mappingGuest{VMID: g.VMID, Name: g.Name, Node: g.Node}
		if statusErr == nil {
			if reason := mappingNodeState(g.Node, members); reason != "" {
				guest.Reason = reason
				usage.Unchecked = append(usage.Unchecked, guest)
				continue
			}
		}
		read = append(read, guest)
	}

	var mu sync.Mutex
	unchecked := func(guest mappingGuest, reason string) {
		guest.Reason = reason
		mu.Lock()
		usage.Unchecked = append(usage.Unchecked, guest)
		mu.Unlock()
	}
	eachWithin(budget, mappingUsageConcurrency, read,
		func(guest mappingGuest) {
			callCtx, cancel := context.WithTimeout(budget, usbMappingUsageTimeout)
			defer cancel()
			config, err := px.GetVMConfig(callCtx, guest.Node, guest.VMID)
			switch {
			case err == nil:
				mu.Lock()
				defer mu.Unlock()
				usage.Checked++
				if used := keys(config, id); len(used) > 0 {
					guest.Keys = used
					usage.Users = append(usage.Users, guest)
				}
			case budget.Err() != nil:
				unchecked(guest, mappingBudgetReason())
			default:
				unchecked(guest, unansweredReason(callCtx, err, usbMappingUsageTimeout))
			}
		},
		func(guest mappingGuest) {
			unchecked(guest, mappingBudgetReason())
		})

	byVMID := func(a, b mappingGuest) int { return a.VMID - b.VMID }
	slices.SortFunc(usage.Users, byVMID)
	slices.SortFunc(usage.Unchecked, byVMID)
	return usage, nil
}

// scanUSBMappingUsage is scanMappingUsage for a USB mapping: the VMs whose
// usbN names it.
func scanUSBMappingUsage(ctx context.Context, px usbMappingReader, id string) (usbMappingUsage, error) {
	return scanMappingUsage(ctx, px, usbMappingView.kind, id, proxmox.VMConfig.USBMappingKeys)
}

// GetUSBMappingUsage handles GET /api/v1/clusters/:cluster_id/usb-mappings/:mapping_id/usage.
func (h *VMHandler) GetUSBMappingUsage(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}
	// A request without a user (none reaches here: the route is
	// authenticated) would share the zero id's one slot.
	userID, _ := c.Locals("user_id").(uuid.UUID)
	usage, err := checkUSBMappingUsage(c.Context(), mappingUsageScans, clusterID, userID,
		p.String("mapping_id"), func() (usbMappingReader, error) {
			pxClient, err := h.createProxmoxClient(c, clusterID)
			if err != nil {
				return nil, err
			}
			return pxClient, nil
		})
	if err != nil {
		return err
	}
	return c.JSON(usage)
}

// checkMappingUsage is one usage check, whole, of either kind: a slot first —
// before any Proxmox client is made, so a refused caller costs nothing — then
// the scan, run under the SLOT's context so that a newer check of the same
// user's can stop it, then its answer. The client comes from reader, called
// only once a slot is held, and scan is the kind's scan of it.
func checkMappingUsage[R any](ctx context.Context, slots *usageScanSlots, cluster, user uuid.UUID,
	reader func() (R, error), scan func(context.Context, R) (mappingUsage, error)) (mappingUsage, error) {
	scanCtx, release, err := slots.acquire(ctx, cluster, user)
	if err != nil {
		return mappingUsage{}, err
	}
	defer release()
	px, err := reader()
	if err != nil {
		return mappingUsage{}, err
	}
	usage, scanErr := scan(scanCtx, px)
	return mappingUsageAnswer(scanCtx, usage, scanErr)
}

// checkUSBMappingUsage is checkMappingUsage for a USB mapping.
func checkUSBMappingUsage(ctx context.Context, slots *usageScanSlots, cluster, user uuid.UUID, id string,
	reader func() (usbMappingReader, error)) (usbMappingUsage, error) {
	return checkMappingUsage(ctx, slots, cluster, user, reader,
		func(scanCtx context.Context, px usbMappingReader) (usbMappingUsage, error) {
			return scanUSBMappingUsage(scanCtx, px, id)
		})
}

// mappingUsageAnswer is what a finished scan answers. One that a newer scan
// of the same user's replaced answers errMappingUsageSuperseded, not its
// partial result — its unread guests would carry reasons that are not true of
// them — and not the Proxmox error its cancelled calls produced.
func mappingUsageAnswer(scanCtx context.Context, usage mappingUsage, err error) (mappingUsage, error) {
	if errors.Is(scanCtx.Err(), context.Canceled) {
		return mappingUsage{}, errMappingUsageSuperseded
	}
	if err != nil {
		return mappingUsage{}, mapProxmoxError(err)
	}
	return usage, nil
}

// usbMappingUsageAnswer is mappingUsageAnswer by the name the USB tests use.
func usbMappingUsageAnswer(scanCtx context.Context, usage usbMappingUsage, err error) (usbMappingUsage, error) {
	return mappingUsageAnswer(scanCtx, usage, err)
}

// --- PCI mappings on the Resource Mappings tab ----------------------------------
//
// The USB flows above, over a PCI mapping — whose check is under "checks", and
// whose nodes may each have several entries, a VM starting there taking the
// first device not in use — and an update of its own: an entry is only ever
// built from the node's own report of the device (proxmox.PCIMapEntryForDevice
// says why), so the update keeps the entries the caller names from the listing
// — each saying what it said, re-spelled only as proxmox.CanonicalPCIMap
// writes every entry — and adds or replaces at most one built that way.

// pciMappingView reads a PCI mapping, whose check is under "checks".
var pciMappingView = mappingView[proxmox.PCIMapping]{
	kind:        "PCI",
	id:          func(m proxmox.PCIMapping) string { return m.ID },
	digest:      func(m proxmox.PCIMapping) string { return m.Digest },
	description: func(m proxmox.PCIMapping) string { return m.Description },
	entries:     func(m proxmox.PCIMapping) []string { return m.Map },
	checks:      func(m proxmox.PCIMapping) []proxmox.MappingCheck { return m.Checks },
}

// pciMappingLister reads the cluster's PCI mappings, optionally checked on a
// node. The PCI interfaces here hold only PCI calls: the generic flows take
// their calls as method expressions of these (deleteMapping says why).
type pciMappingLister interface {
	ListPCIMappings(ctx context.Context, checkNode string) ([]proxmox.PCIMapping, error)
}

// pciMappingReader is what the PCI listing reads through.
type pciMappingReader interface {
	pciMappingLister
	nodeLister
}

// pciMappingDeleter is what the PCI delete flow reads and writes.
type pciMappingDeleter interface {
	pciMappingLister
	DeletePCIMapping(ctx context.Context, id string) error
}

// pciMappingEditor is what the PCI update flow reads and writes.
type pciMappingEditor interface {
	pciMappingLister
	ListNodePCIDevicesAllClasses(ctx context.Context, node string) ([]proxmox.NodePCIDevice, error)
	UpdatePCIMapping(ctx context.Context, id string, params proxmox.UpdatePCIMappingParams) error
}

// clusterPCIMapping is one PCI mapping as its cluster-wide listing answers
// it: clusterMapping, the mapping's two flags, and the entries Nexara cannot
// read.
type clusterPCIMapping struct {
	clusterMapping
	// MDev is the mapping's "Use with Mediated Devices" flag, which Proxmox
	// compares with every entry's device.
	MDev bool `json:"mdev"`
	// LiveMigrationCapable is its "Live Migration Capable" flag: shown here,
	// set in Proxmox.
	LiveMigrationCapable bool `json:"live_migration_capable"`
	// UnreadableEntries says, for each entry proxmox.ParsePCIMapEntry
	// refuses, why — keyed by the entry as stored. An update keeps entries
	// only as the parse reads them, so such an entry can only be removed:
	// every other save of the mapping is refused while it is there.
	UnreadableEntries map[string]string `json:"unreadable_entries"`
}

// pciUnreadableEntries is clusterPCIMapping.UnreadableEntries for entries.
// The reason does not repeat the entry, which is its key and in map already.
func pciUnreadableEntries(entries []string) map[string]string {
	out := map[string]string{}
	for _, raw := range entries {
		if problem := proxmox.PCIMapEntryProblem(raw); problem != "" {
			out[raw] = problem
		}
	}
	return out
}

// listClusterPCIMappings is the PCI cluster-wide listing: the plain listing,
// each mapping checked on every node its entries name (checkMappingNodes),
// merged, and each merged mapping given its flags and its unreadable entries
// from the plain mapping it came from — mergeMappingChecks answers them in
// plain's order.
func listClusterPCIMappings(ctx context.Context, px pciMappingReader) ([]clusterPCIMapping, error) {
	plain, err := px.ListPCIMappings(ctx, "")
	if err != nil {
		return nil, mapProxmoxError(err)
	}
	checks := checkMappingNodes(ctx, px, pciMappingReader.ListPCIMappings, pciMappingView,
		mappingEntryNodes(pciMappingView, plain))
	merged := mergeMappingChecks(pciMappingView, plain, checks)
	out := make([]clusterPCIMapping, 0, len(merged))
	for i, cm := range merged {
		out = append(out, clusterPCIMapping{
			clusterMapping:       cm,
			MDev:                 bool(plain[i].MDev),
			LiveMigrationCapable: bool(plain[i].LiveMigrationCapable),
			UnreadableEntries:    pciUnreadableEntries(cm.Map),
		})
	}
	return out, nil
}

// ListClusterPCIMappings handles GET /api/v1/clusters/:cluster_id/pci-mappings.
func (h *VMHandler) ListClusterPCIMappings(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	mappings, err := listClusterPCIMappings(c.Context(), pxClient)
	if err != nil {
		return err
	}
	return RespondItems(c, mappings)
}

// pciMappingDeviceTimeout bounds the read of add_node's devices an update
// makes to build the entry it adds. A variable only so the tests can shorten
// it.
var pciMappingDeviceTimeout = 10 * time.Second

// pciMappingStaleMessage is the 409 an update answers when the digest it
// carried no longer matches pci.cfg. Nothing was changed.
const pciMappingStaleMessage = "The cluster's PCI mappings changed since they were loaded, so nothing was " +
	"changed — reload and try again. A change to any PCI mapping counts, not only to this one."

// pciMappingMissing is the 404 an update answers for an id with no mapping.
const pciMappingMissing = "No PCI mapping with that ID — it may have been deleted"

// pciMappingMissingPhrases is how Proxmox's update refuses an id with no
// mapping: pve-manager PVE/API2/Cluster/Mapping/PCI.pm dies
// "pci ID '$id' does not exist" as USB.pm's dies for a usb ID
// (usbMappingMissingPhrases), after the digest is compared.
var pciMappingMissingPhrases = []string{"does not exist"}

// mapPCIMappingUpdateError is mapUSBMappingUpdateError for a PCI mapping:
// a stale digest is 409 — compared first, as the update does — and a mapping
// that is gone 404.
func mapPCIMappingUpdateError(err error) error {
	stale := mapProxmoxDieError(fiber.StatusConflict, pciMappingStaleMessage, staleDigestPhrases, err)
	var fe *fiber.Error
	if errors.As(stale, &fe) && fe.Code == fiber.StatusConflict {
		return stale
	}
	return mapMissingObjectError(pciMappingMissing, pciMappingMissingPhrases, err)
}

// pciPathSlot is a PCI path's slot — "0000:01:00.1" → "0000:01:00" — and a
// slot is its own.
func pciPathSlot(path string) string {
	if n := len(path); n >= 2 && path[n-2] == '.' {
		return path[:n-2]
	}
	return path
}

// pciPathsOverlap says whether two paths — each one address or a ";"-joined
// list — name any of the same device: the same address, or a whole device
// (a slot) and one of its functions. The SPA's pciPathsOverlap is the same
// rule.
func pciPathsOverlap(a, b string) bool {
	for _, x := range strings.Split(a, ";") {
		for _, y := range strings.Split(b, ";") {
			if x == y || pciPathSlot(x) == y || x == pciPathSlot(y) {
				return true
			}
		}
	}
	return false
}

// pciKeptEntries checks that keep is a sub-multiset of current — every entry
// kept is one the mapping has, exactly as the listing returned it, and no
// entry more often than the mapping has it — and returns the entries of
// current that keep leaves out.
func pciKeptEntries(current, keep []string) (removed []string, ok bool) {
	left := make(map[string]int, len(current))
	for _, raw := range current {
		left[raw]++
	}
	for _, raw := range keep {
		if left[raw] == 0 {
			return nil, false
		}
		left[raw]--
	}
	for _, raw := range current {
		if left[raw] > 0 {
			removed = append(removed, raw)
			left[raw]--
		}
	}
	return removed, true
}

// pciMappingUpdate is a completed update, for its audit row.
type pciMappingUpdate struct {
	id     string
	params proxmox.UpdatePCIMappingParams
	// added is the entry the update built from add_node's device, or nil.
	added *proxmox.PCIMapEntry
	// replaced is the entry added took the place of, or "".
	replaced string
	// removed are the entries the mapping had that map left out.
	removed []string
}

// pciMdevMismatch is the 400 for a device whose mediated-device capability
// is not what the mapping's flag says, while the mapping keeps other entries:
// which side is which, why it matters beyond this device, and what to do.
func pciMdevMismatch(id, node, path string, deviceMDev bool) error {
	side := fmt.Sprintf("%s on %s cannot provide mediated devices, but mapping %s is set to use them", path, node, id)
	if deviceMDev {
		side = fmt.Sprintf("%s on %s can provide mediated devices, but mapping %s is not set to use them", path, node, id)
	}
	return fiber.NewError(fiber.StatusBadRequest, side+". Proxmox refuses to start a VM with an entry whose "+
		"device does not match the mapping's mdev flag, and a mismatched entry stops the node's other entries "+
		"too. Change the flag in Proxmox, or replace the mapping's only entry, which the flag then follows.")
}

// updatePCIMappingRequest makes the update the request asks for — everything
// between reading the request and auditing it:
//
//  1. add_node, when given, is one of the cluster's nodes: its devices are a
//     /nodes/{node} read, which pveproxy forwards wherever the name resolves.
//  2. The plain listing, read first: a digest other than the caller's is 409
//     and nothing more is read or written; a mapping that is gone is 404.
//  3. map is a sub-multiset of the mapping's entries, exactly as listed: no
//     caller can write an entry of its own spelling. And each of them reads,
//     since each is written back as read; one that does not is refused.
//  4. replace, when given, is an entry of map, on add_node.
//  5. The new entry is built from add_node's own report of the device at
//     add_path, and refused when it is, or overlaps, an entry the node keeps.
//  6. The mdev flag: while other entries remain, the device must match it —
//     an entry that does not stops the node's others too; as the only entry
//     left, the device decides it (mdev=1, or delete=mdev).
//  7. The entry goes in replace's place, keeping replace's own description,
//     or at the end.
//  8. The PUT carries the caller's digest, which Proxmox checks again under
//     its lock.
func updatePCIMappingRequest(ctx context.Context, px pciMappingEditor, isMember func(context.Context, string) (bool, error),
	p *apischema.Params) (pciMappingUpdate, error) {
	id := p.String("mapping_id")
	keep := p.Strings("map")
	digest := p.String("digest")
	addNode, adding := p.OptString("add_node")
	addPath, _ := p.OptString("add_path")
	replace, replacing := p.OptString("replace")

	if adding {
		member, err := isMember(ctx, addNode)
		if err != nil {
			return pciMappingUpdate{}, err
		}
		if !member {
			return pciMappingUpdate{}, errMappingNodeNotMember
		}
	}

	current, fileDigest, err := findMapping(ctx, px, pciMappingEditor.ListPCIMappings, pciMappingView, id)
	switch {
	case err != nil:
		return pciMappingUpdate{}, mapProxmoxError(err)
	case fileDigest != "" && fileDigest != digest:
		return pciMappingUpdate{}, fiber.NewError(fiber.StatusConflict, pciMappingStaleMessage)
	case current == nil:
		// With no mapping at all there is no digest to compare, and the
		// mapping is certainly gone.
		return pciMappingUpdate{}, fiber.NewError(fiber.StatusNotFound, pciMappingMissing)
	}

	removed, ok := pciKeptEntries(current.Map, keep)
	if !ok {
		return pciMappingUpdate{}, fiber.NewError(fiber.StatusBadRequest, "map must list only entries the mapping "+
			"has, each exactly as the listing returns it and no more often than the mapping has it.")
	}
	// Every kept entry is written back as the parse reads it
	// (proxmox.CanonicalPCIMap), which refuses one it cannot read. Refused
	// here, before any device is read, so that the checks below read every
	// entry the mapping keeps.
	kept := make([]proxmox.PCIMapEntry, len(keep))
	for i, raw := range keep {
		e, err := proxmox.ParsePCIMapEntry(raw)
		switch {
		case err == nil:
			kept[i] = e
		case replacing && raw == replace:
			return pciMappingUpdate{}, fiber.NewError(fiber.StatusBadRequest,
				"Nexara cannot read the entry to replace, so it can only be removed.")
		default:
			return pciMappingUpdate{}, fiber.NewError(fiber.StatusBadRequest,
				strings.TrimPrefix(err.Error(), proxmox.ErrInvalidInput.Error()+": ")+
					". Nexara cannot save the mapping with it: leave it out of map to remove it.")
		}
	}
	upd := pciMappingUpdate{id: id, removed: removed}
	final := slices.Clone(keep)
	var mdev *bool

	if adding {
		at := -1
		var replaced proxmox.PCIMapEntry
		if replacing {
			at = slices.Index(keep, replace)
			if at < 0 {
				return pciMappingUpdate{}, fiber.NewError(fiber.StatusBadRequest, "replace must be one of the entries in map.")
			}
			replaced = kept[at]
			if replaced.Node != addNode {
				return pciMappingUpdate{}, fiber.NewError(fiber.StatusBadRequest,
					"The entry to replace is on node "+replaced.Node+", not on add_node: a replacement stays on its node.")
			}
		}

		devCtx, cancel := context.WithTimeout(ctx, pciMappingDeviceTimeout)
		devices, err := px.ListNodePCIDevicesAllClasses(devCtx, addNode)
		cancel()
		if err != nil {
			return pciMappingUpdate{}, mapProxmoxError(err)
		}
		entry, deviceMDev, err := proxmox.PCIMapEntryForDevice(addNode, addPath, devices)
		if err != nil {
			return pciMappingUpdate{}, mapProxmoxError(err)
		}
		for i, e := range kept {
			if i != at && e.Node == addNode && pciPathsOverlap(e.Path, addPath) {
				return pciMappingUpdate{}, fiber.NewError(fiber.StatusBadRequest, fmt.Sprintf("%s already has %s in "+
					"mapping %s; a device can be in a mapping once per node, as a whole or as one of its functions.",
					addNode, e.Path, id))
			}
		}

		others := len(keep)
		if replacing {
			others--
		}
		switch flag := bool(current.MDev); {
		case deviceMDev == flag:
		case others > 0:
			return pciMappingUpdate{}, pciMdevMismatch(id, addNode, addPath, deviceMDev)
		default:
			mdev = &deviceMDev
		}

		if replacing {
			entry.Description = replaced.Description
			final[at] = entry.String()
			upd.replaced = replace
		} else {
			final = append(final, entry.String())
		}
		upd.added = &entry
	}

	params := proxmox.UpdatePCIMappingParams{Map: final, MDev: mdev, Digest: digest}
	if description, set := p.OptString("description"); set {
		params.Description = &description
	}
	if err := px.UpdatePCIMapping(ctx, id, params); err != nil {
		return pciMappingUpdate{}, mapPCIMappingUpdateError(err)
	}
	upd.params = params
	return upd, nil
}

// UpdatePCIMapping handles PUT /api/v1/clusters/:cluster_id/pci-mappings/:mapping_id.
func (h *VMHandler) UpdatePCIMapping(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	upd, err := updatePCIMappingRequest(c.Context(), pxClient, h.nodeInCluster(clusterID), p)
	if err != nil {
		return err
	}
	// The entries as Proxmox now stores them. The client has just written
	// exactly this, so the error cannot happen here.
	written, _ := proxmox.CanonicalPCIMap(upd.params.Map)
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), "pci_mapping", upd.id, "updated",
		pciMappingUpdateDetails(upd, written))
	return c.JSON(fiber.Map{"status": "ok"})
}

// pciMappingAuditMap is usbMappingAuditMap for a PCI mapping's entries: each
// recorded as it was when it fits usbMappingAuditTextMax, and otherwise with
// only its description cut, re-written by the PCI parse — or cut whole when it
// does not parse — at most usbMappingAuditEntries of them, and map_count.
func pciMappingAuditMap(detail map[string]any, entries []string) {
	detail["map"] = pciMappingAuditEntries(entries)
	detail["map_count"] = len(entries)
}

// pciMappingAuditEntries is the capped list pciMappingAuditMap records.
func pciMappingAuditEntries(entries []string) []string {
	recorded := make([]string, 0, min(len(entries), usbMappingAuditEntries))
	for _, raw := range entries[:min(len(entries), usbMappingAuditEntries)] {
		if utf8.RuneCountInString(raw) <= usbMappingAuditTextMax {
			recorded = append(recorded, raw)
			continue
		}
		e, err := proxmox.ParsePCIMapEntry(raw)
		if err != nil {
			recorded = append(recorded, auditTruncate(raw, usbMappingAuditTextMax))
			continue
		}
		e.Description = auditTruncate(e.Description, usbMappingAuditTextMax)
		// And the whole of it bounded too: a stored path may be a long list,
		// and nothing holds a node name to a length.
		recorded = append(recorded, auditTruncate(e.String(), 2*usbMappingAuditTextMax))
	}
	return recorded
}

// pciMappingUpdateDetails is the audit detail of a completed update: every
// entry as written, what was added — with the ids the node reported for the
// device — what it replaced, what was removed, a change to the mdev flag, and
// the description when the update touched it.
func pciMappingUpdateDetails(upd pciMappingUpdate, written []string) json.RawMessage {
	detail := map[string]any{}
	pciMappingAuditMap(detail, written)
	if e := upd.added; e != nil {
		added := map[string]any{"node": e.Node, "path": e.Path, "device_id": e.ID}
		if e.SubsystemID != "" {
			added["subsystem_id"] = e.SubsystemID
		}
		if e.IOMMUGroup != nil {
			added["iommugroup"] = *e.IOMMUGroup
		}
		detail["added"] = added
	}
	if upd.replaced != "" {
		detail["replaced"] = pciMappingAuditEntries([]string{upd.replaced})[0]
	}
	if len(upd.removed) > 0 {
		detail["removed"] = pciMappingAuditEntries(upd.removed)
		detail["removed_count"] = len(upd.removed)
	}
	if upd.params.MDev != nil {
		detail["mdev"] = *upd.params.MDev
	}
	if d := upd.params.Description; d != nil {
		if *d == "" {
			detail["description_removed"] = true
		} else {
			detail["description"] = auditTruncate(*d, usbMappingAuditTextMax)
		}
	}
	details, _ := json.Marshal(detail)
	return details
}

// pciMappingDeleteDetails is the audit detail of a completed delete of a PCI
// mapping: the mapping as it was — entries, flags and description — which is
// enough to create it again. As for USB, a snapshot that could not be taken
// is marked, and its error kept out of the row.
func pciMappingDeleteDetails(snapshot *proxmox.PCIMapping, priorStateUnknown bool) json.RawMessage {
	detail := map[string]any{}
	if priorStateUnknown {
		detail["prior_state_unknown"] = true
	}
	if snapshot != nil {
		pciMappingAuditMap(detail, snapshot.Map)
		if snapshot.Description != "" {
			detail["description"] = auditTruncate(snapshot.Description, usbMappingAuditTextMax)
		}
		if snapshot.MDev {
			detail["mdev"] = true
		}
		if snapshot.LiveMigrationCapable {
			detail["live_migration_capable"] = true
		}
	}
	details, _ := json.Marshal(detail)
	return details
}

// deletePCIMapping is deleteMapping for a PCI mapping.
func deletePCIMapping(ctx context.Context, px pciMappingDeleter, id, digest string) (mappingDeleteOutcome[proxmox.PCIMapping], error) {
	return deleteMapping(ctx, px, pciMappingDeleter.ListPCIMappings, pciMappingDeleter.DeletePCIMapping,
		pciMappingView, id, digest)
}

// deletePCIMappingRequest makes the delete the request asks for, as
// deleteUSBMappingRequest does for USB.
func deletePCIMappingRequest(ctx context.Context, px pciMappingDeleter,
	p *apischema.Params) (id string, outcome mappingDeleteOutcome[proxmox.PCIMapping], err error) {
	id = p.String("mapping_id")
	digest, _ := p.OptString("digest")
	outcome, err = deletePCIMapping(ctx, px, id, digest)
	return id, outcome, err
}

// DeletePCIMapping handles DELETE /api/v1/clusters/:cluster_id/pci-mappings/:mapping_id.
//
// Idempotent, like the Proxmox endpoint underneath, as DeleteUSBMapping is.
func (h *VMHandler) DeletePCIMapping(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	id, outcome, err := deletePCIMappingRequest(c.Context(), pxClient, p)
	if err != nil {
		return err
	}
	if outcome.priorStateUnknown {
		slog.Warn("PCI mapping delete: could not read the mappings to snapshot this one; auditing without its entries",
			"cluster_id", clusterID, "mapping_id", id, "error", outcome.snapErr)
	}
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), "pci_mapping", id, outcome.action,
		pciMappingDeleteDetails(outcome.snapshot, outcome.priorStateUnknown))
	return c.JSON(fiber.Map{"status": "ok"})
}

// scanPCIMappingUsage is scanMappingUsage for a PCI mapping: the VMs whose
// hostpciN names it.
func scanPCIMappingUsage(ctx context.Context, px guestConfigReader, id string) (mappingUsage, error) {
	return scanMappingUsage(ctx, px, pciMappingView.kind, id, proxmox.VMConfig.PCIMappingKeys)
}

// checkPCIMappingUsage is checkMappingUsage for a PCI mapping, in the same
// slots as the USB checks (mappingUsageScans): the caps are a user's and a
// cluster's whichever kind they check.
func checkPCIMappingUsage(ctx context.Context, slots *usageScanSlots, cluster, user uuid.UUID, id string,
	reader func() (guestConfigReader, error)) (mappingUsage, error) {
	return checkMappingUsage(ctx, slots, cluster, user, reader,
		func(scanCtx context.Context, px guestConfigReader) (mappingUsage, error) {
			return scanPCIMappingUsage(scanCtx, px, id)
		})
}

// GetPCIMappingUsage handles GET /api/v1/clusters/:cluster_id/pci-mappings/:mapping_id/usage.
func (h *VMHandler) GetPCIMappingUsage(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}
	// As for USB: the route is authenticated, so there is always a user.
	userID, _ := c.Locals("user_id").(uuid.UUID)
	usage, err := checkPCIMappingUsage(c.Context(), mappingUsageScans, clusterID, userID,
		p.String("mapping_id"), func() (guestConfigReader, error) {
			pxClient, err := h.createProxmoxClient(c, clusterID)
			if err != nil {
				return nil, err
			}
			return pxClient, nil
		})
	if err != nil {
		return err
	}
	return c.JSON(usage)
}
