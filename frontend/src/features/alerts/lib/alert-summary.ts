import type { TFunction } from "i18next";
import type { AlertSummary } from "@/types/api";

/**
 * The Alerts page's one-line state. What is firing leads; with nothing firing
 * it says so, then what is still pending or acknowledged. Before the counts
 * arrive it says what the page is for.
 */
export function alertSummaryLine(
  summary: AlertSummary | undefined,
  t: TFunction<"alerts">,
): string {
  if (summary === undefined) return t("summaryPurpose");
  const parts = [
    summary.firing_count > 0
      ? t("summaryFiring", { count: summary.firing_count })
      : t("summaryNoneFiring"),
  ];
  if (summary.pending_count > 0) {
    parts.push(t("summaryPending", { count: summary.pending_count }));
  }
  if (summary.acknowledged_count > 0) {
    parts.push(t("summaryAcknowledged", { count: summary.acknowledged_count }));
  }
  return parts.join(", ");
}
