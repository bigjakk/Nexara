package migration

import (
	"context"
	"fmt"
	"strings"

	"github.com/proxdash/proxdash/internal/proxmox"
)

// RunPreFlightChecks performs all pre-flight validations for a migration.
func RunPreFlightChecks(
	ctx context.Context,
	sourceClient *proxmox.Client,
	targetClient *proxmox.Client,
	sourceNode string,
	targetNode string,
	vmid int,
	vmType string,
	migrationType string,
	storageMap StorageMapping,
	networkMap NetworkMapping,
	targetVMID int,
) (*PreFlightReport, error) {
	report := &PreFlightReport{
		Checks: make([]CheckResult, 0),
		Passed: true,
	}

	// For intra-cluster migration, source and target clients are the same
	// and we only need basic checks.
	if migrationType == TypeIntraCluster {
		report.addCheck(checkTargetNodeOnline(ctx, sourceClient, targetNode))
		report.addCheck(checkTargetVMIDAvailable(ctx, targetClient, targetNode, targetVMID, vmType))
		return report, nil
	}

	// Cross-cluster checks.
	report.addCheck(checkPVEVersion(ctx, sourceClient, sourceNode))
	report.addCheck(checkPVEVersion(ctx, targetClient, targetNode))
	report.addCheck(checkCPUCompatibility(ctx, sourceClient, targetClient, sourceNode, targetNode))
	report.addCheck(checkTargetNodeOnline(ctx, targetClient, targetNode))

	if targetVMID > 0 {
		report.addCheck(checkTargetVMIDAvailable(ctx, targetClient, targetNode, targetVMID, vmType))
	}

	report.addCheck(checkStorageMappings(ctx, targetClient, targetNode, storageMap))
	report.addCheck(checkNetworkMappings(ctx, targetClient, targetNode, networkMap))

	return report, nil
}

func (r *PreFlightReport) addCheck(result CheckResult) {
	r.Checks = append(r.Checks, result)
	if result.Severity == SeverityFail {
		r.Passed = false
	}
}

func checkPVEVersion(ctx context.Context, client *proxmox.Client, node string) CheckResult {
	status, err := client.GetNodeStatus(ctx, node)
	if err != nil {
		return CheckResult{
			Name:     "pve_version",
			Severity: SeverityFail,
			Message:  fmt.Sprintf("Failed to get node status for %s: %v", node, err),
		}
	}

	// PVE version looks like "pve-manager/7.4-15/..." — we need major >= 7.
	ver := status.PVEVersion
	if ver == "" {
		return CheckResult{
			Name:     "pve_version",
			Severity: SeverityWarn,
			Message:  fmt.Sprintf("Could not determine PVE version for %s", node),
		}
	}

	// Extract major version.
	parts := strings.SplitN(ver, "/", 3)
	if len(parts) >= 2 {
		verParts := strings.SplitN(parts[1], ".", 2)
		if len(verParts) >= 1 {
			major := verParts[0]
			if major >= "7" {
				return CheckResult{
					Name:     "pve_version",
					Severity: SeverityPass,
					Message:  fmt.Sprintf("PVE version %s on %s supports remote migration", parts[1], node),
				}
			}
			return CheckResult{
				Name:     "pve_version",
				Severity: SeverityFail,
				Message:  fmt.Sprintf("PVE version %s on %s does not support remote migration (requires >= 7.0)", parts[1], node),
			}
		}
	}

	return CheckResult{
		Name:     "pve_version",
		Severity: SeverityWarn,
		Message:  fmt.Sprintf("Could not parse PVE version %q for %s", ver, node),
	}
}

func checkCPUCompatibility(ctx context.Context, srcClient, tgtClient *proxmox.Client, srcNode, tgtNode string) CheckResult {
	srcStatus, err := srcClient.GetNodeStatus(ctx, srcNode)
	if err != nil {
		return CheckResult{
			Name:     "cpu_compatibility",
			Severity: SeverityWarn,
			Message:  fmt.Sprintf("Could not get source node CPU info: %v", err),
		}
	}

	tgtStatus, err := tgtClient.GetNodeStatus(ctx, tgtNode)
	if err != nil {
		return CheckResult{
			Name:     "cpu_compatibility",
			Severity: SeverityWarn,
			Message:  fmt.Sprintf("Could not get target node CPU info: %v", err),
		}
	}

	srcFlags := parseFlags(srcStatus.CPUInfo.Flags)
	tgtFlags := parseFlags(tgtStatus.CPUInfo.Flags)

	// Check critical CPU flags needed for live migration.
	criticalFlags := []string{"vmx", "svm"} // Intel VT-x / AMD-V
	var missing []string
	for _, flag := range criticalFlags {
		if srcFlags[flag] && !tgtFlags[flag] {
			missing = append(missing, flag)
		}
	}

	if len(missing) > 0 {
		return CheckResult{
			Name:     "cpu_compatibility",
			Severity: SeverityWarn,
			Message:  fmt.Sprintf("Target node %s missing CPU flags: %s", tgtNode, strings.Join(missing, ", ")),
		}
	}

	return CheckResult{
		Name:     "cpu_compatibility",
		Severity: SeverityPass,
		Message:  fmt.Sprintf("CPU compatibility OK (source: %s, target: %s)", srcStatus.CPUInfo.Model, tgtStatus.CPUInfo.Model),
	}
}

