import { memo } from "react";
import { useNavigate } from "react-router-dom";
import { Handle, Position } from "@xyflow/react";
import type { NodeProps } from "@xyflow/react";
import { getStatusColor, getGuestStatusColor, formatBytes } from "../lib/topology-transform";
import type { HostNodeData, HostGuest } from "../lib/topology-transform";
import { UtilRing } from "./UtilRing";

const MAX_SQUARES = 24;

function GuestSquare({
  guest,
  clusterId,
}: {
  guest: HostGuest;
  clusterId: string;
}) {
  const navigate = useNavigate();
  return (
    <button
      title={`${String(guest.vmid)} ${guest.name} — ${guest.status}`}
      onClick={(e) => {
        e.stopPropagation();
        void navigate(
          `/inventory/${guest.type === "qemu" ? "vm" : "ct"}/${clusterId}/${guest.vmId}`,
        );
      }}
      className="h-[7px] w-[7px] rounded-[2px] transition-transform hover:scale-150"
      style={{ backgroundColor: getGuestStatusColor(guest.status) }}
    />
  );
}

export const HostNode = memo(function HostNode({ data }: NodeProps) {
  const d = data as HostNodeData;
  const statusColor = getStatusColor(d.status);
  const running = d.guests.filter((g) => g.status === "running").length;
  // Node versions arrive as "pve-manager/9.2.3/<hash>" — show just the number.
  const version = d.pveVersion.replace(/^pve-manager\/([^/]+).*$/, "$1");

  return (
    <div className="min-w-[200px] cursor-pointer rounded-xl border bg-card px-3 py-2.5 shadow-sm transition-colors hover:border-muted-foreground/30 dark:shadow-[inset_0_1px_0_0_rgba(255,255,255,0.04)]">
      <Handle type="target" position={Position.Top} className="!h-2 !w-2 !bg-muted-foreground" />
      <div className="flex items-center gap-2">
        <div
          className="h-2 w-2 shrink-0 rounded-full"
          style={{
            backgroundColor: statusColor,
            boxShadow: d.status === "online" ? `0 0 6px ${statusColor}` : undefined,
          }}
        />
        <span className="truncate text-sm font-semibold tracking-tight">{d.label}</span>
        <span className="ml-auto shrink-0 text-[10px] text-muted-foreground">
          {d.cpuCount} vCPU · {formatBytes(d.memTotal)}
        </span>
      </div>

      <div className="mt-2 flex items-start justify-center gap-5">
        <UtilRing percent={d.cpuPercent ?? null} baseColor="#38bdf8" label="CPU" />
        <UtilRing percent={d.memPercent ?? null} baseColor="#a78bfa" label="Mem" />
      </div>

      {d.guests.length > 0 && (
        <div className="mt-2 flex flex-wrap items-center gap-[3px]">
          {d.guests.slice(0, MAX_SQUARES).map((g) => (
            <GuestSquare key={g.vmId} guest={g} clusterId={d.clusterId} />
          ))}
          {d.guests.length > MAX_SQUARES && (
            <span className="text-[10px] text-muted-foreground">
              +{d.guests.length - MAX_SQUARES}
            </span>
          )}
        </div>
      )}

      <div className="mt-1.5 flex items-center text-[10px] text-muted-foreground">
        <span>
          {running}/{d.guests.length} running
        </span>
        {version && <span className="ml-auto pl-2">PVE {version}</span>}
      </div>
      <Handle type="source" position={Position.Bottom} className="!h-2 !w-2 !bg-muted-foreground" />
    </div>
  );
});
