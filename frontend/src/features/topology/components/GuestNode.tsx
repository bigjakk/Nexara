import { memo } from "react";
import { Handle, Position } from "@xyflow/react";
import type { NodeProps } from "@xyflow/react";
import { MonitorSmartphone, Container } from "lucide-react";
import { getGuestStatusColor, formatBytes } from "../lib/topology-transform";
import type { GuestNodeData } from "../lib/topology-transform";

export const GuestNode = memo(function GuestNode({ data }: NodeProps) {
  const d = data as GuestNodeData;
  const statusColor = getGuestStatusColor(d.status);
  const Icon = d.type === "qemu" ? MonitorSmartphone : Container;

  return (
    <div className="rounded border bg-card px-2.5 py-2 shadow-sm min-w-[140px] cursor-pointer">
      <Handle type="target" position={Position.Top} className="!bg-muted-foreground !w-1.5 !h-1.5" />
      <div className="flex items-center gap-1.5">
        <div
          className="h-2 w-2 rounded-full shrink-0"
          style={{ backgroundColor: statusColor }}
        />
        <Icon className="h-3.5 w-3.5 text-muted-foreground" />
        <span className="text-xs font-medium truncate">{d.label}</span>
      </div>
      <div className="mt-1 flex gap-2 text-[10px] text-muted-foreground">
        <span>{d.type === "qemu" ? "VM" : "CT"} {String(d.vmid)}</span>
        <span>{formatBytes(d.memTotal)}</span>
      </div>
      {d.haState !== "" && (
        <div className="mt-0.5 text-[10px] text-blue-500">
          HA: {d.haState}
        </div>
      )}
    </div>
  );
});
