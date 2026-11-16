import { memo } from "react";
import { Position } from "@xyflow/react";
import type { NodeProps } from "@xyflow/react";
import { useTranslation } from "react-i18next";
import { StatusIcon } from "@/components/StatusIcon";
import { isHaltedState } from "@/lib/guest-state";
import { formatBytes } from "../lib/topology-transform";
import type { GuestNodeData } from "../lib/topology-transform";
import { TopologyHandle } from "./TopologyHandle";

/** A guest as a chip the same size as a store's, so the two stack together. */
export const GuestNode = memo(function GuestNode({
  data,
  targetPosition,
}: NodeProps) {
  const d = data as GuestNodeData;
  const { t } = useTranslation("vms");
  // d.status is guestState(): a halted VM's state, in the words Inventory uses.
  const state = isHaltedState(d.status) ? t(`runState.${d.status}`) : d.status;

  return (
    <div className="flex h-[54px] w-[172px] cursor-pointer flex-col justify-center rounded-lg bg-card px-2.5 text-card-foreground">
      <TopologyHandle type="target" position={targetPosition ?? Position.Top} />
      <div className="flex items-center gap-1.5">
        <StatusIcon status={d.status} />
        <span
          className="min-w-0 truncate text-[12.5px] font-medium"
          title={d.label}
        >
          {d.label}
        </span>
      </div>
      <div className="mt-1 truncate text-[10.5px] leading-none text-muted-foreground">
        {d.type === "qemu" ? "VM" : "CT"} {String(d.vmid)}, {state},{" "}
        {formatBytes(d.memTotal)}
        {d.haState !== "" && `, HA ${d.haState}`}
      </div>
    </div>
  );
});
