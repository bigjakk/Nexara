import { memo } from "react";
import { Handle, Position } from "@xyflow/react";
import type { NodeProps } from "@xyflow/react";
import { Server } from "lucide-react";
import type { ClusterNodeData } from "../lib/topology-transform";

export const ClusterNode = memo(function ClusterNode({ data }: NodeProps) {
  const d = data as ClusterNodeData;

  return (
    <div
      className={`
        rounded-lg border-2 bg-card px-4 py-3 shadow-md transition-colors
        min-w-[200px] cursor-pointer
        ${d.status === "online" ? "border-green-500" : d.status === "degraded" ? "border-yellow-500" : d.status === "offline" ? "border-red-500" : "border-muted"}
      `}
    >
      <div className="flex items-center gap-2">
        <Server className="h-5 w-5 text-primary" />
        <span className="font-semibold text-sm">{d.label}</span>
      </div>
      <div className="mt-1.5 flex gap-3 text-xs text-muted-foreground">
        <span>{d.nodeCount} node{d.nodeCount !== 1 ? "s" : ""}</span>
        <span>{d.vmCount} guest{d.vmCount !== 1 ? "s" : ""}</span>
      </div>
      <Handle type="source" position={Position.Bottom} className="!bg-primary !w-2 !h-2" />
    </div>
  );
});
