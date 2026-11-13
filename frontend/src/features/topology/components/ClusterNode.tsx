import { memo } from "react";
import { Position } from "@xyflow/react";
import type { NodeProps } from "@xyflow/react";
import { StatusIcon } from "@/components/StatusIcon";
import { cn } from "@/lib/utils";
import type { ClusterNodeData } from "../lib/topology-transform";
import { TopologyHandle } from "./TopologyHandle";

/** What a cluster that is not simply online says about itself, and how loudly. */
const STATE_NOTE: Record<string, { label: string; className: string }> = {
  degraded: {
    label: "Degraded",
    className: "font-semibold text-amber-700 dark:text-amber-400",
  },
  offline: {
    label: "Offline",
    className: "font-semibold text-red-700 dark:text-red-400",
  },
  inactive: { label: "Inactive", className: "" },
  unknown: { label: "Unknown", className: "" },
};

export const ClusterNode = memo(function ClusterNode({
  data,
  sourcePosition,
}: NodeProps) {
  const d = data as ClusterNodeData;
  const note = STATE_NOTE[d.status];

  return (
    // A fixed size, as the layout centres it, so its foot (where its edges
    // leave and the power overlay steps aside) is where the layout puts it;
    // a long name truncates.
    <div className="flex h-16 w-[220px] cursor-pointer flex-col justify-center rounded-[10px] bg-card px-3.5 text-card-foreground">
      <div className="flex items-center gap-2">
        <StatusIcon status={d.status} />
        <span
          className="min-w-0 truncate text-[15px] font-semibold tracking-tight [font-stretch:108%]"
          title={d.label}
        >
          {d.label}
        </span>
        {d.pveVersion && (
          <span className="ml-auto shrink-0 text-[11px] text-muted-foreground">
            PVE {d.pveVersion}
          </span>
        )}
      </div>
      <div className="mt-0.5 flex items-baseline gap-1.5 text-[11.5px] text-muted-foreground">
        <span className="truncate">
          {d.nodeCount} node{d.nodeCount !== 1 ? "s" : ""}, {d.vmCount} guest
          {d.vmCount !== 1 ? "s" : ""}
        </span>
        {note !== undefined && (
          <span className={cn("ml-auto shrink-0", note.className)}>
            {note.label}
          </span>
        )}
      </div>
      <TopologyHandle
        type="source"
        position={sourcePosition ?? Position.Bottom}
      />
    </div>
  );
});
