import { useState, useEffect, useMemo, useCallback } from "react";
import {
  useReactTable,
  getCoreRowModel,
  getSortedRowModel,
  getPaginationRowModel,
  getFilteredRowModel,
  flexRender,
  createColumnHelper,
  type SortingState,
  type ColumnDef,
  type VisibilityState,
  type RowSelectionState,
} from "@tanstack/react-table";
import { Link } from "react-router-dom";
import { ArrowUpDown, ChevronLeft, ChevronRight } from "lucide-react";
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
import { StatusBadge } from "./StatusBadge";
import { ResourceTypeBadge } from "./ResourceTypeBadge";
import { MetricMiniBar } from "./MetricMiniBar";
import { SearchBar } from "./SearchBar";
import { ColumnToggle } from "./ColumnToggle";
import { BulkActionToolbar } from "./BulkActionToolbar";
import { applyFilter } from "../lib/search-parser";
import {
  loadColumnVisibility,
  saveColumnVisibility,
  getDefaultColumnVisibility,
} from "../lib/column-presets";
import type { InventoryRow, ParsedQuery } from "../types/inventory";

function formatUptime(seconds: number): string {
  if (seconds <= 0) return "--";
  const days = Math.floor(seconds / 86400);
  const hours = Math.floor((seconds % 86400) / 3600);
  if (days > 0) return `${String(days)}d ${String(hours)}h`;
  const mins = Math.floor((seconds % 3600) / 60);
  return hours > 0 ? `${String(hours)}h ${String(mins)}m` : `${String(mins)}m`;
}

function formatBytes(bytes: number): string {
  if (bytes === 0) return "0 B";
  const units = ["B", "KB", "MB", "GB", "TB"];
  const i = Math.floor(Math.log(bytes) / Math.log(1024));
  const val = bytes / Math.pow(1024, i);
  return `${val.toFixed(val >= 100 ? 0 : 1)} ${units[i] ?? ""}`;
}

const columnHelper = createColumnHelper<InventoryRow>();

