import type { TFunction } from "i18next";
import type { StatTone } from "@/components/page/StatStrip";
import type { SecurityPosture } from "@/types/api";

/** Whether the posture describes a scan at all: before the first one it is a placeholder. */
export function hasScans(posture: SecurityPosture): boolean {
  return posture.status !== "no_scans";
}

/** A posture score's tone: plain when good, never green. */
export function scoreTone(score: number): StatTone {
  if (score >= 90) return "default";
  if (score >= 70) return "warn";
  return "bad";
}

/** A count of findings: quiet at zero, and `whenAny` once there is one. */
export function countTone(count: number, whenAny: StatTone): StatTone {
  return count > 0 ? whenAny : "quiet";
}

/**
 * The Security page's one-line state, from the newest completed scan: how
 * many nodes it covered and what it found, the worst of it named.
 */
export function postureSummary(
  posture: SecurityPosture | undefined,
  t: TFunction<"security">,
): string {
  if (posture === undefined) return t("summaryPurpose");
  if (!hasScans(posture)) return t("summaryNoScans");
  const nodes = t("summaryNodes", {
    count: posture.total_nodes,
    scanned: posture.scanned_nodes,
  });
  if (posture.total_vulns === 0) return `${nodes}, ${t("summaryClean")}`;
  const worst = [
    ...(posture.critical_count > 0
      ? [t("summaryCritical", { count: posture.critical_count })]
      : []),
    ...(posture.kev_count > 0
      ? [t("summaryExploited", { count: posture.kev_count })]
      : []),
  ];
  const vulns = t("summaryVulns", { count: posture.total_vulns });
  return `${nodes}, ${vulns}${worst.length > 0 ? ` (${worst.join(", ")})` : ""}`;
}
