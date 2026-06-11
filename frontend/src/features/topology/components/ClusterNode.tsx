import { memo } from "react";
import { Handle, Position } from "@xyflow/react";
import type { NodeProps } from "@xyflow/react";
import { cn } from "@/lib/utils";
import { getStatusColor } from "../lib/topology-transform";
import type { ClusterNodeData } from "../lib/topology-transform";

export const ClusterNode = memo(function ClusterNode({ data }: NodeProps) {
  const d = data as ClusterNodeData;
  const statusColor = getStatusColor(d.status);

  return (
    <div
      className={cn(
        "min-w-[220px] cursor-pointer rounded-xl border px-4 py-2.5 shadow-sm transition-colors",
        d.status === "online"
          ? "border-emerald-500/30 bg-emerald-500/[0.05] hover:border-emerald-500/50"
          : d.status === "degraded"
            ? "border-amber-500/30 bg-amber-500/[0.05] hover:border-amber-500/50"
            : d.status === "offline"
              ? "border-red-500/30 bg-red-500/[0.05] hover:border-red-500/50"
              : "border-border bg-card hover:border-muted-foreground/30",
      )}
    >
      <div className="flex items-center gap-2">
        <span
          className="h-2.5 w-2.5 shrink-0 rounded-full"
          style={{
            backgroundColor: statusColor,
            boxShadow: d.status === "online" ? `0 0 8px ${statusColor}` : undefined,
          }}
        />
        <span className="text-sm font-semibold tracking-tight">{d.label}</span>
        {d.pveVersion && (
          <span className="ml-auto shrink-0 rounded-md border px-1.5 py-0.5 text-[10px] leading-none text-muted-foreground">
            PVE {d.pveVersion}
          </span>
        )}
      </div>
      <div className="mt-1.5 flex gap-3 text-xs text-muted-foreground">
        <span>{d.nodeCount} node{d.nodeCount !== 1 ? "s" : ""}</span>
        <span>{d.vmCount} guest{d.vmCount !== 1 ? "s" : ""}</span>
      </div>
      <Handle type="source" position={Position.Bottom} className="!h-2 !w-2 !bg-primary" />
    </div>
  );
});
