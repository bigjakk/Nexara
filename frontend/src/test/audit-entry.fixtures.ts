import type { AuditLogEntry } from "@/features/audit/api/audit-queries";

/**
 * What the entry of a removed cluster looks like: it keeps the cluster id it
 * was written with, which no cluster has any more, and the name it carries is
 * the one kept for it, bare: the "(removed)" is the client's to write.
 */
export const removedClusterEntry: AuditLogEntry = {
  id: "e1",
  cluster_id: "c1",
  user_id: "u1",
  resource_type: "vm",
  resource_id: "r1",
  action: "start",
  details: JSON.stringify({
    upid: "UPID:pve-01:0000A1B2:00000001:6A8CE407:qmstart:100:root@pam:",
  }),
  created_at: "2026-09-01T10:00:00Z",
  source: "nexara",
  user_email: "u@example.com",
  user_display_name: "U",
  cluster_name: "cluster01",
  cluster_removed: true,
  resource_vmid: 100,
  resource_name: "linux01",
};

const haRequestUpid =
  "UPID:pve-01:0000A1B3:00000001:6A8CE408:hastart:100:ops@pam!token01:";
const haWorkerUpid =
  "UPID:pve-01:0000A1B4:00000001:6A8CE40C:qmstart:100:root@pam:";

/**
 * An HA-managed guest's start as Proxmox runs it and the collector links it:
 * the operator's request, and the task the HA manager then runs as root@pam,
 * which names the request. Newest first, as a list holds them.
 */
export const haStartEntries: AuditLogEntry[] = [
  {
    ...removedClusterEntry,
    id: "ha-worker",
    cluster_removed: false,
    source: "proxmox",
    user_display_name: "",
    user_email: "",
    created_at: "2026-09-01T10:00:04Z",
    details: JSON.stringify({
      upid: haWorkerUpid,
      proxmox_user: "root@pam",
      ha_request_upid: haRequestUpid,
    }),
  },
  {
    ...removedClusterEntry,
    id: "ha-request",
    cluster_removed: false,
    user_display_name: "Ops",
    details: JSON.stringify({
      upid: haRequestUpid,
      ha_worker_upid: haWorkerUpid,
    }),
  },
];
