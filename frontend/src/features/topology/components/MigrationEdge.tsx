import { BaseEdge, EdgeLabelRenderer, getBezierPath } from "@xyflow/react";
import type { EdgeProps } from "@xyflow/react";

export interface MigrationEdgeData {
  label: string;
  [key: string]: unknown;
}

/**
 * A migration in flight: dashed emerald arc with a traveling pulse and a
 * floating label chip. Rendered for running migrate tasks between two hosts.
 */
export function MigrationEdge({
  id,
  sourceX,
  sourceY,
  targetX,
  targetY,
  sourcePosition,
  targetPosition,
  data,
}: EdgeProps) {
  const [path, labelX, labelY] = getBezierPath({
    sourceX,
    sourceY,
    sourcePosition,
    targetX,
    targetY,
    targetPosition,
    curvature: 0.5,
  });
  const label = (data as MigrationEdgeData | undefined)?.label ?? "";

  return (
    <>
      <BaseEdge
        id={id}
        path={path}
        style={{
          stroke: "#34d399",
          strokeWidth: 1.5,
          strokeDasharray: "4 6",
          opacity: 0.6,
        }}
      />
      <circle r={7} fill="rgba(52,211,153,0.25)">
        <animateMotion dur="2.4s" repeatCount="indefinite" path={path} />
      </circle>
      <circle r={3} fill="#34d399">
        <animateMotion dur="2.4s" repeatCount="indefinite" path={path} />
      </circle>
      {label !== "" && (
        <EdgeLabelRenderer>
          <div
            style={{
              transform: `translate(-50%, -50%) translate(${String(labelX)}px, ${String(labelY)}px)`,
            }}
            className="nodrag nopan pointer-events-none absolute z-10 rounded-full border border-emerald-500/30 bg-emerald-500/15 px-2 py-0.5 text-[10px] font-medium text-emerald-600 backdrop-blur-sm dark:text-emerald-400"
          >
            {label}
          </div>
        </EdgeLabelRenderer>
      )}
    </>
  );
}
