import { useState } from "react";
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
import { CreateSnapshotDialog } from "./CreateSnapshotDialog";
import type { ResourceKind, Snapshot } from "../types/vm";

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
  const queryClient = useQueryClient();
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

  function handleTaskComplete() {
    void queryClient.invalidateQueries({ queryKey: snapshotQueryKey });
    setUpid(null);
  }

  const [createOpen, setCreateOpen] = useState(false);
  const [upid, setUpid] = useState<string | null>(null);
  const [confirmDelete, setConfirmDelete] = useState<string | null>(null);
  const [confirmRollback, setConfirmRollback] = useState<string | null>(null);

  function handleDelete(name: string) {
    deleteMutation.mutate(
      { clusterId, resourceId, kind, snapName: name },
      {
        onSuccess: (data) => {
          setUpid(data.upid);
          setConfirmDelete(null);
        },
      },
    );
  }

  function handleRollback(name: string) {
    rollbackMutation.mutate(
      { clusterId, resourceId, kind, snapName: name },
      {
        onSuccess: (data) => {
          setUpid(data.upid);
          setConfirmRollback(null);
        },
      },
    );
  }

  function formatDate(ts: number | undefined): string {
    if (!ts) return "--";
    return new Date(ts * 1000).toLocaleString();
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
      {upid && (
        <TaskProgressBanner
          clusterId={clusterId}
          upid={upid}
          onComplete={handleTaskComplete}
          description="Snapshot operation"
        />
      )}

      <Panel
        flush
        title="Snapshots"
        aside={
          isLoading
            ? "Loading..."
            : taken.length === 0
              ? undefined
              : `${String(taken.length)} taken${newest !== undefined ? `, the newest on ${new Date(newest * 1000).toLocaleDateString()}` : ""}`
        }
        actions={
          <Button
            size="sm"
            className="gap-1.5"
            onClick={() => {
              setCreateOpen(true);
            }}
          >
            <Camera className="h-4 w-4" />
            Take snapshot
          </Button>
        }
      >
        {isLoading && (
          <p className="px-[18px] py-6 text-sm text-muted-foreground">
            Reading this guest&apos;s snapshots...
          </p>
        )}

        {!isLoading && snapshots && snapshots.length === 0 && (
          <p className="px-[18px] py-6 text-sm text-muted-foreground">
            No snapshots found. Take one before a risky change, so there is a
            state to roll back to.
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
  formatDate: (ts: number | undefined) => string;
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
  formatDate,
}: SnapshotNodeProps) {
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
            {isCurrent ? "Current state" : snap.name}
          </span>
          {snap.vmstate ? <Badge variant="outline">With RAM</Badge> : null}
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

        {!isCurrent && (
          <div className="flex gap-1">
            {confirmRollback === snap.name ? (
              <>
                <Button
                  size="sm"
                  variant="destructive"
                  onClick={() => {
                    onRollback(snap.name);
                  }}
                  disabled={rollbackPending}
                >
                  Confirm
                </Button>
                <Button
                  size="sm"
                  variant="outline"
                  onClick={() => {
                    onConfirmRollback(null);
                  }}
                >
                  Cancel
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
                Rollback
              </Button>
            )}
            {confirmDelete === snap.name ? (
              <>
                <Button
                  size="sm"
                  variant="destructive"
                  onClick={() => {
                    onDelete(snap.name);
                  }}
                  disabled={deletePending}
                >
                  Confirm
                </Button>
                <Button
                  size="sm"
                  variant="outline"
                  onClick={() => {
                    onConfirmDelete(null);
                  }}
                >
                  Cancel
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
                Delete
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
          formatDate={formatDate}
        />
      ))}
    </>
  );
}
