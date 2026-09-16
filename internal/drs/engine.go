package drs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/proxmox"
)

// Weights holds the scoring weight configuration.
type Weights struct {
	CPU    float64 `json:"cpu"`
	Memory float64 `json:"memory"`
}

// DefaultWeights returns the default scoring weights.
// Memory is weighted higher because it is a more stable and constraining
// resource than CPU, which tends to be spiky and transient.
func DefaultWeights() Weights {
	return Weights{CPU: 0.3, Memory: 0.7}
}

// NodeScore holds the computed load score for a node.
type NodeScore struct {
	Node    string
	Score   float64
	CPULoad float64
	MemLoad float64
}

// Workload represents a VM or CT running on a node.
type Workload struct {
	VMID     int
	Name     string
	Type     string // "qemu" or "lxc"
	Node     string
	CPUUsage float64
	CPUs     int
	Mem      int64
	MaxMem   int64
	NetIn    int64
	NetOut   int64
	Status   string
	Pinned   bool // true = must not be migrated (PCI passthrough, HA pin, etc.)
}

// Recommendation is a single migration recommendation.
type Recommendation struct {
	VMID                int
	VMType              string
	SourceNode          string
	TargetNode          string
	Reason              string
	ScoreBefore         float64
	ScoreAfter          float64
	ExpectedImprovement float64
}

// EvalResult contains the full evaluation output including node scores.
type EvalResult struct {
	Recommendations []Recommendation
	NodeScores      map[string]NodeScore
	Imbalance       float64
	Threshold       float64
	Weights         Weights
	// BlockedByNativeCRS is set when Proxmox's native CRS dynamic load balancer
	// is auto-rebalancing, so Nexara DRS automatic migrations were suppressed to
	// avoid conflicting migrations. No recommendations are produced in this case.
	BlockedByNativeCRS bool
	BlockReason        string
}

// Engine is the DRS evaluation engine.
type Engine struct {
	// queries has exactly one use left: createClient's per-call client build
	// below. Everything Evaluate reads goes through evalReads, and the
	// migration history and audit writes belong to Executor, which is a
	// separate struct holding its own handle.
	queries       *db.Queries
	encryptionKey string
	cache         *proxmox.ClientCache // nil-safe; falls back to per-call construction
	logger        *slog.Logger
	// veeamOwned is the single read the Veeam pin needs, behind its own
	// interface so that decision can be tested without a database.
	veeamOwned veeamOwnedQueries
	// evalReads is the same seam for the two reads Evaluate makes directly, and
	// exists for the same reason: without it Evaluate cannot be called at all
	// without a database, so its safety gates — the HA maintenance filter and
	// the HA rule import, both of which must ABORT rather than proceed on an
	// unreadable listing — had no test at the level that actually ships them.
	//
	// Unlike veeamOwned this one is NOT nil-guarded at its use site, and that
	// is deliberate: there is no safe default for "what is this cluster's DRS
	// config?", so falling back to one would disable DRS silently. NewEngine
	// always assigns it, exactly as it always assigned the concrete handle
	// this replaced, so the failure mode for a struct-literal Engine is
	// unchanged — it was already a nil-deref panic on *db.Queries.
	evalReads evalQueries
	// newClient replaces createClient's cache-then-per-call construction when
	// set. nil in production (NewEngine never assigns it) — it is the seam that
	// lets an Evaluate test point the engine at an httptest Proxmox. A func
	// rather than an interface because there is exactly one operation, and
	// because *proxmox.Client is concrete the whole way down.
	newClient func(ctx context.Context, clusterID uuid.UUID) (*proxmox.Client, error)
}

// veeamOwnedQueries is the database surface pinVeeamInfrastructure uses.
type veeamOwnedQueries interface {
	ListVeeamInfrastructureGuestsForCluster(ctx context.Context, clusterID uuid.UUID) ([]db.ListVeeamInfrastructureGuestsForClusterRow, error)
}

// evalQueries is the database surface Evaluate reads through directly.
type evalQueries interface {
	GetDRSConfig(ctx context.Context, clusterID uuid.UUID) (db.DrsConfig, error)
	ListDRSRules(ctx context.Context, clusterID uuid.UUID) ([]db.DrsRule, error)
}

// NewEngine creates a new DRS engine.
func NewEngine(queries *db.Queries, encryptionKey string, logger *slog.Logger) *Engine {
	return &Engine{
		queries:       queries,
		encryptionKey: encryptionKey,
		logger:        logger,
		veeamOwned:    queries,
		evalReads:     queries,
	}
}

// SetProxmoxCache attaches the per-server cache. Nil-safe.
func (e *Engine) SetProxmoxCache(cache *proxmox.ClientCache) {
	e.cache = cache
}

// drsBlockedByNativeCRS reports whether DRS must suppress automatic migrations
// because Proxmox's native CRS dynamic load balancer is auto-rebalancing. Only
// automatic mode conflicts; advisory mode (recommendations only) is allowed.
func drsBlockedByNativeCRS(mode string, opts *proxmox.ClusterOptions) bool {
	if mode != "automatic" || opts == nil {
		return false
	}
	return proxmox.ParseCRSSettings(opts.CRS).AutoRebalanceActive()
}

