import {
  useState,
  useEffect,
  useMemo,
  useCallback,
  useRef,
  type ReactNode,
} from "react";
import { createPortal } from "react-dom";
import {
  useReactTable,
  getCoreRowModel,
  getSortedRowModel,
  getPaginationRowModel,
  getFilteredRowModel,
  flexRender,
  createColumnHelper,
  type Column,
  type SortingState,
  type ColumnDef,
  type VisibilityState,
  type RowSelectionState,
  type OnChangeFn,
} from "@tanstack/react-table";
import { Link } from "react-router-dom";
import { useTranslation } from "react-i18next";
import {
  ArrowDown,
  ArrowUp,
  Box,
  ChevronLeft,
  ChevronRight,
  ChevronsUpDown,
  FileBox,
  Monitor,
  MoreVertical,
  Server,
  type LucideIcon,
} from "lucide-react";
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
import { Segmented } from "@/components/ui/segmented";
import { Panel } from "@/components/page/Panel";
import { StatusIcon } from "@/components/StatusIcon";
import { OSIcon } from "@/components/OSIcon";
import { classifyOS } from "@/lib/os-classify";
import { guestState, isFaultState, isHaltedState } from "@/lib/guest-state";
import { cn } from "@/lib/utils";
import { MetricMiniBar } from "./MetricMiniBar";
import { SearchBar, SearchFilterChips } from "./SearchBar";
import { ColumnToggle } from "./ColumnToggle";
import { BulkActionToolbar } from "./BulkActionToolbar";
import { menuOpener } from "@/components/ui/return-focus";
import {
  actionVerb,
  lifecycleActions,
  managementActions,
  type ManagementAction,
} from "@/features/vms/lib/vm-action-defs";
import { useVMAction } from "@/features/vms/api/vm-queries";
import {
  useVMContextMenuStore,
  type VMContextTarget,
} from "@/stores/vm-context-menu-store";
import { useTaskLogStore } from "@/stores/task-log-store";
import { openConsole } from "@/features/console/lib/open-console";
import type { ConsoleClickKeys } from "@/features/console/lib/console-session";
import { useIsMobile } from "@/hooks/useIsMobile";
import { usePermissions } from "@/hooks/usePermissions";
import { FavoritePlainItem } from "@/features/favorites/components/FavoriteMenuItem";
import type { VMAction } from "@/features/vms/types/vm";
import { applyFilter } from "../lib/search-parser";
import {
  filterByQuickStatus,
  type QuickStatus,
} from "../lib/inventory-summary";
import {
  loadColumnVisibility,
  saveColumnVisibility,
  getDefaultColumnVisibility,
} from "../lib/column-presets";
import type {
  InventoryRow,
  ParsedQuery,
  ResourceType,
} from "../types/inventory";
import { formatBytes, formatBytesPerSecond, formatUptime } from "@/lib/format";

/** What an empty cell shows: present, not emphasised. */
const EMPTY = <span className="text-muted-foreground/60">—</span>;

/** A rate short enough for a cell: no decimal once it has three digits. */
function compactRate(bytesPerSec: number): string {
  const text = formatBytesPerSecond(bytesPerSec);
  const match = /^(\d+\.\d+) (.+)$/.exec(text);
  if (!match?.[1] || !match[2] || Number(match[1]) < 100) return text;
  return `${String(Math.round(Number(match[1])))} ${match[2]}`;
}

/**
 * Both directions of a rate on one line, each in a fixed slot so the two
 * line up down the column. An idle pair is a dash, not two zeros.
 */
function RateCell({
  inBps,
  outBps,
  inLabel,
  outLabel,
}: {
  inBps: number | null;
  outBps: number | null;
  inLabel: "in" | "read";
  outLabel: "out" | "write";
}) {
  if ((inBps ?? 0) === 0 && (outBps ?? 0) === 0) return EMPTY;
  return (
    <span className="flex items-center gap-1 text-[12.5px] tabular-nums text-muted-foreground">
      <span className="flex w-[4.75rem] items-center gap-px">
        <ArrowDown
          className="h-3 w-3 shrink-0 opacity-60"
          aria-label={inLabel}
        />
        {compactRate(inBps ?? 0)}
      </span>
      <span className="flex w-[4.75rem] items-center gap-px">
        <ArrowUp
          className="h-3 w-3 shrink-0 opacity-60"
          aria-label={outLabel}
        />
        {compactRate(outBps ?? 0)}
      </span>
    </span>
  );
}

