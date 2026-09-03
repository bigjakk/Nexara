/** How Veeam's own result strings map onto the Badge palette. */
export function resultVariant(
  result: string,
): "default" | "secondary" | "destructive" | "outline" {
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
