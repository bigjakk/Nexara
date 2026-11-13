import { BaseEdge, EdgeLabelRenderer, getBezierPath } from "@xyflow/react";
import type { EdgeProps } from "@xyflow/react";
import { INFO_COLOR } from "@/lib/chart-colors";

export interface MigrationEdgeData {
  label: string;
  [key: string]: unknown;
}

/**
 * A migration in flight: a dashed blue arc (--info) with a travelling
 * pulse and a label. Rendered for running migrate tasks between two hosts.
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
          stroke: INFO_COLOR,
          strokeWidth: 1.5,
          strokeDasharray: "4 6",
        }}
      />
      <circle r={3.5} className="fill-info">
        <animateMotion dur="2.4s" repeatCount="indefinite" path={path} />
      </circle>
      {label !== "" && (
        <EdgeLabelRenderer>
          <div
            style={{
              transform: `translate(-50%, -50%) translate(${String(labelX)}px, ${String(labelY)}px)`,
            }}
            className="nodrag nopan pointer-events-none absolute z-10 rounded-md bg-card px-2 py-0.5 text-[11px] font-medium text-info ring-1 ring-info/40"
          >
            {label}
          </div>
        </EdgeLabelRenderer>
      )}
    </>
  );
}
