import type { CSSProperties } from "react";

// The look the backup charts share with the metric charts elsewhere: muted
// tick labels, no axis lines, a popover-coloured tooltip. Plain values, kept
// out of the component files so those export components only.

export const AXIS_TICK = {
  fontSize: 10,
  fill: "hsl(var(--muted-foreground))",
} as const;

export const TOOLTIP_STYLE: CSSProperties = {
  backgroundColor: "hsl(var(--popover))",
  border: "1px solid hsl(var(--border))",
  borderRadius: "8px",
  fontSize: "12px",
  boxShadow: "0 4px 12px rgba(0,0,0,0.25)",
};

export const TOOLTIP_CURSOR = {
  stroke: "hsl(var(--muted-foreground))",
  strokeOpacity: 0.4,
} as const;

/**
 * The second of two series in one chart (write beside read, the projection
 * beside the reading): neutral and dashed, so it is told apart by shape and
 * no state colour is spent on it.
 */
export const SECOND_SERIES_COLOR = "hsl(var(--muted-foreground))";
export const SECOND_SERIES_DASH = "5 4";

/**
 * Recharts wraps a tick label at its spaces when the axis is narrow, which
 * splits "12.5 GiB" over two lines. A non-breaking space keeps it whole.
 */
export function unbroken(label: string): string {
  return label.replaceAll(" ", "\u00a0");
}
