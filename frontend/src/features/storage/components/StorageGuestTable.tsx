import { useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { ArrowRightLeft, Boxes, Server } from "lucide-react";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Badge } from "@/components/ui/badge";
import {
  ContextMenu,
  ContextMenuContent,
  ContextMenuItem,
  ContextMenuTrigger,
} from "@/components/ui/context-menu";
import { apiClient } from "@/lib/api-client";
import { formatBytes } from "@/lib/format";
import type { VMResponse } from "@/types/api";
import type { StorageContentItem } from "../types/storage";
import { MigrateBatchDialog, type MigrateBatchJob } from "./MigrateBatchDialog";

interface StorageGuestTableProps {
  /** All `images` + `rootdir` items on the pool. */
  items: StorageContentItem[];
  clusterId: string;
  migrateTargets: string[];
}

interface GuestRow {
  vmid: number;
  guestId: string;
  guestKind: "vm" | "ct";
  name: string;
  volumes: StorageContentItem[];
  totalBytes: number;
}

export function StorageGuestTable({
  items,
  clusterId,
  migrateTargets,
}: StorageGuestTableProps) {
  const [selectedVmids, setSelectedVmids] = useState<Set<number>>(new Set());
  const [migrating, setMigrating] = useState<MigrateBatchJob[] | null>(null);
  const [migrateTitle, setMigrateTitle] = useState("");

  const vmsQuery = useQuery({
    queryKey: ["clusters", clusterId, "vms"],
    queryFn: () =>
      apiClient.get<VMResponse[]>(`/api/v1/clusters/${clusterId}/vms`),
    enabled: clusterId.length > 0,
    staleTime: 30_000,
  });
  const ctsQuery = useQuery({
    queryKey: ["clusters", clusterId, "containers"],
    queryFn: () =>
      apiClient.get<VMResponse[]>(`/api/v1/clusters/${clusterId}/containers`),
    enabled: clusterId.length > 0,
    staleTime: 30_000,
  });

  const rows = useMemo<GuestRow[]>(() => {
    const byVmidVm = new Map<number, VMResponse>();
    for (const v of vmsQuery.data ?? []) byVmidVm.set(v.vmid, v);
    const byVmidCT = new Map<number, VMResponse>();
    for (const c of ctsQuery.data ?? []) byVmidCT.set(c.vmid, c);

    // Group items by (vmid, kind). Pick kind from item.content (images→vm,
    // rootdir→ct) since that matches Proxmox's storage content tagging.
    const groups = new Map<string, GuestRow>();
    for (const item of items) {
      if (item.vmid == null) continue;
      const kind: "vm" | "ct" = item.content === "rootdir" ? "ct" : "vm";
      const key = `${kind}:${String(item.vmid)}`;
      const existing = groups.get(key);
      if (existing) {
        existing.volumes.push(item);
        existing.totalBytes += item.size;
        continue;
      }
      const meta = kind === "ct" ? byVmidCT.get(item.vmid) : byVmidVm.get(item.vmid);
      groups.set(key, {
        vmid: item.vmid,
        guestId: meta?.id ?? "",
        guestKind: kind,
        name: meta?.name ?? `(unknown ${kind === "ct" ? "container" : "VM"})`,
        volumes: [item],
        totalBytes: item.size,
      });
    }

    return Array.from(groups.values()).sort((a, b) => a.vmid - b.vmid);
  }, [items, vmsQuery.data, ctsQuery.data]);

  const allSelected = rows.length > 0 && selectedVmids.size === rows.length;
  const someSelected = selectedVmids.size > 0 && selectedVmids.size < rows.length;

  function toggleAll() {
    if (allSelected) setSelectedVmids(new Set());
    else setSelectedVmids(new Set(rows.map((r) => r.vmid)));
  }

  function toggleRow(vmid: number) {
    setSelectedVmids((prev) => {
      const next = new Set(prev);
      if (next.has(vmid)) next.delete(vmid);
      else next.add(vmid);
      return next;
    });
  }

  function buildJobs(targetRows: GuestRow[]): MigrateBatchJob[] {
    const jobs: MigrateBatchJob[] = [];
    for (const row of targetRows) {
      if (!row.guestId) continue;
      for (const v of row.volumes) {
        jobs.push({
          label: `${row.name} (${String(row.vmid)}) — ${v.volid}`,
          guestId: row.guestId,
          guestKind: row.guestKind,
          vmid: row.vmid,
          volid: v.volid,
        });
      }
    }
    return jobs;
  }

  function migrateRow(row: GuestRow) {
    setMigrateTitle(`Migrate ${row.name} (${String(row.vmid)})`);
    setMigrating(buildJobs([row]));
  }

  function migrateSelected() {
    const targets = rows.filter((r) => selectedVmids.has(r.vmid));
    if (targets.length === 0) return;
    setMigrateTitle(`Migrate ${String(targets.length)} guests`);
    setMigrating(buildJobs(targets));
  }

  if (rows.length === 0) {
    return (
      <p className="py-8 text-center text-sm text-muted-foreground">
        No VM disks or container volumes on this pool.
      </p>
    );
  }

  const canMigrate = migrateTargets.length > 0;

  return (
    <div className="space-y-2">
      {selectedVmids.size > 0 && (
        <div className="flex items-center justify-between rounded-md border bg-muted/30 px-3 py-2 text-sm">
          <span>
            {selectedVmids.size} {selectedVmids.size === 1 ? "guest" : "guests"} selected
          </span>
          <div className="flex items-center gap-2">
            <Button
              size="sm"
              variant="outline"
              onClick={() => { setSelectedVmids(new Set()); }}
            >
              Clear
            </Button>
            <Button
              size="sm"
              onClick={migrateSelected}
              disabled={!canMigrate}
              title={canMigrate ? undefined : "No compatible target storage"}
            >
              <ArrowRightLeft className="mr-1.5 h-3.5 w-3.5" />
              Migrate selected
            </Button>
          </div>
        </div>
      )}

      <Table>
        <TableHeader>
          <TableRow>
            <TableHead className="w-10">
              <Checkbox
                checked={allSelected ? true : someSelected ? "indeterminate" : false}
                onCheckedChange={toggleAll}
                aria-label="Select all guests"
              />
            </TableHead>
            <TableHead>Name</TableHead>
            <TableHead>VMID</TableHead>
            <TableHead>Type</TableHead>
            <TableHead className="text-right">Disks</TableHead>
            <TableHead className="text-right">Total size</TableHead>
            <TableHead className="w-20" />
          </TableRow>
        </TableHeader>
        <TableBody>
          {rows.map((row) => (
            <ContextMenu key={`${row.guestKind}:${String(row.vmid)}`}>
              <ContextMenuTrigger asChild>
                <TableRow
                  data-state={selectedVmids.has(row.vmid) ? "selected" : undefined}
                  className="cursor-default"
                >
                  <TableCell>
                    <Checkbox
                      checked={selectedVmids.has(row.vmid)}
                      onCheckedChange={() => { toggleRow(row.vmid); }}
                      aria-label={`Select ${row.name}`}
                    />
                  </TableCell>
                  <TableCell className="font-medium">
                    <div className="flex items-center gap-2">
                      {row.guestKind === "ct" ? (
                        <Boxes className="h-4 w-4 text-muted-foreground" />
                      ) : (
                        <Server className="h-4 w-4 text-muted-foreground" />
                      )}
                      {row.name}
                    </div>
                  </TableCell>
                  <TableCell className="text-xs">{row.vmid}</TableCell>
                  <TableCell>
                    <Badge variant="outline" className="text-xs">
                      {row.guestKind === "ct" ? "CT" : "VM"}
                    </Badge>
                  </TableCell>
                  <TableCell className="text-right text-xs">
                    {row.volumes.length}
                  </TableCell>
                  <TableCell className="text-right text-xs">
                    {formatBytes(row.totalBytes)}
                  </TableCell>
                  <TableCell>
                    <div className="flex items-center justify-end">
                      <Button
                        variant="ghost"
                        size="icon"
                        className="h-7 w-7"
                        title={
                          canMigrate
                            ? `Migrate ${String(row.volumes.length)} ${row.volumes.length === 1 ? "disk" : "disks"} to another storage`
                            : "No compatible target storage available"
                        }
                        disabled={!canMigrate || !row.guestId}
                        onClick={() => { migrateRow(row); }}
                      >
                        <ArrowRightLeft className="h-4 w-4" />
                      </Button>
                    </div>
                  </TableCell>
                </TableRow>
              </ContextMenuTrigger>
              <ContextMenuContent>
                <ContextMenuItem
                  onSelect={() => { migrateRow(row); }}
                  disabled={!canMigrate || !row.guestId}
                >
                  <ArrowRightLeft className="mr-2 h-4 w-4" />
                  Migrate {row.volumes.length === 1 ? "disk" : `${String(row.volumes.length)} disks`}
                </ContextMenuItem>
              </ContextMenuContent>
            </ContextMenu>
          ))}
        </TableBody>
      </Table>

      <MigrateBatchDialog
        open={migrating != null}
        onOpenChange={(o) => {
          if (!o) {
            setMigrating(null);
            setSelectedVmids(new Set());
          }
        }}
        clusterId={clusterId}
        jobs={migrating ?? []}
        targetOptions={migrateTargets}
        title={migrateTitle}
      />
    </div>
  );
}
