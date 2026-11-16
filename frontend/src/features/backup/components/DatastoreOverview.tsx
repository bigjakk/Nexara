import { useMemo, useState } from "react";
import { Panel } from "@/components/page/Panel";
import { Stat, StatStrip, type StatTone } from "@/components/page/StatStrip";
import { Segmented } from "@/components/ui/segmented";
import { ClusterHistoryButton } from "@/features/history/components/ClusterHistoryButton";
import { backupHistories } from "@/features/history/lib/backup-histories";
import {
  formatBytes,
  formatDateTime,
  formatExactDate,
  formatPercent,
} from "@/lib/format";
import { usageLevel } from "@/lib/usage-level";
import {
  useDatastoreConfig,
  usePBSDatastoreMetrics,
  usePBSSnapshots,
  usePBSTasks,
  usePBSVerifyJobs,
} from "../api/backup-queries";
import {
  FORECAST_HORIZON_DAYS,
  capacityForecast,
  lastGarbageCollection,
  snapshotFacts,
  verifyFacts,
  type LastRun,
  type VerifyFacts,
} from "../lib/datastore-facts";
import type { PBSDatastoreStatus } from "../types/backup";
import { CapacityForecastView } from "./CapacityForecastChart";
import { DatastoreChart, DatastoreUsageView } from "./DatastoreChart";
import { DatastoreConfig } from "./DatastoreConfig";
import { DatastoreIOView } from "./DatastoreIOChart";
import {
  IO_TIMEFRAMES,
  USAGE_TIMEFRAMES,
  type IOTimeframe,
  type UsageTimeframe,
} from "./datastore-timeframes";
import { GCDialog } from "./GCDialog";
import { PruneDialog } from "./PruneDialog";

interface DatastoreOverviewProps {
  datastores: PBSDatastoreStatus[];
  /** The server they are on. */
  pbsId: string;
}

/**
 * A server's datastores, one panel each: how full it is and what looks after
 * it, its charts, how it is configured. With several, a chart comparing them
 * comes first.
 */
export function DatastoreOverview({
  datastores,
  pbsId,
}: DatastoreOverviewProps) {
  if (datastores.length === 0) {
    return (
      <Panel>
        <p className="text-sm text-muted-foreground">No datastores found.</p>
      </Panel>
    );
  }

  return (
    <div className="space-y-4">
      {datastores.length > 1 && <DatastoreChart pbsId={pbsId} />}
      {datastores.map((ds) => (
        <DatastorePanel key={ds.store} pbsId={pbsId} datastore={ds} />
      ))}
    </div>
  );
}

/** Which chart the panel is drawing. */
type DatastoreView = "usage" | "forecast" | "io";

const VIEW_OPTIONS = [
  { value: "usage", label: "Usage" },
  { value: "forecast", label: "Forecast" },
  { value: "io", label: "I/O" },
] as const;

function ago(unixSeconds: number): string {
  return formatDateTime(unixSeconds * 1000, "—", "relative");
}

function gcSub(last: LastRun | null): string | undefined {
  if (last === null) return undefined;
  switch (last.outcome) {
    case "running":
      return "Running now";
    case "ok":
      return `Ran ${ago(last.at)}`;
    case "warning":
      return `Warnings ${ago(last.at)}`;
    case "failed":
      return `Failed ${ago(last.at)}`;
  }
}

function verifySub(facts: VerifyFacts, verifyNew: boolean): string {
  if (facts.jobs === 0) {
    return verifyNew ? "New backups are verified" : "No verify job";
  }
  const jobs = facts.jobs > 1 ? `${String(facts.jobs)} jobs. ` : "";
  switch (facts.tone) {
    case "ok":
      return `${jobs}Last run OK`;
    case "warn":
      return `${jobs}Last run had warnings`;
    case "bad":
      return `${jobs}Last run failed`;
    case "idle":
      return `${jobs}No result yet`;
  }
}

