import { BaseEdge } from "@xyflow/react";
import type { EdgeProps } from "@xyflow/react";
import { STACK_INSET } from "../lib/topology-layout";

export interface StackEdgeData {
  /** How far below its source the edge turns toward the stack's trunk. */
  drop?: number;
  [key: string]: unknown;
}

const RADIUS = 6;

/** A path through `points` with its corners rounded. */
function roundedPath(points: readonly (readonly [number, number])[]): string {
  // A zero-length leg (the source already on the trunk) has no corner to draw.
  const pts = points.filter(
    (p, i) =>
      i === 0 || p[0] !== points[i - 1]?.[0] || p[1] !== points[i - 1]?.[1],
  );
  const first = pts[0];
  if (first === undefined) return "";
  let d = `M ${String(first[0])} ${String(first[1])}`;
  for (let i = 1; i < pts.length; i++) {
    const prev = pts[i - 1];
    const here = pts[i];
    const next = pts[i + 1];
    if (prev === undefined || here === undefined) continue;
    if (next === undefined) {
      d += ` L ${String(here[0])} ${String(here[1])}`;
      continue;
    }
    const inLen = Math.hypot(here[0] - prev[0], here[1] - prev[1]);
    const outLen = Math.hypot(next[0] - here[0], next[1] - here[1]);
    const r = Math.min(RADIUS, inLen / 2, outLen / 2);
    const ax = here[0] - ((here[0] - prev[0]) / inLen) * r;
    const ay = here[1] - ((here[1] - prev[1]) / inLen) * r;
    const bx = here[0] + ((next[0] - here[0]) / outLen) * r;
    const by = here[1] + ((next[1] - here[1]) / outLen) * r;
    d += ` L ${String(ax)} ${String(ay)} Q ${String(here[0])} ${String(here[1])} ${String(bx)} ${String(by)}`;
  }
  return d;
}

/**
 * The edge from a cluster into a chip of its shared-storage stack: down from
 * the cluster, across to the trunk that runs beside the stack, down the trunk
 * and into the chip's side. Every chip of the stack shares the bus and the
 * trunk, so the stack reads as one branch.
 */
export function StackEdge({
  id,
  sourceX,
  sourceY,
  targetX,
  targetY,
  data,
  style,
}: EdgeProps) {
  const drop = (data as StackEdgeData | undefined)?.drop ?? 12;
  const trunkX = targetX - STACK_INSET / 2;
  const turnY = Math.min(sourceY + drop, targetY);
  const path = roundedPath([
    [sourceX, sourceY],
    [sourceX, turnY],
    [trunkX, turnY],
    [trunkX, targetY],
    [targetX, targetY],
  ]);
  return <BaseEdge id={id} path={path} {...(style ? { style } : {})} />;
}
