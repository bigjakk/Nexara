import { useState, type ReactNode } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { Camera, CornerDownRight, RotateCcw, Trash2 } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Panel } from "@/components/page/Panel";
import {
  useSnapshots,
  useDeleteSnapshot,
  useRollbackSnapshot,
} from "../api/vm-queries";
import { TaskProgressBanner } from "./TaskProgressBanner";
import { useGuestActionAllowed } from "../lib/vm-action-defs";
import { CreateSnapshotDialog } from "./CreateSnapshotDialog";
import type { ResourceKind, Snapshot } from "../types/vm";
import { formatExactDate } from "@/lib/format";
import { DateTime } from "@/components/DateTime";
import { useTranslation } from "react-i18next";

interface SnapshotPanelProps {
  clusterId: string;
  resourceId: string;
  kind: ResourceKind;
  resourceName: string;
}

export function SnapshotPanel({
  clusterId,
  resourceId,
  kind,
  resourceName,
}: SnapshotPanelProps) {
  const { t } = useTranslation("vms");
  const queryClient = useQueryClient();
  const allowed = useGuestActionAllowed();
  const { data: snapshots, isLoading } = useSnapshots(
    clusterId,
    resourceId,
    kind,
  );
  const deleteMutation = useDeleteSnapshot();
  const rollbackMutation = useRollbackSnapshot();

  const snapshotQueryKey = [
    "clusters",
    clusterId,
    kind === "ct" ? "containers" : "vms",
    resourceId,
    "snapshots",
  ];

  function handleTaskComplete(success: boolean) {
    void queryClient.invalidateQueries({ queryKey: snapshotQueryKey });
    // A failed rollback or delete keeps its banner, which gives Proxmox's
    // reason, until the next one.
    if (!success) setFailedUpid((failed) => upid ?? failed);
    setUpid(null);
  }

  const [createOpen, setCreateOpen] = useState(false);
  const [upid, setUpid] = useState<string | null>(null);
  const [failedUpid, setFailedUpid] = useState<string | null>(null);
  const [confirmDelete, setConfirmDelete] = useState<string | null>(null);
  const [confirmRollback, setConfirmRollback] = useState<string | null>(null);

  function handleDelete(name: string) {
    setFailedUpid(null);
    deleteMutation.mutate(
      { clusterId, resourceId, kind, snapName: name },
      {
        onSuccess: (data) => {
          // Again on success: an older task may have failed meanwhile.
          setFailedUpid(null);
          setUpid(data.upid);
          setConfirmDelete(null);
        },
      },
    );
  }

  function handleRollback(name: string) {
    setFailedUpid(null);
    rollbackMutation.mutate(
      { clusterId, resourceId, kind, snapName: name },
      {
        onSuccess: (data) => {
          setFailedUpid(null);
          setUpid(data.upid);
          setConfirmRollback(null);
        },
      },
    );
  }

  function formatDate(ts: number | undefined) {
    return (
      <DateTime value={ts === undefined ? null : ts * 1000} fallback="--" />
    );
  }

  // Build tree from flat list using parent field
  function buildTree(snaps: Snapshot[]): {
    roots: Snapshot[];
    childrenMap: Map<string, Snapshot[]>;
  } {
    const childrenMap = new Map<string, Snapshot[]>();
    const roots: Snapshot[] = [];
    for (const snap of snaps) {
      if (snap.parent) {
        const siblings = childrenMap.get(snap.parent);
        if (siblings) {
          siblings.push(snap);
        } else {
          childrenMap.set(snap.parent, [snap]);
        }
      } else {
        roots.push(snap);
      }
    }
    return { roots, childrenMap };
  }

  // "current" is Proxmox's marker for the running state, not a snapshot.
  const taken = (snapshots ?? []).filter((snap) => snap.name !== "current");
  const newest = taken.reduce<number | undefined>(
    (latest, snap) =>
      snap.snap_time !== undefined &&
      (latest === undefined || snap.snap_time > latest)
        ? snap.snap_time
        : latest,
    undefined,
  );
  const tree =
    !isLoading && snapshots && snapshots.length > 0
      ? buildTree(snapshots)
      : null;

  return (
    <div className="space-y-4">
      {(upid ?? failedUpid) && (
        <TaskProgressBanner
          clusterId={clusterId}
          upid={upid ?? failedUpid}
          onComplete={handleTaskComplete}
          description={t("snapshotPanel.snapshotOperation")}
        />
      )}

      <Panel
        flush
        title={t("snapshotPanel.snapshots")}
        aside={
          isLoading
            ? t("snapshotPanel.loading")
            : taken.length === 0
              ? undefined
              : newest !== undefined
                ? t("snapshotPanel.takenNewestOn", {
                    number: String(taken.length),
                    date: formatExactDate(newest * 1000),
                  })
                : t("snapshotPanel.taken", { number: String(taken.length) })
        }
        actions={
          allowed("snapshot", kind) ? (
            <Button
              size="sm"
              className="gap-1.5"
              onClick={() => {
                setFailedUpid(null);
                setCreateOpen(true);
              }}
            >
              <Camera className="h-4 w-4" />
              {t("snapshotPanel.takeSnapshot")}
            </Button>
          ) : undefined
        }
      >
        {isLoading && (
          <p className="px-[18px] py-6 text-sm text-muted-foreground">
            {t("snapshotPanel.readingThisGuestSSnapshots")}
          </p>
        )}

        {!isLoading && snapshots && snapshots.length === 0 && (
          <p className="px-[18px] py-6 text-sm text-muted-foreground">
            {t("snapshotPanel.noSnapshotsFoundTakeOne")}
          </p>
        )}

        {tree && (
          <ul className="divide-y border-t">
            {tree.roots.map((snap) => (
              <SnapshotNode
                key={snap.name}
                snap={snap}
                depth={0}
                childrenMap={tree.childrenMap}
                confirmDelete={confirmDelete}
                confirmRollback={confirmRollback}
                onConfirmDelete={setConfirmDelete}
                onConfirmRollback={setConfirmRollback}
                onDelete={handleDelete}
                onRollback={handleRollback}
                deletePending={deleteMutation.isPending}
                rollbackPending={rollbackMutation.isPending}
                canRollback={allowed("rollback", kind)}
                canDelete={allowed("delete-snapshot", kind)}
                formatDate={formatDate}
              />
            ))}
          </ul>
        )}
      </Panel>

      <CreateSnapshotDialog
        open={createOpen}
        onOpenChange={setCreateOpen}
        clusterId={clusterId}
        resourceId={resourceId}
        kind={kind}
        resourceName={resourceName}
      />
    </div>
  );
}

