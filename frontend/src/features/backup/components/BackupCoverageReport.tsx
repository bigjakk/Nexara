import { useMemo, useState } from "react";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Input } from "@/components/ui/input";
import {
  ShieldCheck,
  ShieldAlert,
  ShieldOff,
  ShieldQuestion,
  Search,
} from "lucide-react";
import { Skeleton } from "@/components/ui/skeleton";
import { useBackupCoverage } from "../api/backup-queries";
import type { BackupCoverageEntry, BackupEligibility } from "../types/backup";

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
      <span className="text-xs text-muted-foreground">
        {ELIGIBILITY_LABELS[entry.eligibility] || "Not a backup target"}
      </span>
    );
  }
  if (entry.protection === "none") {
    return <span className="text-xs text-muted-foreground">—</span>;
  }
  return (
    <div className="flex flex-wrap items-center gap-1">
      {(entry.protection === "pbs" || entry.protection === "both") && (
        <Badge variant="outline" className="text-xs">
          PBS
        </Badge>
      )}
      {(entry.protection === "veeam" || entry.protection === "both") && (
        <Badge variant="outline" className="text-xs">
          Veeam
        </Badge>
      )}
      {/*
        A name match is a guess: a rebuilt host reuses its name, so the
        backup behind it may be of the machine it replaced. Saying so is the
        entire reason match_method is carried through to the UI.
      */}
      {entry.veeam?.match_method === "name" && (
        <Badge
          variant="secondary"
          className="text-xs"
          title="Matched by name, not by SMBIOS UUID — verify before relying on it"
        >
          name match
        </Badge>
      )}
    </div>
  );
}

function CoverageBadge({
  status,
}: {
  status: BackupCoverageEntry["coverage_status"];
}) {
  switch (status) {
    case "recent":
      return (
        <Badge variant="default" className="gap-1 bg-emerald-600">
          <ShieldCheck className="h-3 w-3" />
          Protected
        </Badge>
      );
    case "stale":
      return (
        <Badge variant="default" className="gap-1 bg-amber-600">
          <ShieldAlert className="h-3 w-3" />
          Stale
        </Badge>
      );
    case "none":
      return (
        <Badge variant="destructive" className="gap-1">
          <ShieldOff className="h-3 w-3" />
          No Backup
        </Badge>
      );
    case "not_eligible":
      return (
        <Badge variant="secondary" className="gap-1">
          <ShieldQuestion className="h-3 w-3" />
          Not a target
        </Badge>
      );
  }
}

