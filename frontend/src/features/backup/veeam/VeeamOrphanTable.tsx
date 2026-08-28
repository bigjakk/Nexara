import { Fragment, useEffect, useState } from "react";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { ChevronDown, ChevronRight, Ghost } from "lucide-react";
import { formatBytes } from "@/lib/format";
import { SortableTableHead } from "@/components/SortableTableHead";
import { byId, useTableSort, type SortAccessors } from "@/hooks/useTableSort";
import { useMapVeeamBackupObject } from "../api/backup-queries";
import type { VeeamOrphanedObject } from "../types/backup";

/** Epoch ms, so date columns order chronologically rather than by their text. */
function toEpoch(value: string | null): number | null {
  if (value == null || value === "") return null;
  const parsed = new Date(value).getTime();
  return Number.isNaN(parsed) ? null : parsed;
}

type OrphanSortKey = "name" | "cluster" | "points" | "size" | "newest";

/** Each accessor sorts on what its cell SHOWS, not on the underlying field. */
const ORPHAN_SORT: SortAccessors<VeeamOrphanedObject, OrphanSortKey> = {
  name: (object) => object.name,
  // The cell falls back to the platform name, so the column must too.
  cluster: (object) => object.cluster_name || object.platform_name || null,
  points: (object) => object.restore_points_count,
  size: (object) => object.restore_point_bytes,
  newest: (object) => toEpoch(object.latest_restore_point),
};

function formatTime(value: string | null): string {
  if (value == null || value === "") return "Never";
  const parsed = new Date(value);
  return Number.isNaN(parsed.getTime()) ? "Unknown" : parsed.toLocaleString();
}

/**
 * Backup objects whose Veeam platform IS mapped to a cluster but which match
 * no guest on it.
 *
 * Not an error list — a feature. These are restore points consuming repository
 * space for machines that no longer exist in the form that was backed up: a
 * deleted VM, a template whose name was reused under a new uuid, a host
 * rebuilt in place. The Veeam console does not call them out, and a coverage
 * view built on name matching could not: it would instead report each of those
 * guests as protected, by a backup of the machine it replaced.
 *
 * The row expands into an override, for the cases correlation cannot know —
 * a guest renamed since its last backup, or one whose SMBIOS uuid changed.
 */
export function VeeamOrphanTable({
  serverId,
  objects,
}: {
  serverId: string;
  objects: VeeamOrphanedObject[];
}) {
  const {
    rows: sortedObjects,
    toggle: toggleSort,
    directionFor,
  } = useTableSort(objects, ORPHAN_SORT, byId);

  const [expanded, setExpanded] = useState<string | null>(null);

  // Switching servers must not leave a row from the previous one expanded.
  useEffect(() => {
    setExpanded(null);
  }, [serverId]);

  if (objects.length === 0) {
    return (
      <p className="py-8 text-center text-sm text-muted-foreground">
        Every backup object on a mapped connection matches a guest. Nothing is
        holding restore points for a machine that no longer exists.
      </p>
    );
  }

  return (
    <div className="space-y-3">
      <div className="flex items-start gap-2 rounded-md border bg-muted/40 p-3">
        <Ghost className="mt-0.5 h-4 w-4 shrink-0 text-muted-foreground" />
        <p className="text-xs text-muted-foreground">
          Veeam still holds backups for these, but nothing on the mapped cluster
          matches them — the guest was deleted, or rebuilt with a new identity
          and its old backup left behind. They consume repository space and can
          usually be removed in the Veeam console. Map one to a guest only if
          you know the backup really is that guest&apos;s.
        </p>
      </div>

      <div className="rounded-md border">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead className="w-8" />
              <SortableTableHead
                direction={directionFor("name")}
                onSort={() => {
                  toggleSort("name");
                }}
              >
                Name
              </SortableTableHead>
              <SortableTableHead
                direction={directionFor("cluster")}
                onSort={() => {
                  toggleSort("cluster");
                }}
              >
                Cluster
              </SortableTableHead>
              <SortableTableHead
                align="right"
                direction={directionFor("points")}
                onSort={() => {
                  toggleSort("points");
                }}
              >
                Restore points
              </SortableTableHead>
              <SortableTableHead
                align="right"
                direction={directionFor("size")}
                onSort={() => {
                  toggleSort("size");
                }}
              >
                Size
              </SortableTableHead>
              <SortableTableHead
                direction={directionFor("newest")}
                onSort={() => {
                  toggleSort("newest");
                }}
              >
                Newest point
              </SortableTableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {sortedObjects.map((object) => {
              const isOpen = expanded === object.id;
              return (
                <Fragment key={object.id}>
                  <TableRow
                    className="cursor-pointer"
                    onClick={() => {
                      setExpanded(isOpen ? null : object.id);
                    }}
                  >
                    <TableCell>
                      {isOpen ? (
                        <ChevronDown className="h-4 w-4" />
                      ) : (
                        <ChevronRight className="h-4 w-4" />
                      )}
                    </TableCell>
                    <TableCell className="font-medium">{object.name}</TableCell>
                    <TableCell className="text-sm">
                      {object.cluster_name || object.platform_name}
                    </TableCell>
                    <TableCell className="text-right font-mono text-sm">
                      {object.restore_points_count}
                    </TableCell>
                    <TableCell className="text-right font-mono text-sm">
                      {formatBytes(object.restore_point_bytes)}
                    </TableCell>
                    <TableCell className="text-sm">
                      {formatTime(object.latest_restore_point)}
                    </TableCell>
                  </TableRow>
                  {isOpen && (
                    <TableRow>
                      <TableCell colSpan={6} className="bg-muted/30">
                        <OrphanDetail serverId={serverId} object={object} />
                      </TableCell>
                    </TableRow>
                  )}
                </Fragment>
              );
            })}
          </TableBody>
        </Table>
      </div>
    </div>
  );
}

