import type { HealthIssue } from "@/types/api";

// U+0001 field delimiter — a control char that can't appear in cluster ids,
// issue types, targets, or messages, so distinct issue states never collide in
// a signature. Built via fromCharCode so the source stays plain ASCII.
const SEP = String.fromCharCode(1);

/**
 * issueSig is the stable per-issue signature (identity + current state) used for
 * dismiss persistence: it stays constant while the condition is unchanged but
 * changes if the issue escalates or its reason changes, so a recurrence re-shows.
 */
export function issueSig(clusterId: string, i: HealthIssue): string {
  return [clusterId, i.type, i.target, i.severity, i.detail].join(SEP);
}

/** An issue is hidden if its type is muted or this exact state was dismissed. */
export function isIssueHidden(
  clusterId: string,
  i: HealthIssue,
  dismissed: ReadonlySet<string>,
  muted: ReadonlySet<string>,
): boolean {
  return muted.has(i.type) || dismissed.has(issueSig(clusterId, i));
}

/** Filters a cluster's issues to those that should currently be shown. */
export function visibleClusterIssues(
  clusterId: string,
  issues: HealthIssue[],
  dismissed: ReadonlySet<string>,
  muted: ReadonlySet<string>,
): HealthIssue[] {
  return issues.filter((i) => !isIssueHidden(clusterId, i, dismissed, muted));
}
