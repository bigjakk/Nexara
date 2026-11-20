import { useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { ArrowRightLeft, MoreVertical } from "lucide-react";
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
import { ContextMenuItem } from "@/components/ui/context-menu";
import { useClusterNodes } from "@/features/clusters/api/cluster-queries";
import { StatusBadge } from "@/features/inventory/components/StatusBadge";
import { VMContextMenu } from "@/features/vms/components/VMContextMenu";
import { usePermissions } from "@/hooks/usePermissions";
import { apiClient } from "@/lib/api-client";
import { apiPath } from "@/lib/api-path";
import { formatBytes, formatUptime } from "@/lib/format";
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
  /** PVE power state (e.g. "running", "stopped"); "" when the guest is unknown. */
  status: string;
  /** A running VM's halted QEMU state, if any. */
  runState: string;
  /** Resolved host node name; "" when unknown. */
  nodeName: string;
  cpuCount: number;
  memTotal: number;
  uptime: number;
  template: boolean;
  volumes: StorageContentItem[];
  totalBytes: number;
}

/**
 * Opens a row's guest menu from its actions button, as a right click there
 * would: a Radix context menu opens on a contextmenu event and nothing else.
 * At the button's lower left: the menu opens beside it, above or below as it
 * fits, and never over it.
 */
