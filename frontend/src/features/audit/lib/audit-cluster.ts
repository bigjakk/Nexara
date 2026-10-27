import i18n from "@/lib/i18n";
import type { AuditLogEntry } from "../api/audit-queries";

/**
 * The cluster an entry was recorded against, in words. The server answers the
 * bare name and says when the cluster is gone (cluster_removed), so that the
 * "removed" is written here, where it can be translated. `empty` is what an entry
 * with no cluster shows, which differs by surface.
 */
export function auditClusterLabel(
  entry: Pick<AuditLogEntry, "cluster_name" | "cluster_removed">,
  empty: string,
): string {
  if (!entry.cluster_removed) return entry.cluster_name || empty;
  return entry.cluster_name
    ? i18n.t("audit:clusterRemoved", { name: entry.cluster_name })
    : i18n.t("audit:clusterRemovedUnnamed");
}
