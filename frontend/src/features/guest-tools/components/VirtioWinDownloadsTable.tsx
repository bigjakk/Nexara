import { Panel } from "@/components/page/Panel";
import { TableBody, TableHeader, TableRow } from "@/components/ui/table";
import { Badge, type BadgeVariant } from "@/components/ui/badge";
import { Skeleton } from "@/components/ui/skeleton";
import { byId } from "@/hooks/useTableSort";
import type { ColumnDef } from "@/hooks/useColumnLayout";
import { useDataTable } from "@/hooks/useDataTable";
import { DataTableFrame } from "@/components/DataTableFrame";
import { DataTableHeadRow } from "@/components/DataTableHeadCells";
import { DataTableCells } from "@/components/DataTableCells";
import { ResetColumnsButton } from "@/components/ResetColumnsButton";
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

function statusVariant(status: VirtioWinDownloadStatus): BadgeVariant {
  switch (status) {
    // Healthy is quiet: a dot, not a coloured pill.
    case "succeeded":
      return "healthy";
    case "failed":
      return "destructive";
    case "running":
    case "pending":
      return "secondary";
  }
}

/** Each column sorts on what its cell SHOWS, not on the underlying field. */
const COLUMNS: ColumnDef<VirtioWinDownload, DownloadSortKey>[] = [
  {
    key: "version",
    label: "Version",
    width: 150,
    sortValue: (d) => d.version,
    cell: (d) => <span className="font-medium">{d.version}</span>,
  },
  {
    key: "storage",
    label: "Storage",
    width: 130,
    sortValue: (d) => d.storage,
    cell: (d) => d.storage,
  },
  {
    key: "node",
    label: "Node",
    width: 130,
    sortValue: (d) => d.node,
    cell: (d) => d.node,
  },
  {
    key: "status",
    label: "Status",
    width: 260,
    sortValue: (d) => d.status,
    // The error string is the only diagnostic this table offers.
    wrap: true,
    cell: (d) => (
      <div className="space-y-1">
        <Badge variant={statusVariant(d.status)}>{d.status}</Badge>
        {d.error ? (
          <p className="break-words text-xs text-destructive">{d.error}</p>
        ) : null}
      </div>
    ),
  },
  {
    key: "trigger",
    label: "Trigger",
    width: 130,
    sortValue: (d) => d.triggered_by,
    cell: (d) => (
      <span className="text-muted-foreground">{d.triggered_by}</span>
    ),
  },
  {
    key: "started",
    label: "Started",
    width: 190,
    sortValue: (d) => new Date(d.started_at).getTime(),
    cell: (d) => (
      <span className="text-muted-foreground">
        {new Date(d.started_at).toLocaleString()}
      </span>
    ),
  },
];

interface VirtioWinDownloadsTableProps {
  clusterId: string;
}

export function VirtioWinDownloadsTable({
  clusterId,
}: VirtioWinDownloadsTableProps) {
  const { data: downloads, isLoading } = useVirtioWinDownloads(clusterId);
  const {
    layout,
    rows: sorted,
    toggle: toggleSort,
    directionFor,
  } = useDataTable("virtio-win-downloads", COLUMNS, downloads ?? [], byId);

  if (isLoading) {
    return <Skeleton className="h-48 w-full" />;
  }

  return (
    <Panel
      flush
      title="Download history"
      actions={<ResetColumnsButton layout={layout} />}
    >
      {sorted.length === 0 ? (
        <p className="px-[18px] py-3 text-sm text-muted-foreground">
          No virtio-win downloads yet.
        </p>
      ) : (
        <DataTableFrame layout={layout}>
          <TableHeader>
            <DataTableHeadRow
              layout={layout}
              directionFor={directionFor}
              onSort={toggleSort}
            />
          </TableHeader>
          <TableBody>
            {sorted.map((d) => (
              <TableRow key={d.id}>
                <DataTableCells row={d} layout={layout} ctx={undefined} />
              </TableRow>
            ))}
          </TableBody>
        </DataTableFrame>
      )}
    </Panel>
  );
}
