package rolling

import (
	"context"
	"fmt"
	"strconv"
	"strings"

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

	// Fetch HA data from Proxmox.
	haResources, err := client.GetHAResources(ctx)
	if err != nil {
		// Non-fatal — cluster may not have HA configured.
		haResources = nil
	}

	haGroups, err := client.GetHAGroups(ctx)
	if err != nil {
		haGroups = nil
	}

	// PVE 9+ HA rules — may not exist on older versions.
	haRules, err := client.GetHARules(ctx)
	if err != nil {
		haRules = nil
	}

	// Build lookup maps.
	groupMap := make(map[string]proxmox.HAGroup, len(haGroups))
	for _, g := range haGroups {
		groupMap[g.Group] = g
	}

	resSID := make(map[string]proxmox.HAResource, len(haResources))
	for _, r := range haResources {
		resSID[r.SID] = r
	}

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

	nodeWorkloads := buildNodeWorkloads(ctx, client, clusterNodes)

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

// buildNodeWorkloads creates a workload map from the Proxmox API.
func buildNodeWorkloads(ctx context.Context, client *proxmox.Client, nodes []proxmox.NodeListEntry) map[string][]drs.Workload {
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
