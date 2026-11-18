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
import { DetailField } from "@/components/DetailField";
import { DefGrid } from "@/components/page/Panel";
import { useIsMobile } from "@/hooks/useIsMobile";
import { usePermissions } from "@/hooks/usePermissions";
import { USAGE_TEXT_CLASS } from "@/lib/usage-level";
import { cn } from "@/lib/utils";
import { TaskProgressBanner } from "@/features/vms/components/TaskProgressBanner";
import { useDeleteSnapshot } from "@/features/vms/api/vm-queries";
import { useResyncGuestSnapshots } from "../api/snapshot-queries";
import { ageBucket, ageDays, formatAge, snapshotRowKey } from "../lib/age";
import type { GuestSnapshotRow } from "../types/snapshots";
import { DateTime } from "@/components/DateTime";

interface GuestSnapshotTableProps {
  rows: GuestSnapshotRow[];
}

function formatUnixTime(ts: number) {
  return <DateTime value={ts > 0 ? ts * 1000 : null} />;
}

/** A row's icon actions stay quiet until the pointer is on them. */
const ROW_ACTION_CLASS = "h-7 w-7 p-0 text-muted-foreground";

/** Fresh is plain; past a week reads amber and past a month red. */
function Age({ days }: { days: number | null }) {
  const bucket = ageBucket(days);
  const label = formatAge(days);
  switch (bucket) {
    case "month":
      return <span className={USAGE_TEXT_CLASS.err}>{label}</span>;
    case "week":
      return <span className={USAGE_TEXT_CLASS.warn}>{label}</span>;
    case "fresh":
      return <span>{label}</span>;
    case "unknown":
      return <span className="text-muted-foreground">—</span>;
  }
}

