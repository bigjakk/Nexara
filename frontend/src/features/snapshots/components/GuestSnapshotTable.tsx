import { Fragment, useState } from "react";
import { Link } from "react-router-dom";
import { useTranslation } from "react-i18next";
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
import {
  ChevronRight,
  ChevronDown,
  ChevronUp,
  Trash2,
  RefreshCw,
  SquareArrowOutUpRight,
  Loader2,
} from "lucide-react";
import { usePermissions } from "@/hooks/usePermissions";
import { TaskProgressBanner } from "@/features/vms/components/TaskProgressBanner";
import { useDeleteSnapshot } from "@/features/vms/api/vm-queries";
import { useResyncGuestSnapshots } from "../api/snapshot-queries";
import { ageBucket, ageDays, formatAge, snapshotRowKey } from "../lib/age";
import type { GuestSnapshotRow } from "../types/snapshots";

interface GuestSnapshotTableProps {
  rows: GuestSnapshotRow[];
}

function formatUnixTime(ts: number): string {
  if (ts <= 0) return "—";
  return new Date(ts * 1000).toLocaleString();
}

function AgeBadge({ days }: { days: number | null }) {
  const bucket = ageBucket(days);
  const label = formatAge(days);
  switch (bucket) {
    case "month":
      return <Badge variant="destructive">{label}</Badge>;
    case "week":
      return (
        <Badge variant="default" className="bg-amber-600">
          {label}
        </Badge>
      );
    case "fresh":
      return <span className="text-sm">{label}</span>;
    case "unknown":
      return <span className="text-muted-foreground">—</span>;
  }
}

