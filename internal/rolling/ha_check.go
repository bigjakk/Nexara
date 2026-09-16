package rolling

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/drs"
	"github.com/bigjakk/nexara/internal/proxmox"
)

// HAConflict describes a single HA/DRS conflict detected during pre-flight analysis.
type HAConflict struct {
	Source   string `json:"source"`    // "ha_group", "ha_rule", "drs_rule"
	RuleName string `json:"rule_name"` // group/rule identifier
	Type     string `json:"type"`      // "restricted_group", "node_affinity", "anti_affinity", "pin"
	Severity string `json:"severity"`  // "error" (hard, will block migration) or "warning" (soft)
	VMID     int    `json:"vmid"`
	VMName   string `json:"vm_name,omitempty"`
	Message  string `json:"message"`
	Node     string `json:"node"` // the node being drained that causes this conflict
}

// HAPreFlightReport contains the full constraint analysis for a rolling update.
type HAPreFlightReport struct {
	Conflicts []HAConflict `json:"conflicts"`
	HasErrors bool         `json:"has_errors"`
}

// AnalyzeHAConstraints checks what HA/DRS rules would be violated by draining
// the given nodes. Each node is analyzed independently (as if drained one at a time).
func AnalyzeHAConstraints(
	ctx context.Context,
	client *proxmox.Client,
	queries *db.Queries,
	clusterID uuid.UUID,
	nodesToDrain []string,
) (*HAPreFlightReport, error) {
	report := &HAPreFlightReport{
		Conflicts: []HAConflict{},
	}

	// These used to be nilled on error as "non-fatal — cluster may not have HA
	// configured". They are not the same thing, and the difference lands on the
	// gate: this report feeds `haPolicy == "strict"`, which refuses the job when
	// it finds conflicts. A listing that failed produced an empty rule set,
	// therefore no conflicts, therefore a green light — the strict gate
	// answering "all clear" precisely because it could not look.
	//
	// loadHAConstraints keeps the genuinely-empty versions non-fatal and turns
	// everything else into an error, which both callers surface. One attempt,
	// not three: this runs inside an HTTP request against a client whose
	// timeout is minutes.
	//
	// Note what is lost when it fails: the passthrough, DRS-rule and no-targets
	// checks below never needed the HA listings and used to survive one
	// failing. Most are informational — every DRS conflict is severity
	// "warning" and none sets HasErrors — but no_targets is an error-severity
	// conflict, so this does drop a blocking finding on the floor. The gate
	// outcome is unchanged either way, because foldPreflight substitutes an
	// error-severity preflight_unavailable; what the operator loses is the more
	// specific reason.
	ha, err := loadHAConstraints(ctx, client, listingRetryOnce)
	if err != nil {
		return nil, err
	}
	haRules := ha.rules
	groupMap := ha.groups
	resSID := ha.resources

	// Fetch DRS rules from Nexara DB.
	dbRules, err := queries.ListDRSRules(ctx, clusterID)
	if err != nil {
		dbRules = nil
	}
	drsRules := drs.ParseDBRules(dbRules)

	// Get all cluster nodes and their workloads for DRS rule checking.
	clusterNodes, err := client.GetNodes(ctx)
	if err != nil {
		return nil, fmt.Errorf("get cluster nodes: %w", err)
	}

	nodeWorkloads := BuildNodeWorkloads(ctx, client, clusterNodes)

	drainSet := make(map[string]bool, len(nodesToDrain))
	for _, n := range nodesToDrain {
		drainSet[n] = true
	}

	// Detect passthrough devices on VMs across all nodes being drained.
	for _, drainNode := range nodesToDrain {
		wls := nodeWorkloads[drainNode]
		for _, w := range wls {
			if w.Type != "qemu" {
				continue
			}
			config, cfgErr := client.GetVMConfig(ctx, drainNode, w.VMID)
			if cfgErr != nil {
				continue
			}
			if hasPassthrough(config) {
				report.Conflicts = append(report.Conflicts, HAConflict{
					Source:   "passthrough",
					RuleName: "hardware_passthrough",
					Type:     "passthrough",
					Severity: "warning",
					VMID:     w.VMID,
					VMName:   w.Name,
					Message:  fmt.Sprintf("VM %d (%s) has PCI/USB passthrough — will be shut down during drain and restarted after update (cannot live-migrate)", w.VMID, w.Name),
					Node:     drainNode,
				})
			}
		}
	}

	// Analyze each node being drained.
	for _, drainNode := range nodesToDrain {
		guests, ok := nodeWorkloads[drainNode]
		if !ok || len(guests) == 0 {
			continue
		}

		// Available targets: online nodes not being drained.
		var targets []string
		for _, cn := range clusterNodes {
			if cn.Node != drainNode && cn.Status == "online" && !drainSet[cn.Node] {
				targets = append(targets, cn.Node)
			}
		}
		// Also include other drain nodes that haven't been drained yet as fallback.
		for _, cn := range clusterNodes {
			if cn.Node != drainNode && cn.Status == "online" && drainSet[cn.Node] {
				targets = append(targets, cn.Node)
			}
		}

		if len(targets) == 0 {
			report.Conflicts = append(report.Conflicts, HAConflict{
				Source:   "cluster",
				RuleName: "no_targets",
				Type:     "no_available_nodes",
				Severity: "error",
				Message:  fmt.Sprintf("No available target nodes when draining %s", drainNode),
				Node:     drainNode,
			})
			report.HasErrors = true
			continue
		}

		for _, guest := range guests {
			sid := guestSID(guest)

			// Check HA group restricted constraints.
			if res, ok := resSID[sid]; ok && res.Group != "" {
				if grp, ok := groupMap[res.Group]; ok && grp.Restricted == 1 {
					groupNodes := parseHAGroupNodes(grp.Nodes)
					hasValidTarget := false
					for _, t := range targets {
						if groupNodes[t] {
							hasValidTarget = true
							break
						}
					}
					if !hasValidTarget {
						report.Conflicts = append(report.Conflicts, HAConflict{
							Source:   "ha_group",
							RuleName: res.Group,
							Type:     "restricted_group",
							Severity: "error",
							VMID:     guest.VMID,
							VMName:   guest.Name,
							Message:  fmt.Sprintf("VM %d (%s) is in restricted HA group %q — no valid target node available when draining %s", guest.VMID, guest.Name, res.Group, drainNode),
							Node:     drainNode,
						})
						report.HasErrors = true
					}
				}
			}

			// Check PVE 9+ HA rules.
			for _, rule := range haRules {
				if rule.Disable == 1 {
					continue
				}
				if !ruleAppliesToGuest(rule, sid) {
					continue
				}

				switch rule.Type {
				case "node-affinity":
					allowedNodes := parseCSV(rule.Nodes)
					hasValidTarget := false
					for _, t := range targets {
						if allowedNodes[t] {
							hasValidTarget = true
							break
						}
					}
					if !hasValidTarget {
						severity := "warning"
						if rule.Strict == 1 {
							severity = "error"
							report.HasErrors = true
						}
						report.Conflicts = append(report.Conflicts, HAConflict{
							Source:   "ha_rule",
							RuleName: rule.Rule,
							Type:     "node_affinity",
							Severity: severity,
							VMID:     guest.VMID,
							VMName:   guest.Name,
							Message:  fmt.Sprintf("VM %d (%s) has node-affinity rule %q — no allowed target node when draining %s", guest.VMID, guest.Name, rule.Rule, drainNode),
							Node:     drainNode,
						})
					}

				case "resource-affinity":
					if rule.Affinity != "negative" {
						continue
					}
					// Anti-affinity: check if any other resource in this rule group
					// is already on a target node, forcing colocation.
					otherSIDs := parseCSV(rule.Resources)
					delete(otherSIDs, sid)
					for otherSID := range otherSIDs {
						otherNode := findWorkloadNode(otherSID, nodeWorkloads)
						if otherNode == "" || otherNode == drainNode {
							continue
						}
						// The other resource is on a target node — draining would
						// potentially force this guest onto the same node.
						severity := "warning"
						if rule.Strict == 1 {
							severity = "error"
							report.HasErrors = true
						}
						otherVMID := vmidFromSID(otherSID)
						report.Conflicts = append(report.Conflicts, HAConflict{
							Source:   "ha_rule",
							RuleName: rule.Rule,
							Type:     "anti_affinity",
							Severity: severity,
							VMID:     guest.VMID,
							VMName:   guest.Name,
							Message:  fmt.Sprintf("VM %d (%s) has anti-affinity with VM %d — may be forced onto same node when draining %s", guest.VMID, guest.Name, otherVMID, drainNode),
							Node:     drainNode,
						})
						break
					}
				}
			}

			// Check Nexara DRS rules.
			checkDRSConflicts(report, guest, drainNode, targets, drsRules, nodeWorkloads)
		}
	}

	return report, nil
}

