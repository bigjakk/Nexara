import { useMemo, useState } from "react";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { Badge } from "@/components/ui/badge";
import { StatusBadge } from "@/features/inventory/components/StatusBadge";
import { Input } from "@/components/ui/input";
import { Search } from "lucide-react";
import { Skeleton } from "@/components/ui/skeleton";
import { Panel } from "@/components/page/Panel";
import { Stat, StatStrip } from "@/components/page/StatStrip";
import { useBackupCoverage } from "../api/backup-queries";
import { coverageStats } from "../lib/backup-summary";
import type { BackupCoverageEntry, BackupEligibility } from "../types/backup";
import { StateText } from "./StateText";

/**
 * "targets" is every guest the Backup targets figure counts: all of them but
 * Veeam's own, which "all" lists too.
 */
type CoverageFilter =
  "all" | "targets" | BackupCoverageEntry["coverage_status"];

function formatBackupAge(unixTs: number | null): string {
  if (unixTs == null) return "Never";
  const ageSec = Math.floor(Date.now() / 1000) - unixTs;
  if (ageSec < 3600) return `${String(Math.floor(ageSec / 60))}m ago`;
  if (ageSec < 86400) return `${String(Math.floor(ageSec / 3600))}h ago`;
  return `${String(Math.floor(ageSec / 86400))}d ago`;
}

/**
 * The freshest backup from ANY provider. Reading only latest_backup showed
 * "Never" beside a "Protected" badge for every guest Veeam protects, since
 * that field is deliberately still the PBS figure.
 */
function freshestBackup(entry: BackupCoverageEntry): number | null {
  const veeamTs = entry.veeam?.latest_restore_point
    ? Math.floor(new Date(entry.veeam.latest_restore_point).getTime() / 1000)
    : null;
  if (entry.latest_backup == null) return veeamTs;
  if (veeamTs == null) return entry.latest_backup;
  return Math.max(entry.latest_backup, veeamTs);
}

const ELIGIBILITY_LABELS: Record<BackupEligibility, string> = {
  eligible: "",
  veeam_worker: "Veeam worker appliance",
  veeam_backup_server: "Veeam backup server",
};

/**
 * Which providers protect a guest. Kept separate from the coverage badge,
 * which answers "how fresh": an operator deciding whether a guest is safe
 * needs both, and collapsing them loses the case where one provider is
 * current and the other has silently stopped.
 */
function ProtectionCell({ entry }: { entry: BackupCoverageEntry }) {
  if (entry.protection === "not_eligible") {
    return (
      <span className="whitespace-nowrap text-xs text-muted-foreground">
        {ELIGIBILITY_LABELS[entry.eligibility] || "Not a backup target"}
      </span>
    );
  }
  if (entry.protection === "none") {
    return <span className="text-xs text-muted-foreground">—</span>;
  }
  const providers = [
    ...(entry.protection === "pbs" || entry.protection === "both"
      ? ["PBS"]
      : []),
    ...(entry.protection === "veeam" || entry.protection === "both"
      ? ["Veeam"]
      : []),
  ];
  return (
    <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
      <span>{providers.join(" and ")}</span>
      {/*
        A name match is a guess: a rebuilt host reuses its name, so the
        backup behind it may be of the machine it replaced. Saying so is the
        entire reason match_method is carried through to the UI.
      */}
      {entry.veeam?.match_method === "name" && (
        <Badge
          variant="secondary"
          className="text-xs"
          title="Matched by name, not by SMBIOS UUID. Verify before relying on it."
        >
          name match
        </Badge>
      )}
    </div>
  );
}

/** How fresh a guest's newest backup is. Only a problem takes a colour. */
function CoverageState({
  status,
}: {
  status: BackupCoverageEntry["coverage_status"];
}) {
  switch (status) {
    case "recent":
      return <StateText tone="ok">Protected</StateText>;
    case "stale":
      return <StateText tone="warn">Stale</StateText>;
    case "none":
      return <StateText tone="bad">No backup</StateText>;
    case "not_eligible":
      return <StateText tone="idle">Not a target</StateText>;
  }
}

