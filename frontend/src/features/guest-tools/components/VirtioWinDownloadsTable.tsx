import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import {
  Table,
  TableBody,
  TableCell,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { Badge } from "@/components/ui/badge";
import { Skeleton } from "@/components/ui/skeleton";
import { History } from "lucide-react";
import { useTableSort, byId } from "@/hooks/useTableSort";
import type { SortAccessors } from "@/hooks/useTableSort";
import { SortableTableHead } from "@/components/SortableTableHead";
import { useVirtioWinDownloads } from "../api/virtio-win-queries";
import type {
  VirtioWinDownload,
  VirtioWinDownloadStatus,
} from "../types/virtio-win";

type DownloadSortKey =
  | "version"
  | "storage"
  | "node"
  | "status"
  | "trigger"
  | "started";

/** Each accessor sorts on what its cell SHOWS, not on the underlying field. */
const DOWNLOAD_SORT: SortAccessors<VirtioWinDownload, DownloadSortKey> = {
  version: (d) => d.version,
  storage: (d) => d.storage,
  node: (d) => d.node,
  status: (d) => d.status,
  trigger: (d) => d.triggered_by,
  started: (d) => new Date(d.started_at).getTime(),
};

function statusVariant(
  status: VirtioWinDownloadStatus,
): "default" | "secondary" | "destructive" | "outline" {
  switch (status) {
    case "succeeded":
      return "default";
    case "failed":
      return "destructive";
    case "running":
    case "pending":
      return "secondary";
    default:
      return "outline";
  }
}

interface VirtioWinDownloadsTableProps {
  clusterId: string;
}

export function VirtioWinDownloadsTable({
  clusterId,
}: VirtioWinDownloadsTableProps) {
  const { data: downloads, isLoading } = useVirtioWinDownloads(clusterId);
  const {
    rows: sorted,
    toggle: toggleSort,
    directionFor,
  } = useTableSort(downloads ?? [], DOWNLOAD_SORT, byId);

  if (isLoading) {
    return <Skeleton className="h-48 w-full" />;
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle className="flex items-center gap-2">
          <History className="h-5 w-5" />
          Download history
        </CardTitle>
      </CardHeader>
      <CardContent>
        {sorted.length === 0 ? (
          <p className="text-sm text-muted-foreground">
            No virtio-win downloads yet.
          </p>
        ) : (
          <div className="overflow-x-auto">
            <Table>
              <TableHeader>
                <TableRow>
                  <SortableTableHead
                    direction={directionFor("version")}
                    onSort={() => {
                      toggleSort("version");
                    }}
                  >
                    Version
                  </SortableTableHead>
                  <SortableTableHead
                    direction={directionFor("storage")}
                    onSort={() => {
                      toggleSort("storage");
                    }}
                  >
                    Storage
                  </SortableTableHead>
                  <SortableTableHead
                    direction={directionFor("node")}
                    onSort={() => {
                      toggleSort("node");
                    }}
                  >
                    Node
                  </SortableTableHead>
                  <SortableTableHead
                    direction={directionFor("status")}
                    onSort={() => {
                      toggleSort("status");
                    }}
                  >
                    Status
                  </SortableTableHead>
                  <SortableTableHead
                    direction={directionFor("trigger")}
                    onSort={() => {
                      toggleSort("trigger");
                    }}
                  >
                    Trigger
                  </SortableTableHead>
                  <SortableTableHead
                    direction={directionFor("started")}
                    onSort={() => {
                      toggleSort("started");
                    }}
                  >
                    Started
                  </SortableTableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {sorted.map((d) => (
                  <TableRow key={d.id}>
                    <TableCell className="font-medium">{d.version}</TableCell>
                    <TableCell>{d.storage}</TableCell>
                    <TableCell>{d.node}</TableCell>
                    <TableCell>
                      <div className="space-y-1">
                        <Badge variant={statusVariant(d.status)}>
                          {d.status}
                        </Badge>
                        {d.error ? (
                          <p className="max-w-md break-words text-xs text-destructive">
                            {d.error}
                          </p>
                        ) : null}
                      </div>
                    </TableCell>
                    <TableCell className="text-muted-foreground">
                      {d.triggered_by}
                    </TableCell>
                    <TableCell className="text-muted-foreground">
                      {new Date(d.started_at).toLocaleString()}
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>
        )}
      </CardContent>
    </Card>
  );
}
