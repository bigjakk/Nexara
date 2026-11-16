import { useState, type ReactNode } from "react";
import { createPortal } from "react-dom";
import {
  ArrowRightLeft,
  FileText,
  Loader2,
  MoreHorizontal,
  Power,
  RefreshCw,
  Wrench,
} from "lucide-react";

import { Button } from "@/components/ui/button";
import { Label } from "@/components/ui/label";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
  AlertDialogTrigger,
} from "@/components/ui/alert-dialog";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import {
  useDownloadNodeReport,
  useRebootNode,
  useEvacuateNode,
  useSetNodeMaintenance,
  type EvacuateMigration,
} from "../../api/cluster-queries";
import { useSSHConfigured } from "@/features/rolling-updates/api/rolling-update-queries";
import { useAuth } from "@/hooks/useAuth";
import { useSaveOutcome } from "@/hooks/useSaveOutcome";
import { describeError } from "@/lib/api-error";
import { NodeShutdownConfirm } from "./NodeShutdownConfirm";
import { useTaskLogStore } from "@/stores/task-log-store";

/**
 * What can be done to a node, ranked: the page's own primary button first
 * (`children`, its Shell), maintenance beside it, and what is rare or takes
 * the node away behind "More actions". Each still asks before it acts.
 */
export function NodePowerActions({
  clusterId,
  nodeName,
  otherNodes,
  inMaintenance,
  canReport,
  bannerContainer,
  children,
}: {
  clusterId: string;
  nodeName: string;
  otherNodes: string[];
  inMaintenance: boolean;
  /** Whether to offer the `pvereport` support bundle: it takes manage:node. */
  canReport: boolean;
  /**
   * Where a failure, and the note that a bundle is being generated, go.
   * Without it they sit among the buttons; a page that puts the buttons in
   * its header gives them a full-width place of their own.
   */
  bannerContainer?: HTMLElement | null | undefined;
  /** Shown first in the row: the page's primary action. */
  children?: ReactNode;
}) {
  const reboot = useRebootNode(clusterId, nodeName);
  const evacuate = useEvacuateNode(clusterId, nodeName);
  const maintenance = useSetNodeMaintenance(clusterId, nodeName, true);
  // These show a failure in the page's banner (the hooks opt out of the app's
  // toast), and one that lands after the node's page was left is toasted.
  const settle = useSaveOutcome(`${clusterId}/${nodeName}`);
  // Generated on demand, and Proxmox shells out to a long list of commands to
  // build it, so the pending state is held for however long that takes.
  const report = useDownloadNodeReport(clusterId, nodeName);
  // Maintenance needs manage:node; without it the check is not sent at all.
  const { canManage } = useAuth();
  const canNode = canManage("node");
  const ssh = useSSHConfigured(clusterId, canNode);
  // Could not check: offered all the same, and the action says if it fails.
  const hasSSH = canNode && (ssh.configured || ssh.unknown);
  const setPanelOpen = useTaskLogStore((s) => s.setPanelOpen);
  const setFocusedTask = useTaskLogStore((s) => s.setFocusedTask);
  const [actionError, setActionError] = useState<string | null>(null);
  // Opened from the menu, whose items are gone by the time a dialog shows.
  const [dialog, setDialog] = useState<
    "evacuate" | "reboot" | "shutdown" | null
  >(null);
  const [mode, setMode] = useState<"distribute" | "single">("distribute");
  const [targetNode, setTargetNode] = useState("");
  const [migrations, setMigrations] = useState<EvacuateMigration[] | null>(
    null,
  );
  // An evacuate's failure, shown in its dialog, which stays open over the
  // page's banner; one that lands after the dialog closed is toasted.
  const [evacuateError, setEvacuateError] = useState<string | null>(null);
  const settleEvacuate = useSaveOutcome(
    dialog === "evacuate" ? `${clusterId}/${nodeName}` : null,
  );

  const handleError = (err: unknown) => {
    const msg = err instanceof Error ? err.message : "Unknown error";
    setActionError(msg);
  };

  const handleEvacuate = () => {
    if (mode === "single" && !targetNode) return;
    setActionError(null);
    setEvacuateError(null);
    setMigrations(null);
    const params = mode === "single" ? { target_node: targetNode } : {};
    // Focus the first successful migration task, the dialog open or not.
    const focusFirst = (data: { migrations: EvacuateMigration[] }) => {
      const first = data.migrations.find((m) => m.upid && !m.error);
      if (first) {
        setFocusedTask({
          clusterId,
          upid: first.upid,
          description: `Evacuate ${nodeName}`,
        });
        setPanelOpen(true);
      }
    };
    settleEvacuate(evacuate.mutateAsync(params), {
      action: `Evacuating ${nodeName}`,
      onSuccess: (data) => {
        setMigrations(data.migrations);
        focusFirst(data);
      },
      onLateSuccess: focusFirst,
      onError: (err) => {
        setEvacuateError(describeError(err) || "Unknown error");
      },
    });
  };

  const closeEvacuate = () => {
    setDialog(null);
    setTargetNode("");
    setMode("distribute");
    setMigrations(null);
    setEvacuateError(null);
  };

  const reportError =
    report.error == null
      ? null
      : report.error instanceof Error
        ? report.error.message
        : "Failed to download the report.";

  const banner = (
    <>
      {actionError && (
        <Notice
          onDismiss={() => {
            setActionError(null);
          }}
        >
          {actionError}
        </Notice>
      )}
      {reportError !== null && (
        <Notice
          onDismiss={() => {
            report.reset();
          }}
        >
          {reportError}
        </Notice>
      )}
      {report.isPending && (
        <p
          role="status"
          className="flex items-center gap-2 text-sm text-muted-foreground"
        >
          <Loader2 className="h-4 w-4 animate-spin" />
          Generating the support bundle for {nodeName}. Proxmox runs a long list
          of commands to build it, so this can take a while.
        </p>
      )}
    </>
  );

  return (
    <>
      {children}

      {hasSSH ? (
        <AlertDialog>
          <AlertDialogTrigger asChild>
            <Button variant="outline" size="sm" className="gap-1.5">
              <Wrench className="h-4 w-4" />
              {inMaintenance ? "Exit maintenance" : "Enter maintenance"}
            </Button>
          </AlertDialogTrigger>
          <AlertDialogContent>
            <AlertDialogHeader>
              <AlertDialogTitle>
                {inMaintenance
                  ? `Exit maintenance on ${nodeName}?`
                  : `Enter maintenance on ${nodeName}?`}
              </AlertDialogTitle>
              <AlertDialogDescription>
                {inMaintenance
                  ? "Takes the node out of HA maintenance so HA can place guests on it again. Runs ha-manager node-maintenance disable over SSH."
                  : "Puts the node into HA maintenance: HA-managed guests are migrated away and the node will not receive new ones, while the rest of the cluster stays HA-protected. Runs ha-manager node-maintenance over SSH."}
              </AlertDialogDescription>
            </AlertDialogHeader>
            <AlertDialogFooter>
              <AlertDialogCancel>Cancel</AlertDialogCancel>
              <AlertDialogAction
                onClick={() => {
                  setActionError(null);
                  settle(maintenance.mutateAsync(!inMaintenance), {
                    action: `${inMaintenance ? "Taking" : "Putting"} ${nodeName} ${inMaintenance ? "out of" : "into"} maintenance`,
                    onError: handleError,
                  });
                }}
                disabled={maintenance.isPending}
              >
                {inMaintenance ? "Exit maintenance" : "Enter maintenance"}
              </AlertDialogAction>
            </AlertDialogFooter>
          </AlertDialogContent>
        </AlertDialog>
      ) : (
        <Button
          variant="outline"
          size="sm"
          className="gap-1.5"
          disabled
          title={
            canNode
              ? "Configure SSH credentials (Security → Rolling Updates → SSH Credentials) to use node maintenance, or use Evacuate."
              : "Node maintenance needs the manage:node permission."
          }
        >
          <Wrench className="h-4 w-4" />
          Maintenance
        </Button>
      )}

      <DropdownMenu>
        <DropdownMenuTrigger asChild>
          <Button
            variant="outline"
            size="sm"
            className="px-2"
            aria-label="More actions"
            aria-busy={report.isPending || undefined}
          >
            {report.isPending ? (
              <Loader2 className="animate-spin" />
            ) : (
              <MoreHorizontal />
            )}
          </Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="end">
          {canReport && (
            <DropdownMenuItem
              className="gap-2"
              disabled={report.isPending}
              title="Download this node's pvereport support bundle"
              onClick={() => {
                report.mutate();
              }}
            >
              <FileText className="h-4 w-4" />
              {report.isPending
                ? "Generating support bundle…"
                : "Support bundle"}
            </DropdownMenuItem>
          )}
          <DropdownMenuItem
            className="gap-2"
            disabled={otherNodes.length === 0}
            onClick={() => {
              setDialog("evacuate");
            }}
          >
            <ArrowRightLeft className="h-4 w-4" />
            Evacuate
          </DropdownMenuItem>
          <DropdownMenuSeparator />
          <DropdownMenuItem
            className="gap-2"
            onClick={() => {
              setDialog("reboot");
            }}
          >
            <RefreshCw className="h-4 w-4" />
            Reboot
          </DropdownMenuItem>
          <DropdownMenuItem
            className="gap-2 text-red-700 focus:text-red-700 dark:text-red-400 dark:focus:text-red-400"
            onClick={() => {
              setDialog("shutdown");
            }}
          >
            <Power className="h-4 w-4" />
            Shutdown
          </DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>

      {bannerContainer != null ? createPortal(banner, bannerContainer) : banner}

      <Dialog
        open={dialog === "evacuate"}
        onOpenChange={(v) => {
          if (!v) closeEvacuate();
        }}
      >
        <DialogContent className="max-w-lg">
          <DialogHeader>
            <DialogTitle>Evacuate all guests from {nodeName}</DialogTitle>
          </DialogHeader>

          {!migrations ? (
            <>
              <p className="text-sm text-muted-foreground">
                Migrate all VMs and containers off this node.
              </p>
              <div className="space-y-3">
                <div className="space-y-2">
                  <Label>Distribution mode</Label>
                  <div className="flex flex-wrap gap-3">
                    <label className="flex items-center gap-2 text-sm">
                      <input
                        type="radio"
                        name="evac-mode"
                        checked={mode === "distribute"}
                        onChange={() => {
                          setMode("distribute");
                          setTargetNode("");
                        }}
                      />
                      Distribute across nodes (DRS-aware)
                    </label>
                    <label className="flex items-center gap-2 text-sm">
                      <input
                        type="radio"
                        name="evac-mode"
                        checked={mode === "single"}
                        onChange={() => {
                          setMode("single");
                        }}
                      />
                      Single target
                    </label>
                  </div>
                </div>
                {mode === "single" && (
                  <div className="space-y-2">
                    <Label>Target node</Label>
                    <select
                      className="flex h-9 w-full rounded-md border border-input bg-background px-3 py-1 text-sm shadow-xs transition-colors focus-visible:outline-hidden focus-visible:ring-1 focus-visible:ring-ring"
                      value={targetNode}
                      onChange={(e) => {
                        setTargetNode(e.target.value);
                      }}
                    >
                      <option value="">Select a target node...</option>
                      {otherNodes.map((n) => (
                        <option key={n} value={n}>
                          {n}
                        </option>
                      ))}
                    </select>
                  </div>
                )}
              </div>
              {evacuateError && (
                <p role="alert" className="text-sm text-destructive">
                  {evacuateError}
                </p>
              )}
              <div className="flex justify-end gap-2 pt-2">
                <Button variant="outline" onClick={closeEvacuate}>
                  Cancel
                </Button>
                <Button
                  onClick={handleEvacuate}
                  disabled={
                    (mode === "single" && !targetNode) || evacuate.isPending
                  }
                >
                  {evacuate.isPending ? "Evacuating…" : "Evacuate"}
                </Button>
              </div>
            </>
          ) : (
            <>
              <div className="space-y-2">
                <p className="text-sm font-medium">
                  {migrations.length} guest{migrations.length !== 1 ? "s" : ""}{" "}
                  migrated:
                </p>
                <div className="max-h-64 space-y-1 overflow-auto">
                  {migrations.map((m) => (
                    <div
                      key={m.vmid}
                      className={`flex items-center justify-between rounded px-2 py-1 text-sm ${m.error ? "bg-destructive/10 text-destructive" : "bg-muted"}`}
                    >
                      <span className="font-medium">
                        {m.name}{" "}
                        <span className="text-muted-foreground">
                          ({m.type === "lxc" ? "CT" : "VM"} {String(m.vmid)})
                        </span>
                      </span>
                      {m.error ? (
                        <span className="text-xs">{m.error}</span>
                      ) : (
                        <button
                          type="button"
                          className="text-xs text-primary hover:underline"
                          onClick={() => {
                            setFocusedTask({
                              clusterId,
                              upid: m.upid,
                              description: `Migrate ${m.name}`,
                            });
                            setPanelOpen(true);
                          }}
                        >
                          → {m.target_node}
                        </button>
                      )}
                    </div>
                  ))}
                </div>
              </div>
              <div className="flex justify-end pt-2">
                <Button variant="outline" onClick={closeEvacuate}>
                  Close
                </Button>
              </div>
            </>
          )}
        </DialogContent>
      </Dialog>

      <AlertDialog
        open={dialog === "reboot"}
        onOpenChange={(v) => {
          if (!v) setDialog(null);
        }}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Reboot {nodeName}?</AlertDialogTitle>
            <AlertDialogDescription>
              This will reboot the node. All running guests will be affected if
              not migrated first.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction
              onClick={() => {
                setActionError(null);
                settle(reboot.mutateAsync(), {
                  action: `Rebooting ${nodeName}`,
                  onError: handleError,
                });
              }}
              disabled={reboot.isPending}
            >
              Reboot
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>

      <NodeShutdownConfirm
        clusterId={clusterId}
        nodeName={nodeName}
        open={dialog === "shutdown"}
        onOpenChange={(v) => {
          if (!v) setDialog(null);
        }}
        onConfirm={() => {
          setActionError(null);
        }}
        onError={handleError}
      />
    </>
  );
}

/** A failed action, said where the whole width is free, until dismissed. */
function Notice({
  children,
  onDismiss,
}: {
  children: ReactNode;
  onDismiss: () => void;
}) {
  return (
    <div
      role="alert"
      className="flex items-center gap-2 rounded-md border border-destructive/40 bg-destructive/10 px-3 py-1.5 text-sm text-destructive"
    >
      <span className="min-w-0 flex-1 [overflow-wrap:anywhere]">
        {children}
      </span>
      <Button
        variant="ghost"
        size="sm"
        className="h-6 px-2 text-xs"
        onClick={onDismiss}
      >
        Dismiss
      </Button>
    </div>
  );
}