// Evaluate runs DRS evaluation for a single cluster and returns recommendations.
func (e *Engine) Evaluate(ctx context.Context, clusterID uuid.UUID) (*EvalResult, error) {
	cfg, err := e.evalReads.GetDRSConfig(ctx, clusterID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("get DRS config: %w", err)
	}
	if !cfg.Enabled || cfg.Mode == "disabled" {
		return nil, nil
	}

	var weights Weights
	if err := json.Unmarshal(cfg.Weights, &weights); err != nil {
		weights = DefaultWeights()
	}

	client, err := e.createClient(ctx, clusterID)
	if err != nil {
		return nil, fmt.Errorf("create proxmox client: %w", err)
	}

	// Coexistence guard: Proxmox VE 9.2's native CRS dynamic load balancer, when
	// set to auto-rebalance, already live-migrates HA-managed guests. Running
	// Nexara DRS in automatic mode on top of that makes the two fight over the
	// same guests, so we suppress automatic migrations here. Advisory mode is
	// unaffected (recommendations only — no migration). Fail open on a read
	// error: the DRS config page surfaces native-CRS state separately, and a
	// transient options fetch shouldn't silently disable a configured feature.
	if cfg.Mode == "automatic" {
		if opts, optErr := client.GetClusterOptions(ctx); optErr != nil {
			e.logger.Warn("DRS: could not read cluster options for native-CRS coexistence check",
				"cluster_id", clusterID, "error", optErr)
		} else if drsBlockedByNativeCRS(cfg.Mode, opts) {
			e.logger.Info("DRS: suppressing automatic migrations — Proxmox native CRS auto-rebalance is enabled",
				"cluster_id", clusterID)
			return &EvalResult{
				BlockedByNativeCRS: true,
				BlockReason:        "Proxmox native CRS auto-rebalance is enabled on this cluster; Nexara DRS automatic migrations are disabled to avoid conflicting migrations. Advisory mode still works.",
			}, nil
		}
	}

	nodes, err := client.GetNodes(ctx)
	if err != nil {
		return nil, fmt.Errorf("get nodes: %w", err)
	}

	// Detect nodes in HA maintenance / shutdown / unhealthy state. These
	// must be excluded as both source and target — Proxmox is already
	// evacuating them for reboot, and DRS counter-migrating would create a
	// ping-pong with the HA manager. A status nobody could read stops the
	// evaluation rather than clearing the filter: see unhealthyHANodes.
	unhealthy, err := unhealthyHANodes(ctx, client, e.logger, clusterID)
	if err != nil {
		return nil, err
	}

	// Collect workloads per node.
	//
	// When include_containers is false, containers still count toward node load
	// scoring (so DRS has accurate scores) but are marked as pinned so they
	// won't be selected for migration. Container migration requires downtime
	// (stop → move → start) unlike VM live migration.
	//
	// A node is registered in nodeEntries — which is what makes it visible to
	// scoring and to the planner, as both a migration source and a target —
	// only once its guests have actually been read. Registering it up front and
	// then giving up on the listing is what this used to do, and it did not
	// merely leave the node unconstrained, it INVERTED its score: an empty
	// workload list reads as cpuLoad 0 and memLoad 0, so ScoreNode calls the
	// node idle, while the affinity checks find no guest on it for any rule to
	// conflict with. The node nobody could read thereby became the single most
	// attractive target on the cluster — findSourceTargetPairs ranks by score
	// gap, and a fabricated 0.0 is the widest gap available — so DRS piled
	// guests onto the one node it had no information about.
	//
	// Failing the whole evaluation is the wrong treatment at this level, unlike
	// the HA gates above. Those listings are cluster-wide and read once; this
	// one is per-node and read N times, so a single flaky node would stop all
	// balancing everywhere. Dropping the node keeps the other N-1 balancing and
	// is the honest position when its load and its guest list are both unknown.
	//
	// Dropping rather than PINNING is deliberate, and is the opposite of the
	// call made for Veeam guests and for containers under
	// include_containers=false (see pinVeeamInfrastructure and Workload.Pinned).
	// Pinning keeps a guest visible — still counted toward node load, still
	// matched by affinity rules — because its figures are known and only its
	// mobility is in question. Here nothing is known: there is no load to count
	// and no guest list to show a rule. An "unknown" marker would have to be
	// excluded from scoring, from targeting and from sourcing alike, which is
	// dropping the node with extra steps.
	//
	// What the drop does cost: a guest on the dropped node is invisible to
	// findVMNode, so an AFFINITY rule with a member there is skipped rather than
	// enforced (see isAffinityAllowed) and the movable member may be moved away
	// from a partner it should have stayed with. That fail-open predates this
	// and cannot be closed without the listing itself. The anti-affinity
	// direction — the one that co-locates guests that must never share a node —
	// is not affected, because the dropped node can no longer be a target.
	includeContainers := cfg.IncludeContainers
	nodeWorkloads := make(map[string][]Workload)
	nodeEntries := make(map[string]proxmox.NodeListEntry)
	eligible, unreadable := 0, 0
	var lastListErr error
	for _, n := range nodes {
		if n.Status != "online" {
			continue
		}
		if _, skip := unhealthy[n.Node]; skip {
			e.logger.Info("DRS skipping node in HA maintenance/shutdown",
				"cluster_id", clusterID,
				"node", n.Node,
			)
			continue
		}
		eligible++

		wl, err := collectNodeWorkloads(ctx, client, n.Node, includeContainers)
		if err != nil {
			unreadable++
			lastListErr = err
			e.logger.Warn("DRS excluding node from this evaluation: its guests could not be listed",
				"cluster_id", clusterID, "node", n.Node, "error", err)
			continue
		}
		nodeEntries[n.Node] = n
		if len(wl) > 0 {
			nodeWorkloads[n.Node] = wl
		}
	}

	// Dropping nodes one at a time is right while enough of the cluster remains
	// to compare; below two readable nodes there is nothing to compare, and the
	// drop must not be allowed to turn that into a verdict. CalculateImbalance
	// returns 0 for a map of fewer than two entries and Plan bails at the same
	// count, so Evaluate would otherwise hand back imbalance 0.0000 and no
	// recommendations — which the API renders, and the UI reads, as "Cluster
	// Balanced, variance 0%". That green light would be reporting the health of
	// a cluster two thirds of which nobody could read.
	//
	// The threshold is two rather than "all N failed" for exactly that reason:
	// the honesty defect appears as soon as fewer than two nodes survive, not
	// only when none do. GetNodes already hard-fails on cluster-wide faults, so
	// erroring here is consistent with it rather than a new failure mode.
	//
	// The unreadable count is what distinguishes zero from zero-out-of-zero —
	// the same distinction unhealthyHANodes draws for unparseable LRM entries. A
	// cluster whose nodes are all offline, or all in HA maintenance, also
	// arrives here under the node threshold, but nothing failed to read and
	// there is nothing to report; likewise a genuine single-node cluster, which
	// has no migrations available and never did.
	if unreadable > 0 && len(nodeEntries) < 2 {
		return nil, fmt.Errorf("only %d of %d eligible node(s) could be listed, need at least 2 to compare: %w",
			len(nodeEntries), eligible, lastListErr)
	}

	// Auto-import HA pin rules. A listing nobody could read stops the
	// evaluation: planning against the empty rule set it would otherwise yield
	// is how DRS migrates a guest off its node-affinity pin, or onto the node
	// holding its anti-affinity partner, with no error anywhere.
	haRules, err := e.importHARules(ctx, client, nodeWorkloads)
	if err != nil {
		return nil, fmt.Errorf("import HA rules: %w", err)
	}

	// Detect PCI/USB passthrough VMs and mark as pinned.
	e.detectPassthrough(ctx, client, nodeWorkloads)

	// Pin the guests Veeam owns. The config gate lives inside, so it is
	// covered by the same tests as the pinning itself.
	e.pinVeeamInfrastructure(ctx, clusterID, cfg.ExcludeVeeamWorkers, nodeWorkloads)

	// Score nodes (pinned workloads still count toward node load).
	scores := make(map[string]NodeScore)
	for name, n := range nodeEntries {
		wl := nodeWorkloads[name]
		scores[name] = ScoreNode(n, wl, weights)
	}

	imbalance := CalculateImbalance(scores)

	// Log per-node scores and per-dimension imbalance for debugging.
	for name, s := range scores {
		totalWL := len(nodeWorkloads[name])
		e.logger.Info("DRS node score",
			"cluster_id", clusterID,
			"node", name,
			"score", fmt.Sprintf("%.4f", s.Score),
			"cpu_load", fmt.Sprintf("%.4f", s.CPULoad),
			"mem_load", fmt.Sprintf("%.4f", s.MemLoad),
			"workloads", totalWL,
		)
	}

	result := &EvalResult{
		NodeScores: scores,
		Imbalance:  imbalance,
		Threshold:  cfg.ImbalanceThreshold,
		Weights:    weights,
	}

	if imbalance <= cfg.ImbalanceThreshold {
		e.logger.Info("cluster balanced", "cluster_id", clusterID,
			"imbalance", fmt.Sprintf("%.4f", imbalance),
			"threshold", cfg.ImbalanceThreshold)
		return result, nil
	}

	e.logger.Info("cluster imbalanced, planning migrations",
		"cluster_id", clusterID,
		"imbalance", fmt.Sprintf("%.4f", imbalance),
		"threshold", cfg.ImbalanceThreshold)

	// Load rules.
	dbRules, err := e.evalReads.ListDRSRules(ctx, clusterID)
	if err != nil {
		return nil, fmt.Errorf("list DRS rules: %w", err)
	}
	rules := ParseDBRules(dbRules)
	rules = append(rules, haRules...)

	// Pinned workloads (PCI/USB passthrough, containers when include_containers
	// is off, etc.) are NOT filtered out before planning. They still occupy
	// their node — so they must count toward node load scoring — and they still
	// participate in affinity/anti-affinity rules — so a movable VM is never
	// migrated onto a node hosting its pinned anti-affinity partner. The planner
	// skips them as migration *candidates* instead (see Plan). Filtering them
	// here previously made the planner blind to pinned rule members, which let
	// DRS recommend migrations Proxmox HA then rejected (exit code 2).
	pinnedCount := 0
	for _, wls := range nodeWorkloads {
		for _, w := range wls {
			if w.Pinned {
				pinnedCount++
			}
		}
	}

	e.logger.Info("DRS planner input",
		"cluster_id", clusterID,
		"pinned_count", pinnedCount,
		"rule_count", len(rules),
	)

	// Plan migrations over the full workload set; the planner keeps pinned VMs
	// for scoring and rule visibility but never selects them for migration.
	result.Recommendations = Plan(scores, nodeWorkloads, nodeEntries, rules, weights, cfg.ImbalanceThreshold, e.logger)

	return result, nil
}