interface SnapshotNodeProps {
  snap: Snapshot;
  depth: number;
  childrenMap: Map<string, Snapshot[]>;
  confirmDelete: string | null;
  confirmRollback: string | null;
  onConfirmDelete: (name: string | null) => void;
  onConfirmRollback: (name: string | null) => void;
  onDelete: (name: string) => void;
  onRollback: (name: string) => void;
  deletePending: boolean;
  rollbackPending: boolean;
  canRollback: boolean;
  canDelete: boolean;
  formatDate: (ts: number | undefined) => ReactNode;
}

function SnapshotNode({
  snap,
  depth,
  childrenMap,
  confirmDelete,
  confirmRollback,
  onConfirmDelete,
  onConfirmRollback,
  onDelete,
  onRollback,
  deletePending,
  rollbackPending,
  canRollback,
  canDelete,
  formatDate,
}: SnapshotNodeProps) {
  const { t } = useTranslation("vms");
  const children = childrenMap.get(snap.name) ?? [];
  const isCurrent = snap.name === "current";

  return (
    <>
      <li
        className="flex flex-wrap items-center gap-x-3 gap-y-1.5 py-2.5 pr-[18px] text-sm"
        // Each level of the tree steps in; the gutter is the panel's 18px.
        style={{ paddingLeft: `${String(18 + depth * 22)}px` }}
      >
        <div className="flex min-w-0 flex-[1_1_16rem] items-center gap-2">
          {depth > 0 && (
            <CornerDownRight
              aria-hidden="true"
              className="h-3.5 w-3.5 shrink-0 text-muted-foreground/60"
            />
          )}
          <span
            className={
              isCurrent
                ? "text-muted-foreground"
                : "min-w-0 truncate font-medium"
            }
          >
            {isCurrent ? t("snapshotPanel.currentState") : snap.name}
          </span>
          {snap.vmstate ? (
            <Badge variant="outline">{t("snapshotPanel.withRAM")}</Badge>
          ) : null}
          {snap.description ? (
            <span
              className="min-w-0 truncate text-muted-foreground"
              title={snap.description}
            >
              {snap.description}
            </span>
          ) : null}
        </div>
        {!isCurrent && (
          <span className="text-[12.5px] tabular-nums text-muted-foreground">
            {formatDate(snap.snap_time)}
          </span>
        )}

        {!isCurrent && (canRollback || canDelete) && (
          <div className="flex gap-1">
            {!canRollback ? null : confirmRollback === snap.name ? (
              <>
                <Button
                  size="sm"
                  variant="destructive"
                  onClick={() => {
                    onRollback(snap.name);
                  }}
                  disabled={rollbackPending}
                >
                  {t("snapshotPanel.confirm")}
                </Button>
                <Button
                  size="sm"
                  variant="outline"
                  onClick={() => {
                    onConfirmRollback(null);
                  }}
                >
                  {t("snapshotPanel.cancel")}
                </Button>
              </>
            ) : (
              <Button
                size="sm"
                variant="ghost"
                className="gap-1.5 px-2"
                onClick={() => {
                  onConfirmRollback(snap.name);
                }}
              >
                <RotateCcw className="h-3.5 w-3.5" />
                {t("snapshotPanel.rollback")}
              </Button>
            )}
            {!canDelete ? null : confirmDelete === snap.name ? (
              <>
                <Button
                  size="sm"
                  variant="destructive"
                  onClick={() => {
                    onDelete(snap.name);
                  }}
                  disabled={deletePending}
                >
                  {t("snapshotPanel.confirm")}
                </Button>
                <Button
                  size="sm"
                  variant="outline"
                  onClick={() => {
                    onConfirmDelete(null);
                  }}
                >
                  {t("snapshotPanel.cancel")}
                </Button>
              </>
            ) : (
              <Button
                size="sm"
                variant="ghost"
                className="gap-1.5 px-2 text-muted-foreground hover:text-destructive"
                onClick={() => {
                  onConfirmDelete(snap.name);
                }}
              >
                <Trash2 className="h-3.5 w-3.5" />
                {t("snapshotPanel.delete")}
              </Button>
            )}
          </div>
        )}
      </li>
      {children.map((child) => (
        <SnapshotNode
          key={child.name}
          snap={child}
          depth={depth + 1}
          childrenMap={childrenMap}
          confirmDelete={confirmDelete}
          confirmRollback={confirmRollback}
          onConfirmDelete={onConfirmDelete}
          onConfirmRollback={onConfirmRollback}
          onDelete={onDelete}
          onRollback={onRollback}
          deletePending={deletePending}
          rollbackPending={rollbackPending}
          canRollback={canRollback}
          canDelete={canDelete}
          formatDate={formatDate}
        />
      ))}
    </>
  );
}
