import { memo } from "react";
import { Position } from "@xyflow/react";
import type { NodeProps } from "@xyflow/react";
import { BatteryCharging } from "lucide-react";
import { formatRuntime } from "@/features/power/lib/ups-state";
import type { UpsTone } from "@/features/power/lib/ups-state";
import { cn } from "@/lib/utils";
import type { UpsNodeData } from "../lib/topology-transform";
import { TopologyHandle } from "./TopologyHandle";

/** Healthy is plain text with a small dot; only trouble takes colour. */
const TONE_TEXT: Record<UpsTone, string> = {
  ok: "text-muted-foreground",
  warn: "font-semibold text-amber-700 dark:text-amber-400",
  err: "font-semibold text-red-700 dark:text-red-400",
  idle: "text-muted-foreground",
};

/** A UPS on the power overlay, in a lane beside the hosts it powers. */
export const UpsNode = memo(function UpsNode({ data }: NodeProps) {
  const d = data as UpsNodeData;

  return (
    <div className="flex h-16 w-[220px] cursor-pointer flex-col justify-center rounded-[10px] bg-card px-3 text-card-foreground">
      <div className="flex items-center gap-2">
        <BatteryCharging
          className="h-3.5 w-3.5 shrink-0 text-muted-foreground"
          aria-hidden="true"
        />
        <span className="truncate text-sm font-semibold tracking-tight">
          {d.label}
        </span>
        <span
          className={cn(
            "ml-auto flex shrink-0 items-center gap-1.5 text-[11px]",
            TONE_TEXT[d.tone],
          )}
        >
          {d.tone === "ok" && (
            <span
              aria-hidden="true"
              className="h-1.5 w-1.5 rounded-full bg-emerald-500"
            />
          )}
          {d.status}
        </span>
      </div>
      <div className="mt-1 flex items-center text-[11px] text-muted-foreground">
        <span>
          {d.batteryCharge == null
            ? "—"
            : `${String(Math.round(d.batteryCharge))}%`}
          , {formatRuntime(d.batteryRuntime)}
        </span>
        <span className="ml-auto truncate pl-2">via {d.serverName}</span>
      </div>
      <TopologyHandle
        type="source"
        position={d.direction === "LR" ? Position.Right : Position.Bottom}
      />
    </div>
  );
});
