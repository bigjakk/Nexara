import { memo } from "react";
import { Position } from "@xyflow/react";
import type { NodeProps } from "@xyflow/react";
import { Database } from "lucide-react";
import { UsageBar } from "@/components/UsageBar";
import { usageLevel, USAGE_TEXT_CLASS } from "@/lib/usage-level";
import { cn } from "@/lib/utils";
import { formatBytes } from "../lib/topology-transform";
import type { StorageNodeData } from "../lib/topology-transform";
import { TopologyHandle } from "./TopologyHandle";

/** A store as a chip: its name and how full it is. The layout stacks these. */
export const StorageNode = memo(function StorageNode({
  data,
  targetPosition,
}: NodeProps) {
  const d = data as StorageNodeData;
  const usedPercent = d.total > 0 ? (d.used / d.total) * 100 : null;
  const level = usageLevel(usedPercent);

  return (
    <div
      className="flex h-[54px] w-[172px] cursor-pointer flex-col justify-center rounded-lg bg-card px-2.5 text-card-foreground"
      title={`${d.label}: ${formatBytes(d.used)} of ${formatBytes(d.total)} used`}
    >
      <TopologyHandle type="target" position={targetPosition ?? Position.Top} />
      <div className="flex items-center gap-1.5">
        <Database
          aria-hidden="true"
          className="h-3 w-3 shrink-0 text-muted-foreground"
        />
        <span className="min-w-0 truncate text-[12.5px] font-medium">
          {d.label}
        </span>
        <span
          className={cn(
            "ml-auto shrink-0 text-[11px]",
            level === "ok" ? "text-muted-foreground" : USAGE_TEXT_CLASS[level],
          )}
        >
          {usedPercent === null ? "—" : `${String(Math.round(usedPercent))}%`}
        </span>
      </div>
      <UsageBar percent={usedPercent} className="mt-1 h-1" />
      <div className="mt-1 flex items-baseline gap-1 text-[10.5px] leading-none text-muted-foreground">
        <span className="min-w-0 truncate">
          {d.storageType}
          {d.shared ? ", shared" : ""}
        </span>
        {d.active ? (
          <span className="ml-auto shrink-0">{formatBytes(d.total)}</span>
        ) : (
          <span className="ml-auto shrink-0 font-semibold text-amber-700 dark:text-amber-400">
            Inactive
          </span>
        )}
      </div>
    </div>
  );
});