export function GuestSnapshotTable({ rows }: GuestSnapshotTableProps) {
  const { t } = useTranslation("snapshots");
  const { canDelete } = usePermissions();
  const deleteMutation = useDeleteSnapshot();
  const resyncMutation = useResyncGuestSnapshots();

  const [expanded, setExpanded] = useState<Set<string>>(new Set());
  const [confirmDelete, setConfirmDelete] = useState<string | null>(null);
  const [resyncingKey, setResyncingKey] = useState<string | null>(null);
  const [oldestFirst, setOldestFirst] = useState(true);
  // Delete tasks being watched (one banner each), with the guest to resync
  // when each ends. An array, not a single slot: this page's whole point is
  // sequential cleanup, and a second delete must not orphan the first
  // task's feedback or its post-completion resync.
  const [tasks, setTasks] = useState<
    { upid: string; clusterId: string; vmid: number }[]
  >([]);

  const nowMs = Date.now();

  // Server order is oldest-first with unknown ages last; re-sorting locally
  // keeps the toggle instant and unknown ages pinned to the bottom.
  const sorted = [...rows].sort((a, b) => {
    if (a.snap_time <= 0 && b.snap_time <= 0) return 0;
    if (a.snap_time <= 0) return 1;
    if (b.snap_time <= 0) return -1;
    return oldestFirst ? a.snap_time - b.snap_time : b.snap_time - a.snap_time;
  });

  const toggleExpand = (key: string) => {
    setExpanded((prev) => {
      const next = new Set(prev);
      if (next.has(key)) {
        next.delete(key);
      } else {
        next.add(key);
      }
      return next;
    });
  };

  const canDeleteRow = (row: GuestSnapshotRow): boolean =>
    row.guest_type === "lxc" ? canDelete("container") : canDelete("vm");

  const handleDelete = (row: GuestSnapshotRow) => {
    if (!row.vm_id) return;
    deleteMutation.mutate(
      {
        clusterId: row.cluster_id,
        resourceId: row.vm_id,
        kind: row.guest_type === "lxc" ? "ct" : "vm",
        snapName: row.name,
      },
      {
        onSuccess: (data) => {
          setTasks((prev) => [
            ...prev,
            { upid: data.upid, clusterId: row.cluster_id, vmid: row.vmid },
          ]);
          setConfirmDelete(null);
        },
      },
    );
  };

  const handleTaskComplete = (
    upid: string,
    clusterId: string,
    vmid: number,
  ) => {
    // Success or failure, resync the guest so the row reflects Proxmox truth
    // (a failed delete keeps the row; a successful one drops it).
    resyncMutation.mutate({ clusterId, vmid });
    setTasks((prev) => prev.filter((task) => task.upid !== upid));
  };

  const handleResync = (row: GuestSnapshotRow) => {
    const key = snapshotRowKey(row);
    setResyncingKey(key);
    resyncMutation.mutate(
      { clusterId: row.cluster_id, vmid: row.vmid },
      {
        onSettled: () => {
          // Functional clear: an overlapping resync on another row may have
          // claimed the spinner; don't stomp its state.
          setResyncingKey((prev) => (prev === key ? null : prev));
        },
      },
    );
  };

  // Banners render above the empty state so an in-flight delete's feedback
  // (and its completion resync) survives the filters hiding every row.
  const banners = tasks.map((task) => (
    <TaskProgressBanner
      key={task.upid}
      clusterId={task.clusterId}
      upid={task.upid}
      onComplete={() => {
        handleTaskComplete(task.upid, task.clusterId, task.vmid);
      }}
      description={t("taskDescription")}
    />
  ));

  if (rows.length === 0) {
    return (
      <div className="space-y-3">
        {banners}
        <p className="py-8 text-center text-sm text-muted-foreground">
          {t("emptyFiltered")}
        </p>
      </div>
    );
  }

  return (
    <div className="space-y-3">
      {banners}

      <div className="rounded-md border">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead className="w-8" />
              <TableHead>
                <button
                  type="button"
                  className="flex items-center gap-1 font-medium"
                  onClick={() => {
                    setOldestFirst((prev) => !prev);
                  }}
                >
                  {t("table.age")}
                  {oldestFirst ? (
                    <ChevronUp className="h-3.5 w-3.5" />
                  ) : (
                    <ChevronDown className="h-3.5 w-3.5" />
                  )}
                </button>
              </TableHead>
              <TableHead>{t("table.name")}</TableHead>
              <TableHead>{t("table.guest")}</TableHead>
              <TableHead>{t("table.cluster")}</TableHead>
              <TableHead>{t("table.node")}</TableHead>
              <TableHead>{t("table.created")}</TableHead>
              <TableHead className="w-28" />
            </TableRow>
          </TableHeader>
          <TableBody>
            {sorted.map((row) => {
              const key = snapshotRowKey(row);
              const isExpanded = expanded.has(key);
              const days = ageDays(row.snap_time, nowMs);
              const guestKind = row.guest_type === "lxc" ? "ct" : "vm";
              const deletable = canDeleteRow(row) && row.vm_id !== null;

              return (
                <Fragment key={key}>
                  <TableRow
                    className="cursor-pointer"
                    onClick={() => {
                      toggleExpand(key);
                    }}
                  >
                    <TableCell className="px-2">
                      {isExpanded ? (
                        <ChevronDown className="h-4 w-4" />
                      ) : (
                        <ChevronRight className="h-4 w-4" />
                      )}
                    </TableCell>
                    <TableCell>
                      <AgeBadge days={days} />
                    </TableCell>
                    <TableCell className="font-mono text-sm">
                      {row.name}
                      {row.vmstate && (
                        <Badge variant="secondary" className="ml-2">
                          {t("ramState")}
                        </Badge>
                      )}
                    </TableCell>
                    <TableCell>
                      <div className="flex items-center gap-2">
                        <Badge
                          variant={
                            row.guest_type === "qemu" ? "default" : "secondary"
                          }
                        >
                          {row.guest_type === "qemu" ? "VM" : "CT"}
                        </Badge>
                        <span
                          className={
                            row.vm_name ? "" : "text-muted-foreground italic"
                          }
                        >
                          {row.vm_name ?? `#${String(row.vmid)}`}
                        </span>
                      </div>
                    </TableCell>
                    <TableCell className="text-sm">
                      {row.cluster_name}
                    </TableCell>
                    <TableCell className="text-sm text-muted-foreground">
                      {row.node || "—"}
                    </TableCell>
                    <TableCell className="text-sm">
                      {formatUnixTime(row.snap_time)}
                    </TableCell>
                    <TableCell>
                      <div
                        className="flex items-center justify-end gap-1"
                        onClick={(e) => {
                          e.stopPropagation();
                        }}
                      >
                        {confirmDelete === key ? (
                          <>
                            <Button
                              variant="destructive"
                              size="sm"
                              onClick={() => {
                                handleDelete(row);
                              }}
                              disabled={deleteMutation.isPending}
                            >
                              {deleteMutation.isPending ? (
                                <Loader2 className="h-4 w-4 animate-spin" />
                              ) : (
                                t("confirm")
                              )}
                            </Button>
                            <Button
                              variant="ghost"
                              size="sm"
                              onClick={() => {
                                setConfirmDelete(null);
                              }}
                            >
                              {t("cancel")}
                            </Button>
                          </>
                        ) : (
                          <>
                            <Button
                              variant="ghost"
                              size="sm"
                              title={
                                row.vm_id
                                  ? t("refreshRow")
                                  : t("openGuestUnavailable")
                              }
                              onClick={() => {
                                handleResync(row);
                              }}
                              // Resync resolves the guest from inventory and
                              // 404s for orphans — disable rather than error.
                              disabled={resyncingKey === key || !row.vm_id}
                            >
                              {resyncingKey === key ? (
                                <Loader2 className="h-4 w-4 animate-spin" />
                              ) : (
                                <RefreshCw className="h-4 w-4" />
                              )}
                            </Button>
                            {row.vm_id ? (
                              <Button
                                variant="ghost"
                                size="sm"
                                title={t("openGuest")}
                                asChild
                              >
                                <Link
                                  to={`/inventory/${guestKind}/${row.cluster_id}/${row.vm_id}?tab=snapshots`}
                                >
                                  <SquareArrowOutUpRight className="h-4 w-4" />
                                </Link>
                              </Button>
                            ) : (
                              <Button
                                variant="ghost"
                                size="sm"
                                title={t("openGuestUnavailable")}
                                disabled
                              >
                                <SquareArrowOutUpRight className="h-4 w-4" />
                              </Button>
                            )}
                            <Button
                              variant="ghost"
                              size="sm"
                              title={
                                deletable
                                  ? t("delete")
                                  : row.vm_id
                                    ? t("deleteDenied")
                                    : t("openGuestUnavailable")
                              }
                              onClick={() => {
                                setConfirmDelete(key);
                              }}
                              disabled={!deletable}
                            >
                              <Trash2 className="h-4 w-4 text-destructive" />
                            </Button>
                          </>
                        )}
                      </div>
                    </TableCell>
                  </TableRow>
                  {isExpanded && (
                    <TableRow>
                      <TableCell colSpan={8} className="bg-muted/30 px-8 py-4">
                        <div className="grid grid-cols-2 gap-x-8 gap-y-2 text-sm md:grid-cols-4">
                          <div>
                            <span className="text-muted-foreground">
                              {t("detail.description")}:
                            </span>{" "}
                            {row.description || "—"}
                          </div>
                          <div>
                            <span className="text-muted-foreground">
                              {t("detail.parent")}:
                            </span>{" "}
                            {row.parent || "—"}
                          </div>
                          <div>
                            <span className="text-muted-foreground">
                              {t("detail.vmid")}:
                            </span>{" "}
                            {row.vmid}
                          </div>
                          <div>
                            <span className="text-muted-foreground">
                              {t("detail.lastSeen")}:
                            </span>{" "}
                            {new Date(row.last_seen_at).toLocaleString()}
                          </div>
                          {row.vm_status && (
                            <div>
                              <span className="text-muted-foreground">
                                {t("detail.guestStatus")}:
                              </span>{" "}
                              {row.vm_status}
                            </div>
                          )}
                          {!row.vm_id && (
                            <div className="col-span-2 text-muted-foreground italic md:col-span-4">
                              {t("orphanHint")}
                            </div>
                          )}
                        </div>
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
