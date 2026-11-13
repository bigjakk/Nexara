import { memo } from "react";
import { StatusIcon } from "@/components/StatusIcon";
import { GuestCellSwatch } from "@/features/dashboard/components/GuestCell";
import { DATA_COLOR } from "@/lib/chart-colors";
import { cn } from "@/lib/utils";
import { TONE_COLOR } from "../lib/topology-power";

const STATES = [
  { status: "online", label: "Online" },
  { status: "degraded", label: "Degraded or paused" },
  { status: "offline", label: "Offline" },
  { status: "stopped", label: "Stopped" },
] as const;

function Line({
  label,
  color,
  dash,
}: {
  label: string;
  color: string;
  dash?: string;
}) {
  return (
    <span className="flex items-center gap-1.5">
      <svg width="18" height="4" aria-hidden="true">
        <line
          x1="0"
          y1="2"
          x2="18"
          y2="2"
          style={{ stroke: color }}
          strokeWidth="1.5"
          strokeDasharray={dash}
        />
      </svg>
      {label}
    </span>
  );
}

/**
 * How to read the map: the state dots, the guest cells, and the lines that
 * are only there some of the time (a migration, the power overlay).
 */
export const TopologyLegend = memo(function TopologyLegend({
  power = false,
  migrating = false,
  className,
}: {
  power?: boolean;
  migrating?: boolean;
  className?: string;
}) {
  return (
    <div
      className={cn(
        "flex flex-wrap items-center gap-x-4 gap-y-1 rounded-[10px] bg-card px-3 py-1.5 text-xs text-muted-foreground ring-1 ring-border",
        className,
      )}
    >
      {STATES.map((s) => (
        // The dot is decoration beside its own label here.
        <span key={s.status} className="flex items-center gap-1.5">
          <span aria-hidden="true" className="flex">
            <StatusIcon status={s.status} />
          </span>
          {s.label}
        </span>
      ))}
      <span className="flex items-center gap-1.5">
        Guest CPU: idle
        <span aria-hidden="true" className="flex gap-0.5">
          {([1, 2, 3, 4] as const).map((level) => (
            <GuestCellSwatch key={level} load={level} />
          ))}
        </span>
        busy
      </span>
      <span className="flex items-center gap-1.5">
        <span aria-hidden="true" className="flex">
          <GuestCellSwatch load={2} container />
        </span>
        container
      </span>
      {migrating && <Line label="Migrating" color={DATA_COLOR} dash="4 4" />}
      {power && (
        <>
          <Line label="On mains" color={TONE_COLOR.ok} />
          <Line label="On battery" color={TONE_COLOR.warn} dash="6 4" />
        </>
      )}
    </div>
  );
});
