import { spellOutKey } from "@/lib/spell-out-key";

/** The audit log's resource types with their labels: its type filter's
 *  options (AuditLogPanel), and the names a History gives a kind of object. */
export const resourceTypes = [
  { value: "", label: "All types" },
  { value: "vm", label: "VM" },
  { value: "container", label: "Container" },
  { value: "migration", label: "Migration" },
  { value: "cluster", label: "Cluster" },
  { value: "storage", label: "Storage" },
  { value: "ceph_pool", label: "Ceph pool" },
  { value: "ceph_osd", label: "Ceph OSD" },
  { value: "ceph_mon", label: "Ceph monitor" },
  { value: "ceph_mgr", label: "Ceph manager" },
  { value: "ceph_mds", label: "Ceph MDS" },
  { value: "ceph_fs", label: "CephFS" },
  { value: "certificate", label: "Certificate" },
  { value: "auth", label: "Auth" },
  { value: "task", label: "Task" },
  { value: "drs", label: "DRS" },
  { value: "firewall_rule", label: "Firewall rule" },
  { value: "firewall_options", label: "Firewall options" },
  { value: "sdn", label: "SDN apply" },
  { value: "sdn_zone", label: "SDN zone" },
  { value: "sdn_vnet", label: "SDN VNet" },
  { value: "sdn_subnet", label: "SDN subnet" },
  { value: "sdn_controller", label: "SDN controller" },
  { value: "sdn_ipam", label: "SDN IPAM" },
  { value: "sdn_dns", label: "SDN DNS" },
  { value: "schedule", label: "Schedule" },
  { value: "pbs_server", label: "PBS server" },
  { value: "backup", label: "Backup" },
  { value: "veeam_server", label: "Veeam server" },
  { value: "veeam_job", label: "Veeam job" },
  { value: "veeam_session", label: "Veeam session" },
  { value: "network", label: "Network" },
  { value: "role", label: "Role" },
  { value: "rolling_update", label: "Rolling update" },
  { value: "cve_scan", label: "CVE scan" },
  { value: "proxmox_task", label: "Proxmox task" },
  { value: "node", label: "Node" },
  { value: "setting", label: "Setting" },
] as const;

/** A resource type's label, else its id spelled out in sentence case. */
export function resourceTypeLabel(type: string): string {
  const found = resourceTypes.find((rt) => rt.value === type);
  if (found) return found.label;
  return spellOutKey(type);
}