function openRowMenu(button: HTMLElement) {
  const rect = button.getBoundingClientRect();
  button.dispatchEvent(
    new MouseEvent("contextmenu", {
      bubbles: true,
      cancelable: true,
      clientX: rect.left,
      clientY: rect.bottom + 4,
    }),
  );
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
      apiClient.list<VMResponse>(apiPath`/api/v1/clusters/${clusterId}/vms`),
    enabled: clusterId.length > 0,
    staleTime: 30_000,
  });
  const ctsQuery = useQuery({
    queryKey: ["clusters", clusterId, "containers"],
    queryFn: () =>
      apiClient.list<VMResponse>(
        apiPath`/api/v1/clusters/${clusterId}/containers`,
      ),
    enabled: clusterId.length > 0,
    staleTime: 30_000,
  });
  const nodesQuery = useClusterNodes(clusterId);
  // A VM's disk move takes manage:vm, a container's volume move
  // execute:container: a row is offered a migration only for its kind.
  const { hasPermission } = usePermissions();
  const canMove = (kind: GuestRow["guestKind"]) =>
    kind === "ct"
      ? hasPermission("execute", "container")
      : hasPermission("manage", "vm");

  const rows = useMemo<GuestRow[]>(() => {
    const byVmidVm = new Map<number, VMResponse>();
    for (const v of vmsQuery.data ?? []) byVmidVm.set(v.vmid, v);
    const byVmidCT = new Map<number, VMResponse>();
    for (const c of ctsQuery.data ?? []) byVmidCT.set(c.vmid, c);
    const nodeNameById = new Map<string, string>();
    for (const n of nodesQuery.data ?? []) nodeNameById.set(n.id, n.name);

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
      const meta =
        kind === "ct" ? byVmidCT.get(item.vmid) : byVmidVm.get(item.vmid);
      groups.set(key, {
        vmid: item.vmid,
        guestId: meta?.id ?? "",
        guestKind: kind,
        name: meta?.name ?? `(unknown ${kind === "ct" ? "container" : "VM"})`,
        status: meta?.status ?? "",
        runState: meta?.run_state ?? "",
        nodeName: meta ? (nodeNameById.get(meta.node_id) ?? "") : "",
        cpuCount: meta?.cpu_count ?? 0,
        memTotal: meta?.mem_total ?? 0,
        uptime: meta?.uptime ?? 0,
        template: meta?.template ?? false,
        volumes: [item],
        totalBytes: item.size,
      });
    }

    return Array.from(groups.values()).sort((a, b) => a.vmid - b.vmid);
  }, [items, vmsQuery.data, ctsQuery.data, nodesQuery.data]);

  // Only the rows this role may migrate can be selected.
  const movableRows = rows.filter((r) => canMove(r.guestKind));
  const allSelected =
    movableRows.length > 0 && selectedVmids.size === movableRows.length;
  const someSelected =
    selectedVmids.size > 0 && selectedVmids.size < movableRows.length;

  function toggleAll() {
    if (allSelected) setSelectedVmids(new Set());
    else setSelectedVmids(new Set(movableRows.map((r) => r.vmid)));
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
          running: row.status === "running",
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
      <p className="px-[18px] py-8 text-center text-sm text-muted-foreground">
        No VM disks or container volumes on this store.
      </p>
    );
  }

  const canMigrate = migrateTargets.length > 0;

  return (
    <div>
      {selectedVmids.size > 0 && (
        <div className="flex flex-wrap items-center justify-between gap-2 border-y bg-primary/[0.06] px-[18px] py-2 text-sm">
          <span>
            {selectedVmids.size} {selectedVmids.size === 1 ? "guest" : "guests"}{" "}
            selected
          </span>
          <div className="flex items-center gap-2">
            <Button
              size="sm"
              variant="outline"
              onClick={() => {
                setSelectedVmids(new Set());
              }}
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
              {movableRows.length > 0 && (
                <Checkbox
                  checked={
                    allSelected ? true : someSelected ? "indeterminate" : false
                  }
                  onCheckedChange={toggleAll}
                  aria-label="Select all guests"
                />
              )}
            </TableHead>
            <TableHead>Name</TableHead>
            <TableHead>Status</TableHead>
            <TableHead className="hidden sm:table-cell">ID</TableHead>
            <TableHead className="hidden sm:table-cell">Kind</TableHead>
            <TableHead className="hidden md:table-cell">Node</TableHead>
            <TableHead className="hidden text-right lg:table-cell">
              CPU
            </TableHead>
            <TableHead className="hidden text-right lg:table-cell">
              Memory
            </TableHead>
            <TableHead className="hidden text-right lg:table-cell">
              Uptime
            </TableHead>
            <TableHead className="hidden text-right sm:table-cell">
              Disks
            </TableHead>
            <TableHead className="text-right">Total size</TableHead>
            <TableHead className="w-10 sm:w-20">
              <span className="sr-only">Actions</span>
            </TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {rows.map((row) => {
            const key = `${row.guestKind}:${String(row.vmid)}`;
            const tableRow = (
              <TableRow
                key={key}
                data-state={
                  selectedVmids.has(row.vmid) ? "selected" : undefined
                }
                className="cursor-default"
              >
                <TableCell>
                  {canMove(row.guestKind) && (
                    <Checkbox
                      checked={selectedVmids.has(row.vmid)}
                      onCheckedChange={() => {
                        toggleRow(row.vmid);
                      }}
                      aria-label={`Select ${row.name}`}
                    />
                  )}
                </TableCell>
                <TableCell>
                  <div className="flex items-center gap-2 font-medium">
                    <span>{row.name}</span>
                    {row.template && (
                      <Badge variant="secondary" className="text-xs">
                        Template
                      </Badge>
                    )}
                  </div>
                  {/* A phone has no room for these as columns. */}
                  <div className="text-[12.5px] text-muted-foreground sm:hidden">
                    {row.guestKind === "ct" ? "CT" : "VM"} {row.vmid},{" "}
                    {row.volumes.length === 1
                      ? "1 disk"
                      : `${String(row.volumes.length)} disks`}
                  </div>
                </TableCell>
                <TableCell>
                  {row.status ? (
                    <StatusBadge status={row.status} runState={row.runState} />
                  ) : (
                    <span className="text-muted-foreground">—</span>
                  )}
                </TableCell>
                <TableCell className="hidden tabular-nums text-muted-foreground sm:table-cell">
                  {row.vmid}
                </TableCell>
                <TableCell className="hidden text-muted-foreground sm:table-cell">
                  {row.guestKind === "ct" ? "CT" : "VM"}
                </TableCell>
                <TableCell className="hidden text-muted-foreground md:table-cell">
                  {row.nodeName || "—"}
                </TableCell>
                <TableCell className="hidden text-right tabular-nums lg:table-cell">
                  {row.cpuCount > 0 ? row.cpuCount : "—"}
                </TableCell>
                <TableCell className="hidden whitespace-nowrap text-right tabular-nums lg:table-cell">
                  {row.memTotal > 0 ? formatBytes(row.memTotal) : "—"}
                </TableCell>
                <TableCell className="hidden whitespace-nowrap text-right tabular-nums text-muted-foreground lg:table-cell">
                  {formatUptime(row.uptime, "—")}
                </TableCell>
                <TableCell className="hidden text-right tabular-nums sm:table-cell">
                  {row.volumes.length}
                </TableCell>
                <TableCell className="whitespace-nowrap text-right tabular-nums">
                  {formatBytes(row.totalBytes)}
                </TableCell>
                <TableCell>
                  <div className="flex items-center justify-end gap-0.5">
                    {/* A phone has room for one button: the menu's, which
                        leads with this. */}
                    {canMove(row.guestKind) && (
                      <Button
                        variant="ghost"
                        size="icon"
                        className="hidden h-7 w-7 sm:inline-flex"
                        title={
                          canMigrate
                            ? `Migrate ${String(row.volumes.length)} ${row.volumes.length === 1 ? "disk" : "disks"} to another storage`
                            : "No compatible target storage available"
                        }
                        disabled={!canMigrate || !row.guestId}
                        onClick={() => {
                          migrateRow(row);
                        }}
                      >
                        <ArrowRightLeft className="h-4 w-4" />
                      </Button>
                    )}
                    {/* The menu a right click opens, as a mouse can see it and
                        a tap can reach it; a row with no guest has none. */}
                    {row.guestId && (
                      <Button
                        variant="ghost"
                        size="icon"
                        className="h-7 w-7"
                        aria-label={`Actions for ${row.name}`}
                        aria-haspopup="menu"
                        onClick={(e) => {
                          openRowMenu(e.currentTarget);
                        }}
                      >
                        <MoreVertical className="h-4 w-4" />
                      </Button>
                    )}
                  </div>
                </TableCell>
              </TableRow>
            );
            // The guest's own menu, as the tree has it, led by moving its disks
            // off this store. A volume no guest in the inventory owns has no
            // guest to act on.
            if (!row.guestId) return tableRow;
            return (
              <VMContextMenu
                key={key}
                target={{
                  clusterId,
                  resourceId: row.guestId,
                  vmid: row.vmid,
                  name: row.name,
                  kind: row.guestKind,
                  status: row.status,
                  runState: row.runState,
                  currentNode: row.nodeName,
                  template: row.template,
                }}
                pageItems={
                  canMove(row.guestKind)
                    ? [
                        <ContextMenuItem
                          key="migrate-disks"
                          onSelect={() => {
                            migrateRow(row);
                          }}
                          disabled={!canMigrate}
                        >
                          <ArrowRightLeft className="mr-2 h-4 w-4" />
                          Migrate{" "}
                          {row.volumes.length === 1
                            ? "disk"
                            : `${String(row.volumes.length)} disks`}
                        </ContextMenuItem>,
                      ]
                    : []
                }
              >
                {tableRow}
              </VMContextMenu>
            );
          })}
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
