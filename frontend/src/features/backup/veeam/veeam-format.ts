import type { BadgeVariant } from "@/components/ui/badge";
import type { VeeamSession } from "../types/backup";

/** How Veeam's own result strings map onto the Badge palette. */
export function resultVariant(result: string): BadgeVariant {
  switch (result) {
    case "Success":
      return "default";
    case "Warning":
      return "outline";
    case "Failed":
      return "destructive";
    default:
      return "secondary";
  }
}

/**
 * Veeam's bottleneck analysis, or an em dash when it has none.
 *
 * "NotDefined" is Veeam's own placeholder for a run it did not analyse —
 * rendering it verbatim reads as a finding rather than the absence of one.
 */
export function bottleneckLabel(value: string): string {
  return value === "" || value === "NotDefined" ? "—" : value;
}

/** Veeam's processing rate, or an em dash — "N/A" is its no-data spelling. */
export function processingRateLabel(value: string): string {
  return value === "" || value === "N/A" ? "—" : value;
}

/**
 * The Result cell's own label.
 *
 * Named for sessions specifically: VeeamJobTable has its own `resultLabel`
 * taking a bare result string, and a job's last_result carries no
 * nexara_stopped flag to consult. Merging them would silently drop the
 * distinction between an operator's stop and a genuine backup failure.
 */
export function sessionResultLabel(session: VeeamSession): string | null {
  if (session.result === "") return null;
  if (session.result !== "Failed") return session.result;
  return session.nexara_stopped ? "Stopped from Nexara" : "Failed or cancelled";
}