function OrphanDetail({
  serverId,
  object,
}: {
  serverId: string;
  object: VeeamOrphanedObject;
}) {
  const [vmid, setVmid] = useState("");
  const mapObject = useMapVeeamBackupObject(serverId);

  const parsed = Number(vmid);
  const vmidValid =
    vmid !== "" &&
    Number.isInteger(parsed) &&
    parsed > 0 &&
    parsed <= 2147483647;

  return (
    <div className="space-y-3 py-2">
      <dl className="grid gap-x-6 gap-y-1 text-sm sm:grid-cols-2">
        <div className="flex gap-2">
          <dt className="text-muted-foreground">Type</dt>
          <dd>{object.object_type || "—"}</dd>
        </div>
        <div className="flex gap-2">
          <dt className="text-muted-foreground">Guest size</dt>
          <dd className="font-mono">{formatBytes(object.size_bytes)}</dd>
        </div>
        <div className="flex gap-2 sm:col-span-2">
          <dt className="shrink-0 text-muted-foreground">SMBIOS UUID</dt>
          {/*
            The whole reason this row is here: Veeam recorded this identity,
            and no guest on the mapped cluster carries it.
          */}
          <dd className="truncate font-mono text-xs">
            {object.smbios_uuid || "none recorded"}
          </dd>
        </div>
        <div className="flex gap-2">
          <dt className="text-muted-foreground">Last seen</dt>
          <dd>{formatTime(object.last_seen_at)}</dd>
        </div>
        {object.last_run_failed && (
          <div className="flex gap-2">
            <dt className="text-muted-foreground">Last run</dt>
            <dd>
              <Badge variant="destructive" className="text-xs">
                Failed or cancelled
              </Badge>
            </dd>
          </div>
        )}
      </dl>

      <div className="flex flex-wrap items-end gap-2 border-t pt-3">
        <div className="space-y-1">
          <label
            htmlFor={`vmid-${object.id}`}
            className="text-xs text-muted-foreground"
          >
            Map to guest VMID on {object.cluster_name || "the mapped cluster"}
          </label>
          <Input
            id={`vmid-${object.id}`}
            value={vmid}
            onChange={(e) => {
              setVmid(e.target.value);
            }}
            placeholder="e.g. 105"
            className="w-40"
          />
        </div>
        {/*
          No cluster picker, deliberately. The cluster is not the operator's
          choice: a Veeam platformId IS one Proxmox connection, so the object
          can only belong to the cluster that platform is mapped to, and
          offering a picker would invite a cross-cluster mapping the API
          rejects — and that the API rejects precisely because it would let a
          holder on one cluster read another's restore-point history.
        */}
        <Button
          size="sm"
          disabled={
            !vmidValid || mapObject.isPending || object.cluster_id === null
          }
          onClick={() => {
            mapObject.mutate({
              objectId: object.id,
              clusterId: object.cluster_id,
              vmid: parsed,
            });
          }}
        >
          Map to this guest
        </Button>
        <p className="text-xs text-muted-foreground">
          Recorded as a manual match. Automatic correlation will not overwrite
          it, but it is dropped if that VMID is later given to a different
          machine.
        </p>
      </div>

      {mapObject.isError && (
        <p className="text-sm text-destructive">
          Could not map this object.{" "}
          {mapObject.error instanceof Error ? mapObject.error.message : ""}
        </p>
      )}
    </div>
  );
}
