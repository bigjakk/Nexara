import { useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { Skeleton } from "@/components/ui/skeleton";
import { PageHeader } from "@/components/page/PageHeader";
import { Panel } from "@/components/page/Panel";
import { Stat, StatStrip } from "@/components/page/StatStrip";
import { useGuestSnapshots } from "../api/snapshot-queries";
import { GuestSnapshotTable } from "../components/GuestSnapshotTable";
import {
  SnapshotFilterBar,
  type SnapshotFilters,
} from "../components/SnapshotFilterBar";
import { ageBucket, ageDays } from "../lib/age";
import type { GuestSnapshotRow } from "../types/snapshots";

function matchesFilters(
  row: GuestSnapshotRow,
  filters: SnapshotFilters,
  nowMs: number,
): boolean {
  if (filters.clusterId && row.cluster_id !== filters.clusterId) return false;
  if (filters.guestType && row.guest_type !== filters.guestType) return false;

  if (filters.age) {
    const days = ageDays(row.snap_time, nowMs);
    const bucket = ageBucket(days);
    if (filters.age === "unknown" && bucket !== "unknown") return false;
    if (filters.age === "over30" && bucket !== "month") return false;
    if (filters.age === "over7" && bucket !== "month" && bucket !== "week")
      return false;
  }

  if (filters.search) {
    const q = filters.search.toLowerCase();
    const haystack = [
      row.name,
      row.vm_name ?? "",
      String(row.vmid),
      row.description,
      row.node,
      row.cluster_name,
    ]
      .join(" ")
      .toLowerCase();
    if (!haystack.includes(q)) return false;
  }
  return true;
}

export function SnapshotsPage() {
  const { t } = useTranslation("snapshots");
  const { data, isLoading, error } = useGuestSnapshots();
  const [filters, setFilters] = useState<SnapshotFilters>({
    search: "",
    clusterId: "",
    guestType: "",
    age: "",
  });

  const rows = useMemo(() => data?.items ?? [], [data]);
  const nowMs = Date.now();

  const stats = useMemo(() => {
    const guests = new Set<string>();
    let over7 = 0;
    let over30 = 0;
    let lastSeen = 0;
    for (const row of rows) {
      guests.add(`${row.cluster_id}:${String(row.vmid)}`);
      const bucket = ageBucket(ageDays(row.snap_time, nowMs));
      if (bucket === "month") {
        over30++;
        over7++;
      } else if (bucket === "week") {
        over7++;
      }
      const seen = Date.parse(row.last_seen_at);
      if (!Number.isNaN(seen) && seen > lastSeen) lastSeen = seen;
    }
    return { total: rows.length, guests: guests.size, over7, over30, lastSeen };
  }, [rows, nowMs]);

  const clusterOptions = useMemo(() => {
    const seen = new Map<string, string>();
    for (const row of rows) {
      if (!seen.has(row.cluster_id)) seen.set(row.cluster_id, row.cluster_name);
    }
    return [...seen.entries()].map(([id, name]) => ({ id, name }));
  }, [rows]);

  const filtered = useMemo(
    () => rows.filter((row) => matchesFilters(row, filters, nowMs)),
    [rows, filters, nowMs],
  );

  const filtering =
    filters.search !== "" ||
    filters.clusterId !== "" ||
    filters.guestType !== "" ||
    filters.age !== "";

  // "34 snapshots on 12 guests, 3 older than 30 days": the oldest bucket that
  // has anything in it is the one worth saying.
  const summary =
    stats.total === 0
      ? t("subtitle")
      : [
          `${t("summary.snapshots", { count: stats.total })} ${t("summary.guests", { count: stats.guests })}`,
          ...(stats.over30 > 0
            ? [t("summary.olderThan30", { count: stats.over30 })]
            : stats.over7 > 0
              ? [t("summary.olderThan7", { count: stats.over7 })]
              : []),
        ].join(", ");

  return (
    <div className="space-y-5">
      <PageHeader
        title={t("title")}
        summary={
          <>
            {summary}
            {stats.lastSeen > 0 && (
              <span className="mt-0.5 block text-[12.5px] text-muted-foreground/75">
                {t("lastSynced", {
                  time: new Date(stats.lastSeen).toLocaleString(),
                })}
              </span>
            )}
          </>
        }
      />

      <Panel flush bodyClassName="py-4">
        <StatStrip>
          <Stat
            label={t("stats.total")}
            value={isLoading ? "—" : stats.total}
            tone={stats.total === 0 ? "quiet" : "default"}
          />
          <Stat
            label={t("stats.guests")}
            value={isLoading ? "—" : stats.guests}
            tone={stats.guests === 0 ? "quiet" : "default"}
          />
          <Stat
            label={t("stats.olderThan7")}
            value={isLoading ? "—" : stats.over7}
            tone={stats.over7 > 0 ? "warn" : "quiet"}
          />
          <Stat
            label={t("stats.olderThan30")}
            value={isLoading ? "—" : stats.over30}
            tone={stats.over30 > 0 ? "bad" : "quiet"}
          />
        </StatStrip>
      </Panel>

      <Panel
        flush
        title={t("table.title")}
        aside={
          filtering && rows.length > 0
            ? t("table.matching", {
                shown: filtered.length,
                total: rows.length,
              })
            : undefined
        }
        actions={
          <SnapshotFilterBar
            filters={filters}
            onChange={setFilters}
            clusterOptions={clusterOptions}
          />
        }
      >
        {isLoading ? (
          <div className="space-y-2 px-[18px] pb-1">
            <Skeleton className="h-10 w-full" />
            <Skeleton className="h-10 w-full" />
            <Skeleton className="h-10 w-full" />
          </div>
        ) : error ? (
          <p className="px-[18px] py-8 text-center text-sm text-destructive">
            {t("loadError")}
          </p>
        ) : rows.length === 0 ? (
          <p className="px-[18px] py-8 text-center text-sm text-muted-foreground">
            {t("empty")}
          </p>
        ) : (
          <GuestSnapshotTable rows={filtered} />
        )}
      </Panel>
    </div>
  );
}