// collectNodeWorkloads reads one node's running guests.
//
// BOTH listings must succeed for the node to be usable. Keeping the VMs when
// only the container listing failed is the tempting half-fix, and it reproduces
// the same bug at lower amplitude: the node is then undercounted rather than
// empty, which moves its score down — the one direction that makes it a more
// attractive migration target — while the containers it is actually running
// stay invisible to every anti-affinity check. Partial knowledge of a node's
// workload is not usable knowledge here, because every consumer of it reads
// "fewer guests" as "more room".
//
// Returning early on the VM listing also preserves the old behaviour of not
// issuing the container request for a node already known to be unreadable.
func collectNodeWorkloads(ctx context.Context, client *proxmox.Client, node string, includeContainers bool) ([]Workload, error) {
	vms, err := client.GetVMs(ctx, node)
	if err != nil {
		return nil, fmt.Errorf("list VMs: %w", err)
	}
	cts, err := client.GetContainers(ctx, node)
	if err != nil {
		return nil, fmt.Errorf("list containers: %w", err)
	}

	workloads := make([]Workload, 0, len(vms)+len(cts))
	for _, vm := range vms {
		if vm.Status != "running" || vm.Template == 1 {
			continue
		}
		workloads = append(workloads, Workload{
			VMID:     vm.VMID,
			Name:     vm.Name,
			Type:     "qemu",
			Node:     node,
			CPUUsage: vm.CPU,
			CPUs:     vm.CPUs,
			Mem:      vm.Mem,
			MaxMem:   vm.MaxMem,
			NetIn:    vm.NetIn,
			NetOut:   vm.NetOut,
			Status:   vm.Status,
		})
	}
	for _, ct := range cts {
		if ct.Status != "running" || ct.Template == 1 {
			continue
		}
		workloads = append(workloads, Workload{
			VMID:     ct.VMID,
			Name:     ct.Name,
			Type:     "lxc",
			Node:     node,
			CPUUsage: ct.CPU,
			CPUs:     ct.CPUs,
			Mem:      ct.Mem,
			MaxMem:   ct.MaxMem,
			NetIn:    ct.NetIn,
			NetOut:   ct.NetOut,
			Status:   ct.Status,
			Pinned:   !includeContainers,
		})
	}
	return workloads, nil
}