export function BackupCoverageReport() {
  const { data: entries, isLoading, isError, error } = useBackupCoverage();
  const [search, setSearch] = useState("");
  const [filterStatus, setFilterStatus] = useState<CoverageFilter>("all");

  const filtered = useMemo(() => {
    if (!entries) return [];
    let result = entries;
    if (filterStatus === "targets") {
      result = result.filter((e) => e.coverage_status !== "not_eligible");
    } else if (filterStatus !== "all") {
      result = result.filter((e) => e.coverage_status === filterStatus);
    }
    if (search) {
      const q = search.toLowerCase();
      result = result.filter(
        (e) =>
          e.name.toLowerCase().includes(q) ||
          String(e.vmid).includes(q) ||
          e.cluster_name.toLowerCase().includes(q),
      );
    }
    return result;
  }, [entries, search, filterStatus]);

  const stats = useMemo(() => coverageStats(entries ?? []), [entries]);

  if (isLoading) {
    return (
      <div className="space-y-4">
        <Skeleton className="h-24 rounded-[10px]" />
        <Skeleton className="h-64 rounded-[10px]" />
      </div>
    );
  }

  // A failed read must not render as "0 backup targets": the coverage
  // computation now fails loudly when a provider table cannot be read, and
  // an empty page would turn that into a confident all-clear.
  if (isError && !entries) {
    return (
      <Panel>
        <p className="text-sm text-destructive">
          Backup coverage could not be computed
          {error instanceof Error ? `: ${error.message}` : "."}
        </p>
      </Panel>
    );
  }

  // Each figure is also the filter for the rows below: pressing one shows
  // only those guests, pressing it again shows them all.
  const pick = (status: CoverageFilter) => () => {
    setFilterStatus(filterStatus === status ? "all" : status);
  };

  return (
    <div className="space-y-4">
      <Panel flush aria-label="Coverage summary" bodyClassName="pt-4 pb-0">
        <StatStrip>
          <Stat
            label="Backup targets"
            value={stats.total}
            sub={
              stats.notEligible > 0
                ? `${String(stats.notEligible)} excluded (Veeam infrastructure)`
                : undefined
            }
            tone={stats.total === 0 ? "quiet" : "default"}
            selected={filterStatus === "targets"}
            onSelect={pick("targets")}
          />
          <Stat
            label="Protected"
            value={stats.recent}
            sub="Backed up in the last 24 hours"
            tone={stats.recent === 0 ? "quiet" : "default"}
            selected={filterStatus === "recent"}
            onSelect={pick("recent")}
          />
          <Stat
            label="Stale"
            value={stats.stale}
            sub="Last backup over 24 hours ago"
            tone={stats.stale === 0 ? "quiet" : "warn"}
            selected={filterStatus === "stale"}
            onSelect={pick("stale")}
          />
          <Stat
            label="No backup"
            value={stats.none}
            sub="No backup from any provider"
            tone={stats.none === 0 ? "quiet" : "bad"}
            selected={filterStatus === "none"}
            onSelect={pick("none")}
          />
        </StatStrip>
      </Panel>

      <Panel
        flush
        title="Guests"
        aside={
          filtered.length === (entries?.length ?? 0)
            ? undefined
            : `${String(filtered.length)} of ${String(entries?.length ?? 0)}`
        }
        actions={
          <>
            <div className="relative">
              <Search className="pointer-events-none absolute left-2.5 top-2 h-4 w-4 text-muted-foreground" />
              <Input
                placeholder="Search VMs..."
                aria-label="Search guests"
                value={search}
                onChange={(e) => {
                  setSearch(e.target.value);
                }}
                className="h-8 w-52 pl-8"
              />
            </div>
            <select
              aria-label="Coverage"
              className="h-8 rounded-md border bg-transparent px-2 text-sm outline-hidden focus-visible:ring-2 focus-visible:ring-ring"
              value={filterStatus}
              onChange={(e) => {
                setFilterStatus(e.target.value as CoverageFilter);
              }}
            >
              <option value="all">All</option>
              <option value="targets">Backup targets</option>
              <option value="recent">Protected</option>
              <option value="stale">Stale</option>
              <option value="none">No backup</option>
              <option value="not_eligible">Not a target</option>
            </select>
          </>
        }
      >
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Guest</TableHead>
              <TableHead>Cluster</TableHead>
              <TableHead>Status</TableHead>
              <TableHead>Coverage</TableHead>
              <TableHead>Protected by</TableHead>
              <TableHead>Last backup</TableHead>
              <TableHead className="text-right">Backups</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {filtered.length === 0 && (
              <TableRow>
                <TableCell
                  colSpan={7}
                  className="py-8 text-center text-sm text-muted-foreground"
                >
                  {entries?.length === 0 ? "No VMs found." : "No matching VMs."}
                </TableCell>
              </TableRow>
            )}
            {filtered.map((entry) => (
              <TableRow
                key={`${entry.cluster_id}-${String(entry.vmid)}`}
                className={
                  entry.coverage_status === "not_eligible"
                    ? "text-muted-foreground"
                    : ""
                }
              >
                <TableCell className="whitespace-nowrap">
                  <span className="font-medium">
                    {entry.name || `VM ${String(entry.vmid)}`}
                  </span>
                  <span className="ml-2 font-mono text-xs text-muted-foreground">
                    {entry.type === "qemu" ? "VM" : "CT"} {entry.vmid}
                  </span>
                </TableCell>
                <TableCell>{entry.cluster_name}</TableCell>
                <TableCell>
                  <StatusBadge status={entry.status} />
                </TableCell>
                <TableCell>
                  <CoverageState status={entry.coverage_status} />
                </TableCell>
                <TableCell>
                  <ProtectionCell entry={entry} />
                </TableCell>
                <TableCell className="whitespace-nowrap">
                  {formatBackupAge(freshestBackup(entry))}
                </TableCell>
                <TableCell className="text-right tabular-nums">
                  {entry.backup_count + (entry.veeam?.restore_point_count ?? 0)}
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </Panel>
    </div>
  );
}
