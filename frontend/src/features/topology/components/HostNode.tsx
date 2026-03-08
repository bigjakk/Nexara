import { memo } from "react";
import { Handle, Position } from "@xyflow/react";
import type { NodeProps } from "@xyflow/react";
import { Monitor } from "lucide-react";
import { getStatusColor, formatBytes } from "../lib/topology-transform";
import type { HostNodeData } from "../lib/topology-transform";

export const HostNode = memo(function HostNode({ data }: NodeProps) {
  const d = data as HostNodeData;
  const statusColor = getStatusColor(d.status);

  return (
    <div className="rounded-lg border bg-card px-3 py-2.5 shadow-sm min-w-[180px] cursor-pointer">
      <Handle type="target" position={Position.Top} className="!bg-muted-foreground !w-2 !h-2" />
      <div className="flex items-center gap-2">
        <div
          className="h-2.5 w-2.5 rounded-full shrink-0"
          style={{ backgroundColor: statusColor }}
        />
        <Monitor className="h-4 w-4 text-muted-foreground" />
        <span className="font-medium text-sm truncate">{d.label}</span>
      </div>
      <div className="mt-1.5 grid grid-cols-2 gap-x-3 text-xs text-muted-foreground">
        <span>{d.cpuCount} vCPU{d.cpuCount !== 1 ? "s" : ""}</span>
        <span>{formatBytes(d.memTotal)} RAM</span>
      </div>
      {d.pveVersion && (
        <div className="mt-0.5 text-[10px] text-muted-foreground/70">
          PVE {d.pveVersion}
        </div>
      )}
      <Handle type="source" position={Position.Bottom} className="!bg-muted-foreground !w-2 !h-2" />
    </div>
  );
});
