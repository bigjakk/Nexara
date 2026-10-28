/** The audit log's resource types with their labels: its type filter's
 *  options (AuditLogPanel), and the names a History gives a kind of object. */
export const resourceTypes = [
  { value: "", label: "All Types" },
  { value: "vm", label: "VM" },
  { value: "container", label: "Container" },
  { value: "migration", label: "Migration" },
  { value: "cluster", label: "Cluster" },
  { value: "storage", label: "Storage" },
  { value: "ceph_pool", label: "Ceph Pool" },
  { value: "ceph_osd", label: "Ceph OSD" },
  { value: "ceph_mon", label: "Ceph Monitor" },
  { value: "ceph_mgr", label: "Ceph Manager" },
  { value: "ceph_mds", label: "Ceph MDS" },
  { value: "ceph_fs", label: "CephFS" },
  { value: "certificate", label: "Certificate" },
  { value: "auth", label: "Auth" },
  { value: "task", label: "Task" },
  { value: "drs", label: "DRS" },
  { value: "firewall_rule", label: "Firewall Rule" },
  { value: "firewall_options", label: "Firewall Options" },
  { value: "sdn", label: "SDN Apply" },
  { value: "sdn_zone", label: "SDN Zone" },
  { value: "sdn_vnet", label: "SDN VNet" },
  { value: "sdn_subnet", label: "SDN Subnet" },
  { value: "sdn_controller", label: "SDN Controller" },
  { value: "sdn_ipam", label: "SDN IPAM" },
  { value: "sdn_dns", label: "SDN DNS" },
  { value: "schedule", label: "Schedule" },
  { value: "pbs_server", label: "PBS Server" },
  { value: "backup", label: "Backup" },
  { value: "veeam_server", label: "Veeam Server" },
  { value: "veeam_job", label: "Veeam Job" },
  { value: "veeam_session", label: "Veeam Session" },
  { value: "network", label: "Network" },
  { value: "role", label: "Role" },
  { value: "rolling_update", label: "Rolling Update" },
  { value: "cve_scan", label: "CVE Scan" },
  { value: "proxmox_task", label: "Proxmox Task" },
  { value: "node", label: "Node" },
  { value: "setting", label: "Setting" },
] as const;

/** A resource type's label, else its id spelled out in title case. */
export function resourceTypeLabel(type: string): string {
  const found = resourceTypes.find((rt) => rt.value === type);
  if (found) return found.label;
  return type.replace(/_/g, " ").replace(/\b\w/g, (c) => c.toUpperCase());
}