func checkDRSConflicts(report *HAPreFlightReport, guest drs.Workload, drainNode string, targets []string, rules []drs.Rule, nodeWorkloads map[string][]drs.Workload) {
	for _, rule := range rules {
		switch rule.Type {
		case drs.RuleTypeAntiAffinity:
			if !containsInt(rule.VMIDs, guest.VMID) {
				continue
			}
			// Check if any target would violate anti-affinity.
			allViolate := true
			for _, target := range targets {
				wouldViolate := false
				for _, otherVMID := range rule.VMIDs {
					if otherVMID == guest.VMID {
						continue
					}
					targetNode := drs.FindVMNodePublic(otherVMID, nodeWorkloads)
					if targetNode == target {
						wouldViolate = true
						break
					}
				}
				if !wouldViolate {
					allViolate = false
					break
				}
			}
			if allViolate {
				report.Conflicts = append(report.Conflicts, HAConflict{
					Source:   "drs_rule",
					Type:     "anti_affinity",
					Severity: "warning",
					VMID:     guest.VMID,
					VMName:   guest.Name,
					Message:  fmt.Sprintf("VM %d (%s) anti-affinity rule will be temporarily violated when draining %s", guest.VMID, guest.Name, drainNode),
					Node:     drainNode,
				})
			}

		case drs.RuleTypePin:
			if !containsInt(rule.VMIDs, guest.VMID) {
				continue
			}
			if len(rule.NodeNames) == 0 {
				continue
			}
			hasValidTarget := false
			for _, t := range targets {
				if containsString(rule.NodeNames, t) {
					hasValidTarget = true
					break
				}
			}
			if !hasValidTarget {
				report.Conflicts = append(report.Conflicts, HAConflict{
					Source:   "drs_rule",
					Type:     "pin",
					Severity: "warning",
					VMID:     guest.VMID,
					VMName:   guest.Name,
					Message:  fmt.Sprintf("VM %d (%s) is pinned to %s — no valid target when draining", guest.VMID, guest.Name, drainNode),
					Node:     drainNode,
				})
			}
		}
	}
}