function DatastorePanel({
  pbsId,
  datastore: ds,
}: {
  pbsId: string;
  datastore: PBSDatastoreStatus;
}) {
  const [view, setView] = useState<DatastoreView>("usage");
  const [usageWindow, setUsageWindow] = useState<UsageTimeframe>("24h");
  const [ioWindow, setIoWindow] = useState<IOTimeframe>("hour");

  // The lists the page reads for its tabs, shared through the query cache.
  const { data: snapshots } = usePBSSnapshots(pbsId);
  const { data: tasks } = usePBSTasks(pbsId);
  const { data: verifyJobs } = usePBSVerifyJobs(pbsId);
  const { data: config } = useDatastoreConfig(pbsId, ds.store);
  const week = usePBSDatastoreMetrics(pbsId, "7d");
  const usageWeek = { isLoading: week.isLoading, isError: week.isError };

  const forecast = useMemo(
    () => capacityForecast(week.data ?? [], ds.store),
    [week.data, ds.store],
  );

  const usedPct = ds.total > 0 ? (ds.used / ds.total) * 100 : null;
  const level = usageLevel(usedPct);
  const snaps = snapshotFacts(snapshots ?? [], ds.store);
  const lastGC = lastGarbageCollection(tasks ?? [], ds.store);
  const verify = verifyFacts(verifyJobs ?? [], ds.store);
  const verifyNew = config?.["verify-new"] === true;
  // Not known yet, or not readable: the jobs have not been read, or there are
  // none and the config that could still say "on arrival" has not been either.
  // A dash then, as the figures beside it show, never a confident "None".
  const verifyUnknown =
    verifyJobs === undefined || (verify.jobs === 0 && config === undefined);

  const gcSchedule = config?.["gc-schedule"] ?? "";
  const gcTone: StatTone =
    lastGC?.outcome === "failed"
      ? "bad"
      : lastGC?.outcome === "warning"
        ? "warn"
        : gcSchedule === ""
          ? "quiet"
          : "default";
  const verifyTone: StatTone =
    verify.tone === "bad"
      ? "bad"
      : verify.tone === "warn"
        ? "warn"
        : verify.jobs === 0
          ? "quiet"
          : "default";

  return (
    <Panel
      flush
      title={ds.store}
      aside={
        config?.["maintenance-mode"] ? (
          <span className="text-amber-700 dark:text-amber-400">
            Maintenance: {config["maintenance-mode"]}
          </span>
        ) : undefined
      }
      actions={
        <>
          <ClusterHistoryButton
            history={backupHistories.pbsServer(pbsId, ds.store)}
            title={ds.store}
          />
          <GCDialog pbsId={pbsId} store={ds.store} />
          <PruneDialog pbsId={pbsId} store={ds.store} />
        </>
      }
    >
      <StatStrip className="pb-4">
        <Stat
          label="Used"
          value={formatBytes(ds.used)}
          sub={
            usedPct === null
              ? undefined
              : `${formatPercent(usedPct)} of ${formatBytes(ds.total)}`
          }
          percent={usedPct}
          tone={level === "err" ? "bad" : level === "warn" ? "warn" : "default"}
        />
        <Stat
          label="Free"
          value={formatBytes(ds.avail)}
          sub={
            forecast.fullDate
              ? `Full around ${formatExactDate(forecast.fullDate)}`
              : forecast.points.length > 0
                ? `Not full within ${String(FORECAST_HORIZON_DAYS)} days`
                : undefined
          }
          tone={forecast.fullDate ? "warn" : "default"}
        />
        <Stat
          label="Snapshots"
          value={snapshots === undefined ? "—" : String(snaps.count)}
          sub={
            snaps.count > 0
              ? `${String(snaps.guests)} ${snaps.guests === 1 ? "guest" : "guests"}, ${String(snaps.verified)} verified`
              : undefined
          }
          tone={
            snapshots !== undefined && snaps.count === 0 ? "quiet" : "default"
          }
        />
        <Stat
          label="Garbage collection"
          value={
            config === undefined
              ? "—"
              : gcSchedule !== ""
                ? gcSchedule
                : "Manual"
          }
          sub={gcSub(lastGC)}
          tone={gcTone}
        />
        <Stat
          label="Verification"
          value={
            verifyUnknown
              ? "—"
              : verify.schedule !== ""
                ? verify.schedule
                : verify.jobs > 0
                  ? "Manual"
                  : verifyNew
                    ? "On arrival"
                    : "None"
          }
          sub={verifyUnknown ? undefined : verifySub(verify, verifyNew)}
          tone={verifyUnknown ? "default" : verifyTone}
        />
      </StatStrip>

      {/* The chart and the configuration side by side, folding to a stack.
          Each wears a rule on its top and left edge, and the clipped
          negative margin hides whichever of them lies on the outside. */}
      <div className="overflow-hidden border-t">
        <div className="-ml-px -mt-px flex flex-wrap">
          <div className="min-w-0 flex-[999_1_460px] border-l border-t px-[18px] pb-2 pt-4">
            <div className="flex flex-wrap items-center gap-2 pb-4">
              <Segmented
                aria-label={`Chart for ${ds.store}`}
                value={view}
                onValueChange={setView}
                options={VIEW_OPTIONS}
              />
              <div className="ml-auto">
                {view === "usage" && (
                  <Segmented
                    aria-label="Time range"
                    value={usageWindow}
                    onValueChange={setUsageWindow}
                    options={USAGE_TIMEFRAMES.map((tf) => ({
                      value: tf,
                      label: tf,
                    }))}
                  />
                )}
                {view === "io" && (
                  <Segmented
                    aria-label="Time range"
                    value={ioWindow}
                    onValueChange={setIoWindow}
                    options={IO_TIMEFRAMES}
                  />
                )}
              </div>
            </div>
            {view === "usage" && (
              <DatastoreUsageView
                pbsId={pbsId}
                store={ds.store}
                timeframe={usageWindow}
              />
            )}
            {view === "forecast" && (
              <CapacityForecastView forecast={forecast} query={usageWeek} />
            )}
            {view === "io" && (
              <DatastoreIOView
                pbsId={pbsId}
                store={ds.store}
                timeframe={ioWindow}
              />
            )}
          </div>
          <div className="min-w-0 flex-[1_1_280px] border-l border-t px-[18px] pb-2 pt-4">
            <h3 className="pb-3 text-[13.5px] font-semibold">Configuration</h3>
            <DatastoreConfig pbsId={pbsId} store={ds.store} />
          </div>
        </div>
      </div>
    </Panel>
  );
}
