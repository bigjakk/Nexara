import { useState } from "react";
import { ArrowRightLeft, RefreshCw, Power, Wrench } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Label } from "@/components/ui/label";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
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
  useShutdownNode,
  useRebootNode,
  useEvacuateNode,
  useSetNodeMaintenance,
  type EvacuateMigration,
} from "../../api/cluster-queries";
import { useSSHCredentials } from "@/features/rolling-updates/api/rolling-update-queries";
import { useTaskLogStore } from "@/stores/task-log-store";

export function NodePowerActions({ clusterId, nodeName, otherNodes, inMaintenance }: { clusterId: string; nodeName: string; otherNodes: string[]; inMaintenance: boolean }) {
  const shutdown = useShutdownNode(clusterId, nodeName);
  const reboot = useRebootNode(clusterId, nodeName);
  const evacuate = useEvacuateNode(clusterId, nodeName);
  const maintenance = useSetNodeMaintenance(clusterId, nodeName);
  const { data: sshCreds } = useSSHCredentials(clusterId);
  const hasSSH = sshCreds != null;
  const setPanelOpen = useTaskLogStore((s) => s.setPanelOpen);
  const setFocusedTask = useTaskLogStore((s) => s.setFocusedTask);
  const [actionError, setActionError] = useState<string | null>(null);
  const [evacuateOpen, setEvacuateOpen] = useState(false);
  const [mode, setMode] = useState<"distribute" | "single">("distribute");
  const [targetNode, setTargetNode] = useState("");
  const [migrations, setMigrations] = useState<EvacuateMigration[] | null>(null);

  const handleError = (err: unknown) => {
    const msg = err instanceof Error ? err.message : "Unknown error";
    setActionError(msg);
  };

  const handleEvacuate = () => {
    if (mode === "single" && !targetNode) return;
    setActionError(null);
    setMigrations(null);
    const params = mode === "single" ? { target_node: targetNode } : {};
    evacuate.mutate(params, {
      onSuccess: (data) => {
        setMigrations(data.migrations);
        // Focus the first successful migration task.
        const first = data.migrations.find((m) => m.upid && !m.error);
        if (first) {
          setFocusedTask({ clusterId, upid: first.upid, description: `Evacuate ${nodeName}` });
          setPanelOpen(true);
        }
      },
      onError: handleError,
    });
  };

  const closeEvacuate = () => {
    setEvacuateOpen(false);
    setTargetNode("");
    setMode("distribute");
    setMigrations(null);
  };

  return (
    <>
      {actionError && (
        <div className="flex items-center gap-2 rounded-md border border-destructive bg-destructive/10 px-3 py-1.5 text-sm text-destructive">
          <span className="flex-1">{actionError}</span>
          <Button variant="ghost" size="sm" className="h-6 px-2 text-xs" onClick={() => { setActionError(null); }}>Dismiss</Button>
        </div>
      )}
      <Dialog open={evacuateOpen} onOpenChange={(v) => { if (!v) closeEvacuate(); else setEvacuateOpen(true); }}>
        <DialogTrigger asChild>
          <Button variant="outline" size="sm" className="gap-1.5" disabled={otherNodes.length === 0}>
            <ArrowRightLeft className="h-4 w-4" />
            Evacuate
          </Button>
        </DialogTrigger>
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
                  <Label>Distribution Mode</Label>
                  <div className="flex gap-3">
                    <label className="flex items-center gap-2 text-sm">
                      <input type="radio" name="evac-mode" checked={mode === "distribute"} onChange={() => { setMode("distribute"); setTargetNode(""); }} />
                      Distribute across nodes (DRS-aware)
                    </label>
                    <label className="flex items-center gap-2 text-sm">
                      <input type="radio" name="evac-mode" checked={mode === "single"} onChange={() => { setMode("single"); }} />
                      Single target
                    </label>
                  </div>
                </div>
                {mode === "single" && (
                  <div className="space-y-2">
                    <Label>Target Node</Label>
                    <select
                      className="flex h-9 w-full rounded-md border border-input bg-background px-3 py-1 text-sm shadow-xs transition-colors focus-visible:outline-hidden focus-visible:ring-1 focus-visible:ring-ring"
                      value={targetNode}
                      onChange={(e) => { setTargetNode(e.target.value); }}
                    >
                      <option value="">Select a target node...</option>
                      {otherNodes.map((n) => (
                        <option key={n} value={n}>{n}</option>
                      ))}
                    </select>
                  </div>
                )}
              </div>
              <div className="flex justify-end gap-2 pt-2">
                <Button variant="outline" onClick={closeEvacuate}>Cancel</Button>
                <Button onClick={handleEvacuate} disabled={(mode === "single" && !targetNode) || evacuate.isPending}>
                  {evacuate.isPending ? "Evacuating…" : "Evacuate"}
                </Button>
              </div>
            </>
          ) : (
            <>
              <div className="space-y-2">
                <p className="text-sm font-medium">{migrations.length} guest{migrations.length !== 1 ? "s" : ""} migrated:</p>
                <div className="max-h-64 space-y-1 overflow-auto">
                  {migrations.map((m) => (
                    <div
                      key={m.vmid}
                      className={`flex items-center justify-between rounded px-2 py-1 text-sm ${m.error ? "bg-destructive/10 text-destructive" : "bg-muted"}`}
                    >
                      <span className="font-medium">{m.name} <span className="text-muted-foreground">({m.type === "lxc" ? "CT" : "VM"} {String(m.vmid)})</span></span>
                      {m.error ? (
                        <span className="text-xs">{m.error}</span>
                      ) : (
                        <button
                          type="button"
                          className="text-xs text-primary hover:underline"
                          onClick={() => { setFocusedTask({ clusterId, upid: m.upid, description: `Migrate ${m.name}` }); setPanelOpen(true); }}
                        >
                          → {m.target_node}
                        </button>
                      )}
                    </div>
                  ))}
                </div>
              </div>
              <div className="flex justify-end pt-2">
                <Button variant="outline" onClick={closeEvacuate}>Close</Button>
              </div>
            </>
          )}
        </DialogContent>
      </Dialog>

      {hasSSH ? (
        <AlertDialog>
          <AlertDialogTrigger asChild>
            <Button variant="outline" size="sm" className="gap-1.5">
              <Wrench className="h-4 w-4" />
              {inMaintenance ? "Exit Maintenance" : "Enter Maintenance"}
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
                onClick={() => { setActionError(null); maintenance.mutate(!inMaintenance, { onError: handleError }); }}
                disabled={maintenance.isPending}
              >
                {inMaintenance ? "Exit Maintenance" : "Enter Maintenance"}
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
          title="Configure SSH credentials (Settings → SSH Credentials) to use node maintenance, or use Evacuate."
        >
          <Wrench className="h-4 w-4" />
          Maintenance
        </Button>
      )}

      <AlertDialog>
        <AlertDialogTrigger asChild>
          <Button variant="outline" size="sm" className="gap-1.5">
            <RefreshCw className="h-4 w-4" />
            Reboot
          </Button>
        </AlertDialogTrigger>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Reboot {nodeName}?</AlertDialogTitle>
            <AlertDialogDescription>
              This will reboot the node. All running guests will be affected if not migrated first.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction
              onClick={() => { setActionError(null); reboot.mutate(undefined, { onError: handleError }); }}
              disabled={reboot.isPending}
            >
              Reboot
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>

      <AlertDialog>
        <AlertDialogTrigger asChild>
          <Button variant="destructive" size="sm" className="gap-1.5">
            <Power className="h-4 w-4" />
            Shutdown
          </Button>
        </AlertDialogTrigger>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Shutdown {nodeName}?</AlertDialogTitle>
            <AlertDialogDescription>
              This will shut down the node. It will go offline and all running guests will be stopped. You will need physical or out-of-band access to power it back on.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction
              onClick={() => { setActionError(null); shutdown.mutate(undefined, { onError: handleError }); }}
              disabled={shutdown.isPending}
              className="bg-destructive text-destructive-foreground hover:bg-destructive/90"
            >
              Shutdown
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </>
  );
}
