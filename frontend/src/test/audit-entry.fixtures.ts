import type { AuditLogEntry } from "@/features/audit/api/audit-queries";

/**
 * What the entry of a removed cluster looks like once audit_log.cluster_id is
 * SET NULL: its guest and its task are still named, its cluster no longer is.
 */
export const detachedClusterEntry: AuditLogEntry = {
  id: "e1",
  cluster_id: null,
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
  cluster_name: "",
  resource_vmid: 100,
  resource_name: "linux01",
};