export function BackupCoverageReport() {
  const { data: entries, isLoading, isError, error } = useBackupCoverage();
  const [search, setSearch] = useState("");
  const [filterStatus, setFilterStatus] = useState<string>("all");

  const filtered = useMemo(() => {
    if (!entries) return [];
    let result = entries;
    if (filterStatus !== "all") {
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

  const stats = useMemo(() => {
    if (!entries)
      return { total: 0, recent: 0, stale: 0, none: 0, notEligible: 0 };
    const notEligible = entries.filter(
      (e) => e.coverage_status === "not_eligible",
    ).length;
    return {
      // Backup TARGETS, not rows. Counting Veeam's own appliances here would
      // make the tiles disagree with each other and overstate the estate.
      total: entries.length - notEligible,
      recent: entries.filter((e) => e.coverage_status === "recent").length,
      stale: entries.filter((e) => e.coverage_status === "stale").length,
      none: entries.filter((e) => e.coverage_status === "none").length,
      notEligible,
    };
  }, [entries]);

  if (isLoading) {
    return (
      <div className="space-y-4">
        <div className="grid gap-4 md:grid-cols-4">
          {Array.from({ length: 4 }).map((_, i) => (
            <Skeleton key={i} className="h-20" />
          ))}
        </div>
        <Skeleton className="h-64" />
      </div>
    );
  }

  // A failed read must not render as "0 backup targets": the coverage
  // computation now fails loudly when a provider table cannot be read, and
  // an empty page would turn that into a confident all-clear.
  if (isError && !entries) {
    return (
      <div className="rounded-md border border-destructive/40 bg-destructive/5 p-4 text-sm text-destructive">
        Backup coverage could not be computed
        {error instanceof Error ? `: ${error.message}` : "."}
      </div>
    );
  }

  return (
    <div className="space-y-4">
      <div className="grid gap-4 md:grid-cols-4">
        <Card>
          <CardHeader className="pb-1">
            <CardTitle className="text-xs text-muted-foreground">
              Backup targets
            </CardTitle>
          </CardHeader>
          <CardContent>
            <p className="text-2xl font-bold tracking-tight">{stats.total}</p>
            {stats.notEligible > 0 && (
              <p className="mt-1 text-xs text-muted-foreground">
                {stats.notEligible} excluded (Veeam infrastructure)
              </p>
            )}
          </CardContent>
        </Card>
        <Card>
          <CardHeader className="pb-1">
            <CardTitle className="text-xs text-emerald-600">
              Protected (&lt;24h)
            </CardTitle>
          </CardHeader>
          <CardContent>
            <p className="text-2xl font-bold tracking-tight text-emerald-600">
              {stats.recent}
            </p>
          </CardContent>
        </Card>
        <Card>
          <CardHeader className="pb-1">
            <CardTitle className="text-xs text-amber-600">
              Stale (&gt;24h)
            </CardTitle>
          </CardHeader>
          <CardContent>
            <p className="text-2xl font-bold tracking-tight text-amber-600">
              {stats.stale}
            </p>
          </CardContent>
        </Card>
        <Card>
          <CardHeader className="pb-1">
            <CardTitle className="text-xs text-destructive">
              No Backup
            </CardTitle>
          </CardHeader>
          <CardContent>
            <p className="text-2xl font-bold tracking-tight text-destructive">
              {stats.none}
            </p>
          </CardContent>
        </Card>
      </div>

      <div className="flex flex-wrap items-center gap-2">
        <div className="relative flex-1">
          <Search className="absolute left-2.5 top-2.5 h-4 w-4 text-muted-foreground" />
          <Input
            placeholder="Search VMs..."
            value={search}
            onChange={(e) => {
              setSearch(e.target.value);
            }}
            className="pl-8"
          />
        </div>
        <select
          className="rounded-md border bg-background px-3 py-2 text-sm"
          value={filterStatus}
          onChange={(e) => {
            setFilterStatus(e.target.value);
          }}
        >
          <option value="all">All</option>
          <option value="recent">Protected</option>
          <option value="stale">Stale</option>
          <option value="none">No Backup</option>
          <option value="not_eligible">Not a target</option>
        </select>
      </div>

      <div className="rounded-md border">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>VMID</TableHead>
              <TableHead>Name</TableHead>
              <TableHead>Type</TableHead>
              <TableHead>Cluster</TableHead>
              <TableHead>Status</TableHead>
              <TableHead>Coverage</TableHead>
              <TableHead>Protected by</TableHead>
              <TableHead>Last Backup</TableHead>
              <TableHead className="text-right">Backups</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {filtered.length === 0 && (
              <TableRow>
                <TableCell
                  colSpan={9}
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
                  entry.coverage_status === "none"
                    ? "bg-destructive/5"
                    : entry.coverage_status === "stale"
                      ? "bg-amber-500/5"
                      : entry.coverage_status === "not_eligible"
                        ? "text-muted-foreground"
                        : ""
                }
              >
                <TableCell className="font-mono text-xs">
                  {entry.vmid}
                </TableCell>
                <TableCell className="font-medium">
                  {entry.name || `VM ${String(entry.vmid)}`}
                </TableCell>
                <TableCell>
                  <Badge variant="outline" className="text-xs">
                    {entry.type === "qemu" ? "VM" : "CT"}
                  </Badge>
                </TableCell>
                <TableCell className="text-sm">{entry.cluster_name}</TableCell>
                <TableCell>
                  <Badge
                    variant={
                      entry.status === "running" ? "default" : "secondary"
                    }
                    className={
                      entry.status === "running" ? "bg-emerald-600" : ""
                    }
                  >
                    {entry.status}
                  </Badge>
                </TableCell>
                <TableCell>
                  <CoverageBadge status={entry.coverage_status} />
                </TableCell>
                <TableCell>
                  <ProtectionCell entry={entry} />
                </TableCell>
                <TableCell className="text-sm">
                  {formatBackupAge(freshestBackup(entry))}
                </TableCell>
                <TableCell className="text-right font-mono text-sm">
                  {entry.backup_count + (entry.veeam?.restore_point_count ?? 0)}
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </div>
    </div>
  );
}
