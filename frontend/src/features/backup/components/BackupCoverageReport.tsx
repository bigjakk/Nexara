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
import { ShieldCheck, ShieldAlert, ShieldOff, Search } from "lucide-react";
import { Skeleton } from "@/components/ui/skeleton";
import { useBackupCoverage } from "../api/backup-queries";
import type { BackupCoverageEntry } from "../types/backup";

function formatBackupAge(unixTs: number | null): string {
  if (unixTs == null) return "Never";
  const ageSec = Math.floor(Date.now() / 1000) - unixTs;
  if (ageSec < 3600) return `${Math.floor(ageSec / 60)}m ago`;
  if (ageSec < 86400) return `${Math.floor(ageSec / 3600)}h ago`;
  return `${Math.floor(ageSec / 86400)}d ago`;
}

function CoverageBadge({ status }: { status: BackupCoverageEntry["coverage_status"] }) {
  switch (status) {
    case "recent":
      return (
        <Badge variant="default" className="gap-1 bg-green-600">
          <ShieldCheck className="h-3 w-3" />
          Protected
        </Badge>
      );
    case "stale":
      return (
        <Badge variant="default" className="gap-1 bg-yellow-600">
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
  }
}

export function BackupCoverageReport() {
  const { data: entries, isLoading } = useBackupCoverage();
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
    if (!entries) return { total: 0, recent: 0, stale: 0, none: 0 };
    return {
      total: entries.length,
      recent: entries.filter((e) => e.coverage_status === "recent").length,
      stale: entries.filter((e) => e.coverage_status === "stale").length,
      none: entries.filter((e) => e.coverage_status === "none").length,
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

  return (
    <div className="space-y-4">
      <div className="grid gap-4 md:grid-cols-4">
        <Card>
          <CardHeader className="pb-1">
            <CardTitle className="text-xs text-muted-foreground">Total VMs</CardTitle>
          </CardHeader>
          <CardContent>
            <p className="text-2xl font-bold">{stats.total}</p>
          </CardContent>
        </Card>
        <Card>
          <CardHeader className="pb-1">
            <CardTitle className="text-xs text-green-600">Protected (&lt;24h)</CardTitle>
          </CardHeader>
          <CardContent>
            <p className="text-2xl font-bold text-green-600">{stats.recent}</p>
          </CardContent>
        </Card>
        <Card>
          <CardHeader className="pb-1">
            <CardTitle className="text-xs text-yellow-600">Stale (&gt;24h)</CardTitle>
          </CardHeader>
          <CardContent>
            <p className="text-2xl font-bold text-yellow-600">{stats.stale}</p>
          </CardContent>
        </Card>
        <Card>
          <CardHeader className="pb-1">
            <CardTitle className="text-xs text-destructive">No Backup</CardTitle>
          </CardHeader>
          <CardContent>
            <p className="text-2xl font-bold text-destructive">{stats.none}</p>
          </CardContent>
        </Card>
      </div>

      <div className="flex flex-wrap items-center gap-2">
        <div className="relative flex-1">
          <Search className="absolute left-2.5 top-2.5 h-4 w-4 text-muted-foreground" />
          <Input
            placeholder="Search VMs..."
            value={search}
            onChange={(e) => { setSearch(e.target.value); }}
            className="pl-8"
          />
        </div>
        <select
          className="rounded-md border bg-background px-3 py-2 text-sm"
          value={filterStatus}
          onChange={(e) => { setFilterStatus(e.target.value); }}
        >
          <option value="all">All</option>
          <option value="recent">Protected</option>
          <option value="stale">Stale</option>
          <option value="none">No Backup</option>
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
              <TableHead>Last Backup</TableHead>
              <TableHead className="text-right">Backups</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {filtered.length === 0 && (
              <TableRow>
                <TableCell colSpan={8} className="py-8 text-center text-sm text-muted-foreground">
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
                      ? "bg-yellow-500/5"
                      : ""
                }
              >
                <TableCell className="font-mono text-xs">{entry.vmid}</TableCell>
                <TableCell className="font-medium">{entry.name || `VM ${String(entry.vmid)}`}</TableCell>
                <TableCell>
                  <Badge variant="outline" className="text-xs">
                    {entry.type === "qemu" ? "VM" : "CT"}
                  </Badge>
                </TableCell>
                <TableCell className="text-sm">{entry.cluster_name}</TableCell>
                <TableCell>
                  <Badge
                    variant={entry.status === "running" ? "default" : "secondary"}
                    className={entry.status === "running" ? "bg-green-600" : ""}
                  >
                    {entry.status}
                  </Badge>
                </TableCell>
                <TableCell>
                  <CoverageBadge status={entry.coverage_status} />
                </TableCell>
                <TableCell className="text-sm">
                  {formatBackupAge(entry.latest_backup)}
                </TableCell>
                <TableCell className="text-right font-mono text-sm">
                  {entry.backup_count}
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </div>
    </div>
  );
}
