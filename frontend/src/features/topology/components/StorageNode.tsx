import { memo } from "react";
import { Handle, Position } from "@xyflow/react";
import type { NodeProps } from "@xyflow/react";
import { Database } from "lucide-react";
import { formatBytes } from "../lib/topology-transform";
import type { StorageNodeData } from "../lib/topology-transform";

export const StorageNode = memo(function StorageNode({ data }: NodeProps) {
  const d = data as StorageNodeData;
  const usedPercent = d.total > 0 ? (d.used / d.total) * 100 : 0;
  const barColor =
    usedPercent > 90 ? "bg-red-500" : usedPercent > 70 ? "bg-yellow-500" : "bg-green-500";

  return (
    <div className="rounded border bg-card px-2.5 py-2 shadow-sm min-w-[140px] cursor-pointer">
      <Handle type="target" position={Position.Top} className="!bg-muted-foreground !w-1.5 !h-1.5" />
      <div className="flex items-center gap-1.5">
        <Database className="h-3.5 w-3.5 text-muted-foreground" />
        <span className="text-xs font-medium truncate">{d.label}</span>
        {d.shared && (
          <span className="rounded bg-blue-500/10 px-1 text-[9px] text-blue-500">
            shared
          </span>
        )}
      </div>
      <div className="mt-1 text-[10px] text-muted-foreground">
        {d.storageType} &middot; {formatBytes(d.used)} / {formatBytes(d.total)}
      </div>
      {d.total > 0 && (
        <div className="mt-1 h-1 w-full rounded-full bg-muted">
          <div
            className={`h-full rounded-full ${barColor}`}
            style={{ width: `${Math.min(usedPercent, 100).toFixed(0)}%` }}
          />
        </div>
      )}
    </div>
  );
});
