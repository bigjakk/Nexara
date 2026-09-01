import { useState } from "react";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
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
import { Skeleton } from "@/components/ui/skeleton";
import { MonitorCog, RefreshCw, Download, X } from "lucide-react";
import { useTableSort } from "@/hooks/useTableSort";
import type { SortAccessors } from "@/hooks/useTableSort";
import { SortableTableHead } from "@/components/SortableTableHead";
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
  guestToolsStateLabel,
  guestToolsStateVariant,
} from "../lib/guest-tools-state";

type FleetSortKey =
  | "vmid"
  | "name"
  | "node"
  | "installed"
  | "target"
  | "state";

/** Each accessor sorts on what its cell SHOWS, not on the underlying field. */
const FLEET_SORT: SortAccessors<GuestToolsGuest, FleetSortKey> = {
  vmid: (g) => g.vmid,
  name: (g) => g.name,
  node: (g) => g.node,
  installed: (g) => g.installed_version || null,
  target: (g) => g.target_version || null,
  state: (g) => guestToolsStateLabel(g),
};

const byVMID = (g: GuestToolsGuest) => String(g.vmid);

/**
 * What the stage action will do for this guest, which is not always "update".
 *
 * Staging is deliberately NOT gated on the guest being behind. The backend has
 * never required it — only the automatic scheduler pass skips current guests —
 * and two real cases need it: reinstalling to repair a broken driver install,
 * and installing on a Windows guest that has no virtio-win at all. That second
 * one reports needs_update=false (an unknown version is not "behind"), so
 * gating on it made the feature refuse the guest that most needed it.
 */
function stageActionLabel(g: GuestToolsGuest): string {
  if (g.needs_update) return "Stage an update for the next boot";
  if (!g.installed_version) return "Install guest tools at the next boot";
  return "Reinstall the current version at the next boot";
}

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
    rows: sorted,
    toggle: toggleSort,
    directionFor,
  } = useTableSort(fleet ?? [], FLEET_SORT, byVMID);

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

  return (
    <Card>
      <CardHeader>
        <CardTitle className="flex items-center gap-2">
          <MonitorCog className="h-5 w-5" />
          Windows guests
        </CardTitle>
      </CardHeader>
      <CardContent className="space-y-3">
        {note ? <p className="text-xs text-destructive">{note}</p> : null}
        {sorted.length === 0 ? (
          <p className="text-sm text-muted-foreground">
            No Windows guests found on this cluster.
          </p>
        ) : (
          <div className="overflow-x-auto">
            <Table>
              <TableHeader>
                <TableRow>
                  <SortableTableHead
                    direction={directionFor("vmid")}
                    onSort={() => {
                      toggleSort("vmid");
                    }}
                  >
                    VMID
                  </SortableTableHead>
                  <SortableTableHead
                    direction={directionFor("name")}
                    onSort={() => {
                      toggleSort("name");
                    }}
                  >
                    Name
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
                    direction={directionFor("installed")}
                    onSort={() => {
                      toggleSort("installed");
                    }}
                  >
                    Installed
                  </SortableTableHead>
                  <SortableTableHead
                    direction={directionFor("target")}
                    onSort={() => {
                      toggleSort("target");
                    }}
                  >
                    Target
                  </SortableTableHead>
                  <SortableTableHead
                    direction={directionFor("state")}
                    onSort={() => {
                      toggleSort("state");
                    }}
                  >
                    State
                  </SortableTableHead>
                  <TableHead className="text-right">Actions</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {sorted.map((g) => {
                  const busy = busyVMID === g.vmid;
                  const inFlight =
                    g.stage === "staged" ||
                    g.stage === "running" ||
                    g.stage === "staging";
                  return (
                    <TableRow key={g.vmid}>
                      <TableCell className="font-mono text-xs">
                        {g.vmid}
                      </TableCell>
                      <TableCell className="font-medium">
                        {g.name}
                        {g.template ? (
                          <span className="ml-2 text-xs text-muted-foreground">
                            template
                          </span>
                        ) : null}
                      </TableCell>
                      <TableCell className="text-muted-foreground">
                        {g.node}
                      </TableCell>
                      <TableCell>
                        {g.installed_version || (
                          <span className="text-muted-foreground">&mdash;</span>
                        )}
                      </TableCell>
                      <TableCell className="text-muted-foreground">
                        {g.target_version || <span>&mdash;</span>}
                        {g.policy_target_version ? (
                          <span className="ml-1 text-xs">(pinned)</span>
                        ) : null}
                      </TableCell>
                      <TableCell>
                        <div className="space-y-1">
                          <Badge variant={guestToolsStateVariant(g)}>
                            {guestToolsStateLabel(g)}
                          </Badge>
                          {g.last_error ? (
                            <p
                              className={
                                g.reboot_required
                                  ? "max-w-xs break-words text-xs text-muted-foreground"
                                  : "max-w-xs break-words text-xs text-destructive"
                              }
                            >
                              {g.last_error}
                            </p>
                          ) : null}
                        </div>
                      </TableCell>
                      <TableCell className="text-right">
                        <div className="flex justify-end gap-1">
                          <Button
                            size="sm"
                            variant="ghost"
                            title="Re-read the installed version"
                            disabled={busy || g.status !== "running"}
                            onClick={() => {
                              void run(g.vmid, () =>
                                detect.mutateAsync(g.vmid),
                              );
                            }}
                          >
                            <RefreshCw className="h-3.5 w-3.5" />
                          </Button>
                          {inFlight ? (
                            <Button
                              size="sm"
                              variant="ghost"
                              title="Cancel the staged update"
                              disabled={busy || !mayExecute}
                              onClick={() => {
                                void run(g.vmid, () =>
                                  cancel.mutateAsync(g.vmid),
                                );
                              }}
                            >
                              <X className="h-3.5 w-3.5" />
                            </Button>
                          ) : (
                            <Button
                              size="sm"
                              variant="ghost"
                              title={stageActionLabel(g)}
                              disabled={
                                busy ||
                                !mayExecute ||
                                g.excluded ||
                                g.status !== "running"
                              }
                              onClick={() => {
                                void run(g.vmid, () =>
                                  stage.mutateAsync({
                                    vmid: g.vmid,
                                    body: { run_now: false },
                                  }),
                                );
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
                            disabled={busy || !mayManage}
                            onClick={() => {
                              void run(g.vmid, () =>
                                setPolicy.mutateAsync({
                                  vmid: g.vmid,
                                  policy: {
                                    excluded: !g.excluded,
                                    target_version: g.policy_target_version,
                                    note: g.note,
                                  },
                                }),
                              );
                            }}
                          >
                            {g.excluded ? "Include" : "Exclude"}
                          </Button>
                        </div>
                      </TableCell>
                    </TableRow>
                  );
                })}
              </TableBody>
            </Table>
          </div>
        )}
      </CardContent>
    </Card>
  );
}