// ScoreNode computes a weighted load score for a node (0.0 = idle, 1.0 = fully loaded).
// CPU and memory are derived from the workloads placed on the node (not node-level metrics)
// so that the planner's move simulation produces accurate score changes.
func ScoreNode(node proxmox.NodeListEntry, workloads []Workload, weights Weights) NodeScore {
	var cpuLoad float64
	if node.MaxCPU > 0 {
		var totalCPU float64
		for _, w := range workloads {
			totalCPU += w.CPUUsage * float64(w.CPUs)
		}
		cpuLoad = totalCPU / float64(node.MaxCPU)
		if cpuLoad > 1.0 {
			cpuLoad = 1.0
		}
	}

	var memLoad float64
	if node.MaxMem > 0 {
		var totalMem int64
		for _, w := range workloads {
			totalMem += w.Mem
		}
		memLoad = float64(totalMem) / float64(node.MaxMem)
		if memLoad > 1.0 {
			memLoad = 1.0
		}
	}

	score := weights.CPU*cpuLoad + weights.Memory*memLoad

	return NodeScore{
		Node:    node.Node,
		Score:   score,
		CPULoad: cpuLoad,
		MemLoad: memLoad,
	}
}

// CalculateImbalance computes the coefficient of variation (stddev/mean) of node scores.
func CalculateImbalance(scores map[string]NodeScore) float64 {
	if len(scores) < 2 {
		return 0
	}

	var sum float64
	for _, s := range scores {
		sum += s.Score
	}
	mean := sum / float64(len(scores))
	if mean == 0 {
		return 0
	}

	var varianceSum float64
	for _, s := range scores {
		diff := s.Score - mean
		varianceSum += diff * diff
	}
	stddev := math.Sqrt(varianceSum / float64(len(scores)))

	return stddev / mean
}

