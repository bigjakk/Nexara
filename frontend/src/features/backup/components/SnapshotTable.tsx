import { useState } from "react";
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
import type { PBSSnapshot } from "../types/backup";
import { RestoreDialog } from "./RestoreDialog";
import { DeleteSnapshotDialog } from "./DeleteSnapshotDialog";
import {
  useProtectSnapshot,
  useUpdateSnapshotNotes,
} from "../api/backup-queries";

function formatBytes(bytes: number): string {
  if (bytes === 0) return "0 B";
  const k = 1024;
  const sizes = ["B", "KB", "MB", "GB", "TB"];
  const i = Math.floor(Math.log(bytes) / Math.log(k));
  return `${(bytes / Math.pow(k, i)).toFixed(1)} ${sizes[i] ?? ""}`;
}

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
  const [editingComment, setEditingComment] = useState<string | null>(null);
  const [commentValue, setCommentValue] = useState("");
  // Optimistic overrides: snapKey -> protected value
  const [protectOverrides, setProtectOverrides] = useState<Record<string, boolean>>({});
  const [protectingKey, setProtectingKey] = useState<string | null>(null);
  const [protectError, setProtectError] = useState<string | null>(null);

  const protectMutation = useProtectSnapshot();
  const notesMutation = useUpdateSnapshotNotes();

  if (snapshots.length === 0) {
    return (
      <p className="py-8 text-center text-sm text-muted-foreground">
        No snapshots found.
      </p>
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
    protectMutation.mutate(
      {
        pbsId,
        store: snap.datastore,
        body: {
          backup_type: snap.backup_type,
          backup_id: snap.backup_id,
          backup_time: snap.backup_time,
          protected: newProtected,
        },
      },
      {
        onSuccess: () => {
          setProtectingKey(null);
        },
        onError: () => {
          // Revert optimistic update
          setProtectOverrides((prev) => {
            const next = { ...prev };
            delete next[key];
            return next;
          });
          setProtectingKey(null);
          setProtectError(key);
          setTimeout(() => { setProtectError(null); }, 3000);
        },
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
      <div className="rounded-md border">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead className="w-8" />
              <TableHead>Type</TableHead>
              <TableHead>Backup ID</TableHead>
              <TableHead>Datastore</TableHead>
              <TableHead>Time</TableHead>
              <TableHead className="text-right">Size</TableHead>
              <TableHead>Verified</TableHead>
              <TableHead>Protected</TableHead>
              <TableHead>Owner</TableHead>
              <TableHead className="w-32" />
            </TableRow>
          </TableHeader>
          <TableBody>
            {snapshots.map((snap) => {
              const key = snapKey(snap);
              const isExpanded = expanded.has(key);
              const isEditingThis = editingComment === key;

              return (
                <>
                  <TableRow
                    key={key}
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
                      <Badge
                        variant={
                          snap.backup_type === "vm" ? "default" : "secondary"
                        }
                      >
                        {snap.backup_type.toUpperCase()}
                      </Badge>
                    </TableCell>
                    <TableCell className="font-mono text-sm">
                      {snap.backup_id}
                    </TableCell>
                    <TableCell>{snap.datastore}</TableCell>
                    <TableCell className="text-sm">
                      {formatUnixTime(snap.backup_time)}
                    </TableCell>
                    <TableCell className="text-right font-mono text-sm">
                      {formatBytes(snap.size)}
                    </TableCell>
                    <TableCell>
                      {snap.verified ? (
                        <Badge variant="default" className="bg-green-600">
                          Yes
                        </Badge>
                      ) : (
                        <Badge variant="secondary">No</Badge>
                      )}
                    </TableCell>
                    <TableCell>
                      {isProtected(snap) ? (
                        <Badge variant="default" className="bg-blue-600">
                          <ShieldCheck className="mr-1 h-3 w-3" />
                          Protected
                        </Badge>
                      ) : (
                        <span className="text-muted-foreground">No</span>
                      )}
                    </TableCell>
                    <TableCell className="text-sm text-muted-foreground">
                      {snap.owner || "-"}
                    </TableCell>
                    <TableCell>
                      <div
                        className="flex items-center gap-1"
                        onClick={(e) => {
                          e.stopPropagation();
                        }}
                      >
                        <Button
                          variant="ghost"
                          size="sm"
                          title={
                            isProtected(snap) ? "Unprotect" : "Protect"
                          }
                          onClick={() => {
                            handleProtect(snap);
                          }}
                          disabled={protectingKey === key}
                          className={protectError === key ? "text-destructive" : ""}
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
                    <TableRow key={`${key}-detail`}>
                      <TableCell colSpan={10} className="bg-muted/30 px-8 py-4">
                        <div className="grid grid-cols-2 gap-x-8 gap-y-2 text-sm md:grid-cols-4">
                          <div>
                            <span className="text-muted-foreground">
                              Owner:
                            </span>{" "}
                            {snap.owner || "-"}
                          </div>
                          <div>
                            <span className="text-muted-foreground">
                              Protected:
                            </span>{" "}
                            {isProtected(snap) ? "Yes" : "No"}
                          </div>
                          <div>
                            <span className="text-muted-foreground">
                              Verified:
                            </span>{" "}
                            {snap.verified ? "Yes" : "No"}
                          </div>
                          <div>
                            <span className="text-muted-foreground">
                              Size:
                            </span>{" "}
                            {formatBytes(snap.size)}
                          </div>
                          <div>
                            <span className="text-muted-foreground">
                              Backup Time:
                            </span>{" "}
                            {formatUnixTime(snap.backup_time)}
                          </div>
                          <div>
                            <span className="text-muted-foreground">
                              Last Seen:
                            </span>{" "}
                            {snap.last_seen_at
                              ? new Date(snap.last_seen_at).toLocaleString()
                              : "-"}
                          </div>
                          <div className="col-span-2 flex items-center gap-2">
                            <span className="text-muted-foreground">
                              Comment:
                            </span>
                            {isEditingThis ? (
                              <div
                                className="flex items-center gap-1"
                                onClick={(e) => {
                                  e.stopPropagation();
                                }}
                              >
                                <Input
                                  className="h-7 w-48 text-sm"
                                  value={commentValue}
                                  onChange={(e) => {
                                    setCommentValue(e.target.value);
                                  }}
                                  onKeyDown={(e) => {
                                    if (e.key === "Enter") saveComment(snap);
                                    if (e.key === "Escape")
                                      setEditingComment(null);
                                  }}
                                  autoFocus
                                />
                                <Button
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
                        </div>
                      </TableCell>
                    </TableRow>
                  )}
                </>
              );
            })}
          </TableBody>
        </Table>
      </div>
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
