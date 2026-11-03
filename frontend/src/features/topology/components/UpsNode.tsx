import { memo } from "react";
import { Handle, Position } from "@xyflow/react";
import type { NodeProps } from "@xyflow/react";
import { BatteryCharging } from "lucide-react";
import { formatRuntime } from "@/features/power/lib/ups-state";
import { TONE_COLOR } from "../lib/topology-power";
import type { UpsNodeData } from "../lib/topology-transform";

/** A UPS on the power overlay, in a lane beside the hosts it powers. */
export const UpsNode = memo(function UpsNode({ data }: NodeProps) {
  const d = data as UpsNodeData;
  const color = TONE_COLOR[d.tone];

  return (
    <div className="w-[220px] cursor-pointer rounded-xl border bg-card px-3 py-2 shadow-xs transition-colors hover:border-muted-foreground/30 dark:shadow-[inset_0_1px_0_0_rgba(255,255,255,0.04)]">
      <div className="flex items-center gap-2">
        <BatteryCharging className="h-3.5 w-3.5 shrink-0 text-muted-foreground" aria-hidden="true" />
        <span className="truncate text-sm font-semibold tracking-tight">{d.label}</span>
        <span className="ml-auto shrink-0 text-[10px] font-medium" style={{ color }}>
          {d.status}
        </span>
      </div>
      <div className="mt-1 flex items-center text-[10px] text-muted-foreground">
        <span className="tabular-nums">
          {d.batteryCharge == null ? "—" : `${String(Math.round(d.batteryCharge))}%`} · {formatRuntime(d.batteryRuntime)}
        </span>
        <span className="ml-auto truncate pl-2">via {d.serverName}</span>
      </div>
      <Handle
        type="source"
        position={d.direction === "LR" ? Position.Right : Position.Bottom}
        className="h-2! w-2! bg-muted-foreground!"
      />
    </div>
  );
});