// SelectTarget picks the best target node for migrating a guest during drain.
// Returns the target node name and any warnings generated.
func SelectTarget(
	guest GuestSnapshot,
	_ string,
	candidates []string,
	haResources map[string]proxmox.HAResource,
	haGroups map[string]proxmox.HAGroup,
	haRules []proxmox.HARuleEntry,
	drsRules []drs.Rule,
	nodeWorkloads map[string][]drs.Workload,
) (string, error) {
	if len(candidates) == 0 {
		return "", fmt.Errorf("no candidate nodes available")
	}

	sid := fmt.Sprintf("%s:%d", guestTypeToSIDPrefix(guest.Type), guest.VMID)

	type scored struct {
		node  string
		score int
	}

	results := make([]scored, 0, len(candidates))

	for _, candidate := range candidates {
		s := 100

		// HA group restricted check.
		if res, ok := haResources[sid]; ok && res.Group != "" {
			if grp, ok := haGroups[res.Group]; ok && grp.Restricted == 1 {
				groupNodes := parseHAGroupNodes(grp.Nodes)
				if !groupNodes[candidate] {
					s -= 1000 // Hard constraint — will be rejected by Proxmox.
				}
			}
		}

		// HA rule checks.
		for _, rule := range haRules {
			if rule.Disable == 1 {
				continue
			}
			if !ruleAppliesToGuest(rule, sid) {
				continue
			}

			switch rule.Type {
			case "node-affinity":
				allowedNodes := parseCSV(rule.Nodes)
				if !allowedNodes[candidate] {
					if rule.Strict == 1 {
						s -= 1000
					} else {
						s -= 50
					}
				}

			case "resource-affinity":
				if rule.Affinity == "negative" {
					otherSIDs := parseCSV(rule.Resources)
					delete(otherSIDs, sid)
					for otherSID := range otherSIDs {
						otherNode := findWorkloadNode(otherSID, nodeWorkloads)
						if otherNode == candidate {
							if rule.Strict == 1 {
								s -= 1000
							} else {
								s -= 50
							}
							break
						}
					}
				}
			}
		}

		// DRS anti-affinity check.
		for _, rule := range drsRules {
			if rule.Type != drs.RuleTypeAntiAffinity {
				continue
			}
			if !containsInt(rule.VMIDs, guest.VMID) {
				continue
			}
			for _, otherVMID := range rule.VMIDs {
				if otherVMID == guest.VMID {
					continue
				}
				otherNode := drs.FindVMNodePublic(otherVMID, nodeWorkloads)
				if otherNode == candidate {
					s -= 50
					break
				}
			}
		}

		// DRS pin check.
		for _, rule := range drsRules {
			if rule.Type != drs.RuleTypePin {
				continue
			}
			if !containsInt(rule.VMIDs, guest.VMID) {
				continue
			}
			if len(rule.NodeNames) > 0 && !containsString(rule.NodeNames, candidate) {
				s -= 50
			}
		}

		// Prefer nodes with fewer workloads (simple load spreading).
		wlCount := len(nodeWorkloads[candidate])
		s -= wlCount

		results = append(results, scored{node: candidate, score: s})
	}

	// Pick the highest score.
	best := results[0]
	for _, r := range results[1:] {
		if r.score > best.score {
			best = r
		}
	}

	return best.node, nil
}

