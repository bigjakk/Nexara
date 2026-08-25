/** One row of the central guest snapshot inventory, as served by
 * GET /api/v1/guest-snapshots. Keyed on (cluster_id, vmid, name) — the
 * Proxmox-stable identity — while vm_id/vm_name/vm_status come from a
 * LEFT JOIN on the live vms row and are null when the guest is not
 * currently in inventory. */
export interface GuestSnapshotRow {
  cluster_id: string;
  cluster_name: string;
  vmid: number;
  guest_type: "qemu" | "lxc";
  vm_id: string | null;
  vm_name: string | null;
  vm_status: string | null;
  node: string;
  name: string;
  description: string;
  parent: string;
  vmstate: boolean;
  /** Unix seconds; 0 = Proxmox omitted snaptime (age unknown). */
  snap_time: number;
  last_seen_at: string;
}
