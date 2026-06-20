import { useState } from "react";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { Label } from "@/components/ui/label";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import {
  Dialog,
  DialogContent,
  DialogFooter,
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
import { AlertTriangle, ShieldCheck, ShieldOff } from "lucide-react";
import { useAuth } from "@/hooks/useAuth";
import { isPVEAtLeast, PVE_FEATURES } from "@/lib/pve-version";
import { useArmHA, useDisarmHA } from "../api/ha-queries";

type ResourceMode = "freeze" | "ignore";

interface HAMaintenanceCardProps {
  clusterId: string;
  pveVersion: string;
}

/**
 * Cluster-wide HA maintenance controls (Arm / Disarm HA) — a Proxmox VE 9.2+
 * feature. Renders nothing on older clusters or without manage:ha.
 */
export function HAMaintenanceCard({ clusterId, pveVersion }: HAMaintenanceCardProps) {
  const { canManage } = useAuth();
  const armHA = useArmHA(clusterId);
  const disarmHA = useDisarmHA(clusterId);
  const [disarmOpen, setDisarmOpen] = useState(false);
  const [resourceMode, setResourceMode] = useState<ResourceMode>("freeze");
  const [error, setError] = useState<string | null>(null);

  if (!isPVEAtLeast(pveVersion, PVE_FEATURES.HA_ARM_DISARM)) return null;
  if (!canManage("ha")) return null;

  const handleError = (e: unknown) => {
    setError(e instanceof Error ? e.message : "Action failed");
  };

  const handleDisarm = () => {
    setError(null);
    disarmHA.mutate(resourceMode, {
      onSuccess: () => { setDisarmOpen(false); },
      onError: handleError,
    });
  };

  return (
    <Card>
      <CardHeader className="pb-2">
        <CardTitle className="text-sm font-medium text-muted-foreground">
          HA Maintenance
        </CardTitle>
      </CardHeader>
      <CardContent className="space-y-3">
        <p className="text-xs text-muted-foreground">
          Disarm the HA stack cluster-wide before planned maintenance so controlled
          reboots aren&apos;t treated as failures (no fencing). Re-arm when done.
        </p>
        {error && (
          <div className="flex items-start gap-2 rounded-md border border-destructive bg-destructive/10 p-2 text-xs text-destructive">
            <AlertTriangle className="mt-0.5 h-3.5 w-3.5 shrink-0" />
            <span>{error}</span>
          </div>
        )}
        <div className="flex flex-wrap gap-2">
          <Dialog
            open={disarmOpen}
            onOpenChange={(v) => {
              setDisarmOpen(v);
              if (!v) setError(null);
            }}
          >
            <DialogTrigger asChild>
              <Button variant="outline" size="sm" className="gap-1.5">
                <ShieldOff className="h-4 w-4" />
                Disarm HA&hellip;
              </Button>
            </DialogTrigger>
            <DialogContent className="max-w-md">
              <DialogHeader>
                <DialogTitle>Disarm HA cluster-wide</DialogTitle>
              </DialogHeader>
              <div className="space-y-3">
                <p className="text-sm text-muted-foreground">
                  Releases all HA watchdogs across the cluster so planned actions
                  won&apos;t trigger fencing or recovery. Re-arm HA when maintenance is
                  complete.
                </p>
                <div className="space-y-2">
                  <Label>Resource mode</Label>
                  <Select
                    value={resourceMode}
                    onValueChange={(v) => { setResourceMode(v as ResourceMode); }}
                  >
                    <SelectTrigger><SelectValue /></SelectTrigger>
                    <SelectContent>
                      <SelectItem value="freeze">Freeze — lock services in place</SelectItem>
                      <SelectItem value="ignore">Ignore — suspend HA tracking (manage manually)</SelectItem>
                    </SelectContent>
                  </Select>
                </div>
                {error && <p className="text-sm text-destructive">{error}</p>}
              </div>
              <DialogFooter>
                <Button
                  variant="outline"
                  onClick={() => { setDisarmOpen(false); setError(null); }}
                >
                  Cancel
                </Button>
                <Button onClick={handleDisarm} disabled={disarmHA.isPending}>
                  {disarmHA.isPending ? "Disarming…" : "Disarm HA"}
                </Button>
              </DialogFooter>
            </DialogContent>
          </Dialog>

          <AlertDialog>
            <AlertDialogTrigger asChild>
              <Button variant="outline" size="sm" className="gap-1.5">
                <ShieldCheck className="h-4 w-4" />
                Re-arm HA
              </Button>
            </AlertDialogTrigger>
            <AlertDialogContent>
              <AlertDialogHeader>
                <AlertDialogTitle>Re-arm HA?</AlertDialogTitle>
                <AlertDialogDescription>
                  Restores automatic HA fencing and recovery cluster-wide. Resources
                  return to their previous state and node placement.
                </AlertDialogDescription>
              </AlertDialogHeader>
              <AlertDialogFooter>
                <AlertDialogCancel>Cancel</AlertDialogCancel>
                <AlertDialogAction
                  onClick={() => {
                    setError(null);
                    armHA.mutate(undefined, { onError: handleError });
                  }}
                  disabled={armHA.isPending}
                >
                  Re-arm HA
                </AlertDialogAction>
              </AlertDialogFooter>
            </AlertDialogContent>
          </AlertDialog>
        </div>
      </CardContent>
    </Card>
  );
}
