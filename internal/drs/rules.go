package drs

import (
	"encoding/json"

	db "github.com/bigjakk/nexara/internal/db/generated"
)

// Rule type constants.
const (
	RuleTypeAffinity     = "affinity"
	RuleTypeAntiAffinity = "anti-affinity"
	RuleTypePin          = "pin"
)

// Rule is a parsed DRS rule.
type Rule struct {
	Type      string
	VMIDs     []int
	NodeNames []string
	Enabled   bool
}

// ParseDBRules converts DB rule records to engine Rule structs.
func ParseDBRules(dbRules []db.DrsRule) []Rule {
	rules := make([]Rule, 0, len(dbRules))
	for _, r := range dbRules {
		if !r.Enabled {
			continue
		}
		rule := Rule{
			Type:    r.RuleType,
			Enabled: r.Enabled,
		}
		_ = json.Unmarshal(r.VmIds, &rule.VMIDs)
		_ = json.Unmarshal(r.NodeNames, &rule.NodeNames)
		rules = append(rules, rule)
	}
	return rules
}

// IsMigrationAllowed checks if a proposed migration violates any rules.
func IsMigrationAllowed(migration Recommendation, nodeWorkloads map[string][]Workload, rules []Rule) bool {
	for _, rule := range rules {
		switch rule.Type {
		case RuleTypePin:
			if !isPinAllowed(migration, rule) {
				return false
			}
		case RuleTypeAffinity:
			if !isAffinityAllowed(migration, nodeWorkloads, rule) {
				return false
			}
		case RuleTypeAntiAffinity:
			if !isAntiAffinityAllowed(migration, nodeWorkloads, rule) {
				return false
			}
		}
	}
	return true
}

// isPinAllowed checks that a pinned VM is not moved away from its designated node.
func isPinAllowed(migration Recommendation, rule Rule) bool {
	if !containsInt(rule.VMIDs, migration.VMID) {
		return true // rule doesn't apply to this VM
	}
	// If the VM is pinned, it can only go to a node in the rule's node list.
	if len(rule.NodeNames) == 0 {
		return true
	}
	return containsString(rule.NodeNames, migration.TargetNode)
}

// isAffinityAllowed checks that VMs in an affinity group stay on the same node.
func isAffinityAllowed(migration Recommendation, nodeWorkloads map[string][]Workload, rule Rule) bool {
	if !containsInt(rule.VMIDs, migration.VMID) {
		return true
	}

	// Check where the other VMs in this affinity group currently are.
	for _, vmid := range rule.VMIDs {
		if vmid == migration.VMID {
			continue
		}
		vmNode := findVMNode(vmid, nodeWorkloads)
		if vmNode == "" {
			continue
		}
		// After migration, this VM would be on TargetNode. The other VM is on vmNode.
		// For affinity, they must be on the same node.
		if vmNode != migration.TargetNode {
			return false
		}
	}
	return true
}

// isAntiAffinityAllowed checks that VMs in an anti-affinity group don't end up on the same node.
func isAntiAffinityAllowed(migration Recommendation, nodeWorkloads map[string][]Workload, rule Rule) bool {
	if !containsInt(rule.VMIDs, migration.VMID) {
		return true
	}

	for _, vmid := range rule.VMIDs {
		if vmid == migration.VMID {
			continue
		}
		vmNode := findVMNode(vmid, nodeWorkloads)
		if vmNode == "" {
			continue
		}
		// After migration, this VM would be on TargetNode. Anti-affinity means they must NOT be on the same node.
		if vmNode == migration.TargetNode {
			return false
		}
	}
	return true
}

// CheckViolations returns current rule violations in the cluster.
func CheckViolations(nodeWorkloads map[string][]Workload, rules []Rule) []string {
	var violations []string

	for _, rule := range rules {
		switch rule.Type {
		case RuleTypeAffinity:
			// All VMs in the group should be on the same node.
			var nodes []string
			for _, vmid := range rule.VMIDs {
				n := findVMNode(vmid, nodeWorkloads)
				if n != "" {
					nodes = append(nodes, n)
				}
			}
			if len(nodes) > 1 {
				first := nodes[0]
				for _, n := range nodes[1:] {
					if n != first {
						violations = append(violations, "affinity rule violated: VMs are on different nodes")
						break
					}
				}
			}

		case RuleTypeAntiAffinity:
			// No two VMs in the group should be on the same node.
			seen := make(map[string]bool)
			for _, vmid := range rule.VMIDs {
				n := findVMNode(vmid, nodeWorkloads)
				if n == "" {
					continue
				}
				if seen[n] {
					violations = append(violations, "anti-affinity rule violated: multiple VMs on same node "+n)
					break
				}
				seen[n] = true
			}

		case RuleTypePin:
			for _, vmid := range rule.VMIDs {
				n := findVMNode(vmid, nodeWorkloads)
				if n == "" {
					continue
				}
				if len(rule.NodeNames) > 0 && !containsString(rule.NodeNames, n) {
					violations = append(violations, "pin rule violated: VM not on designated node")
				}
			}
		}
	}

	return violations
}

// FindVMNodePublic returns the node a VM is on, given a workload map.
func FindVMNodePublic(vmid int, nodeWorkloads map[string][]Workload) string {
	return findVMNode(vmid, nodeWorkloads)
}

func findVMNode(vmid int, nodeWorkloads map[string][]Workload) string {
	for node, workloads := range nodeWorkloads {
		for _, w := range workloads {
			if w.VMID == vmid {
				return node
			}
		}
	}
	return ""
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
