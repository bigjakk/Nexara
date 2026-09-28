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

// --- The cluster-wide listing -------------------------------------------------

// usbMappingCheckTimeout bounds each call the cluster-wide listing makes per
// node. Proxmox runs a check ON the node it names (the listing's
// proxyto_callback), so a node that is up in the cluster's view but not
// answering would otherwise hold the whole page for the client's five-minute
// timeout: fiber.Ctx.Context() carries no deadline of its own. A variable
// only so the tests can shorten it.
var usbMappingCheckTimeout = 10 * time.Second

// usbMappingCheckBudget bounds all of one listing's node checks together, so
// a cluster of slow nodes cannot hold the page for a timeout per node. A node
// not asked, or not answered, by then is reported unchecked. A variable only so
// the tests can shorten it.
var usbMappingCheckBudget = 20 * time.Second

// usbMappingCheckConcurrency bounds how many nodes are checked at once.
const usbMappingCheckConcurrency = 4

// clusterUSBMapping is one USB mapping as the cluster-wide listing answers it:
// the mapping as Proxmox stores it, and for every node one of its entries
// names, either what Proxmox reported checking it there or why it was not
// checked. Every such node is in exactly one of the two maps — a node that
// could not be checked is never reported as a clean check.
type clusterUSBMapping struct {
	ID          string   `json:"id"`
	Description string   `json:"description"`
	Map         []string `json:"map"`
	// Digest is the whole usb.cfg's, from the same read as Map. An update
	// sends it back, so it conflicts if any USB mapping changed since.
	Digest string `json:"digest"`
	// NodeChecks is what Proxmox reported for this mapping running the check
	// on that node: an empty list is a clean check.
	NodeChecks map[string][]proxmox.MappingCheck `json:"node_checks"`
	// Unchecked says why a node was not checked.
	Unchecked map[string]string `json:"unchecked"`
}

// usbMappingNodeCheck is one node's check-node listing, or why there is none.
type usbMappingNodeCheck struct {
	mappings []proxmox.USBMapping
	// reason is set, and mappings unused, when the node was not checked.
	reason string
}

// usbMappingLister reads the cluster's USB mappings, optionally checked on a
// node. The interfaces here are the parts of the Proxmox client each flow
// uses, so every flow can be tested without a cluster.
type usbMappingLister interface {
	ListUSBMappings(ctx context.Context, checkNode string) ([]proxmox.USBMapping, error)
}

// usbMappingReader is what the listing and the usage scan read through.
type usbMappingReader interface {
	usbMappingLister
	GetNodes(ctx context.Context) ([]proxmox.NodeListEntry, error)
	GetClusterResources(ctx context.Context, resourceType string) ([]proxmox.ClusterResource, error)
	GetVMConfig(ctx context.Context, node string, vmid int) (proxmox.VMConfig, error)
}

// usbMappingDeleter is what the delete flow reads and writes.
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

// usbMapEntryNode is the node a stored entry names, or "". Deliberately
// lenient — the first node= value, whatever else the entry holds — because it
// only decides which node to ask: the node's own check is the authority on
// the entry. An entry Proxmox cannot parse is one its check skips (a warning
// in get_node_mapping), so it reports the node as having no entry, and that
// is what the page shows.
func usbMapEntryNode(raw string) string {
	for _, part := range strings.Split(raw, ",") {
		if node, ok := strings.CutPrefix(part, "node="); ok {
			return node
		}
	}
	return ""
}

// usbMappingEntryNodes is every node an entry of the listing names, each once.
func usbMappingEntryNodes(mappings []proxmox.USBMapping) []string {
	var nodes []string
	for _, m := range mappings {
		for _, raw := range m.Map {
			if node := usbMapEntryNode(raw); node != "" && !slices.Contains(nodes, node) {
				nodes = append(nodes, node)
			}
		}
	}
	return nodes
}

