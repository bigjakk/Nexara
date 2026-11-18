import { useEffect, useState } from "react";
import { Trash2 } from "lucide-react";
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
import { useQueryClient } from "@tanstack/react-query";
import type { StorageContentItem } from "../types/storage";
import { useDeleteContent } from "../api/storage-queries";
import { usePermissions } from "@/hooks/usePermissions";
import { sessionScope } from "@/lib/api-client";
import { formatBytes, formatDateTime } from "@/lib/format";
import { contentKindName } from "../lib/store-rows";
import { DateTime } from "@/components/DateTime";

interface StorageContentTableProps {
  items: StorageContentItem[];
  clusterId: string;
  storageId: string;
  /** What to say when there is nothing to list. */
  emptyText?: string | undefined;
}

function formatDate(unixTs: number): string {
  return formatDateTime(unixTs * 1000);
}

/**
 * A store's content, with its own gutters: it sits flush in a panel, the
 * selection bar and the table running edge to edge.
 */
export function StorageContentTable({
  items,
  clusterId,
  storageId,
  emptyText = "This store holds no content yet.",
}: StorageContentTableProps) {
  const deleteMutation = useDeleteContent();
  const queryClient = useQueryClient();
  // Deleting a volume takes delete:storage, and the selection is only for
  // that: without it the table has no checkboxes and no Delete.
  const canDelete = usePermissions().hasPermission("delete", "storage");
  const [deletingVolid, setDeletingVolid] = useState<string | null>(null);
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [bulkDeleting, setBulkDeleting] = useState(false);
  const [bulkProgress, setBulkProgress] = useState<{
    done: number;
    failed: number;
  } | null>(null);

  // Drop selections that no longer exist in items (e.g. after a delete or
  // when the parent re-tabs).
  useEffect(() => {
    setSelected((prev) => {
      const valid = new Set(items.map((i) => i.volid));
      let changed = false;
      const next = new Set<string>();
      for (const v of prev) {
        if (valid.has(v)) next.add(v);
        else changed = true;
      }
      return changed ? next : prev;
    });
  }, [items]);

  function toggleAll() {
    if (selected.size === items.length) setSelected(new Set());
    else setSelected(new Set(items.map((i) => i.volid)));
  }

  function toggleRow(volid: string) {
    setSelected((prev) => {
      const next = new Set(prev);
      if (next.has(volid)) next.delete(volid);
      else next.add(volid);
      return next;
    });
  }

  function handleDelete(volid: string) {
    if (!confirm(`Delete ${volid}?`)) return;
    setDeletingVolid(volid);
    deleteMutation.mutate(
      { clusterId, storageId, volume: volid },
      {
        onSuccess: (data) => {
          if (data.upid) {
            void queryClient.invalidateQueries({
              queryKey: ["recent-activity"],
            });
          }
        },
        onSettled: () => {
          setDeletingVolid(null);
        },
      },
    );
  }

  async function handleBulkDelete() {
    const volids = Array.from(selected);
    if (volids.length === 0) return;
    if (
      !confirm(
        `Delete ${String(volids.length)} ${volids.length === 1 ? "item" : "items"}?\n\nThis cannot be undone.`,
      )
    ) {
      return;
    }
    setBulkDeleting(true);
    setBulkProgress({ done: 0, failed: 0 });

    // The session that chose these volumes. The loop outlives the table, and a
    // sign-out with it: once that session has ended, nothing more is deleted
    // for it — each is a volume gone for good, and the request would carry
    // whoever signs in next's token.
    const ended = sessionScope();

    let done = 0;
    let failed = 0;
    for (const volid of volids) {
      if (ended()) break;
      try {
        await deleteMutation.mutateAsync({
          clusterId,
          storageId,
          volume: volid,
        });
        done += 1;
      } catch {
        failed += 1;
      }
      setBulkProgress({ done, failed });
    }
    setBulkDeleting(false);
    setSelected(new Set());
    void queryClient.invalidateQueries({ queryKey: ["recent-activity"] });
  }

  if (items.length === 0) {
    return (
      <p className="px-[18px] py-8 text-center text-sm text-muted-foreground">
        {emptyText}
      </p>
    );
  }

  const allSelected = selected.size === items.length;
  const someSelected = selected.size > 0 && selected.size < items.length;

  return (
    <div>
      {selected.size > 0 && (
        <div className="flex flex-wrap items-center justify-between gap-2 border-y bg-primary/[0.06] px-[18px] py-2 text-sm">
          <span>
            {selected.size} {selected.size === 1 ? "item" : "items"} selected
            {bulkProgress && bulkDeleting
              ? `, deleting ${String(bulkProgress.done + bulkProgress.failed + 1)} of ${String(selected.size)}`
              : ""}
          </span>
          <div className="flex items-center gap-2">
            <Button
              size="sm"
              variant="outline"
              onClick={() => {
                setSelected(new Set());
              }}
              disabled={bulkDeleting}
            >
              Clear
            </Button>
            <Button
              size="sm"
              variant="destructive"
              onClick={() => {
                void handleBulkDelete();
              }}
              disabled={bulkDeleting}
            >
              <Trash2 className="mr-1.5 h-3.5 w-3.5" />
              Delete selected
            </Button>
          </div>
        </div>
      )}

      <Table>
        <TableHeader>
          <TableRow>
            <TableHead className="w-10">
              {canDelete && (
                <Checkbox
                  checked={
                    allSelected ? true : someSelected ? "indeterminate" : false
                  }
                  onCheckedChange={toggleAll}
                  aria-label="Select all items"
                />
              )}
            </TableHead>
            <TableHead>Volume</TableHead>
            <TableHead className="hidden md:table-cell">Kind</TableHead>
            <TableHead className="hidden md:table-cell">Format</TableHead>
            <TableHead className="text-right">Size</TableHead>
            <TableHead className="hidden md:table-cell">Created</TableHead>
            <TableHead className="hidden md:table-cell">Guest</TableHead>
            <TableHead className="w-12" />
          </TableRow>
        </TableHeader>
        <TableBody>
          {items.map((item) => (
            <TableRow
              key={item.volid}
              data-state={selected.has(item.volid) ? "selected" : undefined}
            >
              <TableCell>
                {canDelete && (
                  <Checkbox
                    checked={selected.has(item.volid)}
                    onCheckedChange={() => {
                      toggleRow(item.volid);
                    }}
                    aria-label={`Select ${item.volid}`}
                  />
                )}
              </TableCell>
              <TableCell className="max-w-[28rem]">
                <div
                  className="font-mono text-[12.5px] [overflow-wrap:anywhere] md:truncate"
                  title={item.volid}
                >
                  {item.volid}
                </div>
                {/* A phone has no room for these as columns. */}
                <div className="mt-0.5 text-[12.5px] text-muted-foreground md:hidden">
                  {[
                    contentKindName(item.content),
                    item.format,
                    ...(item.ctime === 0 ? [] : [formatDate(item.ctime)]),
                    ...(item.vmid ? [`guest ${String(item.vmid)}`] : []),
                  ].join(", ")}
                </div>
              </TableCell>
              <TableCell className="hidden whitespace-nowrap md:table-cell">
                {contentKindName(item.content)}
              </TableCell>
              <TableCell className="hidden text-muted-foreground md:table-cell">
                {item.format}
              </TableCell>
              <TableCell className="whitespace-nowrap text-right tabular-nums">
                {formatBytes(item.size)}
              </TableCell>
              <TableCell className="hidden whitespace-nowrap text-muted-foreground md:table-cell">
                <DateTime value={item.ctime * 1000} />
              </TableCell>
              <TableCell className="hidden tabular-nums text-muted-foreground md:table-cell">
                {item.vmid ? item.vmid : "—"}
              </TableCell>
              <TableCell>
                {canDelete && (
                  <div className="flex items-center justify-end">
                    <Button
                      aria-label={`Delete ${item.volid}`}
                      variant="ghost"
                      size="icon"
                      className="h-7 w-7 text-muted-foreground hover:text-destructive"
                      onClick={() => {
                        handleDelete(item.volid);
                      }}
                      disabled={deletingVolid === item.volid || bulkDeleting}
                    >
                      <Trash2 className="h-4 w-4" />
                    </Button>
                  </div>
                )}
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </div>
  );
}
