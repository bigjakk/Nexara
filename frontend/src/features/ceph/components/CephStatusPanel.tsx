import { Panel } from "@/components/page/Panel";
import { Stat, StatStrip, type StatTone } from "@/components/page/StatStrip";
import { formatBytes, formatPercent } from "@/lib/format";
import { cn } from "@/lib/utils";
import {
  cephHealthLabel,
  cephSeverity,
  type CephSeverity,
} from "../lib/ceph-health";
import type { CephStatus } from "../types/ceph";
import { CephHealthChecks } from "./CephHealthChecks";

const HEALTH_TONE: Record<CephSeverity, StatTone> = {
  ok: "default",
  warn: "warn",
  err: "bad",
  unknown: "quiet",
};

const HEALTH_DOT: Record<CephSeverity, string> = {
  ok: "bg-emerald-500",
  warn: "bg-amber-500",
  err: "bg-red-500",
  unknown: "bg-muted-foreground/40",
};

/**
 * What state the Ceph cluster is in, above every view of the tab: health,
 * capacity and the daemon counts in one strip, and the health checks behind
 * a warning or an error under it.
 */
export function CephStatusPanel({ status }: { status: CephStatus }) {
  const { health, pgmap, osdmap, monmap } = status;
  const severity = cephSeverity(health.status);
  const checks = health.checks;
  const usedPercent =
    pgmap.bytes_total > 0 ? (pgmap.bytes_used / pgmap.bytes_total) * 100 : null;

  const down = osdmap.num_osds - osdmap.num_up_osds;
  const out = osdmap.num_osds - osdmap.num_in_osds;
  const osdTone: StatTone =
    down > 0 || osdmap.full
      ? "bad"
      : out > 0 || osdmap.nearfull
        ? "warn"
        : "default";
  const osdNotes = [
    `${String(osdmap.num_in_osds)} in`,
    ...(out > 0 ? [`${String(out)} out`] : []),
    ...(osdmap.full ? ["full"] : osdmap.nearfull ? ["near full"] : []),
  ];

  return (
    <Panel flush bodyClassName="py-4">
      <StatStrip>
        <Stat
          label="Health"
          tone={HEALTH_TONE[severity]}
          value={
            <span className="flex items-center gap-2">
              <span
                aria-hidden="true"
                className={cn(
                  "h-2.5 w-2.5 shrink-0 rounded-full",
                  HEALTH_DOT[severity],
                )}
              />
              <span className="truncate">{cephHealthLabel(health.status)}</span>
            </span>
          }
          sub={
            checks.length === 0
              ? "No health checks raised"
              : checks.length === 1
                ? "1 health check raised"
                : `${String(checks.length)} health checks raised`
          }
        />
        <Stat
          label="Capacity used"
          value={formatBytes(pgmap.bytes_used)}
          sub={
            usedPercent === null
              ? "No capacity reported"
              : `${formatPercent(usedPercent)} of ${formatBytes(pgmap.bytes_total)}`
          }
          percent={usedPercent}
        />
        <Stat
          label="OSDs up"
          tone={osdTone}
          value={`${String(osdmap.num_up_osds)} of ${String(osdmap.num_osds)}`}
          sub={osdNotes.join(", ")}
        />
        <Stat
          label="Monitors"
          tone={monmap.num_mons === 0 ? "quiet" : "default"}
          value={String(monmap.num_mons)}
        />
        <Stat
          label="Placement groups"
          tone={pgmap.num_pgs === 0 ? "quiet" : "default"}
          value={String(pgmap.num_pgs)}
        />
      </StatStrip>
      {checks.length > 0 && (
        <div className="mt-4 border-t px-[18px] pt-3.5">
          <h3 className="mb-2 text-[13.5px] font-semibold">Health checks</h3>
          <CephHealthChecks checks={checks} />
        </div>
      )}
    </Panel>
  );
}