function buildColumns(): ColumnDef<InventoryRow>[] {
  return [
    columnHelper.display({
      id: "select",
      header: ({ table }) => (
        <Checkbox
          checked={
            table.getIsAllPageRowsSelected() ||
            (table.getIsSomePageRowsSelected() && "indeterminate")
          }
          onCheckedChange={(value) => {
            table.toggleAllPageRowsSelected(Boolean(value));
          }}
          aria-label="Select all"
        />
      ),
      cell: ({ row }) => (
        <Checkbox
          checked={row.getIsSelected()}
          onCheckedChange={(value) => {
            row.toggleSelected(Boolean(value));
          }}
          aria-label="Select row"
        />
      ),
      enableSorting: false,
      enableHiding: false,
    }) as ColumnDef<InventoryRow>,
    columnHelper.accessor("type", {
      header: "Type",
      cell: ({ row, getValue }) => <ResourceTypeBadge type={getValue()} template={row.original.template} />,
      enableHiding: true,
    }) as ColumnDef<InventoryRow>,
    columnHelper.accessor("name", {
      header: ({ column }) => (
        <Button
          variant="ghost"
          size="sm"
          className="-ml-3 h-8"
          onClick={() => { column.toggleSorting(column.getIsSorted() === "asc"); }}
        >
          Name
          <ArrowUpDown className="ml-1 h-3 w-3" />
        </Button>
      ),
      cell: ({ row, getValue }) => {
        const r = row.original;
        if (r.type === "node") {
          return <span className="font-medium">{getValue()}</span>;
        }
        return (
          <span className="flex items-center gap-1.5">
            <Link
              to={`/inventory/${r.type}/${r.clusterId}/${r.id}`}
              className={`font-medium hover:underline ${r.template ? "text-amber-700 dark:text-amber-400" : "text-primary"}`}
            >
              {getValue()}
            </Link>
          </span>
        );
      },
      enableHiding: true,
    }) as ColumnDef<InventoryRow>,
    columnHelper.accessor("status", {
      header: "Status",
      cell: ({ getValue }) => <StatusBadge status={getValue()} />,
      enableHiding: true,
    }) as ColumnDef<InventoryRow>,
    columnHelper.accessor("clusterName", {
      header: ({ column }) => (
        <Button
          variant="ghost"
          size="sm"
          className="-ml-3 h-8"
          onClick={() => { column.toggleSorting(column.getIsSorted() === "asc"); }}
        >
          Cluster
          <ArrowUpDown className="ml-1 h-3 w-3" />
        </Button>
      ),
      enableHiding: true,
    }) as ColumnDef<InventoryRow>,
    columnHelper.accessor("nodeName", {
      header: ({ column }) => (
        <Button
          variant="ghost"
          size="sm"
          className="-ml-3 h-8"
          onClick={() => { column.toggleSorting(column.getIsSorted() === "asc"); }}
        >
          Node
          <ArrowUpDown className="ml-1 h-3 w-3" />
        </Button>
      ),
      enableHiding: true,
    }) as ColumnDef<InventoryRow>,
    columnHelper.accessor("vmid", {
      header: ({ column }) => (
        <Button
          variant="ghost"
          size="sm"
          className="-ml-3 h-8"
          onClick={() => { column.toggleSorting(column.getIsSorted() === "asc"); }}
        >
          VMID
          <ArrowUpDown className="ml-1 h-3 w-3" />
        </Button>
      ),
      cell: ({ getValue }) => {
        const val = getValue();
        return val !== null ? val : "--";
      },
      enableHiding: true,
    }) as ColumnDef<InventoryRow>,
    columnHelper.accessor("cpuCount", {
      header: "CPUs",
      enableHiding: true,
    }) as ColumnDef<InventoryRow>,
    columnHelper.accessor("memTotal", {
      header: "Memory",
      cell: ({ getValue }) => formatBytes(getValue()),
      enableHiding: true,
    }) as ColumnDef<InventoryRow>,
    columnHelper.accessor("diskTotal", {
      header: "Disk",
      cell: ({ getValue }) => formatBytes(getValue()),
      enableHiding: true,
    }) as ColumnDef<InventoryRow>,
    columnHelper.accessor("cpuPercent", {
      header: ({ column }) => (
        <Button
          variant="ghost"
          size="sm"
          className="-ml-3 h-8"
          onClick={() => { column.toggleSorting(column.getIsSorted() === "asc"); }}
        >
          CPU %
          <ArrowUpDown className="ml-1 h-3 w-3" />
        </Button>
      ),
      cell: ({ getValue }) => <MetricMiniBar value={getValue()} />,
      sortUndefined: "last",
      enableHiding: true,
    }) as ColumnDef<InventoryRow>,
    columnHelper.accessor("memPercent", {
      header: ({ column }) => (
        <Button
          variant="ghost"
          size="sm"
          className="-ml-3 h-8"
          onClick={() => { column.toggleSorting(column.getIsSorted() === "asc"); }}
        >
          Mem %
          <ArrowUpDown className="ml-1 h-3 w-3" />
        </Button>
      ),
      cell: ({ getValue }) => <MetricMiniBar value={getValue()} />,
      sortUndefined: "last",
      enableHiding: true,
    }) as ColumnDef<InventoryRow>,
    columnHelper.accessor("uptime", {
      header: ({ column }) => (
        <Button
          variant="ghost"
          size="sm"
          className="-ml-3 h-8"
          onClick={() => { column.toggleSorting(column.getIsSorted() === "asc"); }}
        >
          Uptime
          <ArrowUpDown className="ml-1 h-3 w-3" />
        </Button>
      ),
      cell: ({ getValue }) => (
        <span className="text-sm tabular-nums">{formatUptime(getValue())}</span>
      ),
      enableHiding: true,
    }) as ColumnDef<InventoryRow>,
    columnHelper.accessor("tags", {
      header: "Tags",
      cell: ({ getValue }) => {
        const tags = getValue();
        if (!tags) return null;
        return (
          <span className="text-xs text-muted-foreground">{tags}</span>
        );
      },
      enableHiding: true,
    }) as ColumnDef<InventoryRow>,
    columnHelper.accessor("haState", {
      header: "HA State",
      cell: ({ getValue }) => {
        const val = getValue();
        return val ? <span className="text-xs">{val}</span> : "--";
      },
      enableHiding: true,
    }) as ColumnDef<InventoryRow>,
    columnHelper.accessor("pool", {
      header: "Pool",
      cell: ({ getValue }) => getValue() || "--",
      enableHiding: true,
    }) as ColumnDef<InventoryRow>,
    columnHelper.accessor("template", {
      header: "Template",
      cell: ({ getValue }) => (getValue() ? "Yes" : "No"),
      enableHiding: true,
    }) as ColumnDef<InventoryRow>,
  ];
}

