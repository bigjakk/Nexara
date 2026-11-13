import { memo } from "react";
import { Position } from "@xyflow/react";
import { AlertTriangle } from "lucide-react";
import type { NodeProps } from "@xyflow/react";
import { StatusIcon } from "@/components/StatusIcon";
import { GuestCells } from "@/features/dashboard/components/GuestCell";
import { loadLevel, type FleetGuest } from "@/features/dashboard/lib/fleet";
import { formatBytes } from "../lib/topology-transform";
import type { HostNodeData } from "../lib/topology-transform";
import { LEAF_HEIGHT } from "../lib/topology-layout";
import { TopologyHandle } from "./TopologyHandle";
import { UtilRing } from "./UtilRing";

/** Two rows of cells, so every host card is about the height the layout allows it. */
const MAX_CELLS = 22;

export const HostNode = memo(function HostNode({
  data,
  sourcePosition,
  targetPosition,
}: NodeProps) {
  const d = data as HostNodeData;
  const running = d.guests.filter((g) => g.status === "running").length;
  // Node versions arrive as "pve-manager/9.2.3/<hash>" — show just the number.
  const version = d.pveVersion.replace(/^pve-manager\/([^/]+).*$/, "$1");
  // The Fleet board's cells: a square is a VM and a circle a container,
  // shaded by its CPU while it runs and hollow when it is stopped.
  const cells = d.guests.slice(0, MAX_CELLS).map((g): FleetGuest => {
    const isRunning = g.status === "running";
    const cpuPercent = isRunning ? (d.guestCpu?.[g.vmId] ?? null) : null;
    const kind = g.type === "qemu" ? "vm" : "ct";
    return {
      id: g.vmId,
      vmid: g.vmid,
      name: g.name,
      kind,
      status: g.status,
      running: isRunning,
      cpuPercent,
      load: isRunning ? loadLevel(cpuPercent) : null,
      href: `/inventory/${kind}/${d.clusterId}/${g.vmId}`,
    };
  });

  return (
    <div className="w-[200px] cursor-pointer rounded-[10px] bg-card px-3 py-2.5 text-card-foreground">
      <TopologyHandle type="target" position={targetPosition ?? Position.Top} />
      {/* Where the power overlay's edges land: off the centre, so they do not
          run into the cluster's edge. */}
      <TopologyHandle
        id="power"
        type="target"
        position={Position.Top}
        style={{ left: "25%" }}
      />
      <div className="flex items-center gap-2">
        <StatusIcon status={d.status} />
        <span
          className="min-w-0 truncate text-sm font-semibold tracking-tight"
          title={d.label}
        >
          {d.label}
        </span>
        {version && (
          <span className="ml-auto shrink-0 text-[11px] text-muted-foreground">
            PVE {version}
          </span>
        )}
      </div>
      <div className="mt-0.5 text-[11px] text-muted-foreground">
        {d.cpuCount} vCPU, {formatBytes(d.memTotal)}
      </div>

      {d.noUpsmon !== undefined && d.noUpsmon.length > 0 && (
        <div
          className="mt-1.5 flex items-center gap-1 rounded-md bg-amber-500/15 px-1.5 py-0.5 text-[11px] font-semibold text-amber-700 dark:text-amber-400"
          title={`No upsmon on ${d.label} is logged in to ${d.noUpsmon.join(", ")}, so it would lose power without shutting down`}
        >
          <AlertTriangle className="h-3 w-3 shrink-0" aria-hidden="true" />
          No upsmon
        </div>
      )}

      <div className="mt-2.5 flex items-start justify-center gap-7">
        <UtilRing percent={d.cpuPercent ?? null} label="CPU" />
        <UtilRing percent={d.memPercent ?? null} label="Memory" />
      </div>

      {cells.length > 0 && (
        // A cell opens its guest: the click must not also open the host.
        <div
          className="nodrag mt-2.5"
          onClick={(e) => {
            e.stopPropagation();
          }}
        >
          <GuestCells
            guests={cells}
            className="gap-[3px]! [&>a]:h-3! [&>a]:w-3!"
          />
        </div>
      )}

      <div className="mt-1.5 flex items-center text-[11px] text-muted-foreground">
        <span>
          {d.guests.length === 0
            ? "No guests"
            : `${String(running)} of ${String(d.guests.length)} running`}
        </span>
        {d.guests.length > MAX_CELLS && (
          <span className="ml-auto pl-2">
            +{d.guests.length - MAX_CELLS} more
          </span>
        )}
      </div>
      {/* Its chips are stacked to its right, level with its head, so the
          edge into the first runs straight. */}
      <TopologyHandle
        type="source"
        position={sourcePosition ?? Position.Right}
        style={{ top: LEAF_HEIGHT / 2 }}
      />
    </div>
  );
});
