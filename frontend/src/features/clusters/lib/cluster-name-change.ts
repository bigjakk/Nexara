import type { HealthIssue } from "@/types/api";

/**
 * The names a cluster_name_changed issue is about: the corosync cluster name
 * Nexara recorded, and the one the API URL answers with now. The issue carries
 * them only in its detail (handlers/health.go), worded "The API URL now reaches
 * a cluster named <reported>, not <recorded>, so …". A corosync name has no
 * space or comma in it, so the two are read from there; null when the wording
 * is not that, and no accept is offered.
 */
export function clusterNameChange(
  issue: HealthIssue,
): { recorded: string; reported: string } | null {
  if (issue.type !== "cluster_name_changed") return null;
  const m = /reaches a cluster named ([^\s,]+), not ([^\s,]+), so /.exec(
    issue.detail,
  );
  if (m?.[1] === undefined || m[2] === undefined) return null;
  return { reported: m[1], recorded: m[2] };
}