// BuildNodeWorkloads creates a workload map from the Proxmox API.
func BuildNodeWorkloads(ctx context.Context, client *proxmox.Client, nodes []proxmox.NodeListEntry) map[string][]drs.Workload {
	workloads := make(map[string][]drs.Workload)

	for _, node := range nodes {
		if node.Status != "online" {
			continue
		}

		vms, err := client.GetVMs(ctx, node.Node)
		if err != nil {
			continue
		}
		for _, vm := range vms {
			if vm.Template == 1 || (vm.Status != "running" && vm.Status != "paused") {
				continue
			}
			workloads[node.Node] = append(workloads[node.Node], drs.Workload{
				VMID: vm.VMID,
				Name: vm.Name,
				Type: "qemu",
				Node: node.Node,
			})
		}

		cts, err := client.GetContainers(ctx, node.Node)
		if err != nil {
			continue
		}
		for _, ct := range cts {
			if ct.Status != "running" {
				continue
			}
			workloads[node.Node] = append(workloads[node.Node], drs.Workload{
				VMID: ct.VMID,
				Name: ct.Name,
				Type: "lxc",
				Node: node.Node,
			})
		}
	}

	return workloads
}

func guestSID(w drs.Workload) string {
	prefix := "vm"
	if w.Type == "lxc" {
		prefix = "ct"
	}
	return fmt.Sprintf("%s:%d", prefix, w.VMID)
}

func guestTypeToSIDPrefix(guestType string) string {
	if guestType == "lxc" {
		return "ct"
	}
	return "vm"
}

// parseHAGroupNodes parses "node1:100,node2:50,node3" into a set of node names.
func parseHAGroupNodes(raw string) map[string]bool {
	nodes := make(map[string]bool)
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		name, _, _ := strings.Cut(part, ":")
		nodes[name] = true
	}
	return nodes
}