// usbMappingNodeState is why a node is not worth asking, or "" when it is,
// from the cluster's node list: its members and their status.
func usbMappingNodeState(node string, members map[string]string) string {
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

// checkUSBMappingNodes runs the check-node listing on each node that the
// mappings name and that is an online member of the cluster, a few at a time,
// each under its own deadline and all within usbMappingCheckBudget, and
// returns what each answered — or why it was not asked or did not answer.
// Every node in nodes gets an entry.
//
// Without the cluster's node list nothing is asked. An entry can name any
// node-name-shaped host — Proxmox does not check it against the cluster — and
// Proxmox resolves a name that is not a member through DNS and connects to it,
// so asking blind would let whoever stored the names point the cluster at
// hosts of their choosing on every page load.
func checkUSBMappingNodes(ctx context.Context, px usbMappingReader, nodes []string) map[string]usbMappingNodeCheck {
	out := make(map[string]usbMappingNodeCheck, len(nodes))
	if len(nodes) == 0 {
		return out
	}
	budget, cancel := context.WithTimeout(ctx, usbMappingCheckBudget)
	defer cancel()

	statusCtx, cancelStatus := context.WithTimeout(budget, usbMappingCheckTimeout)
	entries, statusErr := px.GetNodes(statusCtx)
	cancelStatus()
	if statusErr != nil {
		slog.Warn("USB mapping listing: could not read the cluster's nodes; checking none", "error", statusErr)
		for _, node := range nodes {
			out[node] = usbMappingNodeCheck{reason: "Could not read the cluster's nodes, so no node was checked."}
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
		if reason := usbMappingNodeState(node, members); reason != "" {
			out[node] = usbMappingNodeCheck{reason: reason}
			continue
		}
		ask = append(ask, node)
	}

	var mu sync.Mutex
	record := func(node string, check usbMappingNodeCheck) {
		mu.Lock()
		out[node] = check
		mu.Unlock()
	}
	outOfTime := usbMappingOutOfTime("The checks", usbMappingCheckBudget)
	eachWithin(budget, usbMappingCheckConcurrency, ask,
		func(node string) {
			callCtx, cancel := context.WithTimeout(budget, usbMappingCheckTimeout)
			defer cancel()
			mappings, err := px.ListUSBMappings(callCtx, node)
			switch {
			case err == nil:
				record(node, usbMappingNodeCheck{mappings: mappings})
			case budget.Err() != nil:
				record(node, usbMappingNodeCheck{reason: outOfTime})
			default:
				record(node, usbMappingNodeCheck{reason: unansweredReason(callCtx, err, usbMappingCheckTimeout)})
			}
		},
		func(node string) {
			record(node, usbMappingNodeCheck{reason: outOfTime})
		})
	return out
}

// usbMappingOutOfTime is an unchecked item's reason when the whole run's
// budget ran out before it was asked, or while it was.
func usbMappingOutOfTime(what string, budget time.Duration) string {
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

// mergeUSBMappingChecks folds the per-node checks into the plain listing.
//
// A node's check is used for a mapping only when it came from the same
// usb.cfg as the listing — the digests match. Otherwise the check describes a
// file the page is not showing, and the node is reported unchecked rather than
// letting, say, a check of an entry since replaced vouch for the new one.
func mergeUSBMappingChecks(plain []proxmox.USBMapping, checks map[string]usbMappingNodeCheck) []clusterUSBMapping {
	out := make([]clusterUSBMapping, 0, len(plain))
	for _, m := range plain {
		cm := clusterUSBMapping{
			ID:          m.ID,
			Description: m.Description,
			Map:         m.Map,
			Digest:      m.Digest,
			NodeChecks:  map[string][]proxmox.MappingCheck{},
			Unchecked:   map[string]string{},
		}
		if cm.Map == nil {
			cm.Map = []string{}
		}
		for _, raw := range m.Map {
			node := usbMapEntryNode(raw)
			if node == "" {
				continue
			}
			check, asked := checks[node]
			if !asked {
				// Not reachable through ListClusterUSBMappings, which asks
				// every node an entry names; said rather than assumed clean.
				cm.Unchecked[node] = "Nexara did not check this node."
				continue
			}
			if check.reason != "" {
				cm.Unchecked[node] = check.reason
				continue
			}
			i := slices.IndexFunc(check.mappings, func(c proxmox.USBMapping) bool { return c.ID == m.ID })
			switch {
			case i < 0:
				cm.Unchecked[node] = "The node's check did not list this mapping."
			case check.mappings[i].Digest != m.Digest:
				cm.Unchecked[node] = "The USB mappings changed while they were being checked. Reload to check again."
			default:
				errs := check.mappings[i].Errors
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

// findUSBMapping reads the mapping id from the plain listing: the mapping when
// it is there, nil when the listing does not hold it, and the error when the
// listing could not be read. fileDigest is usb.cfg's digest, which every
// listed mapping carries — "" only when the listing holds no mapping at all.
func findUSBMapping(ctx context.Context, px usbMappingLister, id string) (mapping *proxmox.USBMapping, fileDigest string, err error) {
	mappings, err := px.ListUSBMappings(ctx, "")
	if err != nil {
		return nil, "", err
	}
	for i := range mappings {
		if fileDigest == "" {
			fileDigest = mappings[i].Digest
		}
		if mappings[i].ID == id {
			mapping = &mappings[i]
		}
	}
	return mapping, fileDigest, nil
}

// classifyUSBMappingDelete decides what a completed DELETE of a USB mapping
// may claim, from the pre-delete snapshot attempt. The HA-rule precedent,
// classifyHARuleDelete in ha.go, for the same reason: Proxmox's delete
// succeeds whether or not the mapping exists, so its 200 says the mapping is
// gone now, never that this request removed it.
//
// The snapshot and the delete are two round trips and Proxmox's delete takes
// no digest, so this narrows the doubt rather than closing it: a mapping
// created or deleted by someone else between the two reads is misreported.
// "already_deleted" is evidence of a no-op, not proof of one.
func classifyUSBMappingDelete(snapshot *proxmox.USBMapping, snapErr error) (action string, priorStateUnknown bool) {
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

// usbMappingDeleteStaleMessage is the 409 a delete answers when the digest it
// carried no longer matches usb.cfg. Nothing was deleted.
const usbMappingDeleteStaleMessage = "The cluster's USB mappings changed since they were loaded, so nothing " +
	"was deleted — reload and try again. A change to any USB mapping counts, not only to this one."

// usbMappingDeleteOutcome is what a completed delete may claim, for its
// audit row.
type usbMappingDeleteOutcome struct {
	snapshot          *proxmox.USBMapping
	snapErr           error
	action            string
	priorStateUnknown bool
}

// deleteUSBMapping is the delete flow: a snapshot of the mapping first — the
// audit row's entries, and what tells a deletion from a no-op — then Proxmox's
// delete.
//
// With a digest, the delete is refused with 409 unless usb.cfg still has the
// digest the caller's listing had — compared whether or not the mapping is
// still listed, since one deleted and made again since is a change too.
// Proxmox's delete takes no digest, so this is Nexara's check, made against
// its own read: it narrows the window to the round trip between that read
// and the delete rather than closing it. It is what keeps a delete aimed at
// the mapping the operator was shown — its entries, say — from removing one
// that changed meanwhile. A snapshot that could not be read cannot be
// compared, so a delete asked to compare does not go ahead. Only a listing
// with no mapping at all carries no digest to compare: then the delete is the
// no-op it would have been without one.
func deleteUSBMapping(ctx context.Context, px usbMappingDeleter, id, digest string) (usbMappingDeleteOutcome, error) {
	snapshot, fileDigest, snapErr := findUSBMapping(ctx, px, id)
	if digest != "" {
		switch {
		case snapErr != nil:
			return usbMappingDeleteOutcome{}, mapProxmoxError(snapErr)
		case fileDigest != "" && fileDigest != digest:
			return usbMappingDeleteOutcome{}, fiber.NewError(fiber.StatusConflict, usbMappingDeleteStaleMessage)
		}
	}
	if err := px.DeleteUSBMapping(ctx, id); err != nil {
		return usbMappingDeleteOutcome{}, mapProxmoxError(err)
	}
	action, priorStateUnknown := classifyUSBMappingDelete(snapshot, snapErr)
	return usbMappingDeleteOutcome{
		snapshot:          snapshot,
		snapErr:           snapErr,
		action:            action,
		priorStateUnknown: priorStateUnknown,
	}, nil
}

// deleteUSBMappingRequest makes the delete the request asks for: the id from
// the path and the digest when the caller sent one — everything between
// reading the request and auditing it, so the route's digest is tested all
// the way to the check that uses it.
func deleteUSBMappingRequest(ctx context.Context, px usbMappingDeleter, p *apischema.Params) (string, usbMappingDeleteOutcome, error) {
	id := p.String("mapping_id")
	digest, _ := p.OptString("digest")
	outcome, err := deleteUSBMapping(ctx, px, id, digest)
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
// the mapping. Variables only so the tests can shorten them.
var (
	usbMappingUsageTimeout = 10 * time.Second
	usbMappingUsageBudget  = 30 * time.Second
)

// usbMappingUsageConcurrency bounds how many configs one scan reads at once.
const usbMappingUsageConcurrency = 8

// usbMappingUsageScansPerCluster bounds the scans in flight against one
// cluster, and usageScanSlots holds each user to one at a time. Each scan is
// a Proxmox request per VM, and the route's limiter
// (usbMappingUsageLimiter in internal/api/middleware.go, per user) bounds
// how often one starts, not how many overlap. Per cluster, so no caller can
// hold the slots another cluster's check needs; per user, so no one account
// can hold both of a cluster's. A caller over the cluster's cap gets 429 at
// once rather than a wait: the SPA retries a 429 once, and the delete dialog
// shows a failed check as a warning and still offers the delete.
const usbMappingUsageScansPerCluster = 2

// usbMappingUsageSupersedeWait bounds how long a user's new scan waits for
// the older one it replaces to stop. A variable only so the tests can
// shorten it.
var usbMappingUsageSupersedeWait = 2 * time.Second

// errUSBMappingUsageClusterFull answers a scan the cluster has no room for.
var errUSBMappingUsageClusterFull = fiber.NewError(fiber.StatusTooManyRequests,
	"Other checks of which VMs use a USB mapping are running on this cluster; try again in a moment.")

// errUSBMappingUsageSuperseded answers a scan a newer one of the same user's
// replaced before it finished.
var errUSBMappingUsageSuperseded = fiber.NewError(fiber.StatusConflict,
	"A newer check of which VMs use a USB mapping, by the same user, replaced this one.")

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
// The older request answers errUSBMappingUsageSuperseded.
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
	if s.byCluster[cluster] >= usbMappingUsageScansPerCluster && (older == nil || older.cluster != cluster) {
		s.mu.Unlock()
		return nil, nil, errUSBMappingUsageClusterFull
	}
	if older != nil {
		older.cancel()
		s.mu.Unlock()
		select {
		case <-older.done:
		case <-time.After(usbMappingUsageSupersedeWait):
			return nil, nil, fiber.NewError(fiber.StatusTooManyRequests,
				"Your previous check of which VMs use a USB mapping is still stopping; try again in a moment.")
		}
		s.mu.Lock()
		// Another request of the user's may have taken the slot meanwhile.
		if s.byUser[user] != nil {
			s.mu.Unlock()
			return nil, nil, fiber.NewError(fiber.StatusTooManyRequests,
				"Another check of which VMs use a USB mapping, by you, has just started; try again in a moment.")
		}
	}
	// Looked at again: others may have taken the room while this one waited.
	if s.byCluster[cluster] >= usbMappingUsageScansPerCluster {
		s.mu.Unlock()
		return nil, nil, errUSBMappingUsageClusterFull
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

// usbMappingUsageScans is every usage scan in flight in this process.
var usbMappingUsageScans = newUsageScanSlots()

// usbMappingBudgetReason is an unchecked guest's reason when the scan's
// budget ran out before its config was read, or while it was being read.
func usbMappingBudgetReason() string {
	return fmt.Sprintf("Not read: the scan ran out of its %s.", usbMappingUsageBudget)
}

// usbMappingGuest is a guest in the usage answer.
type usbMappingGuest struct {
	VMID int    `json:"vmid"`
	Name string `json:"name"`
	Node string `json:"node"`
	// Keys are the usbN keys that pass the mapping through; set for a user.
	Keys []string `json:"keys,omitempty"`
	// Reason says why the config was not read; set for an unchecked guest.
	Reason string `json:"reason,omitempty"`
}

// usbMappingUsage is which VMs use a USB mapping. Unchecked is not a subset
// of anything: a guest listed there may or may not use the mapping.
type usbMappingUsage struct {
	MappingID string            `json:"mapping_id"`
	Checked   int               `json:"checked"`
	Users     []usbMappingGuest `json:"users"`
	Unchecked []usbMappingGuest `json:"unchecked"`
}

// scanUSBMappingUsage reads the config of every VM in the cluster and reports
// the ones whose usbN names the mapping, and every one it could not read.
//
// Nexara keeps no guest configs, so this is the only way to know. It reads
// live: the cluster's guest list and node status from Proxmox, then each VM's
// config on the node that holds it. A VM on a node that is not online, or
// whose read fails or runs out of time, is reported unchecked with the reason.
// Without the node list every VM is read: the guest list comes from Proxmox,
// so it names only real members, and a read of a VM on a dead node fails
// alone. Containers are not read: a container cannot use a USB mapping.
func scanUSBMappingUsage(ctx context.Context, px usbMappingReader, id string) (usbMappingUsage, error) {
	usage := usbMappingUsage{MappingID: id, Users: []usbMappingGuest{}, Unchecked: []usbMappingGuest{}}
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
		slog.Warn("USB mapping usage: could not read the nodes' status; reading every guest's config",
			"mapping_id", id, "error", statusErr)
	}
	members := make(map[string]string, len(entries))
	for _, n := range entries {
		members[n.Node] = n.Status
	}

	// Decided before any worker starts, so these appends race with nothing;
	// from the fan-out on, usage is written only under mu.
	read := make([]usbMappingGuest, 0, len(guests))
	for _, g := range guests {
		if g.Type != "qemu" {
			continue
		}
		guest := usbMappingGuest{VMID: g.VMID, Name: g.Name, Node: g.Node}
		if statusErr == nil {
			if reason := usbMappingNodeState(g.Node, members); reason != "" {
				guest.Reason = reason
				usage.Unchecked = append(usage.Unchecked, guest)
				continue
			}
		}
		read = append(read, guest)
	}

	var mu sync.Mutex
	unchecked := func(guest usbMappingGuest, reason string) {
		guest.Reason = reason
		mu.Lock()
		usage.Unchecked = append(usage.Unchecked, guest)
		mu.Unlock()
	}
	eachWithin(budget, usbMappingUsageConcurrency, read,
		func(guest usbMappingGuest) {
			callCtx, cancel := context.WithTimeout(budget, usbMappingUsageTimeout)
			defer cancel()
			config, err := px.GetVMConfig(callCtx, guest.Node, guest.VMID)
			switch {
			case err == nil:
				mu.Lock()
				defer mu.Unlock()
				usage.Checked++
				if keys := config.USBMappingKeys(id); len(keys) > 0 {
					guest.Keys = keys
					usage.Users = append(usage.Users, guest)
				}
			case budget.Err() != nil:
				unchecked(guest, usbMappingBudgetReason())
			default:
				unchecked(guest, unansweredReason(callCtx, err, usbMappingUsageTimeout))
			}
		},
		func(guest usbMappingGuest) {
			unchecked(guest, usbMappingBudgetReason())
		})

	byVMID := func(a, b usbMappingGuest) int { return a.VMID - b.VMID }
	slices.SortFunc(usage.Users, byVMID)
	slices.SortFunc(usage.Unchecked, byVMID)
	return usage, nil
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
	usage, err := checkUSBMappingUsage(c.Context(), usbMappingUsageScans, clusterID, userID,
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

// checkUSBMappingUsage is one usage check, whole: a slot first — before any
// Proxmox client is made, so a refused caller costs nothing — then the scan,
// run under the SLOT's context so that a newer check of the same user's can
// stop it, then its answer. The client comes from reader, called only once a
// slot is held.
func checkUSBMappingUsage(ctx context.Context, slots *usageScanSlots, cluster, user uuid.UUID, id string,
	reader func() (usbMappingReader, error)) (usbMappingUsage, error) {
	scanCtx, release, err := slots.acquire(ctx, cluster, user)
	if err != nil {
		return usbMappingUsage{}, err
	}
	defer release()
	px, err := reader()
	if err != nil {
		return usbMappingUsage{}, err
	}
	usage, scanErr := scanUSBMappingUsage(scanCtx, px, id)
	return usbMappingUsageAnswer(scanCtx, usage, scanErr)
}

// usbMappingUsageAnswer is what a finished scan answers. One that a newer
// scan of the same user's replaced answers errUSBMappingUsageSuperseded, not
// its partial result — its unread guests would carry reasons that are not
// true of them — and not the Proxmox error its cancelled calls produced.
func usbMappingUsageAnswer(scanCtx context.Context, usage usbMappingUsage, err error) (usbMappingUsage, error) {
	if errors.Is(scanCtx.Err(), context.Canceled) {
		return usbMappingUsage{}, errUSBMappingUsageSuperseded
	}
	if err != nil {
		return usbMappingUsage{}, mapProxmoxError(err)
	}
	return usage, nil
}
