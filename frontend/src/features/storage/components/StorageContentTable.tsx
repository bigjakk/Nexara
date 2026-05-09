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
import { Badge } from "@/components/ui/badge";
import { Checkbox } from "@/components/ui/checkbox";
import { useQueryClient } from "@tanstack/react-query";
import type { StorageContentItem } from "../types/storage";
import { useDeleteContent } from "../api/storage-queries";
import { formatBytes } from "@/lib/format";

interface StorageContentTableProps {
  items: StorageContentItem[];
  clusterId: string;
  storageId: string;
}


function formatDate(unixTs: number): string {
  if (unixTs === 0) return "-";
  return new Date(unixTs * 1000).toLocaleString();
}

function contentBadgeVariant(content: string) {
  switch (content) {
    case "iso":
      return "default" as const;
    case "vztmpl":
      return "secondary" as const;
    case "images":
      return "outline" as const;
    case "backup":
      return "destructive" as const;
    default:
      return "outline" as const;
  }
}

export function StorageContentTable({
  items,
  clusterId,
  storageId,
}: StorageContentTableProps) {
  const deleteMutation = useDeleteContent();
  const queryClient = useQueryClient();
  const [deletingVolid, setDeletingVolid] = useState<string | null>(null);
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [bulkDeleting, setBulkDeleting] = useState(false);
  const [bulkProgress, setBulkProgress] = useState<{ done: number; failed: number } | null>(null);

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
            void queryClient.invalidateQueries({ queryKey: ["recent-activity"] });
          }
        },
        onSettled: () => { setDeletingVolid(null); },
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
    let done = 0;
    let failed = 0;
    for (const volid of volids) {
      try {
        await deleteMutation.mutateAsync({ clusterId, storageId, volume: volid });
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
      <p className="py-8 text-center text-sm text-muted-foreground">
        No content found in this storage pool.
      </p>
    );
  }

  const allSelected = selected.size === items.length;
  const someSelected = selected.size > 0 && selected.size < items.length;

  return (
    <div className="space-y-2">
      {selected.size > 0 && (
        <div className="flex items-center justify-between rounded-md border bg-muted/30 px-3 py-2 text-sm">
          <span>
            {selected.size} {selected.size === 1 ? "item" : "items"} selected
            {bulkProgress && bulkDeleting
              ? ` • deleting ${String(bulkProgress.done + bulkProgress.failed + 1)}/${String(selected.size)}…`
              : ""}
          </span>
          <div className="flex items-center gap-2">
            <Button
              size="sm"
              variant="outline"
              onClick={() => { setSelected(new Set()); }}
              disabled={bulkDeleting}
            >
              Clear
            </Button>
            <Button
              size="sm"
              variant="destructive"
              onClick={() => { void handleBulkDelete(); }}
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
              <Checkbox
                checked={allSelected ? true : someSelected ? "indeterminate" : false}
                onCheckedChange={toggleAll}
                aria-label="Select all items"
              />
            </TableHead>
            <TableHead>Volume ID</TableHead>
            <TableHead>Type</TableHead>
            <TableHead>Format</TableHead>
            <TableHead className="text-right">Size</TableHead>
            <TableHead>Created</TableHead>
            <TableHead>VMID</TableHead>
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
                <Checkbox
                  checked={selected.has(item.volid)}
                  onCheckedChange={() => { toggleRow(item.volid); }}
                  aria-label={`Select ${item.volid}`}
                />
              </TableCell>
              <TableCell className="max-w-[300px] truncate font-mono text-xs">
                {item.volid}
              </TableCell>
              <TableCell>
                <Badge variant={contentBadgeVariant(item.content)}>
                  {item.content}
                </Badge>
              </TableCell>
              <TableCell className="text-xs">{item.format}</TableCell>
              <TableCell className="text-right text-xs">
                {formatBytes(item.size)}
              </TableCell>
              <TableCell className="text-xs">
                {formatDate(item.ctime)}
              </TableCell>
              <TableCell className="text-xs">
                {item.vmid ? item.vmid : "-"}
              </TableCell>
              <TableCell>
                <div className="flex items-center justify-end">
                  <Button
                    variant="ghost"
                    size="icon"
                    className="h-7 w-7"
                    onClick={() => { handleDelete(item.volid); }}
                    disabled={deletingVolid === item.volid || bulkDeleting}
                  >
                    <Trash2 className="h-4 w-4 text-destructive" />
                  </Button>
                </div>
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </div>
  );
}