// parseCSV parses a comma-separated string into a set.
func parseCSV(raw string) map[string]bool {
	m := make(map[string]bool)
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			m[part] = true
		}
	}
	return m
}

func ruleAppliesToGuest(rule proxmox.HARuleEntry, sid string) bool {
	for _, part := range strings.Split(rule.Resources, ",") {
		if strings.TrimSpace(part) == sid {
			return true
		}
	}
	return false
}

func findWorkloadNode(sid string, nodeWorkloads map[string][]drs.Workload) string {
	for node, workloads := range nodeWorkloads {
		for _, w := range workloads {
			if guestSID(w) == sid {
				return node
			}
		}
	}
	return ""
}

func vmidFromSID(sid string) int {
	_, after, ok := strings.Cut(sid, ":")
	if !ok {
		return 0
	}
	v, _ := strconv.Atoi(after)
	return v
}

func containsInt(slice []int, val int) bool {
	for _, v := range slice {
		if v == val {
			return true
		}
	}
	return false
}

func containsString(slice []string, val string) bool {
	for _, v := range slice {
		if v == val {
			return true
		}
	}
	return false
}

// haConstraints is the HA state SelectTarget scores a candidate node against.
//
// rules may be nil on success: a PVE older than 9.0 has no rules endpoint and
// genuinely has none. resources and groups are always non-nil, so callers never
// have to nil-check a map they are about to read.
type haConstraints struct {
	resources map[string]proxmox.HAResource
	groups    map[string]proxmox.HAGroup
	rules     []proxmox.HARuleEntry
}

// listingRetryAttempts and listingRetryBackoff bound the retry for any listing
// the orchestrator cannot safely guess at — the HA constraints here, and
// verifyNodeDrained's guest listing, which is why the names are not
// HA-specific. There is no retry above this — failNode fails the whole job, and
// a failed job is recreated rather than resumed — so one blip must not cost the
// operator a multi-node update.
//
// Vars, not consts, only so the tests can shorten the wait; nothing in
// production reassigns them.
var (
	listingRetryAttempts = 3
	listingRetryBackoff  = 2 * time.Second
)

// listingRetryOnce is the attempt count for a request-scoped caller. The cached
// Proxmox client's timeout is minutes, not seconds, so retrying inside an HTTP
// handler multiplies how long the request can hang before it answers; a
// pre-flight that cannot read the cluster is better reported than waited on.
const listingRetryOnce = 1

// retryHAListing runs a listing up to attempts times, returning the first
// success. Errors the caller considers benign are that caller's business: the
// predicate runs inside, so "this PVE has none" stops the retry immediately
// rather than burning the budget on an answer that will not change.
//
// A cancelled ctx returns ctx.Err() rather than the listing's own error, so a
// caller can tell "this process is no longer running the job" — a leadership
// handover or a shutdown — from "the cluster failed to answer". A benign error
// still wins over that, which is right: "this PVE has none" is a complete
// answer and does not become unknown because the caller went away. Only one
// context is taken: the orchestrator's tick ctx descends from the app's, so
// selecting on it covers both.
func retryHAListing[T any](
	ctx context.Context,
	attempts int,
	list func(context.Context) (T, error),
	benign func(error) bool,
) (T, error) {
	var zero T
	var lastErr error
	if attempts < 1 {
		// A zero budget would run no attempts and return (zero, nil) — success
		// with nothing read, which is the one answer this function exists to
		// never give. No caller passes it today; the guard is here so none can.
		attempts = 1
	}
	for attempt := range attempts {
		if attempt > 0 {
			// The ctx case here is latency, not correctness: the post-call
			// check below catches a cancellation too, because a cancelled ctx
			// makes the next call fail before it reaches the network. What this
			// saves is sitting out a full backoff after the job has already
			// moved on. Replacing it with a plain sleep passes every test —
			// deliberately, since the outcome is identical — so do not read
			// that as evidence it is dead.
			select {
			case <-ctx.Done():
				return zero, ctx.Err()
			case <-time.After(listingRetryBackoff):
			}
		}
		v, err := list(ctx)
		if err == nil {
			return v, nil
		}
		if benign != nil && benign(err) {
			return zero, nil
		}
		// A cancellation during the call itself surfaces as the client's own
		// connection error — api_client.go wraps the transport error with %s,
		// not %w, so errors.Is cannot see context.Canceled through it. Ask the
		// context directly, or a handover during the final attempt would be
		// reported as a cluster failure and terminally fail the job.
		if ctxErr := ctx.Err(); ctxErr != nil {
			return zero, ctxErr
		}
		lastErr = err
	}
	return zero, lastErr
}

