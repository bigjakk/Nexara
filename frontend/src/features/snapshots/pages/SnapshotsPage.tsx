import { useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { Camera, Layers, CalendarClock, CalendarX2 } from "lucide-react";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Skeleton } from "@/components/ui/skeleton";
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

interface StatCardProps {
  title: string;
  value: number;
  icon: React.ReactNode;
  valueClassName?: string;
}

function StatCard({ title, value, icon, valueClassName }: StatCardProps) {
  return (
    <Card>
      <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
        <CardTitle className="text-sm font-medium">{title}</CardTitle>
        {icon}
      </CardHeader>
      <CardContent>
        <div
          className={`text-2xl font-bold tracking-tight ${valueClassName ?? ""}`}
        >
          {value}
        </div>
      </CardContent>
    </Card>
  );
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

  return (
    <div className="space-y-6">
      <div className="flex flex-wrap items-end justify-between gap-2">
        <div>
          <h1 className="text-2xl font-bold tracking-tight">{t("title")}</h1>
          <p className="text-muted-foreground">{t("subtitle")}</p>
        </div>
        {stats.lastSeen > 0 && (
          <p className="text-xs text-muted-foreground">
            {t("lastSynced", {
              time: new Date(stats.lastSeen).toLocaleString(),
            })}
          </p>
        )}
      </div>

      <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
        <StatCard
          title={t("stats.total")}
          value={stats.total}
          icon={<Camera className="h-4 w-4 text-muted-foreground" />}
        />
        <StatCard
          title={t("stats.guests")}
          value={stats.guests}
          icon={<Layers className="h-4 w-4 text-muted-foreground" />}
        />
        <StatCard
          title={t("stats.olderThan7")}
          value={stats.over7}
          icon={<CalendarClock className="h-4 w-4 text-amber-500" />}
          valueClassName={
            stats.over7 > 0 ? "text-amber-600 dark:text-amber-400" : ""
          }
        />
        <StatCard
          title={t("stats.olderThan30")}
          value={stats.over30}
          icon={<CalendarX2 className="h-4 w-4 text-red-500" />}
          valueClassName={
            stats.over30 > 0 ? "text-red-600 dark:text-red-400" : ""
          }
        />
      </div>

      <SnapshotFilterBar
        filters={filters}
        onChange={setFilters}
        clusterOptions={clusterOptions}
      />

      {isLoading ? (
        <div className="space-y-2">
          <Skeleton className="h-10 w-full" />
          <Skeleton className="h-10 w-full" />
          <Skeleton className="h-10 w-full" />
        </div>
      ) : error ? (
        <p className="py-8 text-center text-sm text-destructive">
          {t("loadError")}
        </p>
      ) : rows.length === 0 ? (
        <p className="py-8 text-center text-sm text-muted-foreground">
          {t("empty")}
        </p>
      ) : (
        <GuestSnapshotTable rows={filtered} />
      )}
    </div>
  );
}
