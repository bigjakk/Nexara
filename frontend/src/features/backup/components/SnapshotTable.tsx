import { Fragment, useState } from "react";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
  ChevronRight,
  ChevronDown,
  Trash2,
  ShieldCheck,
  ShieldOff,
  Pencil,
  Check,
  X,
  Loader2,
} from "lucide-react";
import { DetailField } from "@/components/DetailField";
import { ExpandedDetailRow } from "@/components/ExpandedDetailRow";
import { DefGrid, Panel } from "@/components/page/Panel";
import { StateText } from "./StateText";
import type { PBSSnapshot } from "../types/backup";
import { RestoreDialog } from "./RestoreDialog";
import { DeleteSnapshotDialog } from "./DeleteSnapshotDialog";
import { ChangeOwnerDialog } from "./ChangeOwnerDialog";
import {
  usePBSServers,
  useProtectSnapshot,
  useUpdateSnapshotNotes,
} from "../api/backup-queries";
import { usePermissions } from "@/hooks/usePermissions";
import { formatBytes } from "@/lib/format";

function formatUnixTime(ts: number): string {
  return new Date(ts * 1000).toLocaleString();
}

function snapKey(snap: PBSSnapshot): string {
  return `${snap.backup_type}-${snap.backup_id}-${String(snap.backup_time)}`;
}

interface SnapshotTableProps {
  snapshots: PBSSnapshot[];
  pbsId: string;
}

