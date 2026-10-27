import type { AuditLogEntry } from "@/features/audit/api/audit-queries";

/**
 * What the entry of a removed cluster looks like: it keeps the cluster id it
 * was written with, which no cluster has any more, and the name it carries is
 * the one kept for it.
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
  cluster_name: "cluster01 (removed)",
  cluster_removed: true,
  resource_vmid: 100,
  resource_name: "linux01",
};