func checkTargetNodeOnline(ctx context.Context, client *proxmox.Client, node string) CheckResult {
	nodes, err := client.GetNodes(ctx)
	if err != nil {
		return CheckResult{
			Name:     "target_node_online",
			Severity: SeverityFail,
			Message:  fmt.Sprintf("Failed to list nodes: %v", err),
		}
	}

	for _, n := range nodes {
		if n.Node == node {
			if n.Status == "online" {
				return CheckResult{
					Name:     "target_node_online",
					Severity: SeverityPass,
					Message:  fmt.Sprintf("Target node %s is online", node),
				}
			}
			return CheckResult{
				Name:     "target_node_online",
				Severity: SeverityFail,
				Message:  fmt.Sprintf("Target node %s is %s", node, n.Status),
			}
		}
	}

	return CheckResult{
		Name:     "target_node_online",
		Severity: SeverityFail,
		Message:  fmt.Sprintf("Target node %s not found in cluster", node),
	}
}

func checkTargetVMIDAvailable(ctx context.Context, client *proxmox.Client, node string, vmid int, vmType string) CheckResult {
	if vmid <= 0 {
		return CheckResult{
			Name:     "target_vmid",
			Severity: SeverityPass,
			Message:  "No target VMID specified; Proxmox will auto-assign",
		}
	}

	// Check all resources in target cluster to see if the VMID is in use.
	resources, err := client.GetClusterResources(ctx, "")
	if err != nil {
		return CheckResult{
			Name:     "target_vmid",
			Severity: SeverityWarn,
			Message:  fmt.Sprintf("Could not check target VMID availability: %v", err),
		}
	}

	for _, r := range resources {
		if r.VMID == vmid && (r.Type == "qemu" || r.Type == "lxc") {
			return CheckResult{
				Name:     "target_vmid",
				Severity: SeverityFail,
				Message:  fmt.Sprintf("VMID %d already exists on target cluster (type: %s, node: %s)", vmid, r.Type, r.Node),
			}
		}
	}

	return CheckResult{
		Name:     "target_vmid",
		Severity: SeverityPass,
		Message:  fmt.Sprintf("VMID %d is available on target cluster", vmid),
	}
}

func checkStorageMappings(ctx context.Context, client *proxmox.Client, node string, storageMap StorageMapping) CheckResult {
	if len(storageMap) == 0 {
		return CheckResult{
			Name:     "storage_mappings",
			Severity: SeverityPass,
			Message:  "No storage mappings (Proxmox will use default)",
		}
	}

	pools, err := client.GetStoragePools(ctx, node)
	if err != nil {
		return CheckResult{
			Name:     "storage_mappings",
			Severity: SeverityWarn,
			Message:  fmt.Sprintf("Could not verify target storage pools: %v", err),
		}
	}

	poolSet := make(map[string]bool)
	for _, p := range pools {
		if p.Active == 1 && p.Enabled == 1 {
			poolSet[p.Storage] = true
		}
	}

	var missing []string
	for _, target := range storageMap {
		if !poolSet[target] {
			missing = append(missing, target)
		}
	}

	if len(missing) > 0 {
		return CheckResult{
			Name:     "storage_mappings",
			Severity: SeverityFail,
			Message:  fmt.Sprintf("Target storage pools not found or inactive: %s", strings.Join(missing, ", ")),
		}
	}

	return CheckResult{
		Name:     "storage_mappings",
		Severity: SeverityPass,
		Message:  fmt.Sprintf("All %d storage mappings verified", len(storageMap)),
	}
}

func checkNetworkMappings(ctx context.Context, client *proxmox.Client, node string, networkMap NetworkMapping) CheckResult {
	if len(networkMap) == 0 {
		return CheckResult{
			Name:     "network_mappings",
			Severity: SeverityPass,
			Message:  "No network mappings (Proxmox will use default)",
		}
	}

	bridges, err := client.GetNetworkBridges(ctx, node)
	if err != nil {
		return CheckResult{
			Name:     "network_mappings",
			Severity: SeverityWarn,
			Message:  fmt.Sprintf("Could not verify target bridges: %v", err),
		}
	}

	bridgeSet := make(map[string]bool)
	for _, b := range bridges {
		bridgeSet[b.Iface] = true
	}

	var missing []string
	for _, target := range networkMap {
		if !bridgeSet[target] {
			missing = append(missing, target)
		}
	}

	if len(missing) > 0 {
		return CheckResult{
			Name:     "network_mappings",
			Severity: SeverityFail,
			Message:  fmt.Sprintf("Target bridges not found: %s", strings.Join(missing, ", ")),
		}
	}

	return CheckResult{
		Name:     "network_mappings",
		Severity: SeverityPass,
		Message:  fmt.Sprintf("All %d network mappings verified", len(networkMap)),
	}
}

func parseFlags(flagStr string) map[string]bool {
	flags := make(map[string]bool)
	for _, f := range strings.Fields(flagStr) {
		flags[f] = true
	}
	return flags
}