// unhealthyHANodes returns the set of node names that are in a Proxmox HA
// state indicating maintenance, shutdown, or fence — DRS must skip these as
// both source and target so it doesn't fight HA's own evacuation.
//
// Proxmox HA LRM states: active, idle, maintenance, wait_for_agent_lock,
// lost_agent_lock, dead, gone. We treat anything other than active/idle as
// not-eligible-for-DRS.
//
// The relevant info lives on lrm:<node> entries (type "lrm"), not the
// node/<node> entries (which only carry online/offline/fence — already
// covered by the standard node status check). The lrm status is a
// human-readable string of the form "<nodename> (<state>, <flags...>,
// <timestamp>)", so we parse the state out of the parenthesized portion.
//
// A read failure is an error, not an empty skip set. It used to be swallowed
// at Debug on the grounds that the error "might mean HA is not configured",
// and that is wrong on the facts: /cluster/ha/status/current is core
// pve-ha-manager, present since PVE 4 with no version gate, and the LRM runs on
// every node whether or not a single HA resource is defined. An unconfigured
// cluster answers with entries, not an error — so there is no benign-error case
// to fold in here, unlike /cluster/ha/rules (absent before PVE 9.0) and
// /cluster/ha/groups (soft-disabled after it). Nothing left over is anything
// but a failure to read.
//
// Failing the evaluation is the deliberate choice over warning and proceeding,
// because the two harms are not symmetric:
//
//   - Proceed on an empty set and every node looks eligible, including one
//     Proxmox HA is actively evacuating for reboot. DRS in automatic mode then
//     migrates guests ONTO it, HA migrates them straight back, and the next
//     tick sees the same imbalance and does it again. That fight is not
//     self-correcting — it burns migration bandwidth and produces exactly the
//     "VM is locked (migrate)" contention that overlapping migrations cause.
//   - Fail and one evaluation cycle is skipped. The scheduler logs it and
//     retries at the cluster's configured interval, having written nothing.
//
// A skipped cycle is recoverable and leaves the cluster as it was; a guest
// placed on a node that is about to reboot is not.
//
// It is also barely a new failure mode. GetNodes runs immediately above and
// already hard-fails, so every transport-class failure — refused connection,
// timeout, TLS mismatch, 401 — ends the evaluation before reaching here; and
// importHARules below already fails on anything but a 501, over the same
// pvedaemon, the same pinned endpoint and the same Sys.Audit privilege. What
// this adds is the narrow slice where manager_status is unreadable while the
// rules file is fine. It also fails CHEAPER than the old path did, landing
// before workload collection instead of after it.
//
// A status that parses as nothing is the second route to the same collapse,
// and classifyHAEntries cannot be the one to judge it: an unparseable state
// counts as healthy there, so all-unparseable would classify every node
// healthy and hand back a filter that passes everything — silently, because
// the empty state is logged nowhere. The count comes back up here instead,
// where "some" and "all" can be told apart. See classifyHAEntries for why the
// per-entry decision stays fail-open.
//
// The paragraph above says a cluster always answers with lrm entries, and that
// is the steady state, not a guarantee to code against: a listing caught mid
// startup, or a partial view, can carry none. That is why the all-unparseable
// check below is written to distinguish zero from zero-out-of-zero.
func unhealthyHANodes(ctx context.Context, client *proxmox.Client, logger *slog.Logger, clusterID uuid.UUID) (map[string]struct{}, error) {
	entries, err := client.GetHAStatus(ctx)
	if err != nil {
		return nil, fmt.Errorf("read HA status for maintenance filter: %w", err)
	}
	skip, unparseable, lrmTotal := classifyHAEntries(entries)

	// unparseable > 0 is load-bearing, not belt-and-braces: a listing that
	// carried no lrm entries at all has unparseable == lrmTotal == 0, and
	// without this the equality below would fire on it and fail an
	// evaluation that should have passed.
	if unparseable > 0 {
		// Every lrm entry unparseable is the format-change signature —
		// a field renamed or moved, not one node answering oddly. Fail
		// the evaluation: classifying the whole cluster healthy off a
		// status nobody could read is how DRS ends up migrating onto a
		// node HA is evacuating. Some-but-not-all is the opposite case
		// and stays a warning, because failing on it would let a single
		// odd node strand DRS for the whole cluster.
		if unparseable == lrmTotal {
			return nil, fmt.Errorf(`read HA status for maintenance filter: none of the %d LRM entries had a parseable state (want "<node> (<state>, ...)"); the Proxmox HA status format may have changed`, lrmTotal)
		}
		logger.Warn("DRS could not parse some HA LRM states; those nodes are being treated as healthy",
			"cluster_id", clusterID,
			"unparseable", unparseable,
			"lrm_entries", lrmTotal,
		)
	}

	for node := range skip {
		logger.Info("DRS marking node unhealthy from HA LRM state",
			"cluster_id", clusterID,
			"node", node,
		)
	}
	return skip, nil
}