export function GuestSnapshotTable({ rows }: GuestSnapshotTableProps) {
  const { t } = useTranslation("snapshots");
  const { canDelete } = usePermissions();
  // A phone has room for one column and the actions: the snapshot's name
  // over its age and guest. Where it is and when it was taken move into the
  // expanded row.
  const isMobile = useIsMobile();
  const deleteMutation = useDeleteSnapshot();
  const resyncMutation = useResyncGuestSnapshots();

  const [expanded, setExpanded] = useState<Set<string>>(new Set());
  const [confirmDelete, setConfirmDelete] = useState<string | null>(null);
  const [resyncingKey, setResyncingKey] = useState<string | null>(null);
  const [oldestFirst, setOldestFirst] = useState(true);
  // Delete tasks being watched (one banner each), with the guest to resync
  // when each ends. An array, not a single slot: this page's whole point is
  // sequential cleanup, and a second delete must not orphan the first
  // task's feedback or its post-completion resync. A failed one stays, with
  // Proxmox's reason, until the next delete.
  const [tasks, setTasks] = useState<
    { upid: string; clusterId: string; vmid: number; failed?: boolean }[]
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
    setTasks((prev) => prev.filter((task) => !task.failed));
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
    success: boolean,
  ) => {
    // Success or failure, resync the guest so the row reflects Proxmox truth
    // (a failed delete keeps the row; a successful one drops it).
    resyncMutation.mutate({ clusterId, vmid });
    setTasks((prev) =>
      success
        ? prev.filter((task) => task.upid !== upid)
        : prev.map((task) =>
            task.upid === upid ? { ...task, failed: true } : task,
          ),
    );
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
      onComplete={(success) => {
        handleTaskComplete(task.upid, task.clusterId, task.vmid, success);
      }}
      description={t("taskDescription")}
    />
  ));

  // The table sits flush in a panel, so what is not the table brings its own
  // gutters.
  const bannerStack = banners.length > 0 && (
    <div className="space-y-3 px-[18px] pb-3">{banners}</div>
  );

  if (rows.length === 0) {
    return (
      <div>
        {bannerStack}
        <p className="px-[18px] py-8 text-center text-sm text-muted-foreground">
          {t("emptyFiltered")}
        </p>
      </div>
    );
  }

  return (
    <div>
      {bannerStack}

      <div>
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead className="w-8 pr-0" />
              <TableHead>
                <button
                  type="button"
                  className="flex items-center gap-1 font-medium hover:text-foreground"
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
              {!isMobile && (
                <>
                  <TableHead>{t("table.name")}</TableHead>
                  <TableHead>{t("table.guest")}</TableHead>
                  <TableHead>{t("table.cluster")}</TableHead>
                  <TableHead>{t("table.node")}</TableHead>
                  <TableHead>{t("table.created")}</TableHead>
                </>
              )}
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
              const name = (
                <>
                  {row.name}
                  {row.vmstate && (
                    <Badge variant="secondary" className="ml-2 font-sans">
                      {t("ramState")}
                    </Badge>
                  )}
                </>
              );
              const guest = (
                <div className="flex items-baseline gap-2">
                  <span
                    className={cn(
                      "font-medium",
                      !row.vm_name && "italic text-muted-foreground",
                    )}
                  >
                    {row.vm_name ?? `#${String(row.vmid)}`}
                  </span>
                  <span className="text-[12.5px] text-muted-foreground">
                    {row.guest_type === "qemu" ? "VM" : "CT"}
                  </span>
                </div>
              );

              return (
                <Fragment key={key}>
                  <TableRow
                    className="cursor-pointer"
                    onClick={() => {
                      toggleExpand(key);
                    }}
                  >
                    <TableCell className="pr-0 text-muted-foreground">
                      {isExpanded ? (
                        <ChevronDown className="h-4 w-4" />
                      ) : (
                        <ChevronRight className="h-4 w-4" />
                      )}
                    </TableCell>
                    {isMobile ? (
                      <TableCell>
                        <div className="font-mono text-[12.5px] [overflow-wrap:anywhere]">
                          {name}
                        </div>
                        <div className="mt-0.5 flex flex-wrap items-baseline gap-x-2.5 tabular-nums">
                          <Age days={days} />
                          {guest}
                        </div>
                      </TableCell>
                    ) : (
                      <>
                        <TableCell className="whitespace-nowrap tabular-nums">
                          <Age days={days} />
                        </TableCell>
                        <TableCell className="font-mono text-[12.5px]">
                          {name}
                        </TableCell>
                        <TableCell>{guest}</TableCell>
                        <TableCell className="text-muted-foreground">
                          {row.cluster_name}
                        </TableCell>
                        <TableCell className="text-muted-foreground">
                          {row.node || "—"}
                        </TableCell>
                        <TableCell className="whitespace-nowrap text-muted-foreground">
                          {formatUnixTime(row.snap_time)}
                        </TableCell>
                      </>
                    )}
                    <TableCell>
                      <div
                        className="flex items-center justify-end gap-0.5"
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
                              className={ROW_ACTION_CLASS}
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
                                className={ROW_ACTION_CLASS}
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
                                className={ROW_ACTION_CLASS}
                                title={t("openGuestUnavailable")}
                                disabled
                              >
                                <SquareArrowOutUpRight className="h-4 w-4" />
                              </Button>
                            )}
                            <Button
                              variant="ghost"
                              size="sm"
                              className={cn(
                                ROW_ACTION_CLASS,
                                "hover:text-destructive",
                              )}
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
                              <Trash2 className="h-4 w-4" />
                            </Button>
                          </>
                        )}
                      </div>
                    </TableCell>
                  </TableRow>
                  {isExpanded && (
                    <TableRow className="hover:bg-transparent">
                      <TableCell
                        colSpan={isMobile ? 3 : 8}
                        className="bg-foreground/[0.03] py-4 sm:first:pl-[58px]"
                      >
                        <DefGrid>
                          {isMobile && (
                            <>
                              <DetailField label={t("table.cluster")}>
                                {row.cluster_name}
                              </DetailField>
                              <DetailField label={t("table.node")}>
                                {row.node || "—"}
                              </DetailField>
                              <DetailField label={t("table.created")}>
                                {formatUnixTime(row.snap_time)}
                              </DetailField>
                            </>
                          )}
                          {row.description ? (
                            <DetailField label={t("detail.description")}>
                              {row.description}
                            </DetailField>
                          ) : (
                            <DetailField
                              label={t("detail.description")}
                              variant="muted"
                            >
                              —
                            </DetailField>
                          )}
                          <DetailField
                            label={t("detail.parent")}
                            variant={row.parent ? "mono" : "muted"}
                          >
                            {row.parent || "—"}
                          </DetailField>
                          <DetailField label={t("detail.vmid")} variant="mono">
                            {row.vmid}
                          </DetailField>
                          <DetailField label={t("detail.lastSeen")}>
                            <DateTime value={row.last_seen_at} />
                          </DetailField>
                          {row.vm_status && (
                            <DetailField label={t("detail.guestStatus")}>
                              {row.vm_status}
                            </DetailField>
                          )}
                        </DefGrid>
                        {!row.vm_id && (
                          <p className="mt-3 text-[12.5px] italic text-muted-foreground">
                            {t("orphanHint")}
                          </p>
                        )}
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
