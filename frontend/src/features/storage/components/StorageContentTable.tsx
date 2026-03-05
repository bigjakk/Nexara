import { useState } from "react";
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
import type { StorageContentItem } from "../types/storage";
import { useDeleteContent } from "../api/storage-queries";

interface StorageContentTableProps {
  items: StorageContentItem[];
  clusterId: string;
  storageId: string;
}

function formatBytes(bytes: number): string {
  if (bytes === 0) return "0 B";
  const units = ["B", "KB", "MB", "GB", "TB", "PB"];
  const k = 1024;
  const i = Math.floor(Math.log(bytes) / Math.log(k));
  const value = bytes / Math.pow(k, i);
  return `${value.toFixed(1)} ${units[i] ?? "?"}`;
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
  const [deletingVolid, setDeletingVolid] = useState<string | null>(null);

  function handleDelete(volid: string) {
    if (!confirm(`Delete ${volid}?`)) return;
    setDeletingVolid(volid);
    deleteMutation.mutate(
      { clusterId, storageId, volume: volid },
      { onSettled: () => { setDeletingVolid(null); } },
    );
  }

  if (items.length === 0) {
    return (
      <p className="py-8 text-center text-sm text-muted-foreground">
        No content found in this storage pool.
      </p>
    );
  }

  return (
    <Table>
      <TableHeader>
        <TableRow>
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
          <TableRow key={item.volid}>
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
              <Button
                variant="ghost"
                size="icon"
                className="h-7 w-7"
                onClick={() => { handleDelete(item.volid); }}
                disabled={deletingVolid === item.volid}
              >
                <Trash2 className="h-4 w-4 text-destructive" />
              </Button>
            </TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  );
}