const TYPE_LABEL: Record<ResourceType, { label: string; icon: LucideIcon }> = {
  vm: { label: "VM", icon: Monitor },
  ct: { label: "CT", icon: Box },
  node: { label: "Node", icon: Server },
};

/** What kind of thing a row is: its OS mark when known, and the kind in words. */
function TypeCell({ row }: { row: InventoryRow }) {
  const isGuest = row.type === "vm" || row.type === "ct";
  const showOS =
    isGuest &&
    (classifyOS(row.ostype) !== "unknown" ||
      classifyOS(row.configOstype) !== "unknown");
  const { label, icon: Icon } =
    isGuest && row.template
      ? { label: "Template", icon: FileBox }
      : TYPE_LABEL[row.type];
  return (
    <span className="flex items-center gap-1.5 text-[12.5px] text-muted-foreground">
      {showOS ? (
        <OSIcon ostype={row.ostype} configOstype={row.configOstype} />
      ) : (
        <Icon aria-hidden="true" className="h-4 w-4 shrink-0 opacity-70" />
      )}
      {label}
    </span>
  );
}

/** States the dot alone says: no word beside the name for these. */
const QUIET_STATES = new Set(["running", "online"]);

/** The state in words, for a row that is not simply running. */
function StateText({ status }: { status: string }) {
  const { t } = useTranslation("vms");
  if (QUIET_STATES.has(status)) return null;
  const halted = isHaltedState(status);
  return (
    <span
      className={cn(
        "shrink-0 text-[12.5px]",
        status === "stopped" || status === "unknown" || status === "asleep"
          ? "text-muted-foreground"
          : status === "offline" || isFaultState(status)
            ? "font-medium text-red-700 dark:text-red-400"
            : "font-medium text-amber-700 dark:text-amber-400",
      )}
    >
      {halted
        ? t(`runState.${status}`)
        : status.charAt(0).toUpperCase() + status.slice(1)}
    </span>
  );
}

/**
 * A heading that sorts. Its arrow hangs in the gap after the label instead of
 * taking width of its own, so a narrow column stays as narrow as its values;
 * the neutral arrows show on hover and focus only.
 */
function SortHeader({
  column,
  children,
}: {
  column: Column<InventoryRow>;
  children: ReactNode;
}) {
  const sorted = column.getIsSorted();
  return (
    <button
      type="button"
      onClick={() => {
        column.toggleSorting(sorted === "asc");
      }}
      className={cn(
        "group relative rounded-sm font-medium outline-hidden transition-colors hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring",
        sorted !== false && "text-foreground",
      )}
    >
      {children}
      <span
        aria-hidden="true"
        className="absolute left-full top-1/2 -translate-y-1/2 pl-0.5"
      >
        {sorted === "asc" ? (
          <ArrowUp className="h-3 w-3" />
        ) : sorted === "desc" ? (
          <ArrowDown className="h-3 w-3" />
        ) : (
          <ChevronsUpDown className="h-3 w-3 opacity-0 transition-opacity group-hover:opacity-50 group-focus-visible:opacity-50" />
        )}
      </span>
    </button>
  );
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
    runState: row.runState,
    currentNode: row.nodeName,
    template: row.template,
  };
}

interface MenuState {
  target: VMContextTarget;
  x: number;
  y: number;
  /** What opened the menu, to give focus back to — see handleCloseMenu. */
  opener: HTMLElement | null;
}