interface ResourceTableProps {
  data: InventoryRow[];
}

export function ResourceTable({ data }: ResourceTableProps) {
  const columns = useMemo(() => buildColumns(), []);
  const [sorting, setSorting] = useState<SortingState>([]);
  const [rowSelection, setRowSelection] = useState<RowSelectionState>({});
  const [query, setQuery] = useState<ParsedQuery>({ filters: [], freeText: "" });

  const savedVisibility = useMemo(() => {
    const saved = loadColumnVisibility();
    const defaults = getDefaultColumnVisibility();
    return { ...defaults, ...saved };
  }, []);

  const [columnVisibility, setColumnVisibility] = useState<VisibilityState>(savedVisibility);

  // Persist column visibility changes
  useEffect(() => {
    saveColumnVisibility(columnVisibility);
  }, [columnVisibility]);

  const filteredData = useMemo(
    () => applyFilter(data, query),
    [data, query],
  );

  const handleQueryChange = useCallback((parsed: ParsedQuery) => {
    setQuery(parsed);
  }, []);

  const table = useReactTable({
    data: filteredData,
    columns,
    state: {
      sorting,
      columnVisibility,
      rowSelection,
    },
    getRowId: (row) => row.key,
    onSortingChange: setSorting,
    onColumnVisibilityChange: setColumnVisibility,
    onRowSelectionChange: setRowSelection,
    getCoreRowModel: getCoreRowModel(),
    getSortedRowModel: getSortedRowModel(),
    getPaginationRowModel: getPaginationRowModel(),
    getFilteredRowModel: getFilteredRowModel(),
    initialState: {
      pagination: { pageSize: 25 },
    },
  });

  return (
    <div className="space-y-4">
      <div className="flex items-start justify-between gap-4">
        <div className="flex-1">
          <SearchBar onQueryChange={handleQueryChange} />
        </div>
        <ColumnToggle table={table} />
      </div>

      <BulkActionToolbar table={table} />

      <div className="rounded-md border">
        <Table>
          <TableHeader>
            {table.getHeaderGroups().map((headerGroup) => (
              <TableRow key={headerGroup.id}>
                {headerGroup.headers.map((header) => (
                  <TableHead key={header.id}>
                    {header.isPlaceholder
                      ? null
                      : flexRender(
                          header.column.columnDef.header,
                          header.getContext(),
                        )}
                  </TableHead>
                ))}
              </TableRow>
            ))}
          </TableHeader>
          <TableBody>
            {table.getRowModel().rows.length > 0 ? (
              table.getRowModel().rows.map((row) => (
                <TableRow
                  key={row.id}
                  data-state={row.getIsSelected() ? "selected" : undefined}
                >
                  {row.getVisibleCells().map((cell) => (
                    <TableCell key={cell.id}>
                      {flexRender(
                        cell.column.columnDef.cell,
                        cell.getContext(),
                      )}
                    </TableCell>
                  ))}
                </TableRow>
              ))
            ) : (
              <TableRow>
                <TableCell
                  colSpan={columns.length}
                  className="h-24 text-center text-muted-foreground"
                >
                  No resources found.
                </TableCell>
              </TableRow>
            )}
          </TableBody>
        </Table>
      </div>

      {/* Pagination */}
      <div className="flex items-center justify-between">
        <p className="text-sm text-muted-foreground">
          {String(filteredData.length)} resource{filteredData.length !== 1 ? "s" : ""} total
          {data.length !== filteredData.length && ` (${String(data.length)} unfiltered)`}
        </p>
        <div className="flex items-center gap-2">
          <p className="text-sm text-muted-foreground">
            Page {String(table.getState().pagination.pageIndex + 1)} of{" "}
            {String(table.getPageCount() || 1)}
          </p>
          <Button
            variant="outline"
            size="sm"
            onClick={() => { table.previousPage(); }}
            disabled={!table.getCanPreviousPage()}
          >
            <ChevronLeft className="h-4 w-4" />
          </Button>
          <Button
            variant="outline"
            size="sm"
            onClick={() => { table.nextPage(); }}
            disabled={!table.getCanNextPage()}
          >
            <ChevronRight className="h-4 w-4" />
          </Button>
        </div>
      </div>
    </div>
  );
}
