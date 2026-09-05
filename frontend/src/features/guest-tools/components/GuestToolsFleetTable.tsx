import { useState } from "react";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { TableBody, TableHeader, TableRow } from "@/components/ui/table";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { MonitorCog, RefreshCw, Download, X } from "lucide-react";
import type { ColumnDef } from "@/hooks/useColumnLayout";
import { useDataTable } from "@/hooks/useDataTable";
import { DataTableFrame } from "@/components/DataTableFrame";
import { DataTableHeadRow } from "@/components/DataTableHeadCells";
import { DataTableCells } from "@/components/DataTableCells";
import { ResetColumnsButton } from "@/components/ResetColumnsButton";
import { usePermissions } from "@/hooks/usePermissions";
import {
  useGuestToolsFleet,
  useDetectGuestTools,
  useStageGuestToolsUpdate,
  useCancelGuestToolsUpdate,
  useSetGuestToolsPolicy,
} from "../api/guest-tools-queries";
import type { GuestToolsGuest } from "../types/guest-tools";
import {
  guestToolsInFlight,
  guestToolsPolicy,
  guestToolsStagedMismatch,
  guestToolsStageAction,
  guestToolsState,
} from "../lib/guest-tools-state";

type FleetSortKey =
  | "vmid"
  | "name"
  | "node"
  | "installed"
  | "target"
  | "state"
  | "actions";

const byVMID = (g: GuestToolsGuest) => String(g.vmid);

/**
 * What each row's action buttons need that the guest itself does not carry.
 *
 * Passed through the column layout rather than closed over, so COLUMNS can
 * stay a module-scope constant — useDataTable derives the sort accessors from
 * it, and re-sorts on every render if it is rebuilt.
 */
interface FleetCtx {
  busyVMID: number | null;
  mayExecute: boolean;
  mayManage: boolean;
  onDetect: (g: GuestToolsGuest) => void;
  onStage: (g: GuestToolsGuest) => void;
  onCancel: (g: GuestToolsGuest) => void;
  onTogglePolicy: (g: GuestToolsGuest) => void;
}

/** Each column sorts on what its cell SHOWS, not on the underlying field. */
const COLUMNS: ColumnDef<GuestToolsGuest, FleetSortKey, FleetCtx>[] = [
  {
    key: "vmid",
    label: "VMID",
    width: 90,
    sortValue: (g) => g.vmid,
    cell: (g) => <span className="font-mono text-xs">{g.vmid}</span>,
  },
  {
    key: "name",
    label: "Name",
    width: 200,
    sortValue: (g) => g.name,
    cell: (g) => (
      <span className="font-medium">
        {g.name}
        {g.template ? (
          <span className="ml-2 text-xs text-muted-foreground">template</span>
        ) : null}
      </span>
    ),
  },
  {
    key: "node",
    label: "Node",
    width: 130,
    sortValue: (g) => g.node,
    cell: (g) => <span className="text-muted-foreground">{g.node}</span>,
  },
  {
    key: "installed",
    label: "Installed",
    width: 130,
    sortValue: (g) => g.installed_version || null,
    cell: (g) =>
      g.installed_version || (
        <span className="text-muted-foreground">&mdash;</span>
      ),
  },
  {
    key: "target",
    label: "Target",
    width: 150,
    sortValue: (g) => g.target_version || null,
    cell: (g) => (
      <span className="text-muted-foreground">
        {g.target_version || <span>&mdash;</span>}
        {g.policy_target_version ? (
          <span className="ml-1 text-xs">(pinned)</span>
        ) : null}
      </span>
    ),
  },
  {
    key: "state",
    label: "State",
    width: 260,
    sortValue: (g) => guestToolsState(g).label,
    // last_error is the only explanation a failed update gives.
    wrap: true,
    cell: (g) => {
      // Amber, not muted: this is the one line telling the operator the Target
      // column beside it is not what would install, so it must not be the
      // quietest text in the row. Deliberately NOT a live region — a target
      // change supersedes the whole fleet at once, and one role=status per row
      // would queue an announcement per affected guest.
      const mismatch = guestToolsStagedMismatch(g);
      const { label, variant, errorTone } = guestToolsState(g);
      return (
        <div className="space-y-1">
          <Badge variant={variant}>{label}</Badge>
          {mismatch ? (
            <p className="break-words text-xs text-amber-600 dark:text-amber-400">
              {mismatch}
            </p>
          ) : null}
          {g.last_error ? (
            <p className={`break-words text-xs ${errorTone}`}>{g.last_error}</p>
          ) : null}
        </div>
      );
    },
  },
  {
    // No sortValue: there is nothing meaningful to order buttons by. `fixed`
    // keeps it at the end — an actions column dragged into the middle of the
    // data reads as a mis-render.
    key: "actions",
    label: "Actions",
    width: 190,
    align: "right",
    fixed: true,
    cell: (g, ctx) => {
      const busy = ctx.busyVMID === g.vmid;
      const inFlight = guestToolsInFlight(g);
      return (
        <div className="flex justify-end gap-1">
          <Button
            size="sm"
            variant="ghost"
            title="Re-read the installed version"
            disabled={busy || g.status !== "running"}
            onClick={() => {
              ctx.onDetect(g);
            }}
          >
            <RefreshCw className="h-3.5 w-3.5" />
          </Button>
          {inFlight ? (
            <Button
              size="sm"
              variant="ghost"
              title="Cancel the staged update"
              disabled={busy || !ctx.mayExecute}
              onClick={() => {
                ctx.onCancel(g);
              }}
            >
              <X className="h-3.5 w-3.5" />
            </Button>
          ) : (
            <Button
              size="sm"
              variant="ghost"
              title={
                {
                  stage: "Stage an update for the next boot",
                  reinstall: "Reinstall the current version at the next boot",
                  install: "Install guest tools at the next boot",
                }[guestToolsStageAction(g)]
              }
              disabled={
                busy || !ctx.mayExecute || g.excluded || g.status !== "running"
              }
              onClick={() => {
                ctx.onStage(g);
              }}
            >
              <Download className="h-3.5 w-3.5" />
            </Button>
          )}
          <Button
            size="sm"
            variant="ghost"
            title={
              g.excluded
                ? "Include this guest again"
                : "Exclude this guest from updates"
            }
            disabled={busy || !ctx.mayManage}
            onClick={() => {
              ctx.onTogglePolicy(g);
            }}
          >
            {g.excluded ? "Include" : "Exclude"}
          </Button>
        </div>
      );
    },
  },
];

