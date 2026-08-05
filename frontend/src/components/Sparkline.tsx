import { useId } from "react";
import { cn } from "@/lib/utils";

/**
 * Inline trend area for stat cards, styled like the large MetricChart:
 * smooth spline, gradient fill, zero-floored. Renders nothing until 2+ points.
 *
 * The y-axis spans `min` (default 0) to `max` (default the data's peak plus
 * 10% headroom), matching the big charts — the shape stays readable at low
 * utilization while the baseline still marks the floor.
 *
 * The SVG stretches to whatever size `className` gives it (e.g. `h-7 w-full`
 * for a full-card strip, `h-[22px] w-16` for a compact inline trend); strokes
 * keep their on-screen width regardless of scaling.
 */
export function Sparkline({
  points,
  className,
  min = 0,
  max,
}: {
  points: number[];
  className?: string;
  min?: number | undefined;
  max?: number | undefined;
}) {
  // Gradient ids must be unique per instance — several sparklines share a page.
  const gradientId = useId();
  if (points.length < 2) return null;
  const W = 64;
  const H = 22;
  const lo = min;
  const peak = Math.max(...points, lo);
  const hi = max ?? lo + (peak - lo) * 1.1;
  const span = hi - lo || 1;
  const xy = points.map((p, i) => ({
    x: (i / (points.length - 1)) * W,
    y: H - 1 - Math.min(1, Math.max(0, (p - lo) / span)) * (H - 2),
  }));
  // Catmull-Rom → cubic Bézier for the MetricChart-like smooth curve; control
  // point y is clamped so overshoot never leaves the drawing area.
  const clampY = (y: number) => Math.min(H - 1, Math.max(1, y));
  const first = xy[0];
  if (first === undefined) return null;
  let d = `M ${first.x.toFixed(2)} ${first.y.toFixed(2)}`;
  for (let i = 0; i < xy.length - 1; i++) {
    const p0 = xy[Math.max(0, i - 1)] ?? first;
    const p1 = xy[i] ?? first;
    const p2 = xy[i + 1] ?? first;
    const p3 = xy[Math.min(xy.length - 1, i + 2)] ?? p2;
    const c1x = p1.x + (p2.x - p0.x) / 6;
    const c1y = clampY(p1.y + (p2.y - p0.y) / 6);
    const c2x = p2.x - (p3.x - p1.x) / 6;
    const c2y = clampY(p2.y - (p3.y - p1.y) / 6);
    d += ` C ${c1x.toFixed(2)} ${c1y.toFixed(2)}, ${c2x.toFixed(2)} ${c2y.toFixed(2)}, ${p2.x.toFixed(2)} ${p2.y.toFixed(2)}`;
  }
  const fillD = `${d} L ${String(W)} ${String(H - 1)} L 0 ${String(H - 1)} Z`;
  return (
    <svg
      viewBox={`0 0 ${String(W)} ${String(H)}`}
      preserveAspectRatio="none"
      className={cn("shrink-0", className)}
      aria-hidden="true"
      data-testid="sparkline"
    >
      <defs>
        <linearGradient id={gradientId} x1="0" y1="0" x2="0" y2="1">
          <stop offset="0%" stopColor="currentColor" stopOpacity="0.3" />
          <stop offset="100%" stopColor="currentColor" stopOpacity="0.03" />
        </linearGradient>
      </defs>
      <path d={fillD} fill={`url(#${gradientId})`} stroke="none" />
      <line
        x1="0"
        y1={H - 0.5}
        x2={W}
        y2={H - 0.5}
        stroke="currentColor"
        strokeOpacity="0.3"
        strokeWidth="1"
        vectorEffect="non-scaling-stroke"
      />
      <path
        d={d}
        fill="none"
        stroke="currentColor"
        strokeWidth="1.5"
        strokeLinecap="round"
        vectorEffect="non-scaling-stroke"
      />
    </svg>
  );
}