export function SnapshotTable({ snapshots, pbsId }: SnapshotTableProps) {
  const [expanded, setExpanded] = useState<Set<string>>(new Set());
  const [deleteTarget, setDeleteTarget] = useState<PBSSnapshot | null>(null);
  const [ownerTarget, setOwnerTarget] = useState<PBSSnapshot | null>(null);
  const canManage = usePermissions().hasPermission("manage", "backup");
  // What a storage made from this server with its token signs in as.
  const serverToken = usePBSServers().data?.find(
    (s) => s.id === pbsId,
  )?.token_id;
  const ownerSuggestions = [
    ...new Set(snapshots.map((s) => s.owner).filter((o) => o !== "")),
  ];
  const [editingComment, setEditingComment] = useState<string | null>(null);
  const [commentValue, setCommentValue] = useState("");
  // Optimistic overrides: snapKey -> protected value
  const [protectOverrides, setProtectOverrides] = useState<
    Record<string, boolean>
  >({});
  const [protectingKey, setProtectingKey] = useState<string | null>(null);
  const [protectError, setProtectError] = useState<string | null>(null);

  const protectMutation = useProtectSnapshot();
  const notesMutation = useUpdateSnapshotNotes();

  if (snapshots.length === 0) {
    return (
      <Panel>
        <p className="py-6 text-center text-sm text-muted-foreground">
          No snapshots found.
        </p>
      </Panel>
    );
  }

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

  const isProtected = (snap: PBSSnapshot): boolean => {
    const key = snapKey(snap);
    if (key in protectOverrides) return protectOverrides[key] ?? false;
    return snap.protected;
  };

  const handleProtect = (snap: PBSSnapshot) => {
    const key = snapKey(snap);
    const newProtected = !isProtected(snap);
    setProtectingKey(key);
    setProtectError(null);
    // Optimistic update
    setProtectOverrides((prev) => ({ ...prev, [key]: newProtected }));
    // The app's error toast reports a failure (the hook has no onError); the
    // promise only reverts the row, and settles even when another toggle has
    // replaced this one, where mutate()'s callbacks would be dropped.
    void protectMutation
      .mutateAsync({
        pbsId,
        store: snap.datastore,
        body: {
          backup_type: snap.backup_type,
          backup_id: snap.backup_id,
          backup_time: snap.backup_time,
          protected: newProtected,
        },
      })
      .then(
        () => {
          setProtectingKey((k) => (k === key ? null : k));
        },
        () => {
          // Revert optimistic update
          setProtectOverrides((prev) =>
            Object.fromEntries(Object.entries(prev).filter(([k]) => k !== key)),
          );
          setProtectingKey((k) => (k === key ? null : k));
          setProtectError(key);
          setTimeout(() => {
            setProtectError(null);
          }, 3000);
        },
      );
  };

  const startEditComment = (snap: PBSSnapshot) => {
    const key = snapKey(snap);
    setEditingComment(key);
    setCommentValue(snap.comment || "");
  };

  const saveComment = (snap: PBSSnapshot) => {
    notesMutation.mutate(
      {
        pbsId,
        store: snap.datastore,
        body: {
          backup_type: snap.backup_type,
          backup_id: snap.backup_id,
          backup_time: snap.backup_time,
          comment: commentValue,
        },
      },
      {
        onSuccess: () => {
          setEditingComment(null);
        },
      },
    );
  };

  return (
    <>
      <Panel flush>
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead className="w-8" />
              <TableHead>Backup</TableHead>
              <TableHead>Datastore</TableHead>
              <TableHead>Taken</TableHead>
              <TableHead className="text-right">Size</TableHead>
              <TableHead>Verified</TableHead>
              <TableHead>Protected</TableHead>
              <TableHead>Owner</TableHead>
              <TableHead className="w-32">
                <span className="sr-only">Actions</span>
              </TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {snapshots.map((snap) => {
              const key = snapKey(snap);
              const isExpanded = expanded.has(key);
              const isEditingThis = editingComment === key;

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
                    <TableCell className="whitespace-nowrap">
                      <span className="text-muted-foreground">
                        {snap.backup_type.toUpperCase()}
                      </span>{" "}
                      <span className="font-mono font-medium">
                        {snap.backup_id}
                      </span>
                    </TableCell>
                    <TableCell>{snap.datastore}</TableCell>
                    <TableCell className="whitespace-nowrap">
                      {formatUnixTime(snap.backup_time)}
                    </TableCell>
                    <TableCell className="whitespace-nowrap text-right tabular-nums">
                      {formatBytes(snap.size)}
                    </TableCell>
                    <TableCell>
                      {snap.verified ? (
                        <StateText tone="ok">Verified</StateText>
                      ) : (
                        <StateText tone="idle">Not verified</StateText>
                      )}
                    </TableCell>
                    <TableCell>
                      {isProtected(snap) ? (
                        <span className="inline-flex items-center gap-1.5">
                          <ShieldCheck className="h-3.5 w-3.5 text-muted-foreground" />
                          Protected
                        </span>
                      ) : (
                        <span className="text-muted-foreground">No</span>
                      )}
                    </TableCell>
                    <TableCell className="text-muted-foreground">
                      {snap.owner || "-"}
                    </TableCell>
                    <TableCell>
                      <div
                        className="flex items-center justify-end gap-0.5"
                        onClick={(e) => {
                          e.stopPropagation();
                        }}
                      >
                        <Button
                          variant="ghost"
                          size="sm"
                          title={isProtected(snap) ? "Unprotect" : "Protect"}
                          onClick={() => {
                            handleProtect(snap);
                          }}
                          disabled={protectingKey === key}
                          className={
                            protectError === key ? "text-destructive" : ""
                          }
                        >
                          {protectingKey === key ? (
                            <Loader2 className="h-4 w-4 animate-spin" />
                          ) : isProtected(snap) ? (
                            <ShieldOff className="h-4 w-4" />
                          ) : (
                            <ShieldCheck className="h-4 w-4" />
                          )}
                        </Button>
                        <RestoreDialog snapshot={snap} pbsId={pbsId} />
                        <Button
                          variant="ghost"
                          size="sm"
                          title="Delete"
                          onClick={() => {
                            setDeleteTarget(snap);
                          }}
                          disabled={isProtected(snap)}
                        >
                          <Trash2 className="h-4 w-4 text-destructive" />
                        </Button>
                      </div>
                    </TableCell>
                  </TableRow>
                  {isExpanded && (
                    <ExpandedDetailRow colSpan={9}>
                      <DefGrid>
                        <DetailField label="Backup time">
                          {formatUnixTime(snap.backup_time)}
                        </DetailField>
                        <DetailField label="Last seen">
                          {snap.last_seen_at
                            ? new Date(snap.last_seen_at).toLocaleString()
                            : "-"}
                        </DetailField>
                        <DetailField label="Size">
                          {formatBytes(snap.size)}
                        </DetailField>
                        <DetailField label="Owner">
                          <span className="inline-flex flex-wrap items-baseline gap-x-2">
                            {snap.owner || "-"}
                            {canManage && (
                              <Button
                                variant="link"
                                size="sm"
                                className="h-auto p-0"
                                aria-label={`Change owner of ${snap.backup_type}/${snap.backup_id}`}
                                onClick={() => {
                                  setOwnerTarget(snap);
                                }}
                              >
                                Change
                              </Button>
                            )}
                          </span>
                        </DetailField>
                        <DetailField label="Verified">
                          {snap.verified ? "Yes" : "No"}
                        </DetailField>
                        <DetailField label="Protected">
                          {isProtected(snap) ? "Yes" : "No"}
                        </DetailField>
                      </DefGrid>
                      <div className="flex flex-wrap items-center gap-2 text-sm">
                        <span className="text-muted-foreground">Comment</span>
                        {isEditingThis ? (
                          <div
                            className="flex items-center gap-1"
                            onClick={(e) => {
                              e.stopPropagation();
                            }}
                          >
                            <Input
                              className="h-7 w-64 max-w-full text-sm"
                              value={commentValue}
                              onChange={(e) => {
                                setCommentValue(e.target.value);
                              }}
                              onKeyDown={(e) => {
                                if (e.key === "Enter") saveComment(snap);
                                if (e.key === "Escape") setEditingComment(null);
                              }}
                              autoFocus
                            />
                            <Button
                              aria-label={`Save comment for ${snap.backup_type}/${snap.backup_id}`}
                              variant="ghost"
                              size="sm"
                              onClick={() => {
                                saveComment(snap);
                              }}
                              disabled={notesMutation.isPending}
                            >
                              <Check className="h-3.5 w-3.5" />
                            </Button>
                            <Button
                              aria-label="Cancel editing comment"
                              variant="ghost"
                              size="sm"
                              onClick={() => {
                                setEditingComment(null);
                              }}
                            >
                              <X className="h-3.5 w-3.5" />
                            </Button>
                          </div>
                        ) : (
                          <div
                            className="flex items-center gap-1"
                            onClick={(e) => {
                              e.stopPropagation();
                            }}
                          >
                            <span>{snap.comment || "-"}</span>
                            <Button
                              aria-label={`Edit comment for ${snapKey(snap)}`}
                              variant="ghost"
                              size="sm"
                              onClick={() => {
                                startEditComment(snap);
                              }}
                            >
                              <Pencil className="h-3.5 w-3.5" />
                            </Button>
                          </div>
                        )}
                      </div>
                    </ExpandedDetailRow>
                  )}
                </Fragment>
              );
            })}
          </TableBody>
        </Table>
      </Panel>
      {ownerTarget && (
        <ChangeOwnerDialog
          pbsId={pbsId}
          snapshot={ownerTarget}
          groupSize={
            snapshots.filter(
              (s) =>
                s.datastore === ownerTarget.datastore &&
                s.backup_type === ownerTarget.backup_type &&
                s.backup_id === ownerTarget.backup_id,
            ).length
          }
          serverToken={serverToken}
          suggestions={ownerSuggestions}
          onClose={() => {
            setOwnerTarget(null);
          }}
        />
      )}
      {deleteTarget && (
        <DeleteSnapshotDialog
          snapshot={deleteTarget}
          pbsId={pbsId}
          open={!!deleteTarget}
          onOpenChange={(open) => {
            if (!open) setDeleteTarget(null);
          }}
        />
      )}
    </>
  );
}