interface GuestToolsFleetTableProps {
  clusterId: string;
}

export function GuestToolsFleetTable({ clusterId }: GuestToolsFleetTableProps) {
  const { data: fleet, isLoading } = useGuestToolsFleet(clusterId);
  const detect = useDetectGuestTools(clusterId);
  const stage = useStageGuestToolsUpdate(clusterId);
  const cancel = useCancelGuestToolsUpdate(clusterId);
  const setPolicy = useSetGuestToolsPolicy(clusterId);
  const { canExecute, canManage } = usePermissions();
  const mayExecute = canExecute("guest_tools");
  const mayManage = canManage("guest_tools");
  const [busyVMID, setBusyVMID] = useState<number | null>(null);
  const [note, setNote] = useState("");

  const {
    layout,
    rows: sorted,
    toggle: toggleSort,
    directionFor,
  } = useDataTable("guest-tools-fleet", COLUMNS, fleet ?? [], byVMID);

  if (isLoading) {
    return <Skeleton className="h-64 w-full" />;
  }

  const run = async (vmid: number, fn: () => Promise<unknown>) => {
    setBusyVMID(vmid);
    setNote("");
    try {
      await fn();
    } catch (err) {
      setNote(err instanceof Error ? err.message : "Action failed.");
    } finally {
      setBusyVMID(null);
    }
  };

  const cellCtx: FleetCtx = {
    busyVMID,
    mayExecute,
    mayManage,
    onDetect: (g) => {
      void run(g.vmid, () => detect.mutateAsync(g.vmid));
    },
    onStage: (g) => {
      void run(g.vmid, () =>
        stage.mutateAsync({ vmid: g.vmid, body: { run_now: false } }),
      );
    },
    onCancel: (g) => {
      void run(g.vmid, () => cancel.mutateAsync(g.vmid));
    },
    onTogglePolicy: (g) => {
      void run(g.vmid, () =>
        setPolicy.mutateAsync({
          vmid: g.vmid,
          policy: guestToolsPolicy(g, { excluded: !g.excluded }),
        }),
      );
    },
  };

  return (
    <Card>
      <CardHeader>
        <CardTitle className="flex items-center gap-2">
          <MonitorCog className="h-5 w-5" />
          Windows guests
          <span className="flex-1" />
          <ResetColumnsButton layout={layout} />
        </CardTitle>
      </CardHeader>
      <CardContent className="space-y-3">
        {note ? <p className="text-xs text-destructive">{note}</p> : null}
        {sorted.length === 0 ? (
          <p className="text-sm text-muted-foreground">
            No Windows guests found on this cluster.
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
              {sorted.map((g) => (
                <TableRow key={g.vmid}>
                  <DataTableCells row={g} layout={layout} ctx={cellCtx} />
                </TableRow>
              ))}
            </TableBody>
          </DataTableFrame>
        )}
      </CardContent>
    </Card>
  );
}