function RowContextMenu({
  menu,
  onClose,
  onDismiss,
}: {
  menu: MenuState;
  onClose: () => void;
  /** A click outside: closes without moving focus — see handleDismissMenu. */
  onDismiss: () => void;
}) {
  const ref = useRef<HTMLDivElement>(null);
  const {
    openSnapshot,
    openClone,
    openCloneToTemplate,
    openDeploy,
    openMigrate,
    openDestroy,
    openConvertToTemplate,
    openConfirmAction,
  } = useVMContextMenuStore();
  const setPanelOpen = useTaskLogStore((s) => s.setPanelOpen);
  const setFocusedTask = useTaskLogStore((s) => s.setFocusedTask);
  const actionMutation = useVMAction();
  const { canConsole } = usePermissions();

  const { target } = menu;
  const normalizedStatus = target.status.toLowerCase();
  const consoleAllowed = canConsole(target.kind === "ct" ? "container" : "vm");

  const visibleLifecycle = lifecycleActions.filter((a) =>
    a.showWhen(guestState(target.status, target.runState), target.kind),
  );
  const visibleManagement = managementActions.filter((a) =>
    a.showWhen(normalizedStatus, target.kind, target.template),
  );

  useEffect(() => {
    function handleClickOutside(e: MouseEvent) {
      if (ref.current && !ref.current.contains(e.target as Node)) {
        onDismiss();
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
  }, [onClose, onDismiss]);

  // Clamp position so menu doesn't overflow viewport
  const menuWidth = 176;
  // +4: the header, the console item, the favorite item, and the separators.
  const menuHeight =
    (visibleLifecycle.length + visibleManagement.length + 4) * 32;
  const x = Math.min(menu.x, window.innerWidth - menuWidth - 8);
  const y = Math.min(menu.y, window.innerHeight - menuHeight - 8);

  function handleLifecycleAction(
    action: VMAction,
    needsConfirm: boolean,
    label: string,
  ) {
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

  function handleManagementAction(action: ManagementAction) {
    if (action === "snapshot") openSnapshot(target);
    if (action === "clone") openClone(target);
    if (action === "clone-to-template") openCloneToTemplate(target);
    if (action === "deploy") openDeploy(target);
    if (action === "migrate") openMigrate(target);
    if (action === "convert-to-template") openConvertToTemplate(target);
    if (action === "destroy") openDestroy(target);
    onClose();
  }

  function handleOpenConsole(keys: ConsoleClickKeys) {
    openConsole(
      {
        clusterID: target.clusterId,
        node: target.currentNode,
        vmid: target.vmid,
        type: target.kind === "ct" ? "ct_vnc" : "vm_vnc",
        resourceId: target.resourceId,
        kind: target.kind,
        guestName: target.name,
      },
      keys,
    );
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
          className="relative flex w-full cursor-default select-none items-center rounded-sm px-2 py-1.5 text-sm outline-hidden hover:bg-accent hover:text-accent-foreground"
          onClick={() => {
            handleLifecycleAction(
              config.action,
              config.needsConfirm,
              actionVerb(config),
            );
          }}
        >
          <span className="mr-2">{config.icon}</span>
          {config.label}
        </button>
      ))}

      {normalizedStatus === "running" && consoleAllowed && (
        <>
          <div className="-mx-1 my-1 h-px bg-border" />
          <button
            className="relative flex w-full cursor-default select-none items-center rounded-sm px-2 py-1.5 text-sm outline-hidden hover:bg-accent hover:text-accent-foreground"
            onClick={handleOpenConsole}
          >
            <span className="mr-2">
              <Monitor className="h-4 w-4" />
            </span>
            Console
          </button>
        </>
      )}

      {/* Guarded like the other separators in this menu: a guest in a state
          with no lifecycle actions and no console would otherwise render a rule
          directly under the header. */}
      {(visibleLifecycle.length > 0 ||
        (normalizedStatus === "running" && consoleAllowed)) && (
        <div className="-mx-1 my-1 h-px bg-border" />
      )}
      {/* Always a guest: toContextTarget returns null for node rows, so this
          menu never opens for one. */}
      <FavoritePlainItem
        target={{
          resource_type: "vm",
          cluster_id: target.clusterId,
          ref: String(target.vmid),
        }}
        onAction={onClose}
      />

      {visibleManagement.length > 0 && (
        <div className="-mx-1 my-1 h-px bg-border" />
      )}

      {visibleManagement.map((config) => (
        <button
          key={config.action}
          className={`relative flex w-full cursor-default select-none items-center rounded-sm px-2 py-1.5 text-sm outline-hidden hover:bg-accent hover:text-accent-foreground ${
            config.variant === "destructive" ? "text-destructive" : ""
          }`}
          onClick={() => {
            handleManagementAction(config.action);
          }}
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

function buildColumns(
  isMobile: boolean,
  singleCluster: boolean,
  openMenu: (
    target: VMContextTarget,
    x: number,
    y: number,
    opener: HTMLElement | null,
  ) => void,
): ColumnDef<InventoryRow>[] {
  const selectColumn = columnHelper.display({
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
  });

  // A per-row kebab opening the same actions menu as a right click: the
  // menu's only way in on a touch screen, and the one a mouse can see.
  const actionsColumn = columnHelper.display({
    id: "actions",
    header: () => <span className="sr-only">Actions</span>,
    cell: ({ row }) => {
      const target = toContextTarget(row.original);
      if (!target) return null;
      return (
        <Button
          variant="ghost"
          size="icon"
          className="h-8 w-8"
          aria-label={`Actions for ${target.name}`}
          onClick={(e) => {
            e.stopPropagation();
            const rect = e.currentTarget.getBoundingClientRect();
            openMenu(target, rect.left, rect.bottom + 4, e.currentTarget);
          }}
        >
          <MoreVertical className="h-4 w-4" />
        </Button>
      );
    },
    enableSorting: false,
    enableHiding: false,
  });

  // A stopped guest measures nothing: a dash, not a row of zeros.
  const live = (row: InventoryRow, value: number | null) =>
    row.status === "stopped" ? null : value;

  const dataColumns = [
    columnHelper.accessor("name", {
      header: ({ column }) => <SortHeader column={column}>Name</SortHeader>,
      cell: ({ row, getValue }) => {
        const r = row.original;
        const state = guestState(r.status, r.runState);
        return (
          <span className="flex min-w-0 items-center gap-2">
            <StatusIcon status={state} />
            {/* One line on a desktop; on a phone the state drops under the
                name, which needs the whole width. */}
            <span className="flex min-w-0 flex-col md:flex-row md:items-center md:gap-2">
              {r.type === "node" ? (
                <span className="truncate font-medium" title={getValue()}>
                  {getValue()}
                </span>
              ) : (
                <Link
                  to={`/inventory/${r.type}/${r.clusterId}/${r.id}`}
                  title={getValue()}
                  className={cn(
                    "truncate font-medium hover:underline",
                    r.template && "text-muted-foreground",
                  )}
                >
                  {getValue()}
                </Link>
              )}
              <StateText status={state} />
            </span>
          </span>
        );
      },
      enableHiding: true,
    }) as ColumnDef<InventoryRow>,
    columnHelper.accessor("type", {
      header: "Type",
      enableSorting: false,
      cell: ({ row }) => <TypeCell row={row.original} />,
      enableHiding: true,
    }) as ColumnDef<InventoryRow>,
    // A blank (a node's VMID, a stopped guest's load) is undefined, not null,
    // for sortUndefined to keep it last both ways: as null it sorted first.
    columnHelper.accessor((row) => row.vmid ?? undefined, {
      id: "vmid",
      header: ({ column }) => <SortHeader column={column}>VMID</SortHeader>,
      cell: ({ getValue }) => {
        const val = getValue();
        return val !== undefined ? (
          <span className="tabular-nums text-muted-foreground">{val}</span>
        ) : (
          EMPTY
        );
      },
      sortUndefined: "last",
      enableHiding: true,
    }) as ColumnDef<InventoryRow>,
    ...(singleCluster
      ? []
      : [
          columnHelper.accessor("clusterName", {
            header: ({ column }) => (
              <SortHeader column={column}>Cluster</SortHeader>
            ),
            cell: ({ getValue }) => (
              <span className="text-muted-foreground">{getValue()}</span>
            ),
            enableHiding: true,
          }) as ColumnDef<InventoryRow>,
        ]),
    columnHelper.accessor("nodeName", {
      header: ({ column }) => <SortHeader column={column}>Node</SortHeader>,
      cell: ({ getValue }) => (
        <span className="text-muted-foreground">{getValue()}</span>
      ),
      enableHiding: true,
    }) as ColumnDef<InventoryRow>,
    columnHelper.accessor("cpuCount", {
      header: "CPUs",
      enableSorting: false,
      enableHiding: true,
    }) as ColumnDef<InventoryRow>,
    columnHelper.accessor("memTotal", {
      header: "Memory size",
      enableSorting: false,
      cell: ({ getValue }) => formatBytes(getValue()),
      enableHiding: true,
    }) as ColumnDef<InventoryRow>,
    columnHelper.accessor("diskTotal", {
      header: "Disk size",
      enableSorting: false,
      cell: ({ getValue }) => formatBytes(getValue()),
      enableHiding: true,
    }) as ColumnDef<InventoryRow>,
    columnHelper.accessor((row) => live(row, row.cpuPercent) ?? undefined, {
      id: "cpuPercent",
      header: ({ column }) => <SortHeader column={column}>CPU</SortHeader>,
      cell: ({ getValue }) => (
        <MetricMiniBar value={getValue() ?? null} bar={!isMobile} />
      ),
      sortUndefined: "last",
      enableHiding: true,
    }) as ColumnDef<InventoryRow>,
    columnHelper.accessor((row) => live(row, row.memPercent) ?? undefined, {
      id: "memPercent",
      header: ({ column }) => <SortHeader column={column}>Memory</SortHeader>,
      cell: ({ getValue }) => (
        <MetricMiniBar value={getValue() ?? null} bar={!isMobile} />
      ),
      sortUndefined: "last",
      enableHiding: true,
    }) as ColumnDef<InventoryRow>,
    columnHelper.accessor(
      (row) =>
        row.netInBps === null && row.netOutBps === null
          ? undefined
          : (row.netInBps ?? 0) + (row.netOutBps ?? 0),
      {
        id: "network",
        header: ({ column }) => (
          <SortHeader column={column}>Network</SortHeader>
        ),
        cell: ({ row }) => (
          <RateCell
            inBps={row.original.netInBps}
            outBps={row.original.netOutBps}
            inLabel="in"
            outLabel="out"
          />
        ),
        sortUndefined: "last",
        enableHiding: true,
      },
    ) as ColumnDef<InventoryRow>,
    columnHelper.accessor(
      (row) =>
        row.diskReadBps === null && row.diskWriteBps === null
          ? undefined
          : (row.diskReadBps ?? 0) + (row.diskWriteBps ?? 0),
      {
        id: "disk",
        header: ({ column }) => (
          <SortHeader column={column}>Disk I/O</SortHeader>
        ),
        cell: ({ row }) => (
          <RateCell
            inBps={row.original.diskReadBps}
            outBps={row.original.diskWriteBps}
            inLabel="read"
            outLabel="write"
          />
        ),
        sortUndefined: "last",
        enableHiding: true,
      },
    ) as ColumnDef<InventoryRow>,
    columnHelper.accessor("uptime", {
      header: ({ column }) => <SortHeader column={column}>Uptime</SortHeader>,
      cell: ({ getValue }) =>
        getValue() > 0 ? (
          <span className="tabular-nums text-muted-foreground">
            {formatUptime(getValue())}
          </span>
        ) : (
          EMPTY
        ),
      enableHiding: true,
    }) as ColumnDef<InventoryRow>,
    columnHelper.accessor("tags", {
      header: "Tags",
      enableSorting: false,
      cell: ({ getValue }) => {
        const tags = getValue();
        if (!tags) return null;
        return (
          <span className="text-[12.5px] text-muted-foreground">{tags}</span>
        );
      },
      enableHiding: true,
    }) as ColumnDef<InventoryRow>,
    columnHelper.accessor("haState", {
      header: "HA state",
      enableSorting: false,
      cell: ({ getValue }) => getValue() || EMPTY,
      enableHiding: true,
    }) as ColumnDef<InventoryRow>,
    columnHelper.accessor("pool", {
      header: "Pool",
      enableSorting: false,
      cell: ({ getValue }) => getValue() || EMPTY,
      enableHiding: true,
    }) as ColumnDef<InventoryRow>,
    columnHelper.accessor("template", {
      header: "Template",
      enableSorting: false,
      cell: ({ getValue }) => (getValue() ? "Yes" : "No"),
      enableHiding: true,
    }) as ColumnDef<InventoryRow>,
  ];

  // The checkbox leads on a desktop; a phone has no bulk selection. The
  // actions button trails on both.
  return isMobile
    ? [...dataColumns, actionsColumn]
    : [selectColumn, ...dataColumns, actionsColumn];
}

const PAGE_SIZE = 25;

/** The selection less any row not among `rows`; the same object when none goes. */
function pruneSelection(
  selection: RowSelectionState,
  rows: InventoryRow[],
): RowSelectionState {
  const keys = new Set(rows.map((r) => r.key));
  const kept = Object.entries(selection).filter(([key]) => keys.has(key));
  return kept.length === Object.keys(selection).length
    ? selection
    : Object.fromEntries(kept);
}

interface ResourceTableProps {
  data: InventoryRow[];
  /**
   * Names the table for assistive tech. The tab or heading above it is what
   * names it on screen, so the panel carries no title of its own.
   */
  label?: string;
  /**
   * Every row is in the same cluster (a cluster's, a node's or a folder's
   * guests): the Cluster column would say one thing all the way down, so it
   * is left out.
   */
  singleCluster?: boolean;
}

/**
 * The guest and node table, as one flush panel: search and the quick filter
 * in its title row, the bulk actions in their place while rows are selected.
 */
export function ResourceTable({
  data,
  label = "Resources",
  singleCluster = false,
}: ResourceTableProps) {
  const isMobile = useIsMobile();
  const [sorting, setSorting] = useState<SortingState>([]);
  const [rowSelection, setRowSelection] = useState<RowSelectionState>({});
  const [query, setQuery] = useState<ParsedQuery>({
    filters: [],
    freeText: "",
  });
  const [quickStatus, setQuickStatus] = useState<QuickStatus>("all");
  const [contextMenu, setContextMenu] = useState<MenuState | null>(null);

  const openRowMenu = useCallback(
    (
      target: VMContextTarget,
      x: number,
      y: number,
      opener: HTMLElement | null,
    ) => {
      setContextMenu({ target, x, y, opener });
    },
    [],
  );
  const columns = useMemo(
    () => buildColumns(isMobile, singleCluster, openRowMenu),
    [isMobile, singleCluster, openRowMenu],
  );

  const savedVisibility = useMemo(() => {
    const saved = loadColumnVisibility(isMobile);
    const defaults = getDefaultColumnVisibility(isMobile);
    return { ...defaults, ...saved };
  }, [isMobile]);

  const [columnVisibility, setColumnVisibility] =
    useState<VisibilityState>(savedVisibility);

  // Swap to the form factor's own column set when crossing the breakpoint —
  // mobile and desktop layouts persist independently.
  useEffect(() => {
    setColumnVisibility(savedVisibility);
  }, [savedVisibility]);

  // Persist only user-driven changes (ColumnToggle): the breakpoint swap
  // above must not write one form factor's layout into the other's key.
  const handleColumnVisibilityChange = useCallback<OnChangeFn<VisibilityState>>(
    (updater) => {
      setColumnVisibility((prev) => {
        const next = typeof updater === "function" ? updater(prev) : updater;
        saveColumnVisibility(next, isMobile);
        return next;
      });
    },
    [isMobile],
  );

  // The search first, then the quick filter over what it left, so each
  // segment can say how many rows it would show.
  const searchedData = useMemo(() => applyFilter(data, query), [data, query]);
  const filteredData = useMemo(
    () => filterByQuickStatus(searchedData, quickStatus),
    [searchedData, quickStatus],
  );
  const quickCounts = useMemo(
    () => ({
      all: searchedData.length,
      running: filterByQuickStatus(searchedData, "running").length,
      stopped: filterByQuickStatus(searchedData, "stopped").length,
    }),
    [searchedData],
  );

  const handleQueryChange = useCallback((parsed: ParsedQuery) => {
    setQuery(parsed);
  }, []);

  // The selection is always a subset of the rows in view: a row that leaves
  // it (the search changed, or a guest stopped under status:running) is
  // unticked, so it cannot come back ticked into a bulk action. Done while
  // rendering, so no bulk action sees the stale selection in between.
  const [selectionRows, setSelectionRows] = useState(filteredData);
  if (selectionRows !== filteredData) {
    setSelectionRows(filteredData);
    setRowSelection((prev) => pruneSelection(prev, filteredData));
  }

  // A new filter starts with nothing selected. A row ticked under the old
  // one may have left the view since (it was stopped while only running
  // guests were listed) and would come back ticked, into the next bulk action.
  const handleQuickStatusChange = useCallback((next: QuickStatus) => {
    setQuickStatus(next);
    setRowSelection({});
  }, []);

  // Like Radix's menus, which this one is not: an item chosen, or Escape,
  // gives focus back to what opened the menu — at once, so that a dialog the
  // item opens (in the same click, as the menu goes) takes that as the
  // element to return focus to when it closes, not the menu's own button,
  // which is gone by then. Without scrolling to it, as Radix's FocusScope
  // does not.
  const handleCloseMenu = useCallback(() => {
    contextMenu?.opener?.focus({ preventScroll: true });
    setContextMenu(null);
  }, [contextMenu]);

  // Not after a click outside, as Radix's do not either: that leaves focus
  // where the click put it — or where what it landed on put it, as a
  // console's terminal does, cancelling the click's own focusing.
  const handleDismissMenu = useCallback(() => {
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
    onColumnVisibilityChange: handleColumnVisibilityChange,
    onRowSelectionChange: setRowSelection,
    getCoreRowModel: getCoreRowModel(),
    getSortedRowModel: getSortedRowModel(),
    getPaginationRowModel: getPaginationRowModel(),
    getFilteredRowModel: getFilteredRowModel(),
    initialState: {
      pagination: { pageSize: PAGE_SIZE },
    },
  });

  const selectedCount = table.getFilteredSelectedRowModel().rows.length;
  const pageCount = table.getPageCount() || 1;
  const isFiltered = data.length !== filteredData.length;
  const searching = query.filters.length > 0 || query.freeText !== "";
  const emptyText =
    data.length === 0
      ? "No resources found."
      : searching || quickStatus === "all"
        ? "No resources match this search."
        : quickStatus === "running"
          ? "No running guests."
          : "No stopped guests.";
  // The counts go when the row is short of room: the filter matters more.
  const count = (n: number) => (
    <span className="hidden font-normal tabular-nums text-muted-foreground sm:inline">
      {String(n)}
    </span>
  );

  return (
    <Panel flush aria-label={label} bodyClassName="pb-0 pt-0">
      <div className="flex flex-wrap items-center gap-x-3 gap-y-2 px-[18px] pb-3 pt-3.5">
        <div className="min-w-[12rem] flex-[1_1_15rem] sm:max-w-md">
          <SearchBar onQueryChange={handleQueryChange} />
        </div>
        <div className="ml-auto flex flex-wrap items-center gap-2">
          {/* Returns nothing until a row is selected, then takes the place of
              the view controls: the row never grows a second line under the
              checkbox that was just clicked. */}
          <BulkActionToolbar table={table} />
          {selectedCount === 0 && (
            <>
              <Segmented
                aria-label="Show"
                value={quickStatus}
                onValueChange={handleQuickStatusChange}
                options={[
                  {
                    value: "all",
                    label: <>All {count(quickCounts.all)}</>,
                  },
                  {
                    value: "running",
                    label: <>Running {count(quickCounts.running)}</>,
                  },
                  {
                    value: "stopped",
                    label: <>Stopped {count(quickCounts.stopped)}</>,
                  },
                ]}
              />
              <ColumnToggle table={table} />
            </>
          )}
        </div>
        {query.filters.length > 0 && (
          <div className="basis-full">
            <SearchFilterChips query={query} />
          </div>
        )}
      </div>

      <Table>
        <TableHeader>
          {table.getHeaderGroups().map((headerGroup) => (
            <TableRow key={headerGroup.id} className="hover:bg-transparent">
              {headerGroup.headers.map((header) => {
                const sorted = header.column.getIsSorted();
                return (
                  <TableHead
                    key={header.id}
                    aria-sort={
                      !header.column.getCanSort()
                        ? undefined
                        : sorted === "asc"
                          ? "ascending"
                          : sorted === "desc"
                            ? "descending"
                            : "none"
                    }
                    className={cn(
                      "whitespace-nowrap px-2.5",
                      header.column.id === "select" && "w-9",
                      header.column.id === "actions" && "w-10",
                    )}
                  >
                    {header.isPlaceholder
                      ? null
                      : flexRender(
                          header.column.columnDef.header,
                          header.getContext(),
                        )}
                  </TableHead>
                );
              })}
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
                  onContextMenu={
                    target
                      ? (e) => {
                          e.preventDefault();
                          setContextMenu({
                            target,
                            x: e.clientX,
                            y: e.clientY,
                            opener: menuOpener(e),
                          });
                        }
                      : undefined
                  }
                >
                  {row.getVisibleCells().map((cell) => (
                    <TableCell
                      key={cell.id}
                      className={cn(
                        "whitespace-nowrap px-2.5 py-2",
                        // The name is what gives way when the row runs out
                        // of room, so the row stays one line tall.
                        cell.column.id === "name" &&
                          "max-w-[11rem] md:max-w-[15rem]",
                        cell.column.id === "actions" && "w-10 py-1 pl-0",
                      )}
                    >
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
            <TableRow className="hover:bg-transparent">
              <TableCell
                colSpan={table.getVisibleLeafColumns().length}
                className="h-24 text-center text-muted-foreground"
              >
                {emptyText}
              </TableCell>
            </TableRow>
          )}
        </TableBody>
      </Table>

      <div className="flex flex-wrap items-center justify-between gap-x-4 gap-y-2 border-t px-[18px] py-2.5">
        <p className="text-[12.5px] text-muted-foreground">
          {String(filteredData.length)} resource
          {filteredData.length !== 1 ? "s" : ""} total
          {isFiltered && ` (${String(data.length)} unfiltered)`}
        </p>
        {pageCount > 1 && (
          <div className="flex items-center gap-1.5">
            <p className="mr-1.5 text-[12.5px] tabular-nums text-muted-foreground">
              Page {String(table.getState().pagination.pageIndex + 1)} of{" "}
              {String(pageCount)}
            </p>
            <Button
              aria-label="Previous page"
              variant="outline"
              size="sm"
              className="h-7 w-7 p-0"
              onClick={() => {
                table.previousPage();
              }}
              disabled={!table.getCanPreviousPage()}
            >
              <ChevronLeft />
            </Button>
            <Button
              aria-label="Next page"
              variant="outline"
              size="sm"
              className="h-7 w-7 p-0"
              onClick={() => {
                table.nextPage();
              }}
              disabled={!table.getCanNextPage()}
            >
              <ChevronRight />
            </Button>
          </div>
        )}
      </div>

      {contextMenu && (
        <RowContextMenu
          menu={contextMenu}
          onClose={handleCloseMenu}
          onDismiss={handleDismissMenu}
        />
      )}
    </Panel>
  );
}
