import { useState, useEffect, useMemo, useCallback, useRef } from "react";
import { createPortal } from "react-dom";
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
import { ArrowUpDown, ChevronLeft, ChevronRight, Monitor } from "lucide-react";
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
import { VMContextDialogs } from "@/features/vms/components/VMContextDialogs";
import { lifecycleActions, managementActions } from "@/features/vms/lib/vm-action-defs";
import { useVMAction } from "@/features/vms/api/vm-queries";
import {
  useVMContextMenuStore,
  type VMContextTarget,
} from "@/stores/vm-context-menu-store";
import { useTaskLogStore } from "@/stores/task-log-store";
import { useConsoleStore } from "@/stores/console-store";
import type { VMAction } from "@/features/vms/types/vm";
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

function toContextTarget(row: InventoryRow): VMContextTarget | null {
  if (row.type === "node" || row.vmid === null) return null;
  return {
    clusterId: row.clusterId,
    resourceId: row.id,
    vmid: row.vmid,
    name: row.name,
    kind: row.type === "ct" ? "ct" : "vm",
    status: row.status,
    currentNode: row.nodeName,
  };
}

interface MenuState {
  target: VMContextTarget;
  x: number;
  y: number;
}

function RowContextMenu({ menu, onClose }: { menu: MenuState; onClose: () => void }) {
  const ref = useRef<HTMLDivElement>(null);
  const { openClone, openMigrate, openDestroy, openConfirmAction } =
    useVMContextMenuStore();
  const setPanelOpen = useTaskLogStore((s) => s.setPanelOpen);
  const setFocusedTask = useTaskLogStore((s) => s.setFocusedTask);
  const actionMutation = useVMAction();
  const addTab = useConsoleStore((s) => s.addTab);
  const showConsole = useConsoleStore((s) => s.showConsole);

  const { target } = menu;
  const normalizedStatus = target.status.toLowerCase();

  const visibleLifecycle = lifecycleActions.filter((a) =>
    a.showWhen(normalizedStatus, target.kind),
  );
  const visibleManagement = managementActions.filter((a) =>
    a.showWhen(normalizedStatus, target.kind),
  );

  useEffect(() => {
    function handleClickOutside(e: MouseEvent) {
      if (ref.current && !ref.current.contains(e.target as Node)) {
        onClose();
      }
    }
    function handleEscape(e: KeyboardEvent) {
      if (e.key === "Escape") onClose();
    }
    document.addEventListener("mousedown", handleClickOutside);
    document.addEventListener("keydown", handleEscape);
    return () => {
      document.removeEventListener("mousedown", handleClickOutside);
      document.removeEventListener("keydown", handleEscape);
    };
  }, [onClose]);

  // Clamp position so menu doesn't overflow viewport
  const menuWidth = 176;
  const menuHeight = (visibleLifecycle.length + visibleManagement.length + 3) * 32;
  const x = Math.min(menu.x, window.innerWidth - menuWidth - 8);
  const y = Math.min(menu.y, window.innerHeight - menuHeight - 8);

  function handleLifecycleAction(action: VMAction, needsConfirm: boolean, label: string) {
    if (needsConfirm) {
      openConfirmAction(target, action, label);
    } else {
      actionMutation.mutate(
        {
          clusterId: target.clusterId,
          resourceId: target.resourceId,
          kind: target.kind,
          action,
        },
        {
          onSuccess: (data) => {
            setFocusedTask({
              clusterId: target.clusterId,
              upid: data.upid,
              description: `${label} ${target.name}`,
            });
            setPanelOpen(true);
          },
        },
      );
    }
    onClose();
  }

  function handleManagementAction(action: "clone" | "migrate" | "destroy") {
    if (action === "clone") openClone(target);
    if (action === "migrate") openMigrate(target);
    if (action === "destroy") openDestroy(target);
    onClose();
  }

  function handleOpenConsole() {
    const type = target.kind === "ct" ? ("ct_vnc" as const) : ("vm_vnc" as const);
    const labelPrefix = target.kind === "ct" ? "CT" : "VNC";
    addTab({
      clusterID: target.clusterId,
      node: target.currentNode,
      vmid: target.vmid,
      type,
      label: `${labelPrefix}: ${target.name}`,
      resourceId: target.resourceId,
      kind: target.kind,
    });
    showConsole();
    onClose();
  }

  return createPortal(
    <div
      ref={ref}
      className="fixed z-50 min-w-[11rem] overflow-hidden rounded-md border bg-popover p-1 text-popover-foreground shadow-md animate-in fade-in-0 zoom-in-95"
      style={{ left: x, top: y }}
    >
      <div className="px-2 py-1.5 text-xs font-semibold text-muted-foreground">
        {String(target.vmid)} {target.name}
      </div>

      {visibleLifecycle.map((config) => (
        <button
          key={config.action}
          className="relative flex w-full cursor-default select-none items-center rounded-sm px-2 py-1.5 text-sm outline-none hover:bg-accent hover:text-accent-foreground"
          onClick={() => { handleLifecycleAction(config.action, config.needsConfirm, config.label); }}
        >
          <span className="mr-2">{config.icon}</span>
          {config.label}
        </button>
      ))}

      {normalizedStatus === "running" && (
        <>
          <div className="-mx-1 my-1 h-px bg-border" />
          <button
            className="relative flex w-full cursor-default select-none items-center rounded-sm px-2 py-1.5 text-sm outline-none hover:bg-accent hover:text-accent-foreground"
            onClick={handleOpenConsole}
          >
            <span className="mr-2"><Monitor className="h-4 w-4" /></span>
            Console
          </button>
        </>
      )}

      {visibleManagement.length > 0 && <div className="-mx-1 my-1 h-px bg-border" />}

      {visibleManagement.map((config) => (
        <button
          key={config.action}
          className={`relative flex w-full cursor-default select-none items-center rounded-sm px-2 py-1.5 text-sm outline-none hover:bg-accent hover:text-accent-foreground ${
            config.variant === "destructive" ? "text-destructive" : ""
          }`}
          onClick={() => { handleManagementAction(config.action); }}
        >
          <span className="mr-2">{config.icon}</span>
          {config.label}
        </button>
      ))}
    </div>,
    document.body,
  );
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
  const [contextMenu, setContextMenu] = useState<MenuState | null>(null);

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

  const handleCloseMenu = useCallback(() => {
    setContextMenu(null);
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
              table.getRowModel().rows.map((row) => {
                const target = toContextTarget(row.original);
                return (
                  <TableRow
                    key={row.id}
                    data-state={row.getIsSelected() ? "selected" : undefined}
                    onContextMenu={target ? (e) => {
                      e.preventDefault();
                      setContextMenu({ target, x: e.clientX, y: e.clientY });
                    } : undefined}
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
                );
              })
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

      {contextMenu && (
        <RowContextMenu menu={contextMenu} onClose={handleCloseMenu} />
      )}

      <VMContextDialogs />
    </div>
  );
}