// loadHAConstraints reads the three listings SelectTarget needs and separates
// "there are none" from "could not look".
//
// SelectTarget cannot tell those apart: an empty map and an unread one both
// score as no constraint, so the guest is placed as if nothing applied to it.
// That is how a failed listing used to move a guest onto a node an active
// anti-affinity rule forbids — the co-location those rules exist to prevent,
// arrived at silently, during an unattended job.
//
// This closes the HA half only. BuildNodeWorkloads, called on the next line and
// feeding the same SelectTarget, still swallows a per-node guest listing and
// leaves that node with an empty workload list — which SelectTarget reads both
// as "no negatively-affine peer here" and as "fewest workloads", so the node
// whose listing failed is not merely unconstrained but actively preferred. The
// same scenario, through the other input. Fixing it needs a signature change
// and is tracked separately.
//
// Two of the three do have a version where an error genuinely means none, and
// failing on those would stop every rolling update on the releases either side
// of PVE's groups-to-rules migration:
//
//   - /cluster/ha/groups 500s once groups have "been migrated to rules", which
//     PVE 9 reports for any cluster whose group config is empty — i.e. the
//     common case on 9.x, not a post-migration edge
//   - /cluster/ha/rules does not exist before PVE 9.0
//
// /cluster/ha/resources has no such case — the HA stack ships with every PVE,
// and an unconfigured cluster answers with an empty list, not an error.
//
// startNode must call it AFTER its disable step, never before: SelectTarget
// skips rules with Disable == 1, so reading once up front would score against
// rules this job has just switched off and over-constrain every target choice.
// That is why startNode reads twice. It does not apply to the pre-flight, which
// runs before anything is disabled and should report the rules as they stand.
//
// It is a plain function rather than a method so the decision does not depend
// on Orchestrator state — the tests build a client against a stub server and
// call it directly.
func loadHAConstraints(ctx context.Context, client *proxmox.Client, attempts int) (haConstraints, error) {
	var ha haConstraints

	resources, err := retryHAListing(ctx, attempts, client.GetHAResources, nil)
	if err != nil {
		return haConstraints{}, fmt.Errorf("list HA resources: %w", err)
	}
	ha.resources = make(map[string]proxmox.HAResource, len(resources))
	for _, r := range resources {
		ha.resources[r.SID] = r
	}

	groups, err := retryHAListing(ctx, attempts, client.GetHAGroups, proxmox.IsGroupsMigratedError)
	if err != nil {
		return haConstraints{}, fmt.Errorf("list HA groups: %w", err)
	}
	ha.groups = make(map[string]proxmox.HAGroup, len(groups))
	for _, g := range groups {
		ha.groups[g.Group] = g
	}

	ha.rules, err = listHARules(ctx, client, attempts)
	if err != nil {
		return haConstraints{}, err
	}

	return ha, nil
}

// listHARules reads the rule listing, folding the one PVE version that
// legitimately has none into an empty result and leaving every other failure as
// a failure.
//
// Shared by loadHAConstraints and by the drain's disable step, which both have
// to draw the same line and would be a silent safety hole if they drew it
// differently: an empty list means "nothing to disable, nothing to score
// against", and an unread one has to mean "stop".
func listHARules(ctx context.Context, client *proxmox.Client, attempts int) ([]proxmox.HARuleEntry, error) {
	rules, err := retryHAListing(ctx, attempts, client.GetHARules, proxmox.IsHARulesUnsupportedError)
	if err != nil {
		return nil, fmt.Errorf("list HA rules: %w", err)
	}
	return rules, nil
}