// classifyHAEntries returns the set of nodes whose LRM state parsed as
// something other than active/idle, along with how many lrm entries yielded
// no usable state at all and how many lrm entries were seen. Pure function —
// no logger, no error — so it can be unit-tested without a live Proxmox
// endpoint; the caller decides what the counts mean.
func classifyHAEntries(entries []proxmox.HAStatusEntry) (skip map[string]struct{}, unparseable, lrmTotal int) {
	skip = make(map[string]struct{})
	for _, entry := range entries {
		if entry.Type != "lrm" {
			continue
		}
		lrmTotal++
		if entry.Node == "" {
			// An lrm entry we cannot attribute to a node is a state
			// we could not read — we just can't say whose. Counting
			// it here rather than skipping it uncounted is what
			// keeps the caller's check honest: "node" renamed away
			// is the same class of format change as "status"
			// renamed away, and dropping these silently would leave
			// unparseable == lrmTotal == 0 and the check vacuous.
			unparseable++
			continue
		}
		state := extractLRMState(entry.Status)
		switch state {
		case "active", "idle":
			// Healthy.
		case "":
			// Unparseable. Fail open per entry — one node answering
			// in a shape we don't recognise must not cost DRS the
			// whole cluster — but this is a deferred decision, not
			// a settled one, so count it and let unhealthyHANodes
			// warn on it and fail when EVERY entry lands here.
			//
			// Worth knowing if that ever fires: the state is parsed
			// out of HAStatusEntry.Status, and HAStatusEntry.State
			// has no reader in Go — it is only passed through to
			// the SPA by the HA status handler, which the HA tab
			// renders. If Proxmox has moved the LRM state there,
			// teach extractLRMState to read it rather than widening
			// the fail-open.
			unparseable++
		default:
			skip[entry.Node] = struct{}{}
		}
	}
	return skip, unparseable, lrmTotal
}

// extractLRMState pulls the state token out of a Proxmox HA LRM status
// string. Proxmox emits these as "<nodename> (<state>, <flags...>,
// <timestamp>)" — e.g. "pve2 (active, watchdog active, Wed Apr 29 ...)"
// or "pve2 (maintenance mode, ...)". Returns the lowercased first token
// inside the parens, or empty if the format is unrecognised.
func extractLRMState(status string) string {
	open := strings.Index(status, "(")
	if open < 0 {
		return ""
	}
	rest := status[open+1:]
	end := strings.IndexAny(rest, ",)")
	if end < 0 {
		end = len(rest)
	}
	token := strings.ToLower(strings.TrimSpace(rest[:end]))
	// "maintenance mode" -> "maintenance" so callers can match on a
	// single canonical token.
	if strings.HasPrefix(token, "maintenance") {
		return "maintenance"
	}
	return token
}

func (e *Engine) createClient(ctx context.Context, clusterID uuid.UUID) (*proxmox.Client, error) {
	if e.newClient != nil {
		return e.newClient(ctx, clusterID)
	}
	if e.cache != nil {
		client, err := e.cache.Get(ctx, clusterID)
		if err == nil {
			return client, nil
		}
		e.logger.Warn("drs: proxmox cache get failed, building per-call",
			"cluster_id", clusterID, "error", err)
	}

	return proxmox.NewClientForCluster(ctx, e.queries, e.encryptionKey, clusterID, 60*time.Second)
}

// importHARules fetches HA rules from Proxmox. It first tries the PVE 9+ rules API
// (GET /cluster/ha/rules) which supports node-affinity and resource-affinity rules.
// If that endpoint does not exist (PVE 8 or earlier), it falls back to the legacy
// HA resources + groups approach.
//
// An HA listing has three outcomes, not two: rules were read, this PVE
// genuinely has none, or nobody could look. Only the middle one is a fallback.
// This used to fall back on ANY error from the rules endpoint, and the legacy
// path then returned nil rules — so a transient 500 produced an empty rule set,
// which the planner cannot tell from a cluster with no affinity rules
// configured. The result was DRS live-migrating guests with every node-affinity
// pin and anti-affinity pairing invisible to it, unattended and with nothing in
// the log above Debug. The third outcome is now an error, and both callers of
// Evaluate already skip the cycle on one (the scheduler retries on the next
// tick; the API handler 500s).
func (e *Engine) importHARules(ctx context.Context, client *proxmox.Client, _ map[string][]Workload) ([]Rule, error) {
	// Prefer the PVE 9+ rules API. It is authoritative whenever it responds —
	// including when the cluster has zero rules defined — so we fall back to the
	// deprecated groups API only when the rules endpoint is genuinely
	// unavailable (PVE 8 or earlier). Falling back on an empty-but-available
	// result would hit /cluster/ha/groups, which PVE 9 soft-disables with a
	// "migrated to rules" 500 logged on every evaluation.
	rules, available, err := e.importHARulesPVE9(ctx, client)
	if err != nil {
		return nil, err
	}
	if available {
		return rules, nil
	}

	// Fallback to legacy HA resources + groups (PVE 8).
	return e.importHARulesLegacy(ctx, client)
}

// importHARulesPVE9 reads HA rules from the PVE 9+ GET /cluster/ha/rules
// endpoint and translates node-affinity / resource-affinity rules into DRS
// rules.
//
// The bool result reports whether the rules API was available (PVE 9+); it is
// true even when the cluster has no rules defined, so the caller does not fall
// back to the deprecated groups API. It is false ONLY for a PVE too old to
// route the path — every other failure comes back as an error, because the
// caller's response to false is to carry on with whatever the legacy path
// yields, and on a cluster that does have rules that means planning against
// none of them.
//
// proxmox.IsHARulesUnsupportedError matches 501 alone. HA affinity rules
// arrived in pve-ha-manager 5.0.2 (PVE 9.0); before that PVE's dispatcher
// answers the unrouted path with HTTP 501 "not implemented", never 404. Widening
// this to 404 is the tempting mistake and would be a real hole: checkStatus maps
// every 404 to a bare ErrNotFound with the body discarded, and Nexara is
// deployed behind nginx/Traefik/Caddy, so a proxy rewrite or a misrouted path
// would arrive indistinguishable from "this PVE has no rules endpoint" — and
// would place guests unconstrained on a cluster that has rules.
func (e *Engine) importHARulesPVE9(ctx context.Context, client *proxmox.Client) ([]Rule, bool, error) {
	haRules, err := client.GetHARules(ctx)
	if err != nil {
		if proxmox.IsHARulesUnsupportedError(err) {
			e.logger.Debug("PVE 9 HA rules API not available, falling back to legacy", "error", err)
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("list HA rules: %w", err)
	}

	var rules []Rule
	for _, entry := range haRules {
		if entry.Disable != 0 {
			continue
		}

		// Parse resource SIDs into VMIDs.
		var vmIDs []int
		for _, res := range strings.Split(entry.Resources, ",") {
			res = strings.TrimSpace(res)
			if vmid, ok := parseSIDToVMID(res); ok {
				vmIDs = append(vmIDs, vmid)
			}
		}
		if len(vmIDs) == 0 {
			continue
		}

		switch entry.Type {
		case "node-affinity":
			nodes := parseHAGroupNodes(entry.Nodes)
			e.logger.Info("HA node-affinity rule (pin)", "rule", entry.Rule, "vmids", vmIDs, "nodes", nodes)
			rules = append(rules, Rule{
				Type:      RuleTypePin,
				VMIDs:     vmIDs,
				NodeNames: nodes,
				Enabled:   true,
			})

		case "resource-affinity":
			if entry.Affinity == "negative" {
				e.logger.Info("HA resource-affinity rule (anti-affinity)", "rule", entry.Rule, "vmids", vmIDs)
				rules = append(rules, Rule{
					Type:    RuleTypeAntiAffinity,
					VMIDs:   vmIDs,
					Enabled: true,
				})
			} else {
				e.logger.Info("HA resource-affinity rule (affinity)", "rule", entry.Rule, "vmids", vmIDs)
				rules = append(rules, Rule{
					Type:    RuleTypeAffinity,
					VMIDs:   vmIDs,
					Enabled: true,
				})
			}
		}
	}

	return rules, true, nil
}

// importHARulesLegacy uses the PVE 8 HA resources + groups API to derive pin rules
// for VMs in restricted HA groups.
//
// It draws the same three-outcome line as the rules path above, for the same
// reason: returning nil on a listing it could not read hands the planner an
// empty rule set, and an empty rule set is what a cluster with no restricted
// groups looks like. These used to be warned about and swallowed, which left
// the migrations themselves indistinguishable from a correctly unconstrained
// balance.
//
// "migrated to rules" stays benign — it is PVE saying there are no groups here,
// which is a complete answer.
func (e *Engine) importHARulesLegacy(ctx context.Context, client *proxmox.Client) ([]Rule, error) {
	haResources, err := client.GetHAResources(ctx)
	if err != nil {
		return nil, fmt.Errorf("list HA resources: %w", err)
	}
	haGroups, err := client.GetHAGroups(ctx)
	if err != nil {
		if proxmox.IsGroupsMigratedError(err) {
			// PVE 9+: HA groups were replaced by rules (read via
			// importHARulesPVE9). Reaching here means PVE answered 501 for the
			// rules path yet still reports groups as migrated — there are no
			// groups to import either way, so skip quietly instead of warning
			// on every evaluation.
			e.logger.Debug("HA groups migrated to rules; no legacy groups to import", "error", err)
			return nil, nil
		}
		return nil, fmt.Errorf("list HA groups: %w", err)
	}

	// Build group→nodes map for restricted groups only.
	restrictedGroups := make(map[string][]string)
	for _, g := range haGroups {
		if g.Restricted == 1 {
			restrictedGroups[g.Group] = parseHAGroupNodes(g.Nodes)
		}
	}

	if len(restrictedGroups) == 0 {
		return nil, nil
	}

	rules := make([]Rule, 0, len(haResources))
	for _, res := range haResources {
		if res.Group == "" {
			continue
		}
		nodes, ok := restrictedGroups[res.Group]
		if !ok {
			continue
		}

		vmid, ok := parseSIDToVMID(res.SID)
		if !ok {
			continue
		}

		e.logger.Info("HA pin rule: VM restricted to nodes",
			"vmid", vmid, "nodes", nodes, "group", res.Group)

		rules = append(rules, Rule{
			Type:      RuleTypePin,
			VMIDs:     []int{vmid},
			NodeNames: nodes,
			Enabled:   true,
		})
	}

	return rules, nil
}

// pinVeeamInfrastructure marks the guests belonging to a Veeam deployment —
// worker appliances, and the VBR server when it runs on the cluster it
// protects — as ineligible for migration.
//
// PINNED, not filtered out. A pinned workload still counts toward node
// scoring, which is what keeps the balance honest: three worker appliances on
// one node is real load, and a planner blind to them would read that node as
// idle and pile more onto it. It also keeps them visible to affinity rules, so
// nothing gets migrated onto a node hosting a pinned anti-affinity partner —
// the same reason containers are pinned rather than dropped when
// include_containers is false.
//
// What this prevents: DRS migrating a worker mid-job, which kills the backup
// running on it and leaves nothing behind but a failed session.
//
// What it does NOT prevent, deliberately: DRS migrating a PROTECTED guest away
// from the node its worker happens to be on, which silently downgrades that
// guest's transport from hot-add to network mode — no error, just a backup
// several times slower, and neither product says why. Covering that would mean
// pinning half the estate to wherever Veeam last placed a worker, and Veeam
// re-places them per job run. Naming the gap beats a half-measure that reads
// like a guarantee.
//
// A lookup failure leaves the workloads alone rather than failing the
// evaluation. DRS balancing a cluster while briefly unaware of its worker
// appliances is a far smaller problem than DRS not running at all.
func (e *Engine) pinVeeamInfrastructure(ctx context.Context, clusterID uuid.UUID, enabled bool, nodeWorkloads map[string][]Workload) {
	if !enabled {
		return
	}
	// Only reachable from a struct-literal Engine in tests — NewEngine always
	// assigns this — but those exist, and a nil interface call panics the
	// whole evaluation.
	if e.veeamOwned == nil {
		return
	}
	owned, err := e.veeamOwned.ListVeeamInfrastructureGuestsForCluster(ctx, clusterID)
	if err != nil {
		e.logger.Warn("DRS could not resolve Veeam-owned guests; they will be treated as ordinary workloads",
			"cluster_id", clusterID, "error", err)
		return
	}
	if len(owned) == 0 {
		// Said out loud, because the alternative signal is the ABSENCE of the
		// pin lines below — and this feature exists to prevent damage that
		// leaves no trace. A cluster showing the protection armed while
		// nothing is recorded for it usually means the Veeam collector has not
		// run, or resolved none of its guests by name.
		e.logger.Debug("DRS found no Veeam-owned guests recorded for this cluster",
			"cluster_id", clusterID)
		return
	}

	roleByVMID := make(map[int]string, len(owned))
	for _, row := range owned {
		// FIRST wins. The query orders (vmid, role) so that is
		// 'backup_server', which is the more specific fact for a guest holding
		// both roles — the VBR server fills a proxy role itself. Last-write
		// would silently report it as a plain worker.
		if _, seen := roleByVMID[int(row.Vmid)]; !seen {
			roleByVMID[int(row.Vmid)] = row.Role
		}
	}

	for node, workloads := range nodeWorkloads {
		for i, w := range workloads {
			role, isVeeam := roleByVMID[w.VMID]
			if !isVeeam || w.Pinned {
				continue
			}
			e.logger.Info("DRS pinning a guest that belongs to Veeam",
				"cluster_id", clusterID, "vmid", w.VMID, "node", node,
				"name", w.Name, "role", role)
			workloads[i].Pinned = true
		}
	}
}

// detectPassthrough checks QEMU VMs for PCI passthrough devices and marks them as pinned.
func (e *Engine) detectPassthrough(ctx context.Context, client *proxmox.Client, nodeWorkloads map[string][]Workload) {
	for node, workloads := range nodeWorkloads {
		for i, w := range workloads {
			if w.Type != "qemu" {
				continue
			}
			config, err := client.GetVMConfig(ctx, node, w.VMID)
			if err != nil {
				e.logger.Warn("failed to get VM config for passthrough check",
					"vmid", w.VMID, "node", node, "error", err)
				continue
			}
			if hasPassthrough(config) {
				e.logger.Info("skipping VM with hardware passthrough",
					"vmid", w.VMID, "node", node, "name", w.Name)
				workloads[i].Pinned = true
			}
		}
	}
}

// parseHAGroupNodes parses an HA group nodes string like "node1:100,node2:50" or "node1,node2"
// into a slice of node names (stripping optional priority suffixes).
func parseHAGroupNodes(nodesStr string) []string {
	if nodesStr == "" {
		return nil
	}
	parts := strings.Split(nodesStr, ",")
	nodes := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		// Strip optional priority suffix (e.g. "node1:100" → "node1").
		if idx := strings.Index(p, ":"); idx >= 0 {
			p = p[:idx]
		}
		nodes = append(nodes, p)
	}
	return nodes
}

// hasPassthrough checks if a VM config contains any PCI passthrough (hostpci0..hostpci15)
// or USB passthrough (usb0..usb4) devices.
func hasPassthrough(config proxmox.VMConfig) bool {
	for i := 0; i <= 15; i++ {
		key := fmt.Sprintf("hostpci%d", i)
		if _, ok := config[key]; ok {
			return true
		}
	}
	for i := 0; i <= 4; i++ {
		key := fmt.Sprintf("usb%d", i)
		if val, ok := config[key]; ok {
			// USB devices configured as "spice" are virtual (SPICE redirection),
			// not physical passthrough — these are safe to migrate.
			if s, isStr := val.(string); isStr && s == "spice" {
				continue
			}
			return true
		}
	}
	return false
}

// parseSIDToVMID extracts the VMID from an HA resource SID like "vm:101" or "ct:200".
func parseSIDToVMID(sid string) (int, bool) {
	parts := strings.SplitN(sid, ":", 2)
	if len(parts) != 2 {
		return 0, false
	}
	vmid, err := strconv.Atoi(parts[1])
	if err != nil {
		return 0, false
	}
	return vmid, true
}
